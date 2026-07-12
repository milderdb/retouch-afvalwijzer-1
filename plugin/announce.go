package plugin

// Spoken announcements at configured times. At each HH:MM in the config the
// plugin speaks the same sentence the OLED shows. It hands ReTouch the Google
// Translate TTS URL (POST /api/speaker/notify), and ReTouch drives the firmware's
// /speaker endpoint, which fetches and plays the clip — ducking whatever is
// playing and resuming it afterwards, so music keeps going.
//
// /speaker has a native volume (10–70), so loudness is just a request parameter;
// unset defaults to a gentle 30.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// defaultAnnounceVolume is the volume used when none is configured (0). 30 (of
// the firmware's 10–70 window) is a comfortable indoor default.
const defaultAnnounceVolume = 30

var announceMu sync.Mutex

// maybeAnnounce fires the announcement when the clock hits a configured time.
// Called every second from the loop; the lastAnnounce guard makes it at most
// once per minute.
func (p *Plugin) maybeAnnounce(now time.Time) {
	p.mu.Lock()
	cfg := p.cfg
	picks := p.pickups
	due := cfg.AnnounceTimes != "" && now.Sub(p.lastAnnounce) > time.Minute && containsTime(cfg.AnnounceTimes, now.Format("15:04"))
	if due {
		p.lastAnnounce = now
	}
	p.mu.Unlock()
	if !due {
		return
	}
	pick, ok := visiblePickup(picks, now, cfg.AlwaysShow)
	if !ok {
		return
	}
	go func() {
		if err := p.announce(pick); err != nil {
			p.log.Printf("announce: %v", err)
		}
	}()
}

// announceTest speaks the next pickup right away (settings-page action).
func (p *Plugin) announceTest() error {
	p.mu.Lock()
	picks := p.pickups
	p.mu.Unlock()
	pick, ok := visiblePickup(picks, time.Now(), true)
	if !ok {
		return fmt.Errorf("%s", tr(p.language(), "err.nothing"))
	}
	return p.announce(pick)
}

var timeRe = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d$`)

// normalizeTimes parses "8:00, 18:30" into "08:00,18:30"; returns "" when any
// entry is invalid (save reports that as an error) or the input is empty.
func normalizeTimes(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if !timeRe.MatchString(t) {
			return ""
		}
		if len(t) == 4 {
			t = "0" + t
		}
		out = append(out, t)
	}
	return strings.Join(out, ",")
}

func containsTime(times, hhmm string) bool {
	for _, t := range strings.Split(times, ",") {
		if strings.TrimSpace(t) == hhmm {
			return true
		}
	}
	return false
}

// announce speaks the pickup sentence through ReTouch's audio-notification API.
func (p *Plugin) announce(pick Pickup) error {
	announceMu.Lock()
	defer announceMu.Unlock()
	lang := p.language()
	p.mu.Lock()
	vol := p.cfg.AnnounceVolume
	p.mu.Unlock()
	if vol <= 0 {
		vol = defaultAnnounceVolume
	}
	text := pickupSentence(pick, time.Now(), lang)
	return p.speak(text, lang, vol)
}

// ttsURL builds the Google Translate TTS URL for text; the firmware fetches it.
func ttsURL(text, lang string) string {
	return "https://translate.google.com/translate_tts?ie=UTF-8&client=tw-ob&tl=" +
		url.QueryEscape(ttsLang(lang)) + "&q=" + url.QueryEscape(text)
}

// speak asks ReTouch to play spoken text: it POSTs the TTS URL to
// /api/speaker/notify, and ReTouch's firmware fetches and plays it at vol.
func (p *Plugin) speak(text, lang string, vol int) error {
	if p.hostURL == "" {
		return fmt.Errorf("no ReTouch host URL configured")
	}
	body, _ := json.Marshal(map[string]any{
		"url":    ttsURL(text, lang),
		"volume": vol,
		"artist": "Afvalwijzer",
		"track":  text,
	})
	req, _ := http.NewRequestWithContext(p.ctx, "POST", p.hostURL+"/api/speaker/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("speaker notify status %d", resp.StatusCode)
	}
	return nil
}

func ttsLang(lang string) string {
	switch lang {
	case "nl", "de", "fr", "es", "af":
		return lang
	}
	return "en"
}
