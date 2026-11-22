package main

import (
	"path/filepath"
	"testing"
	"time"
)

// An app's items on the screen's own pages (a note on a message, Settings)
// may use only core.power, core.lock, core.sleep and core.home: an app
// cannot delete or mark messages, or change the language, in the core's
// name.
func TestAppItemCannotRunCoreSMSActions(t *testing.T) {
	calls, restore := fakeRPC(t, smsList)
	defer restore()
	list, err := fetchSMS(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "apps", "notes", "app.json"), `{"id":"notes","name":"Notes","command":"/bin/false"}`)
	s, _ := testScreen(t, false)
	s.appDirs = []string{filepath.Join(dir, "apps")}
	ms, _ := loadManifests(s.appDirs, true)
	st := &appState{m: ms[0], running: map[string]bool{}}
	s.apps, s.appByID["notes"] = []*appState{st}, st
	s.onSMS(smsResult{list: list})
	// A note on message A whose button says "Track".
	v, err := parseView([]byte(`{"sms":{"GMS1.A":{"items":[
	  {"type":"button","label":"Track","action":{"id":"core.sms_delete","args":["GMS1.B"]}}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	s.onAppResult(appResult{id: "notes", gen: s.gen, view: v})
	s.live = true
	s.open("system", "sms:GMS1.A")
	sc := s.cur()
	s.renderScene(sc)
	if len(sc.hits) != 2 {
		t.Fatalf("want 2 tap areas (the app's button, Delete), got %d", len(sc.hits))
	}
	sc.hits[0].fn() // the person taps "Track"

	deadline := time.After(2 * time.Second)
wait:
	for {
		select {
		case r := <-s.smsCh:
			if r.action == "delete" {
				break wait
			}
		case <-deadline:
			break wait
		}
	}
	for _, c := range *calls {
		if c.method == "remove_sms" {
			t.Fatalf("the app's \"Track\" button on message A ran core.sms_delete: remove_sms %v (no question, the app's command not run)", c.params)
		}
	}
}
