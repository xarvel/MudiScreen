package main

import (
	"strconv"
	"testing"
	"time"
)

// elapsedSince is the {elapsed:T} token for a time ago before now.
func elapsedSince(now time.Time, ago time.Duration) string {
	return "{elapsed:" + strconv.FormatInt(now.Add(-ago).Unix(), 10) + "}"
}

func TestLiveText(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	at := func(sec int) string { return elapsedSince(now, time.Duration(sec)*time.Second) }
	for in, want := range map[string]string{
		"In a call · " + at(151): "In a call · 2:31",
		at(0):                    "0:00",
		at(3725):                 "1:02:05",
		at(-30):                  "0:00", // the app's clock ahead of the screen's
		at(59) + " / " + at(61):  "0:59 / 1:01",
		"no time {elapsed:}":     "no time {elapsed:}",
		"{elapsed:abc}":          "{elapsed:abc}",
		"Calls are on":           "Calls are on",
	} {
		if got := liveText(in, now); got != want {
			t.Errorf("liveText(%q) = %q, want %q", in, got, want)
		}
	}
}

// A view with live time: the home card, the lock screen and the page show
// the time since then, the app's own view stays as it was sent, and while
// it is there the screen draws again every second.
func TestLiveViewRuns(t *testing.T) {
	s, clk := testScreen(t, false)
	st := &appState{m: Manifest{ID: "volte", Name: "Calls"}, running: map[string]bool{}}
	s.apps, s.appByID["volte"] = []*appState{st}, st
	start := elapsedSince(clk.t, 151*time.Second)
	v, err := parseView([]byte(`{"card":{"title":"In a call · ` + start + `"},
	  "lock":[{"text":"In a call · ` + start + `"}],
	  "pages":{"main":{"title":"Calls","items":[{"type":"info","label":"Talking","value":"` + start + `"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	s.onAppResult(appResult{id: "volte", gen: s.gen, view: v})
	if !st.ticking {
		t.Fatal("the view is not seen as live")
	}
	var card, lock string
	for _, av := range s.views() {
		if av.app == "volte" {
			card, lock = av.v.Card.Title, av.v.Lock[0].Text
		}
	}
	if card != "In a call · 2:31" || lock != "In a call · 2:31" {
		t.Fatalf("card %q, lock %q", card, lock)
	}
	if _, items, _ := s.appContent("volte", "main"); len(items) != 1 || items[0].Value != "2:31" {
		t.Fatalf("page %+v", items)
	}
	if st.view.Card.Title == card || st.view.Pages["main"].Items[0].Value != start {
		t.Fatal("the app's own view was changed")
	}

	// The screen on: every tick draws again; off, a tick does not.
	s.onTick() // the minute is noted
	for i := 0; i < 3; i++ {
		gen := s.sceneGen
		clk.add(time.Second)
		s.onTick()
		if s.sceneGen == gen {
			t.Fatalf("tick %d: live time did not draw again", i)
		}
	}
	s.screenOn = false
	gen := s.sceneGen
	clk.add(time.Second)
	s.onTick()
	if s.sceneGen != gen {
		t.Fatal("drawn again with the screen off")
	}

	// The call over: the view has no live time, and a tick draws nothing.
	s.screenOn = true
	s.onAppResult(appResult{id: "volte", gen: s.gen, view: View{Card: &Card{Title: "Calls are on"}}})
	gen = s.sceneGen
	clk.add(time.Second)
	s.onTick()
	if st.ticking || s.sceneGen != gen {
		t.Fatalf("still drawn every second after the call (ticking %v)", st.ticking)
	}
}

// The live time goes through the app's catalog: a translated title keeps
// its token, and the time shows in it.
func TestLiveTextTranslated(t *testing.T) {
	c := loadCatalog([]string{"i18n/ru/volte.tsv"})
	if c == nil {
		t.Fatal("no Russian catalog for volte")
	}
	now := time.Unix(1_800_000_000, 0)
	start := elapsedSince(now, 75*time.Second)
	v := View{Card: &Card{Title: "In a call · " + start, Subtitle: "hidden number"},
		Lock:  []Line{{Text: "Incoming call"}, {Text: "Calling…"}},
		Pages: map[string]*Page{"main": {Title: "Calls", Items: []Item{{Type: "info", Label: "Talking", Value: start}}}}}
	c.apply(&v)
	lv := liveView(v, now)
	if lv.Card.Title != "Разговор · 1:15" || lv.Card.Subtitle != "скрытый номер" ||
		lv.Lock[0].Text != "Входящий звонок" || lv.Lock[1].Text != "Вызов…" {
		t.Fatalf("card %+v, lock %+v", lv.Card, lv.Lock)
	}
	if it := liveItems(v.Pages["main"].Items, now)[0]; it.Label != "Разговор" || it.Value != "1:15" {
		t.Fatalf("item %+v", it)
	}
}

// A notice with an id goes when its app takes it back; another id or none
// leaves it, and a notice without an id stays until its time is up.
func TestNotifyDismiss(t *testing.T) {
	s, _ := testScreen(t, false)
	callUbus(s, "notify", map[string]any{"id": "volte-call", "title": "Incoming call", "app": "volte", "page": "main"})
	if s.modal == nil || s.modal.kind != "notify" || s.modal.id != "volte-call" {
		t.Fatalf("modal %+v", s.modal)
	}
	callUbus(s, "dismiss", map[string]any{"id": "volte-missed"})
	callUbus(s, "dismiss", map[string]any{})
	if s.modal == nil {
		t.Fatal("a dismiss for another notice took this one back")
	}
	callUbus(s, "dismiss", map[string]any{"id": "volte-call"})
	if s.modal != nil {
		t.Fatalf("still shown: %+v", s.modal)
	}
	callUbus(s, "notify", map[string]any{"title": "Missed call"})
	callUbus(s, "dismiss", map[string]any{"id": ""})
	if s.modal == nil {
		t.Fatal("a notice without an id was taken back")
	}
	// A dismiss leaves any other modal alone.
	s.setModal(&Modal{kind: "power"})
	callUbus(s, "dismiss", map[string]any{"id": "volte-call"})
	if s.modal == nil || s.modal.kind != "power" {
		t.Fatalf("modal %+v", s.modal)
	}
}
