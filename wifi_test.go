package main

import (
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGL answers GL's RPC for the Wi-Fi and airplane mode tests and keeps
// the calls.
type fakeGL struct {
	mu     sync.Mutex
	calls  []rpcCall
	status repeaterStatus
	nets   []wifiNet
}

func (f *fakeGL) install(t *testing.T) {
	old := glRPC
	glRPC = func(module, method string, params map[string]any, out any) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, rpcCall{module, method, params})
		switch module + "." + method {
		case "repeater.scan":
			r := out.(*struct {
				Res []wifiNet `json:"res"`
			})
			r.Res = f.nets
		case "repeater.get_status":
			*out.(*repeaterStatus) = f.status
		}
		return nil
	}
	t.Cleanup(func() { glRPC = old })
}

func (f *fakeGL) called(method string) []rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rpcCall
	for _, c := range f.calls {
		if c.module+"."+c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func wnet(ssid, band, enc string, signal int, saved bool) wifiNet {
	n := wifiNet{SSID: ssid, Band: band, Signal: signal, Saved: saved}
	n.Encryption.Enabled, n.Encryption.Description = enc != "", enc
	return n
}

// drain applies what the background calls sent back.
func drainWifi(t *testing.T, s *Screen, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case r := <-s.wifiCh:
			s.onWifi(r)
		case <-time.After(5 * time.Second):
			t.Fatalf("result %d of %d did not come", i+1, n)
		}
	}
}

func TestMergeNets(t *testing.T) {
	got := mergeNets([]wifiNet{
		wnet("Cafe", "2g", "PSK", -70, false), wnet("Cafe", "5g", "PSK", -60, false),
		wnet("", "2g", "PSK", -40, false), // hidden
		wnet("Home", "5g", "PSK", -80, true), wnet("Open", "2g", "", -50, false),
	})
	var names []string
	for _, n := range got {
		names = append(names, n.SSID)
	}
	if strings.Join(names, ",") != "Home,Open,Cafe" {
		t.Fatalf("order %v", names)
	}
	if c := got[2]; c.Signal != -60 || strings.Join(c.bands, "+") != "2g+5g" {
		t.Fatalf("merged %+v", c)
	}
}

// Typing a password on the keyboard and joining: the key goes to
// repeater.connect only, and the join is followed until it is connected.
func TestWifiJoinWithKeyboard(t *testing.T) {
	gl := &fakeGL{nets: []wifiNet{wnet("Bistro Lumen", "5g", "PSK", -55, false)}}
	gl.install(t)
	var logged strings.Builder
	s, clk := testScreen(t, true)
	s.log = log.New(&logged, "", 0)
	s.live = true
	s.open("system", "wifi")
	drainWifi(t, s, 2) // the scan and the status
	if len(s.wifi.nets) != 1 {
		t.Fatalf("nets %+v", s.wifi.nets)
	}
	s.act("system", &Action{ID: "core.wifi_join", Args: []string{"Bistro Lumen"}})
	if !s.onKeyboardPage() || s.kbd.min != 8 {
		t.Fatalf("no keyboard: page %q kbd %+v", s.page, s.kbd)
	}
	clk.add(time.Second)
	s.update()
	k := s.kbd
	k.layoutKeys()
	tap := func(label string) {
		t.Helper()
		for i, key := range k.keys {
			if key.label == label || key.fn == label {
				sc := s.cur()
				x, y := (key.r.Min.X+key.r.Max.X)/2, (key.r.Min.Y+key.r.Max.Y)/2+sc.chromeH
				s.onTouch(clk.ev(touchDown, x, y))
				clk.add(40 * time.Millisecond)
				s.onTouch(clk.ev(touchUp, x, y))
				clk.add(40 * time.Millisecond)
				s.update()
				k.layoutKeys()
				_ = i
				return
			}
		}
		t.Fatalf("no key %q in layer %d", label, k.layer)
	}
	// "Ab1!" and four more: shift, a letter, back to small, a digit, a symbol.
	tap("shift")
	tap("A")
	tap("b")
	tap("1")
	tap("done") // too short: nothing happens
	if !s.onKeyboardPage() {
		t.Fatal("Done with 3 characters closed the keyboard")
	}
	tap("layer1")
	tap("!")
	tap("layer0")
	for _, c := range []string{"x", "y", "z", "w"} {
		tap(c)
	}
	if got := string(k.text); got != "Ab1!xyzw" {
		t.Fatalf("typed %q", got)
	}
	tap("back")
	tap("q")
	tap("done")
	if s.onKeyboardPage() || s.kbd != nil {
		t.Fatal("the keyboard stayed open after Done")
	}
	drainWifi(t, s, 1) // connect
	c := gl.called("repeater.connect")
	if len(c) != 1 || c[0].params["ssid"] != "Bistro Lumen" || c[0].params["key"] != "Ab1!xyzq" || c[0].params["remember"] != true {
		t.Fatalf("connect calls %+v", c)
	}
	if strings.Contains(logged.String(), "Ab1!xyzq") || strings.Contains(logged.String(), "tap ") && strings.Count(logged.String(), "tap ") > 2 {
		t.Fatalf("the log has what was typed:\n%s", logged.String())
	}
	// get_status follows: connecting, then connected.
	gl.mu.Lock()
	gl.status = repeaterStatus{State: "connecting"}
	gl.mu.Unlock()
	drainWifi(t, s, 1)
	if s.wifi.joining != "Bistro Lumen" {
		t.Fatalf("joining %q", s.wifi.joining)
	}
	gl.mu.Lock()
	gl.status = repeaterStatus{State: "connected", SSID: "Bistro Lumen"}
	gl.mu.Unlock()
	clk.add(3 * time.Second)
	s.onTick()
	drainWifi(t, s, 1)
	if s.wifi.joining != "" || s.toast != "Connected to Bistro Lumen" || s.connectedSSID() != "Bistro Lumen" {
		t.Fatalf("after connected: joining %q, toast %q", s.wifi.joining, s.toast)
	}
}

// A wrong password: the repeater fails, the screen says so, and the next
// tap on a saved network asks for the password again.
func TestWifiJoinFails(t *testing.T) {
	gl := &fakeGL{nets: []wifiNet{wnet("Harbor Loft", "2g", "PSK", -50, true)}, status: repeaterStatus{State: "failed", FailType: "wrong_key"}}
	gl.install(t)
	s, clk := testScreen(t, false)
	s.live = true
	s.open("system", "wifi")
	drainWifi(t, s, 2)
	// Saved: a question, no keyboard.
	s.act("system", &Action{ID: "core.wifi_join", Args: []string{"Harbor Loft"}})
	if s.modal == nil || s.modal.kind != "confirm" || s.onKeyboardPage() {
		t.Fatalf("a saved network: modal %+v, keyboard %v", s.modal, s.onKeyboardPage())
	}
	s.modal.onYes()
	drainWifi(t, s, 2) // connect, status
	if s.wifi.joining != "" || !strings.Contains(s.wifi.joinErr, "wrong password") {
		t.Fatalf("join error %q", s.wifi.joinErr)
	}
	c := gl.called("repeater.connect")
	if len(c) != 1 || c[0].params["key"] != nil {
		t.Fatalf("a saved network was joined with a key: %+v", c)
	}
	clk.add(time.Second)
	s.act("system", &Action{ID: "core.wifi_join", Args: []string{"Harbor Loft"}})
	if !s.onKeyboardPage() {
		t.Fatal("after a wrong password, a saved network does not ask for it")
	}
}

// Airplane mode: on asks first, off does not; the switch shows the wanted
// state until the modem agrees.
func TestAirplaneSwitch(t *testing.T) {
	gl := &fakeGL{}
	gl.install(t)
	s, clk := testScreen(t, false)
	s.live = true
	item := func() Item {
		for _, it := range s.systemView().Pages["net"].Items {
			if it.Type == "toggle" && it.Action != nil && it.Action.ID == "core.airplane" {
				return it
			}
		}
		t.Fatal("no airplane switch on the network page")
		return Item{}
	}
	if it := item(); it.Checked || it.Action.Args[0] != "on" {
		t.Fatalf("switch %+v", it)
	}
	s.act("system", item().Action)
	if s.modal == nil || s.modal.kind != "confirm" || len(gl.called("system.set_airplane_mode")) != 0 {
		t.Fatalf("airplane mode went on without a question: %+v", s.modal)
	}
	s.modal.onYes()
	drainWifi(t, s, 1)
	c := gl.called("system.set_airplane_mode")
	if len(c) != 1 || c[0].params["enable"] != true {
		t.Fatalf("calls %+v", c)
	}
	if it := item(); !it.Checked || !it.Busy {
		t.Fatalf("while switching: %+v", it)
	}
	s.st.CellAirplane = true
	clk.add(time.Second)
	s.onTick()
	if it := item(); !it.Checked || it.Busy || it.Action.Args[0] != "off" {
		t.Fatalf("after the modem agreed: %+v", it)
	}
	s.act("system", item().Action) // off: no question
	drainWifi(t, s, 1)
	if c := gl.called("system.set_airplane_mode"); len(c) != 2 || c[1].params["enable"] != false {
		t.Fatalf("off: %+v", c)
	}
}

// An app's item cannot use the screen's network switches.
func TestAppCannotSwitchNetworks(t *testing.T) {
	gl := &fakeGL{}
	gl.install(t)
	s := newScreen(options{}, nil, log.New(io.Discard, "", 0))
	s.live = true
	s.act("someapp", &Action{ID: "core.airplane", Args: []string{"on"}})
	s.act("someapp", &Action{ID: "core.wifi_disconnect"})
	if s.modal != nil || len(gl.calls) != 0 {
		t.Fatalf("an app switched: modal %+v, calls %+v", s.modal, gl.calls)
	}
}
