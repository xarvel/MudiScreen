package main

// System status for the built-in parts of the screen: only what any stock
// Mudi 7 has (power supply, GL's cellular.* and gl-clients ubus objects,
// /proc). Collected in a background goroutine: every 3 s while the screen is
// on, every minute while it is off.

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Status struct {
	At time.Time

	BatPct      int // -1: unknown
	BatPlugged  bool
	BatCharging bool

	CellOK       bool // registered on a network
	CellAirplane bool
	Carrier      string
	Tech         string // 5G, 4G+, 4G, 3G, 2G or ""
	Bars         int    // 0..4
	RSRP, SINR   int    // of the primary carrier; 0 = unknown
	Bands        []string
	Slot         int
	SMSNew       int

	WANKind  string // cell, wifi, cable, usb, other, "" (no default route)
	WANIface string
	WANSSID  string
	RxBps    float64 // bits per second on the WAN interface
	TxBps    float64

	Clients  int
	Uptime   time.Duration
	TempC    float64 // CPU, 0 = unknown
	Firmware string
}

type collector struct {
	now func() time.Time

	prevIface      string
	prevRx, prevTx uint64
	prevT          time.Time

	carrier   string
	carrierAt time.Time
	clients   int
	clientsAt time.Time
	firmware  string
}

// pollStatus needs both directions of out: a status the UI has not taken
// yet is replaced by the fresh one.
func pollStatus(out chan Status, refresh <-chan struct{}, awake *atomic.Bool) {
	c := &collector{now: time.Now}
	for {
		st := c.collect()
		select {
		case out <- st:
		default:
			select {
			case <-out:
			default:
			}
			out <- st
		}
		d := time.Minute
		if awake.Load() {
			d = 3 * time.Second
		}
		select {
		case <-refresh:
		case <-time.After(d):
		}
	}
}

func (c *collector) collect() Status {
	now := c.now()
	st := Status{At: now, BatPct: -1}
	c.battery(&st)
	c.cellular(&st, now)
	c.wan(&st, now)
	if now.Sub(c.clientsAt) > 15*time.Second {
		c.clients = clientsCount()
		c.clientsAt = now
	}
	st.Clients = c.clients
	if f := strings.Fields(readTrim("/proc/uptime")); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			st.Uptime = time.Duration(v * float64(time.Second))
		}
	}
	st.TempC = cpuTemp()
	if c.firmware == "" {
		c.firmware = readTrim("/etc/glversion")
	}
	st.Firmware = c.firmware
	return st
}

func (c *collector) battery(st *Status) {
	const dir = "/sys/class/power_supply"
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		base := dir + "/" + e.Name() + "/"
		switch readTrim(base + "type") {
		case "Battery":
			if readTrim(base+"present") == "0" {
				continue
			}
			if v, err := strconv.Atoi(readTrim(base + "capacity")); err == nil {
				st.BatPct = min(max(v, 0), 100)
			}
			if readTrim(base+"status") == "Charging" {
				st.BatCharging = true
			}
		case "USB", "Mains":
			if readTrim(base+"online") == "1" {
				st.BatPlugged = true
				if readTrim(base+"status") == "Charging" {
					st.BatCharging = true
				}
			}
		}
	}
}

// ubusJSON calls a ubus method over the socket (ubusclient.go), and with
// the ubus program when that fails for a reason other than a missing object.
func ubusJSON(obj, method, args string, v any) bool {
	var m map[string]any
	if args != "" && json.Unmarshal([]byte(args), &m) != nil {
		return false
	}
	err := ubusCallJSON(obj, method, m, v)
	if err == nil {
		return true
	}
	if errors.Is(err, errUbusNotFound) {
		return false
	}
	cmd := []string{"call", obj, method}
	if args != "" {
		cmd = append(cmd, args)
	}
	out, err := runCmd(5*time.Second, "ubus", cmd...)
	if err != nil {
		return false
	}
	return json.Unmarshal([]byte(out), v) == nil
}

func (c *collector) cellular(st *Status, now time.Time) {
	var modem struct {
		Status int `json:"status"`
		Slot   int `json:"current_sim_slot"`
		SMS    int `json:"new_sms_count"`
	}
	if !ubusJSON("cellular.modem", "status", `{"bus":"cpu"}`, &modem) {
		return
	}
	st.CellAirplane = modem.Status == 3
	st.SMSNew = modem.SMS
	slot := modem.Slot
	if slot == 0 {
		slot = 1
	}
	st.Slot = slot
	var ci struct {
		NetworkType int `json:"network_type"`
		Signal      []struct {
			CA          int `json:"ca"`
			NetworkType int `json:"network_type"`
			Band        int `json:"band"`
			RSRP        int `json:"rsrp"`
			SINR        int `json:"sinr"`
			Strength    int `json:"strength"`
		} `json:"signal"`
	}
	if ubusJSON("cellular.network", "cell_info", `{"bus":"cpu","slot":`+strconv.Itoa(slot)+`}`, &ci) {
		sort.Slice(ci.Signal, func(i, j int) bool { return ci.Signal[i].CA < ci.Signal[j].CA })
		nr, lte, n := 0, 0, 0
		for _, s := range ci.Signal {
			// Without service the list keeps one placeholder: band 0,
			// network type 0, rsrp -32768.
			if s.Band == 0 || s.NetworkType == 0 {
				continue
			}
			nrCarrier := s.NetworkType == 5 || s.NetworkType == 51
			if n == 0 {
				st.Bars = min(max(s.Strength, 0), 4)
				if s.RSRP > -32768 {
					st.RSRP = s.RSRP
				}
				if s.SINR > -32768 {
					st.SINR = s.SINR
				}
			}
			n++
			band := "B" + strconv.Itoa(s.Band)
			switch {
			case nrCarrier:
				nr++
				band = "n" + strconv.Itoa(s.Band)
			case s.NetworkType == 4 || s.NetworkType == 41:
				lte++
			}
			st.Bands = append(st.Bands, band)
		}
		st.CellOK = n > 0
		switch {
		case nr > 0:
			st.Tech = "5G"
		case lte > 1:
			st.Tech = "4G+"
		case lte == 1:
			st.Tech = "4G"
		case n > 0 && ci.NetworkType == 1:
			st.Tech = "2G"
		case n > 0:
			st.Tech = "3G"
		}
	}
	if st.CellOK && (c.carrier == "" || now.Sub(c.carrierAt) > time.Minute) {
		var info struct {
			Networks []struct {
				Carrier string `json:"carrier"`
			} `json:"networks"`
		}
		if ubusJSON("cellular.network", "info", `{"bus":"cpu","slot":`+strconv.Itoa(slot)+`}`, &info) && len(info.Networks) > 0 {
			c.carrier = info.Networks[0].Carrier
			c.carrierAt = now
		}
	}
	st.Carrier = c.carrier
}

// defaultRoute returns the interface of the lowest-metric IPv4 default route.
func defaultRoute() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	best, bestMetric := "", 1<<31
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 8 || fl[1] != "00000000" || fl[7] != "00000000" {
			continue
		}
		m, err := strconv.Atoi(fl[6])
		if err != nil {
			continue
		}
		if m < bestMetric {
			best, bestMetric = fl[0], m
		}
	}
	return best
}

func ifaceBytes(iface string) (rx, tx uint64, ok bool) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, found := strings.Cut(sc.Text(), ":")
		if !found || strings.TrimSpace(name) != iface {
			continue
		}
		fl := strings.Fields(rest)
		if len(fl) < 9 {
			return 0, 0, false
		}
		rx, _ = strconv.ParseUint(fl[0], 10, 64)
		tx, _ = strconv.ParseUint(fl[8], 10, 64)
		return rx, tx, true
	}
	return 0, 0, false
}

func wanKind(iface string) string {
	switch {
	case iface == "":
		return ""
	case strings.HasPrefix(iface, "rmnet"), strings.HasPrefix(iface, "wwan0"):
		return "cell"
	case strings.HasPrefix(iface, "wlan"), strings.HasPrefix(iface, "apcli"), strings.HasPrefix(iface, "sta"):
		return "wifi"
	case strings.HasPrefix(iface, "usb"):
		return "usb"
	case strings.HasPrefix(iface, "eth"), strings.HasPrefix(iface, "br-wan"):
		return "cable"
	}
	return "other"
}

func (c *collector) wan(st *Status, now time.Time) {
	iface := defaultRoute()
	st.WANIface = iface
	st.WANKind = wanKind(iface)
	if st.WANKind == "wifi" {
		var rep map[string]any
		if ubusJSON("repeater", "status", "", &rep) {
			if s, ok := rep["ssid"].(string); ok {
				st.WANSSID = s
			}
		}
	}
	rx, tx, ok := ifaceBytes(iface)
	if ok && iface == c.prevIface && !c.prevT.IsZero() && rx >= c.prevRx && tx >= c.prevTx {
		if dt := now.Sub(c.prevT).Seconds(); dt > 0.5 {
			st.RxBps = float64(rx-c.prevRx) * 8 / dt
			st.TxBps = float64(tx-c.prevTx) * 8 / dt
		}
	}
	c.prevIface, c.prevRx, c.prevTx, c.prevT = iface, rx, tx, now
}

func clientsCount() int {
	var cl struct {
		Wireless int `json:"wireless_total"`
		Cable    int `json:"cable_total"`
		USB      int `json:"usbeth_total"`
	}
	if !ubusJSON("gl-clients", "status", "", &cl) {
		return 0
	}
	return cl.Wireless + cl.Cable + cl.USB
}

// cpuTemp returns the hottest cpuss-* zone; thermal_zone0 on this SoC is an
// unpowered sensor that reads -273 °C.
func cpuTemp() float64 {
	ents, err := os.ReadDir("/sys/class/thermal")
	if err != nil {
		return 0
	}
	best := 0.0
	for _, e := range ents {
		base := "/sys/class/thermal/" + e.Name() + "/"
		if !strings.HasPrefix(readTrim(base+"type"), "cpuss") {
			continue
		}
		if v, err := strconv.Atoi(readTrim(base + "temp")); err == nil && v > 0 {
			best = max(best, float64(v)/1000)
		}
	}
	return best
}

// demoStatus is what -preview shows. The carrier and the numbers are made up.
func demoStatus(now time.Time) Status {
	return Status{At: now, BatPct: 92, BatPlugged: true, BatCharging: true,
		CellOK: true, Carrier: "Nimbus", Tech: "5G", Bars: 3, RSRP: -98, SINR: 13, Bands: []string{"n78", "B3"},
		Slot: 1, SMSNew: 3, WANKind: "cell", WANIface: "rmnet_data0", RxBps: 6.0e6, TxBps: 234e3, Clients: 3,
		Uptime: 6*time.Hour + 12*time.Minute, TempC: 42.3, Firmware: "4.10.0"}
}
