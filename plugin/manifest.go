package plugin

import "time"

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
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
	Value any    `json:"value,omitempty"`
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
	text := tr(lang, "section.text")
	if !p.hasOLED {
		text = tr(lang, "status.nooled") + " " + text
	}
	return Manifest{
		Title:  "Afvalwijzer",
		Status: status,
		Sections: []Section{
			{
				Title: tr(lang, "section.address"),
				Text:  text,
				Fields: []Field{
					{Key: "provider", Label: tr(lang, "field.provider"), Type: "text", Value: p.cfg.Provider},
					{Key: "postcode", Label: tr(lang, "field.postcode"), Type: "text", Value: p.cfg.Postcode},
					{Key: "houseNumber", Label: tr(lang, "field.housenumber"), Type: "text", Value: p.cfg.HouseNumber},
					{Key: "suffix", Label: tr(lang, "field.suffix"), Type: "text", Value: p.cfg.Suffix},
					{Key: "enabled", Label: tr(lang, "field.enabled"), Type: "toggle", Value: p.cfg.Enabled},
					{Key: "alwaysShow", Label: tr(lang, "field.alwaysshow"), Type: "toggle", Value: p.cfg.AlwaysShow},
					{Key: "announceTimes", Label: tr(lang, "field.announcetimes"), Type: "text", Value: p.cfg.AnnounceTimes},
					{Key: "announceVolume", Label: tr(lang, "field.announcevolume"), Type: "number", Value: p.cfg.AnnounceVolume},
				},
				Actions: []Action{
					{ID: "save", Label: tr(lang, "action.save"), Style: "primary"},
					{ID: "refresh", Label: tr(lang, "action.refresh")},
					{ID: "test", Label: tr(lang, "action.test")},
					{ID: "announce", Label: tr(lang, "action.announce")},
					{ID: "clear", Label: tr(lang, "action.clear")},
				},
			},
		},
	}
}
