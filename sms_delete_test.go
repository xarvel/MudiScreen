package main

import (
	"testing"
	"time"
)

// A tap on Delete on a message's page asks first; the message goes only
// after Yes.
func TestSMSDeleteTapAsksFirst(t *testing.T) {
	calls, restore := fakeRPC(t, smsList)
	defer restore()
	list, err := fetchSMS(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := testScreen(t, false)
	s.live = true // smsChange talks to GL's RPC (faked) only when live
	s.onSMS(smsResult{list: list})
	s.open("system", "sms:GMS1.B")
	if top := s.top(); top.page != "sms:GMS1.B" {
		t.Fatalf("message page not open: %+v", top)
	}
	sc := s.cur()
	s.renderScene(sc)
	if len(sc.hits) == 0 {
		t.Fatal("no tap areas on the message page")
	}
	// The delete button is the last tappable item of the page: run what a
	// tap on it runs.
	_, items, _ := s.appContent("system", "sms:GMS1.B")
	if b := items[len(items)-1]; b.Action == nil || b.Action.ID != "core.sms_delete" || b.Action.Confirm == "" {
		t.Fatalf("delete button %+v", b)
	}
	sc.hits[len(sc.hits)-1].fn()

	// Give the background RPC a moment.
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
	removed := false
	for _, c := range *calls {
		if c.method == "remove_sms" {
			removed = true
		}
	}
	if removed || s.modal == nil || s.modal.kind != "confirm" {
		kind := "none"
		if s.modal != nil {
			kind = s.modal.kind
		}
		t.Fatalf("one tap on Delete: remove_sms called = %v, dialog = %s; want no remove_sms and a confirm dialog", removed, kind)
	}
}
