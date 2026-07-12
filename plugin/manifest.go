package plugin

import (
	"strconv"
	"time"
)

type Manifest struct {
	Title    string    `json:"title"`
	Status   Status    `json:"status"`
	Sections []Section `json:"sections"`
}

type Status struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

type Section struct {
	Title   string   `json:"title"`
	Text    string   `json:"text,omitempty"`
	Fields  []Field  `json:"fields,omitempty"`
	Actions []Action `json:"actions,omitempty"`
}

type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Value       any      `json:"value,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []Option `json:"options,omitempty"` // for type "select"
	// For type "slider". Older ReTouch hosts render unknown field types as a
	// text input, so the field degrades to the previous free-form number.
	Min  int    `json:"min,omitempty"`
	Max  int    `json:"max,omitempty"`
	Step int    `json:"step,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// Option is one choice of a select field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

type Action struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Style string `json:"style,omitempty"`
}

func (p *Plugin) manifestLocked() Manifest {
	lang := p.lang
	status := Status{Level: "idle", Text: tr(lang, "status.configure")}
	if p.lastErr != "" {
		status = Status{Level: "error", Text: p.lastErr}
	} else if len(p.pickups) > 0 {
		next := p.pickups[0]
		note := ""
		if _, show := visiblePickup(p.pickups, time.Now(), p.cfg.AlwaysShow); !show {
			note = tr(lang, "status.hidden")
		}
		status = Status{Level: "ok", Text: tr(lang, "status.next", wasteName(next, lang), next.Date.Format("02-01-2006")) + note}
	} else if p.cfg.Enabled {
		status = Status{Level: "warn", Text: tr(lang, "status.loading")}
	}
	displayText := tr(lang, "text.display")
	if !p.hasOLED {
		displayText = tr(lang, "status.nooled") + " " + displayText
	}
	// 0 means "no level set"; announce.go plays that at defaultAnnounceVolume,
	// so the slider starts at the effective value. Clamp into the /speaker window
	// so an old config's out-of-range value doesn't land past the slider max.
	announceVol := p.cfg.AnnounceVolume
	if announceVol <= 0 {
		announceVol = defaultAnnounceVolume
	} else if announceVol > 70 {
		announceVol = 70
	}
	return Manifest{
		Title:  "Afvalwijzer",
		Status: status,
		Sections: []Section{
			{
				Title: tr(lang, "section.address"),
				Text:  tr(lang, "section.text"),
				Fields: []Field{
					{Key: "provider", Label: tr(lang, "field.provider"), Type: "select", Value: p.cfg.Provider, Options: providerOptions()},
					{Key: "postcode", Label: tr(lang, "field.postcode"), Type: "text", Value: p.cfg.Postcode, Placeholder: "1234AB"},
					{Key: "houseNumber", Label: tr(lang, "field.housenumber"), Type: "text", Value: p.cfg.HouseNumber, Placeholder: "12"},
					{Key: "suffix", Label: tr(lang, "field.suffix"), Type: "text", Value: p.cfg.Suffix, Placeholder: "A"},
				},
			},
			{
				Title: tr(lang, "section.display"),
				Text:  displayText,
				Fields: []Field{
					{Key: "enabled", Label: tr(lang, "field.enabled"), Type: "toggle", Value: p.cfg.Enabled},
					{Key: "alwaysShow", Label: tr(lang, "field.alwaysshow"), Type: "toggle", Value: p.cfg.AlwaysShow},
				},
				Actions: []Action{
					{ID: "test", Label: tr(lang, "action.test")},
					{ID: "clear", Label: tr(lang, "action.clear")},
				},
			},
			{
				Title: tr(lang, "section.announce"),
				Text:  tr(lang, "text.announce"),
				Fields: []Field{
					{Key: "announceTimes", Label: tr(lang, "field.announcetimes"), Type: "times", Value: p.cfg.AnnounceTimes, Placeholder: "08:00, 18:00"},
					// Value as a string: save() round-trips inputs through str(),
					// which only reads strings — and the old text renderer shows
					// it the same way.
					{Key: "announceVolume", Label: tr(lang, "field.announcevolume"), Type: "slider", Value: strconv.Itoa(announceVol), Min: 10, Max: 70, Step: 5, Unit: "%"},
				},
				Actions: []Action{
					{ID: "announce", Label: tr(lang, "action.announce")},
				},
			},
			{
				Actions: []Action{
					{ID: "save", Label: tr(lang, "action.save"), Style: "primary"},
					{ID: "refresh", Label: tr(lang, "action.refresh")},
				},
			},
		},
	}
}

// providerOptions lists the supported providers for the select field, in
// fixed order with their human names. Older ReTouch hosts render the field
// as a text input, where the stored value keeps working as before.
func providerOptions() []Option {
	out := make([]Option, 0, len(providerOrder))
	for _, key := range providerOrder {
		out = append(out, Option{Value: key, Label: providers[key].Name})
	}
	return out
}
