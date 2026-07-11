package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func fetchAfvalwijzer(ctx context.Context, c *http.Client, cfg Config) ([]Pickup, error) {
	if p, ok := providers[cfg.Provider]; ok && p.Type == "ximmio" {
		return fetchXimmio(ctx, c, cfg, p)
	}
	q := url.Values{}
	q.Set("method", "calendar")
	q.Set("postcode", cfg.Postcode)
	q.Set("huisnummer", cfg.HouseNumber)
	q.Set("toevoeging", cfg.Suffix)
	q.Set("jaar", strconv.Itoa(time.Now().Year()))
	q.Set("platform", "phone")
	q.Set("langs", "nl")
	mijnAfvalwijzerURL := "https://www.mijnafvalwijzer.nl/nl/" + url.PathEscape(cfg.Postcode) + "/" + url.PathEscape(cfg.HouseNumber) + "/"
	if cfg.Suffix != "" {
		mijnAfvalwijzerURL += url.PathEscape(cfg.Suffix) + "/"
	}
	endpoints := []string{
		"https://api.afvalwijzer.app/webservices/appsinput/?" + q.Encode(),
		mijnAfvalwijzerURL,
	}
	var last error
	for _, u := range endpoints {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "retouch-afvalwijzer")
		resp, err := c.Do(req)
		if err != nil {
			last = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			last = fmt.Errorf("afvalwijzer status %d", resp.StatusCode)
			continue
		}
		if picks := parsePickup(body); len(picks) > 0 {
			return picks, nil
		}
		if picks := parseMijnAfvalwijzerHTML(string(body)); len(picks) > 0 {
			return picks, nil
		}
		last = fmt.Errorf("geen ophaaldatum gevonden")
	}
	if last == nil {
		last = fmt.Errorf("geen ophaaldatum gevonden")
	}
	return nil, last
}

type provider struct{ Name, Type, BaseURL, CompanyCode string }

var providers = map[string]provider{
	"mijnafvalwijzer": {Name: "Mijn Afvalwijzer", Type: "html"},
	"avalex":          {Name: "Avalex", Type: "ximmio", BaseURL: "https://wasteprod2api.ximmio.com", CompanyCode: "f7a74ad1-fdbf-4a43-9f91-44644f4d4222"},
	"twentemilieu":    {Name: "Twente Milieu", Type: "ximmio", BaseURL: "https://wasteapi.ximmio.com", CompanyCode: "8d97bb56-5afd-4cbc-a651-b4f7314264b4"},
	"circulus":        {Name: "Circulus", Type: "ximmio", BaseURL: "https://wasteapi.ximmio.com", CompanyCode: "f8e2844a-095e-48f9-9f98-71f790571571"},
	"meerlanden":      {Name: "Meerlanden", Type: "ximmio", BaseURL: "https://wasteapi.ximmio.com", CompanyCode: "800bf8d7-6e1b-4571-b882-adbf42265fad"},
	"waardlanden":     {Name: "Waardlanden", Type: "ximmio", BaseURL: "https://wasteapi.ximmio.com", CompanyCode: "942abcf6-3775-400d-ae5d-7571b38bb4f8"},
}

func fetchXimmio(ctx context.Context, c *http.Client, cfg Config, p provider) ([]Pickup, error) {
	addrReq := map[string]string{"companyCode": p.CompanyCode, "postCode": cfg.Postcode, "houseNumber": cfg.HouseNumber}
	var addr struct {
		DataList []struct {
			UniqueID  string `json:"UniqueId"`
			Community string `json:"Community"`
		} `json:"dataList"`
	}
	if err := postJSON(ctx, c, p.BaseURL+"/api/FetchAdress", addrReq, &addr); err != nil {
		return nil, err
	}
	if len(addr.DataList) == 0 || addr.DataList[0].UniqueID == "" {
		return nil, fmt.Errorf("adres niet gevonden bij %s", p.Name)
	}
	now := time.Now()
	calReq := map[string]string{
		"companyCode":     p.CompanyCode,
		"uniqueAddressID": addr.DataList[0].UniqueID,
		"startDate":       now.Format("2006-01-02"),
		"endDate":         now.AddDate(1, 0, 0).Format("2006-01-02"),
		"community":       addr.DataList[0].Community,
	}
	var cal struct {
		DataList []struct {
			PickupTypeText string   `json:"_pickupTypeText"`
			PickupDates    []string `json:"pickupDates"`
		} `json:"dataList"`
	}
	if err := postJSON(ctx, c, p.BaseURL+"/api/GetCalendar", calReq, &cal); err != nil {
		return nil, err
	}
	var picks []Pickup
	for _, item := range cal.DataList {
		for _, ds := range item.PickupDates {
			if d, ok := parseDate(ds); ok {
				picks = append(picks, Pickup{Date: d, Type: normalizeType(item.PickupTypeText), Text: cleanLabel(item.PickupTypeText)})
			}
		}
	}
	picks = futureSorted(picks)
	if len(picks) == 0 {
		return nil, fmt.Errorf("geen ophaaldatum gevonden bij %s", p.Name)
	}
	return picks, nil
}

func futureSorted(picks []Pickup) []Pickup {
	cutoff := time.Now().AddDate(0, 0, -1)
	out := picks[:0]
	for _, p := range picks {
		if p.Date.After(cutoff) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}

func postJSON(ctx context.Context, c *http.Client, url string, in, out any) error {
	b, _ := json.Marshal(in)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "retouch-afvalwijzer")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}

func parsePickup(body []byte) []Pickup {
	var anyv any
	if json.Unmarshal(body, &anyv) != nil {
		return nil
	}
	var picks []Pickup
	walkJSON(anyv, &picks)
	return futureSorted(picks)
}

var wasteInfoRe = regexp.MustCompile(`(?is)<a[^>]+class="[^"]*wasteInfoIcon[^"]*"[^>]*>(.*?)</a>`)
var dateSpanRe = regexp.MustCompile(`(?is)<span[^>]+class="[^"]*span-line-break[^"]*"[^>]*>(.*?)</span>`)
var descSpanRe = regexp.MustCompile(`(?is)<span[^>]+class="[^"]*afvaldescr[^"]*"[^>]*>(.*?)</span>`)
var tagRe = regexp.MustCompile(`(?is)<[^>]+>`)

func parseMijnAfvalwijzerHTML(s string) []Pickup {
	var picks []Pickup
	for _, m := range wasteInfoRe.FindAllStringSubmatch(s, -1) {
		dateM := dateSpanRe.FindStringSubmatch(m[1])
		descM := descSpanRe.FindStringSubmatch(m[1])
		if len(dateM) < 2 || len(descM) < 2 {
			continue
		}
		d, ok := parseDutchDate(stripHTML(dateM[1]))
		if !ok {
			continue
		}
		text := cleanLabel(stripHTML(descM[1]))
		picks = append(picks, Pickup{Date: d, Type: normalizeType(text), Text: text})
	}
	return futureSorted(picks)
}

func stripHTML(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	return html.UnescapeString(cleanLabel(s))
}

func parseDutchDate(s string) (time.Time, bool) {
	parts := strings.Fields(strings.ToLower(s))
	if len(parts) < 3 {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	month := map[string]time.Month{"januari": 1, "februari": 2, "maart": 3, "april": 4, "mei": 5, "juni": 6, "juli": 7, "augustus": 8, "september": 9, "oktober": 10, "november": 11, "december": 12}[parts[2]]
	if month == 0 {
		return time.Time{}, false
	}
	year := time.Now().Year()
	d := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
	if d.Before(time.Now().AddDate(0, -6, 0)) {
		d = d.AddDate(1, 0, 0)
	}
	return d, true
}

func walkJSON(v any, picks *[]Pickup) {
	switch x := v.(type) {
	case []any:
		for _, it := range x {
			walkJSON(it, picks)
		}
	case map[string]any:
		var date, typ, text string
		for k, v := range x {
			lk := strings.ToLower(k)
			if s, ok := v.(string); ok {
				switch {
				case strings.Contains(lk, "date") || strings.Contains(lk, "datum") || lk == "day":
					date = s
				case strings.Contains(lk, "type") || strings.Contains(lk, "fraction") || strings.Contains(lk, "afval") || strings.Contains(lk, "title"):
					typ = s
				case strings.Contains(lk, "name") || strings.Contains(lk, "text") || strings.Contains(lk, "omschrijving"):
					text = s
				}
			}
		}
		if d, ok := parseDate(date); ok {
			if typ == "" {
				typ = text
			}
			*picks = append(*picks, Pickup{Date: d, Type: normalizeType(typ + " " + text), Text: cleanLabel(firstNonEmpty(text, typ))})
		}
		for _, v := range x {
			walkJSON(v, picks)
		}
	}
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, f := range []string{"2006-01-02", "02-01-2006", time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(f, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func normalizeType(s string) string {
	ls := strings.ToLower(s)
	switch {
	case strings.Contains(ls, "gft") || strings.Contains(ls, "groen") || strings.Contains(ls, "bio"):
		return "groen"
	case strings.Contains(ls, "papier") || strings.Contains(ls, "karton"):
		return "papier"
	case strings.Contains(ls, "pmd") || strings.Contains(ls, "plastic"):
		return "pmd"
	case strings.Contains(ls, "rest"):
		return "rest"
	case strings.Contains(ls, "glas"):
		return "glas"
	default:
		return cleanLabel(s)
	}
}
