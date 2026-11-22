package main

// Live time in the texts of a view: {elapsed:T}, T a Unix time in seconds,
// shows as the time since then (2:31, 1:02:05 after an hour), and the screen
// keeps it running, a second at a time, without asking the app again. For a
// call that is on, a timer, anything that counts up.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var liveRe = regexp.MustCompile(`\{elapsed:([0-9]{1,12})\}`)

// liveText puts the time since T in place of each {elapsed:T} in s.
func liveText(s string, now time.Time) string {
	if !strings.Contains(s, "{elapsed:") {
		return s
	}
	return liveRe.ReplaceAllStringFunc(s, func(m string) string {
		t, _ := strconv.ParseInt(m[len("{elapsed:"):len(m)-1], 10, 64)
		d := int64(now.Sub(time.Unix(t, 0)) / time.Second)
		if d < 0 {
			d = 0
		}
		if d >= 3600 {
			return fmt.Sprintf("%d:%02d:%02d", d/3600, d/60%60, d%60)
		}
		return fmt.Sprintf("%d:%02d", d/60, d%60)
	})
}

// hasLive tells a view with live time in it.
func hasLive(v *View) bool {
	b, err := json.Marshal(v)
	return err == nil && bytes.Contains(b, []byte("{elapsed:"))
}

// liveView is v with the live time of its card, tiles and lock screen lines
// put in for now. The view as the app sent it stays as it is.
func liveView(v View, now time.Time) View {
	if v.Card != nil {
		c := *v.Card
		c.Title, c.Subtitle = liveText(c.Title, now), liveText(c.Subtitle, now)
		v.Card = &c
	}
	if len(v.Tiles) > 0 {
		tiles := make([]Tile, len(v.Tiles))
		for i, t := range v.Tiles {
			t.Label, t.Value = liveText(t.Label, now), liveText(t.Value, now)
			tiles[i] = t
		}
		v.Tiles = tiles
	}
	if len(v.Lock) > 0 {
		lines := make([]Line, len(v.Lock))
		for i, l := range v.Lock {
			l.Text = liveText(l.Text, now)
			lines[i] = l
		}
		v.Lock = lines
	}
	return v
}

// liveItems is items with their live time put in for now.
func liveItems(items []Item, now time.Time) []Item {
	out := make([]Item, len(items))
	for i, it := range items {
		it.Label, it.Value = liveText(it.Label, now), liveText(it.Value, now)
		it.Sub, it.Detail = liveText(it.Sub, now), liveText(it.Detail, now)
		if len(it.Stats) > 0 {
			stats := make([]Stat, len(it.Stats))
			for j, st := range it.Stats {
				st.Value = liveText(st.Value, now)
				stats[j] = st
			}
			it.Stats = stats
		}
		out[i] = it
	}
	return out
}

// anyTicking tells whether an app shows live time now, so that the screen
// draws again every second.
func (s *Screen) anyTicking() bool {
	for _, st := range s.apps {
		if st.ticking && st.haveView {
			return true
		}
	}
	return false
}
