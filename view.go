package main

// The view model, API version 1: what an app prints for `<command> status`.
// Every field is optional and unknown fields are ignored, so that apps
// written for a newer screen still work on an older one. The screen decides
// how things look; apps only say what to show. See docs/EXTENSIONS.md.

import (
	"bytes"
	"encoding/json"
	"image/color"
	"maps"
	"slices"
	"strconv"
	"strings"
)

const apiVersion = 1

type View struct {
	Card     *Card               `json:"card,omitempty"`     // card on the home screen
	Tiles    []Tile              `json:"tiles,omitempty"`    // small tiles on the home screen
	Lock     []Line              `json:"lock,omitempty"`     // lines on the lock screen
	Status   []Indicator         `json:"status,omitempty"`   // icons in the status bar
	Pages    map[string]*Page    `json:"pages,omitempty"`    // pages the app can open
	SMS      map[string]*SMSNote `json:"sms,omitempty"`      // notes on messages of the SMS inbox
	Settings []Item              `json:"settings,omitempty"` // items on the screen's Settings page
}

// SMSNote is what an app adds to a message of the screen's SMS inbox.
// Messages are named as in GL's list (modem.get_sms_list, "name").
type SMSNote struct {
	Preview   string `json:"preview,omitempty"`    // the inbox shows it in place of the text's start
	Items     []Item `json:"items,omitempty"`      // on the message's page, above the text
	TextLabel string `json:"text_label,omitempty"` // heading of the text (default "Text")
	TextColor string `json:"text_color,omitempty"` // colour of the text
}

type Card struct {
	Title         string  `json:"title"`
	Subtitle      string  `json:"subtitle,omitempty"`
	Icon          string  `json:"icon,omitempty"`
	Color         string  `json:"color,omitempty"` // icon colour
	SubtitleColor string  `json:"subtitle_color,omitempty"`
	Open          string  `json:"open,omitempty"` // page to open on tap
	Action        *Action `json:"action,omitempty"`
}

type Tile struct {
	Label  string  `json:"label"`
	Value  string  `json:"value,omitempty"`
	Icon   string  `json:"icon,omitempty"`
	Color  string  `json:"color,omitempty"`
	Open   string  `json:"open,omitempty"`
	Action *Action `json:"action,omitempty"`
}

type Line struct {
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
}

type Indicator struct {
	Icon  string `json:"icon"`
	Color string `json:"color,omitempty"`
}

type Page struct {
	Title string `json:"title"`
	Items []Item `json:"items"`
	Empty string `json:"empty,omitempty"` // shown when there are no items
}

// Series is one line of a chart; values fill the right end of the x axis.
type Series struct {
	Label  string    `json:"label,omitempty"`
	Color  string    `json:"color,omitempty"`
	Values []float64 `json:"values"`
}

// Option is one choice of a segments item.
type Option struct {
	Label   string  `json:"label"`
	Checked bool    `json:"checked,omitempty"`
	Action  *Action `json:"action,omitempty"`
}

// Stat is one number of a stats item.
type Stat struct {
	Label string `json:"label,omitempty"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
	Color string `json:"color,omitempty"`
}

// Item types: section, info, text, entry, radio, toggle, button, link, bar,
// chart, segments, stats.
type Item struct {
	Type     string  `json:"type"`
	Label    string  `json:"label,omitempty"`
	Value    string  `json:"value,omitempty"`
	Detail   string  `json:"detail,omitempty"`
	Sub      string  `json:"sub,omitempty"` // a second, smaller line (entry, link, bar)
	Color    string  `json:"color,omitempty"`
	Icon     string  `json:"icon,omitempty"`
	Checked  bool    `json:"checked,omitempty"`
	Busy     bool    `json:"busy,omitempty"`
	Disabled bool    `json:"disabled,omitempty"`
	Style    string  `json:"style,omitempty"` // button: primary, danger, plain; chart: line, bars
	Open     string  `json:"open,omitempty"`
	Action   *Action `json:"action,omitempty"`

	Fraction  float64  `json:"fraction,omitempty"`  // bar: filled part, 0..1
	Fraction2 float64  `json:"fraction2,omitempty"` // bar: a second part after the first
	Color2    string   `json:"color2,omitempty"`    // bar: colour of the second part
	XStart    string   `json:"x_start,omitempty"`   // chart: label under the left end
	XEnd      string   `json:"x_end,omitempty"`     // chart: label under the right end
	Options   []Option `json:"options,omitempty"`   // segments
	Stats     []Stat   `json:"stats,omitempty"`     // stats: up to 3
	Unit      string   `json:"unit,omitempty"`      // chart: unit of the values
	Span      int      `json:"span,omitempty"`      // chart: points on the x axis
	Max       float64  `json:"max,omitempty"`       // chart: top of the y axis; default from the values
	Series    []Series `json:"series,omitempty"`    // chart: up to 4 lines

	app string // the app an item on a page of the screen's own belongs to
}

// Action is run as `<command> action <id> <args...>`. IDs starting with
// "core." are handled by the screen itself (core.power, core.lock,
// core.sleep, core.home).
type Action struct {
	ID           string   `json:"id"`
	Args         []string `json:"args,omitempty"`
	Confirm      string   `json:"confirm,omitempty"`       // ask this first
	ConfirmLabel string   `json:"confirm_label,omitempty"` // text of the yes button
	Danger       bool     `json:"danger,omitempty"`        // red yes button
}

func (a *Action) key() string {
	if a == nil {
		return ""
	}
	return a.ID + "\x00" + strings.Join(a.Args, "\x00")
}

const (
	maxTiles   = 6
	maxLock    = 4
	maxStatus  = 3
	maxPages   = 32
	maxItems   = 200
	maxSeries  = 4
	maxPoints  = 600
	maxOptions = 5
	maxStats   = 3
	maxNotes   = 500 // messages with notes
	maxNoteIts = 40  // items of a note
	maxSetIts  = 20  // items on the Settings page
)

func parseView(b []byte) (View, error) {
	var v View
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return v, nil // an app may have nothing to show right now
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return View{}, err
	}
	if len(v.Tiles) > maxTiles {
		v.Tiles = v.Tiles[:maxTiles]
	}
	if len(v.Lock) > maxLock {
		v.Lock = v.Lock[:maxLock]
	}
	if len(v.Status) > maxStatus {
		v.Status = v.Status[:maxStatus]
	}
	// Past a limit, the same names go on every poll (in name order), not
	// whichever the map gives first.
	n := 0
	for _, name := range slices.Sorted(maps.Keys(v.Pages)) {
		p := v.Pages[name]
		if p == nil || n >= maxPages {
			delete(v.Pages, name)
			continue
		}
		n++
		p.Items = clampItems(p.Items, maxItems)
	}
	n = 0
	for _, name := range slices.Sorted(maps.Keys(v.SMS)) {
		note := v.SMS[name]
		if note == nil || n >= maxNotes {
			delete(v.SMS, name)
			continue
		}
		n++
		note.Items = clampItems(note.Items, maxNoteIts)
	}
	v.Settings = clampItems(v.Settings, maxSetIts)
	return v, nil
}

func clampItems(items []Item, most int) []Item {
	if len(items) > most {
		items = items[:most]
	}
	for i := range items {
		it := &items[i]
		if len(it.Series) > maxSeries {
			it.Series = it.Series[:maxSeries]
		}
		for j := range it.Series {
			if v := it.Series[j].Values; len(v) > maxPoints {
				it.Series[j].Values = v[len(v)-maxPoints:]
			}
		}
		it.Span = min(max(it.Span, 0), maxPoints)
		if len(it.Options) > maxOptions {
			it.Options = it.Options[:maxOptions]
		}
		if len(it.Stats) > maxStats {
			it.Stats = it.Stats[:maxStats]
		}
	}
	return items
}

// owned copies an app's items for a page of the screen's own (Settings, a
// message), so that taps on them go to the app.
func owned(app string, items []Item) []Item {
	out := make([]Item, len(items))
	copy(out, items)
	for i := range out {
		out[i].app = app
	}
	return out
}

var namedColors = map[string]color.RGBA{
	"green":  colGreen,
	"yellow": colYellow,
	"red":    colRed,
	"blue":   colBlue,
	"orange": colOrange,
	"purple": colPurple,
	"text":   colText,
	"white":  colText,
	"dim":    colDim,
	"grey":   colDim,
	"gray":   colDim,
	"faint":  colFaint,
}

// parseColor takes a colour name or #rrggbb.
func parseColor(s string, def color.RGBA) color.RGBA {
	if c, ok := namedColors[strings.ToLower(s)]; ok {
		return c
	}
	if len(s) == 7 && s[0] == '#' {
		if v, err := strconv.ParseUint(s[1:], 16, 32); err == nil {
			return rgb(uint8(v>>16), uint8(v>>8), uint8(v))
		}
	}
	return def
}
