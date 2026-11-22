package main

// Wi-Fi as the router's way to the internet (GL calls it Repeater), and
// airplane mode: the network switches of the stock screen, through the
// calls GL's own screen and web admin make. A scan lists the networks
// around (repeater.scan), a tap joins one (repeater.connect, the password
// from the on-screen keyboard), the router's own Wi-Fi stays as it is.
// Airplane mode is system.set_airplane_mode: it turns the cellular radio
// off, data, calls and SMS of the SIM with it.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type wifiNet struct {
	SSID       string `json:"ssid"`
	BSSID      string `json:"bssid"`
	Band       string `json:"band"` // 2g, 5g, 6g
	Channel    int    `json:"channel"`
	Signal     int    `json:"signal"` // dBm
	Saved      bool   `json:"saved"`
	Encryption struct {
		Enabled     bool   `json:"enabled"`
		Description string `json:"description"` // PSK, SAE, OWE, EAP…
	} `json:"encryption"`

	bands []string // of all the access points of this name
}

// enterprise tells a network that wants a user name (802.1X): the web
// admin joins those, the keyboard does not.
func (n wifiNet) enterprise() bool {
	d := strings.ToUpper(n.Encryption.Description)
	return strings.Contains(d, "EAP") || strings.Contains(d, "802.1X") || strings.Contains(d, "ENTERPRISE")
}

// minKey is the shortest password the network's security takes.
func (n wifiNet) minKey() int {
	if strings.Contains(strings.ToUpper(n.Encryption.Description), "WEP") {
		return 5
	}
	return 8
}

type repeaterStatus struct {
	State    string `json:"state_s"` // idle, scanning, connecting, connected, failed
	Running  bool   `json:"running"`
	FailType string `json:"fail_type"`
	SSID     string `json:"ssid"`
}

type wifiResult struct {
	op     string // scan, status, connect, disconnect, airplane
	nets   []wifiNet
	status repeaterStatus
	ssid   string
	on     bool
	err    error
}

type wifiState struct {
	nets     []wifiNet
	scanned  bool
	scanning bool
	scanAt   time.Time
	scanErr  string

	status   repeaterStatus
	statusAt time.Time
	asking   bool // a get_status is out

	joining string // the network being joined
	joinAt  time.Time
	joinErr string // why the last join did not work
	askKey  map[string]bool
	busy    bool // connect or disconnect is out

	airBusy bool // airplane mode is being switched
	airWant bool
	airAt   time.Time
}

const joinTimeout = 50 * time.Second

func signalLevel(dbm int) int {
	switch {
	case dbm >= -55:
		return 3
	case dbm >= -67:
		return 2
	case dbm >= -77:
		return 1
	}
	return 0
}

// ---- calls ----

func (s *Screen) wifiScan() {
	if s.wifi.scanning || !s.live {
		return
	}
	s.wifi.scanning, s.wifi.scanErr = true, ""
	s.invalidate()
	go func() {
		var r struct {
			Res []wifiNet `json:"res"`
		}
		err := glRPC("repeater", "scan", map[string]any{"refresh": true}, &r)
		s.wifiCh <- wifiResult{op: "scan", nets: r.Res, err: err}
	}()
}

func (s *Screen) wifiStatus() {
	if s.wifi.asking || !s.live {
		return
	}
	s.wifi.asking = true
	go func() {
		var st repeaterStatus
		err := glRPC("repeater", "get_status", map[string]any{}, &st)
		s.wifiCh <- wifiResult{op: "status", status: st, err: err}
	}()
}

// wifiConnect joins a network; key is empty for open and saved ones. The
// key goes to GL's RPC on stdin (glRPC) and nowhere else: not in the log.
func (s *Screen) wifiConnect(n wifiNet, key string) {
	if s.wifi.busy {
		return
	}
	s.log.Printf("wifi: joining a network")
	s.wifi.busy, s.wifi.joining, s.wifi.joinAt, s.wifi.joinErr = true, n.SSID, s.now(), ""
	s.invalidate()
	if !s.live {
		return
	}
	params := map[string]any{"ssid": n.SSID, "remember": true}
	if key != "" {
		params["key"] = key
	}
	go func() {
		err := glRPC("repeater", "connect", params, nil)
		s.wifiCh <- wifiResult{op: "connect", ssid: n.SSID, err: err}
	}()
}

func (s *Screen) wifiDisconnect() {
	if s.wifi.busy || !s.live {
		return
	}
	s.log.Printf("wifi: leaving the network")
	s.wifi.busy, s.wifi.joining = true, ""
	s.invalidate()
	go func() {
		err := glRPC("repeater", "disconnect", map[string]any{}, nil)
		s.wifiCh <- wifiResult{op: "disconnect", err: err}
	}()
}

func (s *Screen) setAirplane(on bool) {
	if s.wifi.airBusy {
		return
	}
	s.log.Printf("airplane mode: %v", on)
	s.wifi.airBusy, s.wifi.airWant, s.wifi.airAt = true, on, s.now()
	s.invalidate()
	if !s.live {
		return
	}
	go func() {
		err := glRPC("system", "set_airplane_mode", map[string]any{"enable": on}, nil)
		s.wifiCh <- wifiResult{op: "airplane", on: on, err: err}
	}()
}

// ---- results ----

func (s *Screen) onWifi(r wifiResult) {
	w := &s.wifi
	switch r.op {
	case "scan":
		w.scanning, w.scanAt = false, s.now()
		if r.err != nil {
			w.scanErr = r.err.Error()
			s.log.Printf("wifi: scan: %v", r.err)
		} else {
			w.nets, w.scanned, w.scanErr = mergeNets(r.nets), true, ""
		}
	case "status":
		w.asking, w.statusAt = false, s.now()
		if r.err != nil {
			s.log.Printf("wifi: status: %v", r.err)
			break
		}
		w.status = r.status
		s.followJoin()
	case "connect":
		w.busy = false
		if r.err != nil {
			s.log.Printf("wifi: join: %v", r.err)
			w.joining, w.joinErr = "", r.err.Error()
			if w.askKey == nil {
				w.askKey = map[string]bool{}
			}
			w.askKey[r.ssid] = true
			s.showToast(s.trf("wifi_failed", r.ssid))
			break
		}
		s.wifiStatus() // the join goes on; get_status tells how it ends
	case "disconnect":
		w.busy = false
		if r.err != nil {
			s.showToast(s.trf("error", r.err.Error()))
		}
		s.wifiStatus()
		s.requestRefresh()
	case "airplane":
		if r.err != nil {
			w.airBusy = false
			s.log.Printf("airplane mode: %v", r.err)
			s.showToast(s.trf("error", r.err.Error()))
		}
		// Otherwise the switch stays busy until the modem says so (onTick).
		s.requestRefresh()
	}
	s.invalidate()
}

// followJoin ends a join when the repeater says how it went.
func (s *Screen) followJoin() {
	w := &s.wifi
	if w.joining == "" || w.busy {
		return
	}
	switch w.status.State {
	case "connected":
		s.showToast(s.trf("wifi_joined", w.joining))
		w.joining, w.joinErr = "", ""
		s.requestRefresh()
	case "failed":
		msg := s.trf("wifi_failed", w.joining)
		if strings.Contains(strings.ToLower(w.status.FailType), "key") ||
			strings.Contains(strings.ToLower(w.status.FailType), "auth") ||
			strings.Contains(strings.ToLower(w.status.FailType), "password") {
			msg = s.trf("wifi_failed_key", w.joining)
		}
		if w.askKey == nil {
			w.askKey = map[string]bool{}
		}
		w.askKey[w.joining] = true
		w.joinErr, w.joining = msg, ""
		s.showToast(msg)
	}
}

// mergeNets keeps one entry per name, the strongest, with the bands of all,
// and drops the hidden ones; the order is by signal, saved ones first.
func mergeNets(in []wifiNet) []wifiNet {
	by := map[string]*wifiNet{}
	var order []string
	for _, n := range in {
		if strings.TrimSpace(n.SSID) == "" {
			continue
		}
		cur := by[n.SSID]
		if cur == nil {
			c := n
			by[n.SSID] = &c
			order = append(order, n.SSID)
			cur = &c
		} else if n.Signal > cur.Signal {
			bands := cur.bands
			saved := cur.Saved || n.Saved
			*cur = n
			cur.bands, cur.Saved = bands, saved
		} else if n.Saved {
			cur.Saved = true
		}
		if n.Band != "" && !hasString(cur.bands, n.Band) {
			cur.bands = append(cur.bands, n.Band)
		}
	}
	out := make([]wifiNet, 0, len(order))
	for _, k := range order {
		n := *by[k]
		sort.Strings(n.bands)
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Saved != out[j].Saved {
			return out[i].Saved
		}
		return out[i].Signal > out[j].Signal
	})
	return out
}

func hasString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---- actions ----

// wifiCoreAction handles core.wifi_* and core.airplane, which only the
// screen's own pages use.
func (s *Screen) wifiCoreAction(id string, args []string) {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	switch id {
	case "core.wifi_scan":
		s.wifiScan()
	case "core.wifi_disconnect":
		s.wifiDisconnect()
	case "core.wifi_join":
		n, ok := s.wifiNet(arg)
		if !ok {
			return
		}
		switch {
		case n.enterprise():
			s.showToast(s.tr("wifi_eap"))
		case n.Encryption.Enabled && (!n.Saved || s.wifi.askKey[n.SSID]):
			s.openKeyboard(&keyboard{title: s.tr("kbd_password"), label: s.trf("kbd_for", n.SSID),
				min: n.minKey(), done: func(key string) { s.wifiConnect(n, key) }})
		default:
			s.setModal(&Modal{kind: "confirm", title: s.trf("wifi_join_q", n.SSID), yes: s.tr("wifi_join"),
				onYes: func() {
					s.setModal(nil)
					s.wifiConnect(n, "")
				}})
		}
	case "core.airplane":
		on := arg == "on"
		if !on {
			s.setAirplane(false)
			return
		}
		s.setModal(&Modal{kind: "confirm", title: s.tr("airplane_q"), text: s.tr("airplane_note"),
			yes: s.tr("airplane_on"), onYes: func() {
				s.setModal(nil)
				s.setAirplane(true)
			}})
	}
}

func (s *Screen) wifiNet(ssid string) (wifiNet, bool) {
	for _, n := range s.wifi.nets {
		if n.SSID == ssid {
			return n, true
		}
	}
	return wifiNet{}, false
}

// onWifiPage starts a scan when the networks page opens, unless a fresh
// one is there.
func (s *Screen) onWifiPage() {
	if !s.wifi.scanned || s.now().Sub(s.wifi.scanAt) > 30*time.Second {
		s.wifiScan()
	}
	s.wifiStatus()
}

// wifiTick follows a join and an airplane switch, and keeps the status of
// the networks page fresh.
func (s *Screen) wifiTick(now time.Time) {
	w := &s.wifi
	if w.joining != "" && !w.busy {
		if now.Sub(w.joinAt) > joinTimeout {
			w.joinErr = s.trf("wifi_failed", w.joining)
			s.showToast(w.joinErr)
			w.joining = ""
			s.invalidate()
		} else if now.Sub(w.statusAt) >= 2*time.Second {
			s.wifiStatus()
		}
	}
	if p := s.visiblePage("system"); (p == "wifi" || p == "net") && now.Sub(w.statusAt) >= 5*time.Second {
		s.wifiStatus()
	}
	if w.airBusy && (s.st.CellAirplane == w.airWant || now.Sub(w.airAt) > 30*time.Second) {
		w.airBusy = false
		s.invalidate()
	}
}

// airplaneShown is what the switch shows: the wanted state while it is
// being switched.
func (s *Screen) airplaneShown() bool {
	if s.wifi.airBusy {
		return s.wifi.airWant
	}
	return s.st.CellAirplane
}

// connectedSSID is the network the router takes the internet from, if any.
func (s *Screen) connectedSSID() string {
	if s.wifi.status.State == "connected" && s.wifi.status.SSID != "" {
		return s.wifi.status.SSID
	}
	if s.st.WANKind == "wifi" {
		return s.st.WANSSID
	}
	return ""
}

// ---- pages ----

// wifiItems are the Wi-Fi part of the network page.
func (s *Screen) wifiItems() []Item {
	w := &s.wifi
	items := []Item{{Type: "section", Label: s.tr("wifi_uplink")}}
	ssid := s.connectedSSID()
	switch {
	case w.joining != "":
		items = append(items, Item{Type: "info", Label: s.tr("wifi_state"), Value: s.trf("wifi_connecting", w.joining), Busy: true})
	case ssid != "":
		items = append(items, Item{Type: "info", Label: s.tr("wifi_state"), Value: ssid, Color: "green"})
	default:
		items = append(items, Item{Type: "info", Label: s.tr("wifi_state"), Value: s.tr("wifi_not_connected")})
	}
	items = append(items, Item{Type: "link", Label: s.tr("wifi_networks"), Icon: "wifi", Open: "wifi"})
	if ssid != "" && w.joining == "" {
		items = append(items, Item{Type: "button", Label: s.tr("wifi_disconnect"), Style: "plain", Busy: w.busy,
			Action: &Action{ID: "core.wifi_disconnect", Confirm: s.tr("wifi_disconnect_q"), ConfirmLabel: s.tr("wifi_disconnect")}})
	}
	return items
}

// airplaneItem is the switch on the network page.
func (s *Screen) airplaneItem() Item {
	on := s.airplaneShown()
	next := "on"
	if on {
		next = "off"
	}
	return Item{Type: "toggle", Label: s.tr("airplane"), Sub: s.tr("airplane_sub_toggle"), Checked: on,
		Busy: s.wifi.airBusy, Color: "orange", Action: &Action{ID: "core.airplane", Args: []string{next}}}
}

func (s *Screen) wifiPage() *Page {
	w := &s.wifi
	p := &Page{Title: s.tr("wifi_networks")}
	p.Items = append(p.Items, Item{Type: "text", Label: s.tr("wifi_hint"), Color: "dim"})
	if w.joining != "" {
		p.Items = append(p.Items, Item{Type: "entry", Label: s.trf("wifi_connecting", w.joining), Icon: "wifi", Busy: true})
	} else if w.joinErr != "" {
		p.Items = append(p.Items, Item{Type: "text", Label: w.joinErr, Color: "orange"})
	}
	scan := Item{Type: "link", Label: s.tr("wifi_scan"), Icon: "globe", Busy: w.scanning, Action: &Action{ID: "core.wifi_scan"}}
	if w.scanning {
		scan.Label = s.tr("wifi_scanning")
	}
	p.Items = append(p.Items, scan)
	if w.scanErr != "" {
		p.Items = append(p.Items, Item{Type: "text", Label: s.trf("error", w.scanErr), Color: "orange"})
	}
	current := s.connectedSSID()
	if len(w.nets) > 0 {
		p.Items = append(p.Items, Item{Type: "section", Label: s.trf("wifi_found", len(w.nets))})
	}
	for _, n := range w.nets {
		var parts []string
		switch {
		case n.Saved:
			parts = append(parts, s.tr("wifi_saved"))
		case !n.Encryption.Enabled:
			parts = append(parts, s.tr("wifi_open"))
		}
		var bands []string
		for _, b := range n.bands {
			switch b {
			case "2g":
				bands = append(bands, "2.4")
			case "5g":
				bands = append(bands, "5")
			case "6g":
				bands = append(bands, "6")
			}
		}
		if len(bands) > 0 {
			parts = append(parts, strings.Join(bands, " + ")+" "+s.tr("ghz"))
		}
		it := Item{Type: "entry", Label: n.SSID, Sub: strings.Join(parts, " · "),
			Icon: fmt.Sprintf("wifi-%d", signalLevel(n.Signal)), Color: "dim",
			Action: &Action{ID: "core.wifi_join", Args: []string{n.SSID}}}
		if n.SSID == current {
			it.Color, it.Value, it.Action = "blue", s.tr("wifi_current"), nil
		}
		p.Items = append(p.Items, it)
	}
	if len(w.nets) == 0 && !w.scanning && w.scanned {
		p.Empty = s.tr("wifi_none")
	}
	return p
}
