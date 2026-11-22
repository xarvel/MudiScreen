package main

import (
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"
)

// smsScreen is a screen with the inbox of smsList, and the RPC calls it
// makes.
func smsScreen(t *testing.T) (*Screen, *[]rpcCall) {
	t.Helper()
	calls, restore := fakeRPC(t, smsList)
	t.Cleanup(restore)
	list, err := fetchSMS(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	s := newScreen(options{}, nil, log.New(io.Discard, "", 0))
	s.loc = time.UTC
	s.now = func() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) }
	s.live = true
	s.onSMS(smsResult{list: list})
	return s, calls
}

func deletedSMS(s *Screen) string {
	select {
	case x := <-s.smsCh:
		if x.action == "delete" {
			return x.name
		}
	case <-time.After(500 * time.Millisecond):
	}
	return ""
}

// The Delete button's action asks first, the core's own actions included.
func TestSMSDeleteActionAsksFirst(t *testing.T) {
	s, _ := smsScreen(t)
	p := s.smsPages()["sms:GMS1.B"]
	del := p.Items[len(p.Items)-1]
	if del.Action == nil || del.Action.Confirm == "" {
		t.Fatalf("no confirm on the button: %+v", del)
	}
	// The same call the hit of drawItem makes.
	s.tapTarget("system", del.Open, del.Action)
	if name := deletedSMS(s); name != "" {
		t.Fatalf("one tap deleted %s without the question %q (modal %+v)", name, del.Action.Confirm, s.modal)
	}
	if s.modal == nil || s.modal.kind != "confirm" {
		t.Fatalf("no confirm modal: %+v", s.modal)
	}
}

// A button in an app's note on one message cannot delete another message:
// core.sms_* is not for apps.
func TestNoteItemCannotDeleteOtherMessage(t *testing.T) {
	s, _ := smsScreen(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "apps", "notes", "app.json"), `{"id":"notes","name":"Notes","command":"/bin/true"}`)
	s.appDirs = []string{filepath.Join(dir, "apps")}
	ms, _ := loadManifests(s.appDirs, true)
	st := &appState{m: ms[0], running: map[string]bool{}}
	s.apps, s.appByID["notes"] = []*appState{st}, st
	v, err := parseView([]byte(`{"sms":{"GMS1.A":{"items":[{"type":"button","label":"Track again",
	  "action":{"id":"core.sms_delete","args":["GMS1.B"],"confirm":"Delete?"}}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	s.onAppResult(appResult{id: "notes", gen: s.gen, view: v})
	var btn *Item
	for _, it := range s.smsPages()["sms:GMS1.A"].Items {
		if it.app == "notes" {
			it := it
			btn = &it
		}
	}
	if btn == nil {
		t.Fatal("no note item")
	}
	s.tapTarget(btn.app, btn.Open, btn.Action)
	if name := deletedSMS(s); name != "" {
		t.Fatalf("an app's note item on GMS1.A deleted %s at one tap", name)
	}
}
