package main

import (
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An app adds to the SMS inbox and to the Settings page; taps on its items
// go to it.
func TestAppNotesAndSettings(t *testing.T) {
	_, restore := fakeRPC(t, smsList)
	defer restore()
	list, err := fetchSMS(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "apps", "notes", "app.json"), `{"id":"notes","name":"Notes","command":"/bin/true"}`)
	writeFile(t, filepath.Join(dir, "apps", "notes", "i18n", "ru.tsv"), "Checked\tПроверено\n")
	s := newScreen(options{}, nil, log.New(io.Discard, "", 0))
	s.loc = time.UTC
	s.now = func() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) }
	s.appDirs = []string{filepath.Join(dir, "apps")}
	s.lang = "ru"
	ms, _ := loadManifests(s.appDirs, true)
	st := &appState{m: ms[0], running: map[string]bool{}, cat: loadCatalog(catalogPaths(ms[0], "ru", s.appDirs))}
	s.apps, s.appByID["notes"] = []*appState{st}, st
	s.onSMS(smsResult{list: list})

	raw := `{"sms": {
	  "GMS1.A": {"preview": "A short version", "text_label": "Original", "text_color": "dim",
	             "items": [{"type": "section", "label": "Checked"}, {"type": "text", "label": "A note on it"},
	                       {"type": "button", "label": "Again", "action": {"id": "again", "args": ["GMS1.A"]}}]},
	  "GMS1.gone": {"preview": "for a message that is not there"},
	  "GMS1.null": null},
	 "settings": [{"type": "toggle", "label": "Do it", "checked": true, "action": {"id": "toggle"}}]}`
	v, err := parseView([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.SMS["GMS1.null"]; ok {
		t.Fatal("a null note is kept")
	}
	s.onAppResult(appResult{id: "notes", gen: s.gen, view: v})

	pages := s.smsPages()
	var sub string
	for _, it := range pages["sms"].Items {
		if it.Open == "sms:GMS1.A" {
			sub = it.Sub
		}
	}
	if sub != "A short version" {
		t.Fatalf("preview %q", sub)
	}
	var got []string
	for _, it := range pages["sms:GMS1.A"].Items {
		got = append(got, it.Type+":"+it.Label+":"+it.Color+":"+it.app)
	}
	want := "info:От::|info:Дата::|section:Проверено::notes|text:A note on it::notes|button:Again::notes|" +
		"section:Original::|text:Your parcel:dim:|text:is waiting at the office.:dim:|button:Удалить::"
	if j := strings.Join(got, "|"); j != want {
		t.Fatalf("message page\n got %s\nwant %s", j, want)
	}
	// A message without a note is as before.
	for _, it := range pages["sms:GMS1.B"].Items {
		if it.app != "" || it.Type == "section" && it.Label != "Текст" {
			t.Fatalf("a message without a note: %+v", it)
		}
	}

	var section, toggle *Item
	for _, it := range s.settingsPage().Items {
		it := it
		switch {
		case it.Type == "section" && it.Label == "Notes":
			section = &it
		case it.Type == "toggle":
			toggle = &it
		}
	}
	if section == nil || toggle == nil || toggle.app != "notes" || toggle.Action.ID != "toggle" {
		t.Fatalf("settings: %+v %+v", section, toggle)
	}
	// The app's own view keeps no owner: only the copies on the screen's pages.
	if st.view.Settings[0].app != "" || st.view.SMS["GMS1.A"].Items[0].app != "" {
		t.Fatal("the app's view was changed")
	}
}

// A message opened through the passcode (notify's "Open", ubus show) is
// marked read, as one opened without it is.
func TestSMSOpenedThroughPasscodeIsRead(t *testing.T) {
	calls, restore := fakeRPC(t, smsList)
	defer restore()
	list, err := fetchSMS(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	s := newScreen(options{}, nil, log.New(io.Discard, "", 0))
	s.loc = time.UTC
	s.live = true
	s.onSMS(smsResult{list: list})
	s.cfg = ScreenConfig{PasscodeOn: true, Passcode: "1234"}
	s.jump("lock", nil)
	s.open("system", "sms:GMS1.A")
	if s.page != "pin" || s.smsByName("GMS1.A").Status != 0 {
		t.Fatalf("before the passcode: page %q, status %d", s.page, s.smsByName("GMS1.A").Status)
	}
	for _, k := range []string{"1", "2", "3", "4"} {
		s.pinKey(k)
	}
	if s.page != "app" || s.smsByName("GMS1.A").Status != 1 {
		t.Fatalf("after the passcode: page %q, status %d", s.page, s.smsByName("GMS1.A").Status)
	}
	for range 2 { // the mark, and the list asked for on open
		select {
		case <-s.smsCh:
		case <-time.After(5 * time.Second):
			t.Fatal("no RPC result")
		}
	}
	marked := false
	for _, c := range *calls {
		marked = marked || c.method == "set_sms" && c.params["name"] == "GMS1.A"
	}
	if !marked {
		t.Fatalf("set_sms not called: %+v", *calls)
	}
}
