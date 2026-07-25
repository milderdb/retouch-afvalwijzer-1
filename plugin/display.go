package plugin

// The OLED lives in ReTouch: internal/display owns /dev/fb0 and this plugin
// only hands it content over the loopback display API (--host-url). ReTouch
// does the rendering, standby gating, arbitration with other plugins and the
// framebuffer restore; GET /api/display tells us whether this speaker has a
// panel at all.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const displayOwner = "afvalwijzer"

type displayContent struct {
	Owner   string `json:"owner,omitempty"`
	Icon    string `json:"icon"`
	Text    string `json:"text"`
	Large   bool   `json:"large,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
}

// ensureDisplay re-probes a negative answer every 5 minutes: at boot the
// plugin can come up before ReTouch's web server listens, and the panel
// should not stay disabled until the next plugin restart because of that.
func (p *Plugin) ensureDisplay(now time.Time) bool {
	p.mu.Lock()
	has, last := p.hasOLED, p.lastProbe
	p.mu.Unlock()
	if has || now.Sub(last) < 5*time.Minute {
		return has
	}
	has = p.probeDisplay()
	p.mu.Lock()
	p.hasOLED = has
	p.lastProbe = now
	p.mu.Unlock()
	return has
}

// probeDisplay asks ReTouch whether this speaker has the ST20 panel.
func (p *Plugin) probeDisplay() bool {
	if p.hostURL == "" {
		return false
	}
	req, _ := http.NewRequestWithContext(p.ctx, "GET", p.hostURL+"/api/display", nil)
	resp, err := p.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v struct {
		Available bool `json:"available"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&v)
	return v.Available
}

// syncDisplay pushes the current standby screen to ReTouch when it changed
// (and every 5 minutes as a heartbeat, so a restarted host picks it back up).
func (p *Plugin) syncDisplay(pick Pickup, show bool, lang string, now time.Time) {
	var want string
	if show {
		want = iconFor(pick.Type) + "\x00" + pickupDisplayText(pick, now, lang)
	}
	p.mu.Lock()
	changed := want != p.lastShown || (want != "" && now.Sub(p.lastSync) > 5*time.Minute)
	if changed {
		p.lastShown = want
		p.lastSync = now
	}
	p.mu.Unlock()
	if !changed {
		return
	}
	if want == "" {
		_ = p.displayCall("DELETE", "/api/display/standby?owner="+displayOwner, nil)
		return
	}
	parts := strings.SplitN(want, "\x00", 2)
	_ = p.displayCall("PUT", "/api/display/standby", &displayContent{Owner: displayOwner, Icon: parts[0], Text: parts[1], Large: true})
}

// notifyDisplay shows content immediately (test action).
func (p *Plugin) notifyDisplay(pick Pickup, lang string) error {
	return p.displayCall("POST", "/api/display/notify", &displayContent{
		Icon: iconFor(pick.Type), Text: pickupDisplayText(pick, time.Now(), lang), Seconds: 8, Large: true,
	})
}

func (p *Plugin) displayCall(method, path string, body *displayContent) error {
	if p.hostURL == "" {
		return fmt.Errorf("no host url")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(p.ctx, method, p.hostURL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("display api status %d", resp.StatusCode)
	}
	return nil
}

// iconFor maps a waste type to ReTouch's built-in icon set.
func iconFor(typ string) string {
	switch shortType(typ) {
	case "groen", "papier", "pmd", "rest", "glas":
		return shortType(typ)
	}
	return "rest"
}

func daysAhead(d, now time.Time) int {
	return int(dayStart(d).Sub(dayStart(now)).Hours() / 24)
}

func wasteName(p Pickup, lang string) string {
	switch p.Type {
	case "groen", "papier", "pmd", "rest", "glas":
		return tr(lang, "waste."+p.Type)
	}
	return firstNonEmpty(p.Text, p.Type)
}

func pickupSentence(p Pickup, now time.Time, lang string) string {
	name := wasteName(p, lang)
	switch daysAhead(p.Date, now) {
	case 0:
		return tr(lang, "sentence.today", name)
	case 1:
		return tr(lang, "sentence.tomorrow", name)
	default:
		return tr(lang, "sentence.later", name, p.Date.Format("02-01"))
	}
}

// pickupDisplayText returns a short two-word string for the OLED large layout,
// e.g. "Groenafval morgen" or "Groenafval op 30-07". The core renders it 2×
// scaled and wraps at 10 characters so it fits on two lines.
func pickupDisplayText(p Pickup, now time.Time, lang string) string {
	name := wasteName(p, lang)
	switch daysAhead(p.Date, now) {
	case 0:
		return tr(lang, "display.today", name)
	case 1:
		return tr(lang, "display.tomorrow", name)
	default:
		return tr(lang, "display.later", name, p.Date.Format("02-01"))
	}
}
