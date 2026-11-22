package main

// SMS on the screen: the inbox of the router's modem, read and kept through
// GL's own RPC (modem get_sms_list / set_sms / remove_sms), the calls its web
// admin makes, so that read marks and deletions agree with it. The pages
// are part of the built-in system app and open behind the passcode only.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type smsMsg struct {
	Name   string `json:"name"` // the spool file: GL's id of the message
	Number string `json:"phone_number"`
	Date   string `json:"date"` // "26-11-06 13:21:18"
	Status int    `json:"status"`
	Body   string `json:"body"`
	Bus    string `json:"bus"`
	Slot   int    `json:"slot"`

	at time.Time
}

// received is true for messages that came in (GL's status 0 unread,
// 1 read); other statuses are messages sent from the router.
func (m smsMsg) received() bool { return m.Status == 0 || m.Status == 1 }

type smsResult struct {
	list   []smsMsg
	err    error
	action string // "" for a list; "read", "readall", "delete" after a change
	name   string
}

// glRPC calls GL's RPC the way gl_screen does: on 127.0.0.1 with the
// "glinet" header, which the router accepts from itself without a login.
//
// The request goes to curl on stdin: it can hold a Wi-Fi password, which
// would be readable on a command line.
var glRPC = func(module, method string, params map[string]any, out any) error {
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "call",
		"params": []any{"", module, method, params}})
	if err != nil {
		return err
	}
	timeout := rpcTimeout(module, method)
	ctx, cancel := context.WithTimeout(context.Background(), timeout+2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "curl", "-s", "-m", strconv.Itoa(int(timeout/time.Second)),
		"-H", "glinet: 1", "-H", "Content-Type: application/json", "--data-binary", "@-", "http://127.0.0.1/rpc")
	cmd.Stdin = bytes.NewReader(req)
	raw, err := cmd.Output()
	if err != nil {
		return err
	}
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if r.Error != nil {
		return errors.New(r.Error.Message)
	}
	// Some modules answer a failure inside the result.
	var failed struct {
		Code int    `json:"err_code"`
		Msg  string `json:"err_msg"`
	}
	if json.Unmarshal(r.Result, &failed) == nil && failed.Code != 0 {
		if failed.Msg == "" {
			failed.Msg = fmt.Sprintf("error %d", failed.Code)
		}
		return errors.New(failed.Msg)
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// rpcTimeout is how long a call of GL's may take: a Wi-Fi scan or joining
// a network takes seconds.
func rpcTimeout(module, method string) time.Duration {
	switch module + "." + method {
	case "repeater.scan":
		return 40 * time.Second
	case "repeater.connect", "repeater.disconnect", "system.set_airplane_mode":
		return 30 * time.Second
	}
	return 10 * time.Second
}

func parseSMSDate(s string, loc *time.Location) time.Time {
	for _, f := range []string{"06-01-02 15:04:05", "2006-01-02 15:04:05", "06/01/02,15:04:05"} {
		if t, err := time.ParseInLocation(f, s, loc); err == nil {
			return t
		}
	}
	return time.Time{}
}

// fetchSMS reads the list, newest first.
func fetchSMS(loc *time.Location) ([]smsMsg, error) {
	var r struct {
		List []smsMsg `json:"list"`
	}
	if err := glRPC("modem", "get_sms_list", map[string]any{"bus": "cpu"}, &r); err != nil {
		return nil, err
	}
	for i := range r.List {
		r.List[i].at = parseSMSDate(r.List[i].Date, loc)
		if r.List[i].Bus == "" {
			r.List[i].Bus = "cpu"
		}
	}
	sort.SliceStable(r.List, func(i, j int) bool { return r.List[i].at.After(r.List[j].at) })
	return r.List, nil
}

// loadSMS asks for the list in the background.
func (s *Screen) loadSMS() {
	if s.smsLoading || !s.live {
		return
	}
	s.smsLoading = true
	s.smsAt = s.now()
	loc := s.loc
	go func() {
		list, err := fetchSMS(loc)
		s.smsCh <- smsResult{list: list, err: err}
	}()
}

// smsChange marks or deletes in the background, then reads the list again.
func (s *Screen) smsChange(action, name string, params map[string]any) {
	s.log.Printf("sms: %s %s", action, name)
	if !s.live {
		return
	}
	method := "set_sms"
	if action == "delete" {
		method = "remove_sms"
	}
	go func() {
		err := glRPC("modem", method, params, nil)
		s.smsCh <- smsResult{action: action, name: name, err: err}
	}()
}

func (s *Screen) onSMS(r smsResult) {
	if r.action != "" {
		if r.err != nil {
			s.log.Printf("sms: %s: %v", r.action, r.err)
			s.showToast(s.trf("error", r.err.Error()))
		} else if r.action == "delete" {
			s.showToast(s.tr("sms_deleted"))
			if rt := s.top(); rt.app == "system" && rt.page == "sms:"+r.name {
				s.back()
			}
		}
		s.smsLoading = false // a list on its way may predate the change
		s.loadSMS()
		s.requestRefresh() // the new-message count on the home page
		return
	}
	s.smsLoading = false
	if r.err != nil {
		s.smsErr = r.err.Error()
		s.log.Printf("sms: list: %v", r.err)
	} else {
		s.smsErr = ""
		s.sms = r.list
		s.smsLoaded = true
	}
	s.invalidate()
}

func (s *Screen) smsByName(name string) *smsMsg {
	for i := range s.sms {
		if s.sms[i].Name == name {
			return &s.sms[i]
		}
	}
	return nil
}

// onSMSPage runs when a page of the SMS opens: the list is read again, and
// an unread message that opens is marked read, as the web admin does.
func (s *Screen) onSMSPage(page string) {
	s.loadSMS()
	name, ok := strings.CutPrefix(page, "sms:")
	if !ok {
		return
	}
	if m := s.smsByName(name); m != nil && m.Status == 0 {
		m.Status = 1
		s.smsChange("read", name, map[string]any{"bus": m.Bus, "name": name, "status": 1})
	}
}

// smsWhen is a short date: the time today, day and month this year.
func (s *Screen) smsWhen(m smsMsg, long bool) string {
	if m.at.IsZero() {
		return m.Date
	}
	t := m.at.In(s.loc)
	now := s.now().In(s.loc)
	switch {
	case long:
		return t.Format("02.01.2006 15:04")
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("02.01 15:04")
	}
	return t.Format("02.01.06")
}

// smsPreview is the start of a message on one line.
func smsPreview(body string) string {
	return strings.Join(strings.Fields(body), " ")
}

// smsParagraphs splits a message into text items of a size a page shows
// whole (a text item takes up to 12 lines).
func smsParagraphs(body string) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		words := strings.Fields(para)
		for len(words) > 0 {
			n, size := 0, 0
			for n < len(words) && size+len(words[n]) < 280 {
				size += len(words[n]) + 1
				n++
			}
			if n == 0 {
				n = 1
			}
			out = append(out, strings.Join(words[:n], " "))
			words = words[n:]
		}
	}
	return out
}

// smsPages are the inbox and a page for every message.
func (s *Screen) smsPages() map[string]*Page {
	pages := map[string]*Page{}
	inbox := &Page{Title: s.tr("sms_title")}
	pages["sms"] = inbox
	switch {
	case !s.smsLoaded && s.smsErr != "":
		inbox.Empty = s.trf("sms_error", s.smsErr)
		return pages
	case !s.smsLoaded:
		inbox.Empty = s.tr("loading")
		return pages
	}
	var recv, sent []smsMsg
	unread := 0
	for _, m := range s.sms {
		if m.received() {
			recv = append(recv, m)
			if m.Status == 0 {
				unread++
			}
		} else {
			sent = append(sent, m)
		}
	}
	if len(recv)+len(sent) == 0 {
		inbox.Empty = s.tr("sms_none")
		return pages
	}
	if unread > 0 {
		inbox.Items = append(inbox.Items, Item{Type: "link", Label: s.tr("sms_mark_all"), Value: s.trf("sms_new", unread),
			Icon: "check", Color: "blue", Action: &Action{ID: "core.sms_readall"}})
	}
	row := func(m smsMsg) Item {
		who := m.Number
		if who == "" {
			who = s.tr("sms_hidden")
		}
		preview := m.Body
		if n := s.smsNote(m.Name); n.Preview != "" {
			preview = n.Preview
		}
		it := Item{Type: "entry", Label: who, Sub: smsPreview(preview), Value: s.smsWhen(m, false), Open: "sms:" + m.Name}
		if m.Status == 0 {
			it.Icon, it.Color = "dot", "blue"
		}
		return it
	}
	if len(recv) > 0 {
		inbox.Items = append(inbox.Items, Item{Type: "section", Label: s.trf("sms_inbox", len(recv))})
		for _, m := range recv {
			inbox.Items = append(inbox.Items, row(m))
		}
	}
	if len(sent) > 0 {
		inbox.Items = append(inbox.Items, Item{Type: "section", Label: s.trf("sms_sent", len(sent))})
		for _, m := range sent {
			inbox.Items = append(inbox.Items, row(m))
		}
	}
	for _, m := range append(recv, sent...) {
		who := m.Number
		if who == "" {
			who = s.tr("sms_hidden")
		}
		p := &Page{Title: who}
		label := s.tr("sms_from")
		if !m.received() {
			label = s.tr("sms_to")
		}
		p.Items = append(p.Items, Item{Type: "info", Label: label, Value: who},
			Item{Type: "info", Label: s.tr("sms_date"), Value: s.smsWhen(m, true)})
		n := s.smsNote(m.Name)
		p.Items = append(p.Items, n.Items...)
		if n.TextLabel == "" {
			n.TextLabel = s.tr("sms_text")
		}
		p.Items = append(p.Items, Item{Type: "section", Label: n.TextLabel})
		for _, para := range smsParagraphs(m.Body) {
			p.Items = append(p.Items, Item{Type: "text", Label: para, Color: n.TextColor})
		}
		p.Items = append(p.Items, Item{Type: "button", Label: s.tr("sms_delete"), Style: "danger",
			Action: &Action{ID: "core.sms_delete", Args: []string{m.Name}, Confirm: s.tr("sms_delete_q"),
				ConfirmLabel: s.tr("sms_delete"), Danger: true}})
		pages["sms:"+m.Name] = p
	}
	return pages
}

// smsNote is what the apps add to a message, in their order: the first
// preview, heading and colour of the text, and the items of all.
func (s *Screen) smsNote(name string) SMSNote {
	var n SMSNote
	for _, st := range s.apps {
		an := st.view.SMS[name]
		if !st.haveView || an == nil {
			continue
		}
		if n.Preview == "" {
			n.Preview = an.Preview
		}
		if n.TextLabel == "" {
			n.TextLabel = an.TextLabel
		}
		if n.TextColor == "" {
			n.TextColor = an.TextColor
		}
		n.Items = append(n.Items, owned(st.m.ID, an.Items)...)
	}
	return n
}

// smsCoreAction handles the core.sms_* actions of the pages.
func (s *Screen) smsCoreAction(id, arg string) {
	switch id {
	case "core.sms_readall":
		for i := range s.sms {
			if s.sms[i].Status == 0 {
				s.sms[i].Status = 1
			}
		}
		s.invalidate()
		s.smsChange("readall", "", map[string]any{"bus": "cpu", "status": 6})
	case "core.sms_delete":
		m := s.smsByName(arg)
		if m == nil {
			return
		}
		s.smsChange("delete", arg, map[string]any{"bus": m.Bus, "name": arg, "scope": 10})
	}
}
