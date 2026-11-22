package main

import (
	"encoding/json"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

type rpcCall struct {
	module, method string
	params         map[string]any
}

func fakeRPC(t *testing.T, list string) (*[]rpcCall, func()) {
	var mu sync.Mutex
	var calls []rpcCall
	old := glRPC
	glRPC = func(module, method string, params map[string]any, out any) error {
		mu.Lock()
		calls = append(calls, rpcCall{module, method, params})
		mu.Unlock()
		if method == "get_sms_list" {
			return json.Unmarshal([]byte(list), out)
		}
		return nil
	}
	return &calls, func() { glRPC = old }
}

const smsList = `{"list":[
 {"phone_number":"Courier","type":2,"name":"GMS1.A","slot":1,"date":"26-11-06 13:21:18","bus":"cpu","status":0,"body":"Your parcel\nis waiting at the office."},
 {"phone_number":"+447700900123","type":2,"name":"GMS1.B","slot":1,"date":"26-11-21 09:05:00","bus":"cpu","status":1,"body":"See you at nine"},
 {"phone_number":"+447700900456","type":0,"name":"GMS1.C","slot":1,"date":"26-11-20 18:00:00","bus":"cpu","status":3,"body":"On my way"}]}`

func TestSMS(t *testing.T) {
	calls, restore := fakeRPC(t, smsList)
	defer restore()
	loc := time.UTC
	list, err := fetchSMS(loc)
	if err != nil || len(list) != 3 || list[0].Name != "GMS1.B" || list[2].Name != "GMS1.A" {
		t.Fatalf("list %v %+v", err, list)
	}

	s := newScreen(options{}, nil, log.New(io.Discard, "", 0))
	s.loc = loc
	s.now = func() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, loc) }
	s.live = true
	s.onSMS(smsResult{list: list})
	pages := s.smsPages()
	inbox := pages["sms"]
	var rows []string
	for _, it := range inbox.Items {
		rows = append(rows, it.Type+"|"+it.Label+"|"+it.Sub+"|"+it.Value+"|"+it.Icon)
	}
	joined := strings.Join(rows, "\n")
	for _, want := range []string{
		"link|Mark all read||1 new|check",
		"section|Received (2)|||",
		"entry|+447700900123|See you at nine|09:05|",
		"entry|Courier|Your parcel is waiting at the office.|06.11 13:21|dot",
		"section|Sent (1)|||",
		"entry|+447700900456|On my way|20.11 18:00|",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	detail := pages["sms:GMS1.A"]
	if detail == nil || detail.Title != "Courier" {
		t.Fatalf("detail %+v", detail)
	}
	var texts []string
	for _, it := range detail.Items {
		if it.Type == "text" {
			texts = append(texts, it.Label)
		}
	}
	if strings.Join(texts, "|") != "Your parcel|is waiting at the office." {
		t.Fatalf("paragraphs %q", texts)
	}
	if b := detail.Items[len(detail.Items)-1]; b.Action == nil || b.Action.ID != "core.sms_delete" || b.Action.Confirm == "" {
		t.Fatalf("delete button %+v", b)
	}

	// Opening an unread message marks it read, as the web admin does.
	s.onSMSPage("sms:GMS1.A")
	if s.smsByName("GMS1.A").Status != 1 {
		t.Fatal("not marked read on the screen")
	}
	r := <-s.smsCh // the list asked for on open, or the mark
	r2 := <-s.smsCh
	var mark smsResult
	for _, x := range []smsResult{r, r2} {
		if x.action == "read" {
			mark = x
		}
	}
	if mark.name != "GMS1.A" {
		t.Fatalf("no mark result: %+v %+v", r, r2)
	}
	found := false
	for _, c := range *calls {
		if c.method == "set_sms" && c.params["name"] == "GMS1.A" && c.params["status"] == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("set_sms not called: %+v", *calls)
	}

	// Delete goes through remove_sms with GL's scope for one message.
	s.smsCoreAction("core.sms_delete", "GMS1.B")
	for x := range s.smsCh {
		if x.action == "delete" {
			break
		}
	}
	found = false
	for _, c := range *calls {
		if c.method == "remove_sms" && c.params["name"] == "GMS1.B" && c.params["scope"] == 10 {
			found = true
		}
	}
	if !found {
		t.Fatalf("remove_sms not called: %+v", *calls)
	}
	s.smsCoreAction("core.sms_readall", "")
	for x := range s.smsCh {
		if x.action == "readall" {
			break
		}
	}
	if c := (*calls)[len(*calls)-1]; c.method != "set_sms" || c.params["status"] != 6 {
		t.Fatalf("read all: %+v", c)
	}
	if got := smsParagraphs(strings.Repeat("word ", 200)); len(got) < 3 {
		t.Fatalf("a long message is not split: %d", len(got))
	}
}
