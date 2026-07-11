package plugin

import (
	"fmt"
	"testing"
	"time"
)

func TestParseDutchDate(t *testing.T) {
	d, ok := parseDutchDate("maandag 12 januari")
	if !ok || d.Day() != 12 || d.Month() != time.January {
		t.Fatalf("got %v ok=%v", d, ok)
	}
	if _, ok := parseDutchDate("onzin"); ok {
		t.Fatal("expected parse failure")
	}
}

func TestParseMijnAfvalwijzerHTML(t *testing.T) {
	next := time.Now().AddDate(0, 0, 7)
	months := []string{"januari", "februari", "maart", "april", "mei", "juni", "juli", "augustus", "september", "oktober", "november", "december"}
	html := fmt.Sprintf(`<a class="wasteInfoIcon" href="#">
		<span class="span-line-break">maandag %d %s</span>
		<span class="afvaldescr">GFT en etensresten</span></a>`, next.Day(), months[next.Month()-1])
	picks := parseMijnAfvalwijzerHTML(html)
	if len(picks) != 1 || picks[0].Type != "groen" || picks[0].Date.Day() != next.Day() {
		t.Fatalf("got %+v", picks)
	}
}

func TestNormalizeType(t *testing.T) {
	for in, want := range map[string]string{"GFT en etensresten": "groen", "Papier en karton": "papier", "PMD": "pmd", "Restafval": "rest", "glasbak": "glas"} {
		if got := normalizeType(in); got != want {
			t.Fatalf("normalizeType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParsePickupJSON(t *testing.T) {
	future := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	body := []byte(`{"data":{"ophaaldagen":{"data":[{"date":"` + future + `","type":"restafval","nameType":"restafval"}]}}}`)
	picks := parsePickup(body)
	if len(picks) == 0 || picks[0].Type != "rest" {
		t.Fatalf("got %+v", picks)
	}
}

func TestVisiblePickup(t *testing.T) {
	day := func(offset, hour int) time.Time {
		return time.Date(2026, 7, 11+offset, hour, 0, 0, 0, time.Local)
	}
	today := Pickup{Date: day(0, 0), Type: "pmd"}
	tomorrow := Pickup{Date: day(1, 0), Type: "groen"}
	nextWeek := Pickup{Date: day(6, 0), Type: "papier"}

	// Overdag: vandaag tonen.
	if p, ok := visiblePickup([]Pickup{today, tomorrow}, day(0, 10), false); !ok || p.Type != "pmd" {
		t.Fatalf("dag: got %+v ok=%v", p, ok)
	}
	// Vanaf 18:00 schuift door naar morgen.
	if p, ok := visiblePickup([]Pickup{today, tomorrow}, day(0, 18), false); !ok || p.Type != "groen" {
		t.Fatalf("avond: got %+v ok=%v", p, ok)
	}
	// Verder dan morgen: niets tonen zonder alwaysShow.
	if _, ok := visiblePickup([]Pickup{nextWeek}, day(0, 10), false); ok {
		t.Fatal("volgende week zichtbaar zonder alwaysShow")
	}
	// alwaysShow: wel tonen.
	if p, ok := visiblePickup([]Pickup{nextWeek}, day(0, 10), true); !ok || p.Type != "papier" {
		t.Fatalf("alwaysShow: got %+v ok=%v", p, ok)
	}
	// Vandaag na 18:00, geen volgende: niets tonen.
	if _, ok := visiblePickup([]Pickup{today}, day(0, 19), false); ok {
		t.Fatal("vandaag na 18:00 nog zichtbaar")
	}
}

func TestPickupSentence(t *testing.T) {
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.Local)
	p := Pickup{Date: now.AddDate(0, 0, 1), Type: "groen", Text: "GFT"}
	if got := pickupSentence(p, now, "nl"); got != "Morgen wordt groenafval opgehaald" {
		t.Fatalf("got %q", got)
	}
	p.Date = now
	if got := pickupSentence(p, now, "nl"); got != "Vandaag wordt groenafval opgehaald" {
		t.Fatalf("got %q", got)
	}
	p.Date = now.AddDate(0, 0, 5)
	if got := pickupSentence(p, now, "nl"); got != "groenafval wordt opgehaald op 16-07" {
		t.Fatalf("got %q", got)
	}
	if got := pickupSentence(p, now, "de"); got != "Bioabfall wird am 16-07 abgeholt" {
		t.Fatalf("de: got %q", got)
	}
	if got := pickupSentence(p, now, "xx"); got != "organic waste is collected on 16-07" {
		t.Fatalf("fallback: got %q", got)
	}
}

func TestNormalizeTimes(t *testing.T) {
	if got := normalizeTimes(" 8:00, 18:30 "); got != "08:00,18:30" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeTimes("25:00"); got != "" {
		t.Fatalf("invalid accepted: %q", got)
	}
	if got := normalizeTimes(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if !containsTime("08:00,18:30", "18:30") || containsTime("08:00", "08:01") {
		t.Fatal("containsTime wrong")
	}
}
