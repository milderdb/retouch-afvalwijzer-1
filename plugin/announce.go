package plugin

// Spoken announcements at configured times. At each HH:MM in the config the
// plugin speaks the same sentence the OLED shows (via Google Translate TTS,
// decoded to the headerless 48 kHz stereo s16le PCM the firmware's
// /playNotification plays — the same format retouch-ring's chimes use). The
// firmware ducks whatever is playing and resumes it afterwards, so music
// keeps playing.
//
// /playNotification plays at a FIXED firmware level: the speaker's master
// volume does not attenuate it. Loudness is therefore baked into the PCM as
// gain (AnnounceVolume, percent; 100 = as spoken by the TTS).

import (
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	mp3 "github.com/hajimehoshi/go-mp3"
)

const announceRate = 48000 // /playNotification format: s16le, 48 kHz, stereo

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

// announce speaks the pickup sentence through the speaker's ducked
// notification playback.
func (p *Plugin) announce(pick Pickup) error {
	announceMu.Lock()
	defer announceMu.Unlock()
	lang := p.language()
	p.mu.Lock()
	vol := p.cfg.AnnounceVolume
	p.mu.Unlock()
	if vol <= 0 {
		vol = 100
	}

	text := pickupSentence(pick, time.Now(), lang)
	pcm, err := p.ttsPCM(text, lang, vol)
	if err != nil {
		return fmt.Errorf("tts: %w", err)
	}
	path := filepath.Join(filepath.Dir(p.cfgPath), "announce.pcm")
	if err := os.WriteFile(path, pcm, 0o644); err != nil {
		return err
	}
	return p.playNotification(path)
}

// ttsPCM fetches spoken text from Google Translate TTS (MP3) and converts it
// to the firmware's PCM format with the gain baked in. Results are cached per
// sentence+volume next to the config.
func (p *Plugin) ttsPCM(text, lang string, vol int) ([]byte, error) {
	// The readable part is truncated, so a short hash keeps distinct long
	// sentences from colliding onto the same cache file.
	key := fmt.Sprintf("%s-%d-%s", lang, vol, text)
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	cache := filepath.Join(filepath.Dir(p.cfgPath), fmt.Sprintf("tts-%s-%08x.pcm", sanitize(key), h.Sum32()))
	if b, err := os.ReadFile(cache); err == nil && len(b) > 0 {
		return b, nil
	}
	u := "https://translate.google.com/translate_tts?ie=UTF-8&client=tw-ob&tl=" + url.QueryEscape(ttsLang(lang)) + "&q=" + url.QueryEscape(text)
	req, _ := http.NewRequestWithContext(p.ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("tts status %d", resp.StatusCode)
	}
	mp3Body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	pcm, err := mp3ToSpeakerPCM(mp3Body, float64(vol)/100)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(cache, pcm, 0o644) // best-effort cache
	return pcm, nil
}

func ttsLang(lang string) string {
	switch lang {
	case "nl", "de", "fr", "es", "af":
		return lang
	}
	return "en"
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func sanitize(s string) string {
	s = sanitizeRe.ReplaceAllString(s, "-")
	if len(s) > 60 {
		s = s[:60]
	}
	return strings.Trim(s, "-")
}

// mp3ToSpeakerPCM decodes MP3 (go-mp3 always outputs 16-bit stereo at the
// source rate) and linearly resamples to the firmware's 48 kHz stereo s16le,
// applying gain with clipping. /playNotification plays at a fixed level, so
// gain in the samples is the only volume control there is.
func mp3ToSpeakerPCM(data []byte, gain float64) ([]byte, error) {
	dec, err := mp3.NewDecoder(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(dec)
	if err != nil {
		return nil, err
	}
	src := dec.SampleRate()
	if src <= 0 {
		return nil, fmt.Errorf("bad sample rate")
	}
	n := len(raw) / 4 // stereo frames
	if n == 0 {
		return nil, fmt.Errorf("empty audio")
	}
	sample := func(frame, ch int) float64 {
		i := frame*4 + ch*2
		return float64(int16(uint16(raw[i]) | uint16(raw[i+1])<<8))
	}
	outN := int(int64(n) * announceRate / int64(src))
	out := make([]byte, outN*4)
	for i := 0; i < outN; i++ {
		pos := float64(i) * float64(src) / float64(announceRate)
		j := int(pos)
		frac := pos - float64(j)
		j2 := j + 1
		if j >= n {
			j = n - 1
		}
		if j2 >= n {
			j2 = n - 1
		}
		for ch := 0; ch < 2; ch++ {
			v := (sample(j, ch)*(1-frac) + sample(j2, ch)*frac) * gain
			if v > 32767 {
				v = 32767
			} else if v < -32768 {
				v = -32768
			}
			s := int16(v)
			out[i*4+ch*2] = byte(uint16(s))
			out[i*4+ch*2+1] = byte(uint16(s) >> 8)
		}
	}
	return out, nil
}

// playNotification triggers the firmware's ducked playback of a local PCM
// file — music resumes by itself afterwards.
func (p *Plugin) playNotification(path string) error {
	body := `<audioSource pathToFile="` + path + `"/>`
	req, _ := http.NewRequestWithContext(p.ctx, "POST", "http://"+p.speaker+"/playNotification", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("playNotification status %d", resp.StatusCode)
	}
	return nil
}
