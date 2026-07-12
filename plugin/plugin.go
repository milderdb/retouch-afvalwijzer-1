package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Provider    string `json:"provider"`
	Postcode    string `json:"postcode"`
	HouseNumber string `json:"houseNumber"`
	Suffix      string `json:"suffix"`
	Enabled     bool   `json:"enabled"`
	AlwaysShow  bool   `json:"alwaysShow"`

	// AnnounceTimes are HH:MM clock times (comma-separated) at which the pickup
	// sentence is spoken through the speaker; empty disables announcements.
	AnnounceTimes string `json:"announceTimes"`
	// AnnounceVolume is the /speaker playback level for the spoken clip
	// (10–70; 0 = defaultAnnounceVolume).
	AnnounceVolume int `json:"announceVolume"`
}

type Pickup struct {
	Date time.Time `json:"date"`
	Type string    `json:"type"`
	Text string    `json:"text"`
}

type Plugin struct {
	cfgPath string
	speaker string
	hostURL string
	log     *log.Logger
	http    *http.Client
	hasOLED bool

	ctx context.Context

	mu           sync.Mutex
	cfg          Config
	pickups      []Pickup
	lastErr      string
	lastFetch    time.Time
	lang         string
	lastLang     time.Time
	lastAnnounce time.Time
	lastShown    string    // last standby content pushed to ReTouch
	lastSync     time.Time // last time it was pushed (heartbeat)
	lastProbe    time.Time // last (negative) display availability probe
}

func New(ctx context.Context, cfgDir, speaker, hostURL string, logger *log.Logger) (*Plugin, error) {
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return nil, err
	}
	p := &Plugin{
		cfgPath: filepath.Join(cfgDir, "config.json"),
		speaker: speaker,
		hostURL: strings.TrimRight(hostURL, "/"),
		log:     logger,
		http:    &http.Client{Timeout: 12 * time.Second},
		ctx:     ctx,
		lang:    "nl",
	}
	p.hasOLED = p.probeDisplay()
	p.lastProbe = time.Now()
	if !p.hasOLED {
		logger.Printf("host reports no ST20 OLED (or no display API); will re-probe")
	}
	p.cfg = loadConfig(p.cfgPath)
	if p.cfg.Provider == "" {
		p.cfg.Provider = "mijnafvalwijzer"
	}
	go p.loop()
	return p, nil
}

func (p *Plugin) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	mux.HandleFunc("GET /manifest", func(w http.ResponseWriter, _ *http.Request) {
		p.refreshLang()
		p.mu.Lock()
		m := p.manifestLocked()
		p.mu.Unlock()
		writeJSON(w, 200, m)
	})
	mux.HandleFunc("POST /action/{id}", p.action)
	return mux
}

type actionBody struct {
	Values map[string]any `json:"values"`
}

func (p *Plugin) action(w http.ResponseWriter, r *http.Request) {
	p.refreshLang()
	var body actionBody
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	var err error
	switch r.PathValue("id") {
	case "save":
		err = p.save(body.Values)
	case "refresh":
		err = p.refreshNow()
	case "test":
		err = p.drawTest()
	case "announce":
		err = p.announceTest()
	case "clear":
		err = p.displayCall("DELETE", "/api/display/standby?owner="+displayOwner, nil)
	default:
		err = fmt.Errorf("unknown action")
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	p.mu.Lock()
	m := p.manifestLocked()
	p.mu.Unlock()
	writeJSON(w, 200, m)
}

// refreshLang syncs the UI language from ReTouch's settings API (cached for
// a minute). Falls back to the last known language when the host is away.
func (p *Plugin) refreshLang() {
	if p.hostURL == "" {
		return
	}
	p.mu.Lock()
	stale := time.Since(p.lastLang) > time.Minute
	p.mu.Unlock()
	if !stale {
		return
	}
	var s struct {
		Language string `json:"language"`
	}
	req, _ := http.NewRequestWithContext(p.ctx, "GET", p.hostURL+"/api/settings", nil)
	resp, err := p.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&s)
	p.mu.Lock()
	if s.Language != "" {
		p.lang = s.Language
	}
	p.lastLang = time.Now()
	p.mu.Unlock()
}

func (p *Plugin) language() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lang
}

func (p *Plugin) save(v map[string]any) error {
	lang := p.language()
	cfg := Config{
		Provider:    firstNonEmpty(str(v["provider"]), "mijnafvalwijzer"),
		Postcode:    strings.ToUpper(strings.ReplaceAll(str(v["postcode"]), " ", "")),
		HouseNumber: str(v["houseNumber"]),
		Suffix:      str(v["suffix"]),
		Enabled:     boolish(v["enabled"]),
		AlwaysShow:  boolish(v["alwaysShow"]),

		AnnounceTimes:  normalizeTimes(str(v["announceTimes"])),
		AnnounceVolume: clampVolume(atoiDefault(str(v["announceVolume"]), 0)),
	}
	if cfg.Postcode == "" || cfg.HouseNumber == "" {
		return fmt.Errorf("%s", tr(lang, "err.required"))
	}
	if _, ok := providers[cfg.Provider]; !ok {
		return fmt.Errorf(tr(lang, "err.provider"), cfg.Provider)
	}
	if cfg.AnnounceTimes == "" && strings.TrimSpace(str(v["announceTimes"])) != "" {
		return fmt.Errorf("%s", tr(lang, "err.times"))
	}
	p.mu.Lock()
	p.cfg = cfg
	p.lastErr = ""
	p.mu.Unlock()
	if err := saveConfig(p.cfgPath, cfg); err != nil {
		return err
	}
	return p.refreshNow()
}

func (p *Plugin) loop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-p.ctx.Done():
			_ = p.displayCall("DELETE", "/api/display/standby?owner="+displayOwner, nil)
			return
		case <-tick.C:
			p.step()
		}
	}
}

func (p *Plugin) step() {
	p.refreshLang()
	now := time.Now()
	p.mu.Lock()
	cfg := p.cfg
	lang := p.lang
	refreshAfter := 6 * time.Hour
	if p.lastErr != "" {
		refreshAfter = 10 * time.Minute
	}
	stale := cfg.Enabled && cfg.Postcode != "" && cfg.HouseNumber != "" && time.Since(p.lastFetch) > refreshAfter
	pickup, show := visiblePickup(p.pickups, now, cfg.AlwaysShow)
	p.mu.Unlock()
	if stale {
		_ = p.refreshNow()
	}
	p.maybeAnnounce(now)
	if p.ensureDisplay(now) {
		p.syncDisplay(pickup, cfg.Enabled && show, lang, now)
	}
}

// visiblePickup picks the first upcoming pickup that may be shown. From 18:00
// today no longer counts and the display rolls over to the next pickup day.
// Without alwaysShow only today/tomorrow are shown.
func visiblePickup(picks []Pickup, now time.Time, alwaysShow bool) (Pickup, bool) {
	minDay := dayStart(now)
	if now.Hour() >= 18 {
		minDay = minDay.AddDate(0, 0, 1)
	}
	for _, p := range picks {
		d := dayStart(p.Date)
		if d.Before(minDay) {
			continue
		}
		if alwaysShow || daysAhead(p.Date, now) <= 1 {
			return p, true
		}
		return Pickup{}, false
	}
	return Pickup{}, false
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

func (p *Plugin) refreshNow() error {
	p.mu.Lock()
	cfg := p.cfg
	p.mu.Unlock()
	if cfg.Postcode == "" || cfg.HouseNumber == "" {
		return fmt.Errorf("%s", tr(p.language(), "status.configure"))
	}
	picks, err := fetchAfvalwijzer(p.ctx, p.http, cfg)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastFetch = time.Now()
	if err != nil {
		p.lastErr = err.Error()
		return err
	}
	p.pickups = picks
	p.lastErr = ""
	return nil
}

func (p *Plugin) drawTest() error {
	if !p.ensureDisplay(time.Now()) {
		return fmt.Errorf("%s", tr(p.language(), "err.nooled"))
	}
	return p.notifyDisplay(Pickup{Date: time.Now().Add(24 * time.Hour), Type: "groen"}, p.language())
}
