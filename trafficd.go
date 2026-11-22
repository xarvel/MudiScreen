package main

// The traffic analyzer's collector, `mudi-screen app traffic serve`: a
// service of its own (/etc/init.d/mudi-screen-traffic) that keeps
// statistics whether anyone looks or not. Every 2 seconds it reads the
// internet interface's counters and the connection tracking table and
// spreads the bytes over devices, services, hosts and protocols; DNS
// answers and TLS server names (sniff.go) name the far ends. The app's
// `status` asks it for a page over a unix socket.

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	tdSockPath  = "/var/run/mudi-screen-traffic.sock"
	tdStatePath = "/etc/mudi-screen/traffic.json"
	tdLogPath   = "/tmp/mudi-screen-traffic.log"
	tdTick      = 2 * time.Second
	tdLivePts   = 150  // 2-second points kept: 5 minutes
	tdMinPts    = 1440 // 1-minute points kept: 24 hours
	tdDays      = 40
	tdMaxHosts  = 1500
	tdMaxPer    = 300 // services and hosts kept per device
)

// ---- small containers ----

// series is a ring of the newest values.
type series struct {
	v       []float64
	head, n int
}

func newSeries(n int) *series { return &series{v: make([]float64, n)} }

func (s *series) push(x float64) {
	s.v[s.head] = x
	s.head = (s.head + 1) % len(s.v)
	if s.n < len(s.v) {
		s.n++
	}
}

// last returns up to n newest values, oldest first.
func (s *series) last(n int) []float64 {
	n = min(n, s.n)
	out := make([]float64, n)
	start := (s.head - n + len(s.v)) % len(s.v)
	for i := range out {
		out[i] = s.v[(start+i)%len(s.v)]
	}
	return out
}

// rate2 is a smoothed download and upload rate in bit/s.
type rate2 struct{ down, up float64 }

func (r *rate2) update(down, up uint64, dt float64) {
	const keep = 0.45
	r.down = keep*r.down + (1-keep)*float64(down)*8/dt
	r.up = keep*r.up + (1-keep)*float64(up)*8/dt
	if r.down < 8 {
		r.down = 0
	}
	if r.up < 8 {
		r.up = 0
	}
}

func (r rate2) total() float64 { return r.down + r.up }

// ---- what is measured ----

type tdDevice struct {
	key    string // MAC, "ip:ADDRESS" or "router"
	name   string
	ip     netip.Addr
	mac    string
	iface  string
	online bool
	router bool
	rate   rate2
	live   [2]*series // bit/s per tick
	today  [2]uint64
	svcs   map[string]uint64 // today, by service
	hosts  map[string]uint64 // today, by host
	flows  int
	active time.Time
}

func newDevice(key string) *tdDevice {
	return &tdDevice{key: key, live: [2]*series{newSeries(tdLivePts), newSeries(tdLivePts)},
		svcs: map[string]uint64{}, hosts: map[string]uint64{}}
}

// tdEntity is a service, a host or a protocol.
type tdEntity struct {
	name   string
	cat    string
	svc    string // hosts: their service
	rate   rate2
	today  [2]uint64
	flows  int
	devs   map[string]uint64 // today, by device key
	active time.Time
}

type tdFlowKey struct {
	id uint32
	t  ctTuple
}

type tdFlow struct {
	ignore       bool // traffic inside the LAN, broadcast and the like
	dev          string
	client       netip.Addr // the zero address for the router itself
	remote       netip.Addr
	lport, rport uint16
	proto        uint8
	inbound      bool
	host         string
	named        bool
	svc          svcInfo
	pname        string
	prev         [2]uint64 // original, reply bytes at the last tick
	first        time.Time
	rate         rate2
	tick         uint64
}

type glClient struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Iface  string `json:"iface"`
	Online bool   `json:"online"`
	MAC    string `json:"mac"`
}

// addrSet is the router's view of its own addresses, replaced as a whole.
type addrSet struct {
	lan   []netip.Prefix
	local map[netip.Addr]bool
}

func (a *addrSet) isLocal(ip netip.Addr) bool { return a.local[ip.Unmap()] }

func (a *addrSet) isLAN(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range a.lan {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ---- what is kept on flash ----

type tdTotal struct {
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
	Cat  string `json:"cat,omitempty"`
	Down uint64 `json:"down"`
	Up   uint64 `json:"up"`
}

type tdDay struct {
	Day      string    `json:"day"`
	WanDown  uint64    `json:"wan_down"`
	WanUp    uint64    `json:"wan_up"`
	Sim      uint64    `json:"sim"`
	SimOK    bool      `json:"sim_ok,omitempty"`
	Devices  []tdTotal `json:"devices,omitempty"`
	Services []tdTotal `json:"services,omitempty"`
	Hosts    []tdTotal `json:"hosts,omitempty"`
	Protos   []tdTotal `json:"protos,omitempty"`
}

type simMark struct {
	Day   string `json:"day,omitempty"`
	Total uint64 `json:"total"`
	Slot  int    `json:"slot"`
	At    int64  `json:"at,omitempty"` // Unix time the mark was taken during the day (0: at midnight)
}

type tdPersist struct {
	Version int     `json:"version"`
	Today   tdDay   `json:"today"`
	Days    []tdDay `json:"days"`
	SimDay  simMark `json:"sim_day"`  // the counter when today began
	SimLast simMark `json:"sim_last"` // the latest value seen
}

type tdConfig struct {
	capGB    float64
	cycleDay int
}

// ---- the collector ----

type trafficd struct {
	mu    sync.Mutex
	log   *log.Logger
	loc   *time.Location
	now   func() time.Time
	names *nameCache
	dns   *dnsStats
	addrs atomic.Pointer[addrSet]
	macs  atomic.Pointer[map[netip.Addr]string] // d.macOf, as last published

	appDir  string
	catMu   sync.Mutex
	catalog map[string]*catalog

	tick    uint64
	last    time.Time
	started time.Time
	day     string

	flows  map[tdFlowKey]*tdFlow
	devs   map[string]*tdDevice
	svcs   map[string]*tdEntity
	hosts  map[string]*tdEntity
	protos map[string]*tdEntity
	macOf  map[netip.Addr]string
	glc    map[string]glClient
	leases map[string]string
	ctErr  string
	nTCP   int
	nUDP   int
	nOther int

	wanIf    []string
	wanPrev  map[string][2]uint64
	wanRate  rate2
	wanLive  [2]*series // bit/s per tick
	wanMin   [2]*series // bytes per minute
	minute   int64
	minAcc   [2]uint64
	wanToday [2]uint64

	simOK    bool
	simSlot  int
	simTotal uint64

	st       tdPersist
	cfg      tdConfig
	ui       struct{ rng, basis string }
	lastSave time.Time
}

func newTrafficd(logger *log.Logger, loc *time.Location) *trafficd {
	d := &trafficd{log: logger, loc: loc, now: time.Now, names: newNameCache(), dns: newDNSStats(),
		catalog: map[string]*catalog{}, flows: map[tdFlowKey]*tdFlow{}, devs: map[string]*tdDevice{},
		svcs: map[string]*tdEntity{}, hosts: map[string]*tdEntity{}, protos: map[string]*tdEntity{},
		macOf: map[netip.Addr]string{}, glc: map[string]glClient{}, leases: map[string]string{},
		wanPrev: map[string][2]uint64{}, cfg: tdConfig{cycleDay: 1}}
	for i := range d.wanLive {
		d.wanLive[i] = newSeries(tdLivePts)
		d.wanMin[i] = newSeries(tdMinPts)
	}
	d.ui.rng, d.ui.basis = "live", "now"
	d.addrs.Store(&addrSet{local: map[netip.Addr]bool{}})
	d.started = d.now()
	d.day = d.dayOf(d.started)
	d.st.Version = 1
	return d
}

func (d *trafficd) dayOf(t time.Time) string { return t.In(d.loc).Format("2006-01-02") }

// ---- the router's addresses and clients ----

func (d *trafficd) refreshAddrs() {
	set := &addrSet{local: map[netip.Addr]bool{}}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			pn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(pn.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			set.local[ip] = true
			if strings.HasPrefix(ifc.Name, "br-") && ifc.Name != "br-wan" && !ip.IsLinkLocalUnicast() {
				ones, _ := pn.Mask.Size()
				set.lan = append(set.lan, netip.PrefixFrom(ip, ones).Masked())
			}
		}
	}
	d.addrs.Store(set)
}

func cleanName(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "*", "unknown", "localhost":
		return ""
	}
	return s
}

// refreshClients reads GL's client list, the DHCP leases and the ARP table.
func (d *trafficd) refreshClients() {
	var cl struct {
		Clients map[string]glClient `json:"clients"`
	}
	glcOK := ubusCallJSON("gl-clients", "list", nil, &cl) == nil
	leases := map[string]string{}
	if f, err := os.Open("/tmp/dhcp.leases"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fl := strings.Fields(sc.Text())
			if len(fl) >= 4 {
				if n := cleanName(fl[3]); n != "" {
					leases[strings.ToLower(fl[1])] = n
				}
			}
		}
		f.Close()
	}
	arp := map[netip.Addr]string{}
	if f, err := os.Open("/proc/net/arp"); err == nil {
		sc := bufio.NewScanner(f)
		sc.Scan() // header
		for sc.Scan() {
			fl := strings.Fields(sc.Text())
			if len(fl) >= 6 && fl[2] != "0x0" && fl[3] != "00:00:00:00:00:00" {
				if ip, err := netip.ParseAddr(fl[0]); err == nil {
					arp[ip] = strings.ToLower(fl[3])
				}
			}
		}
		f.Close()
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if glcOK {
		d.glc = map[string]glClient{}
		for mac, c := range cl.Clients {
			if c.MAC == "" {
				c.MAC = mac
			}
			c.MAC = strings.ToLower(c.MAC)
			d.glc[c.MAC] = c
		}
	}
	d.leases = leases
	macOf := arp
	for _, c := range d.glc {
		if ip, err := netip.ParseAddr(c.IP); err == nil && c.Online {
			macOf[ip] = c.MAC
		}
	}
	d.macOf = macOf
	d.macs.Store(&macOf) // for the sniffer, which reads it without d.mu
	for _, dev := range d.devs {
		d.nameDevice(dev)
	}
}

func (d *trafficd) nameDevice(dev *tdDevice) {
	if dev.router {
		dev.online = true
		return
	}
	if c, ok := d.glc[dev.mac]; ok && dev.mac != "" {
		if n := cleanName(c.Name); n != "" {
			dev.name = n
		}
		dev.iface = c.Iface
		dev.online = c.Online
		if ip, err := netip.ParseAddr(c.IP); err == nil && c.Online {
			dev.ip = ip
		}
	}
	if dev.name == "" {
		dev.name = d.leases[dev.mac]
	}
	if dev.name == "" && dev.ip.IsValid() {
		dev.name = dev.ip.String()
	}
}

func (d *trafficd) device(key string) *tdDevice {
	dev := d.devs[key]
	if dev == nil {
		dev = newDevice(key)
		if key == "router" {
			dev.router = true
		}
		d.devs[key] = dev
	}
	return dev
}

// deviceFor finds the device of a LAN address.
func (d *trafficd) deviceFor(ip netip.Addr) *tdDevice {
	ip = ip.Unmap()
	mac := d.macOf[ip]
	key := mac
	if key == "" {
		key = "ip:" + ip.String()
	}
	dev := d.device(key)
	dev.ip, dev.mac = ip, mac
	if dev.name == "" || dev.name == ip.String() {
		d.nameDevice(dev)
	}
	return dev
}

// ---- mobile data ----

// refreshSIM reads GL's own counter of the active SIM, the one its web
// admin shows.
func (d *trafficd) refreshSIM() {
	var m struct {
		Slot int `json:"current_sim_slot"`
	}
	if ubusCallJSON("cellular.modem", "status", map[string]any{"bus": "cpu"}, &m) != nil {
		return
	}
	var t struct {
		Traffic []struct {
			Slot  int    `json:"slot"`
			Total string `json:"traffic_total"`
		} `json:"traffic"`
	}
	if ubusCallJSON("cellular.collect", "get_traffic", map[string]any{"bus": "cpu"}, &t) != nil {
		return
	}
	slot := max(m.Slot, 1)
	var total uint64
	found := false
	for _, e := range t.Traffic {
		if e.Slot != slot {
			continue
		}
		if v, err := strconv.ParseUint(e.Total, 10, 64); err == nil {
			total += v
			found = true
		}
	}
	if !found {
		return
	}
	d.mu.Lock()
	d.applySIM(slot, total, d.now())
	d.mu.Unlock()
}

func (d *trafficd) applySIM(slot int, total uint64, now time.Time) {
	today := d.dayOf(now)
	d.simOK, d.simSlot, d.simTotal = true, slot, total
	b := &d.st.SimDay
	if b.Day != today || b.Slot != slot || total < b.Total {
		*b = simMark{Day: today, Total: total, Slot: slot, At: now.Unix()}
	}
	d.st.SimLast = simMark{Day: today, Total: total, Slot: slot}
}

func (d *trafficd) simToday() uint64 {
	if !d.simOK || d.st.SimDay.Slot != d.simSlot || d.simTotal < d.st.SimDay.Total {
		return 0
	}
	return d.simTotal - d.st.SimDay.Total
}

// cycleStart is the first day of the billing period that holds t.
func cycleStart(t time.Time, day int) time.Time {
	if day < 1 || day > 28 {
		day = 1
	}
	y, m, dd := t.Date()
	if dd < day {
		m--
	}
	return time.Date(y, m, day, 0, 0, 0, 0, t.Location())
}

// simPeriod is the mobile data of the billing period so far: the kept
// days of the period and today. since is the first day it counts, later
// than the period's start when the history begins later.
func (d *trafficd) simPeriod() (used uint64, start, since time.Time) {
	now := d.now().In(d.loc)
	start = cycleStart(now, d.cfg.cycleDay)
	s := start.Format("2006-01-02")
	first := d.st.SimDay.Day
	for _, day := range d.st.Days {
		if day.Day >= s && day.SimOK {
			used += day.Sim
			if day.Day < first {
				first = day.Day
			}
		}
	}
	used += d.simToday()
	since = start
	if t, err := time.ParseInLocation("2006-01-02", first, d.loc); err == nil && t.After(start) {
		since = t
	}
	return used, start, since
}

// ---- configuration ----

func (d *trafficd) readConfig() {
	cfg := tdConfig{cycleDay: 1}
	if v, err := strconv.ParseFloat(uciGet("mudi-screen.traffic.cap_gb"), 64); err == nil && v > 0 {
		cfg.capGB = v
	}
	if v, err := strconv.Atoi(uciGet("mudi-screen.traffic.cycle_day")); err == nil && v >= 1 && v <= 28 {
		cfg.cycleDay = v
	}
	d.mu.Lock()
	d.cfg = cfg
	d.mu.Unlock()
}

func saveConfig(key, value string) error {
	if uciGet("mudi-screen.traffic") == "" {
		if _, err := runCmd(3*time.Second, "uci", "set", "mudi-screen.traffic=traffic"); err != nil {
			return err
		}
	}
	if _, err := runCmd(3*time.Second, "uci", "set", "mudi-screen.traffic."+key+"="+value); err != nil {
		return err
	}
	_, err := runCmd(5*time.Second, "uci", "commit", "mudi-screen")
	return err
}

// ---- sampling ----

// wanInterfaces are the interfaces of the default routes, tunnels left
// out: their traffic also crosses a real interface.
func wanInterfaces() []string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 8 || fl[1] != "00000000" || fl[7] != "00000000" || seen[fl[0]] {
			continue
		}
		name := fl[0]
		tunnel := false
		for _, p := range []string{"tun", "wg", "tailscale", "zt", "ipsec", "gre", "vti", "lo"} {
			if strings.HasPrefix(name, p) {
				tunnel = true
			}
		}
		if !tunnel {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func delta(cur, prev uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return cur // the counter started again
}

// step takes one sample.
func (d *trafficd) step() {
	flows, err := dumpConntrack()
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	first := d.last.IsZero()
	dt := tdTick.Seconds()
	if !first {
		dt = max(now.Sub(d.last).Seconds(), 0.2)
	}
	d.last = now
	d.tick++
	d.rollDay(now)
	d.sampleWAN(now, dt, first)
	if err != nil {
		if msg := err.Error(); msg != d.ctErr {
			d.log.Printf("traffic: connection table: %v", err)
			d.ctErr = msg
		}
	} else {
		d.ctErr = ""
		d.sampleFlows(flows, now, dt, first)
	}
	d.dns.tick(now)
}

func (d *trafficd) sampleWAN(now time.Time, dt float64, first bool) {
	ifs := wanInterfaces()
	cur := map[string][2]uint64{}
	var dd, du uint64
	for _, name := range ifs {
		rx, tx, ok := ifaceBytes(name)
		if !ok {
			continue
		}
		cur[name] = [2]uint64{rx, tx}
		if p, ok := d.wanPrev[name]; ok && !first {
			dd += delta(rx, p[0])
			du += delta(tx, p[1])
		}
	}
	d.wanPrev, d.wanIf = cur, ifs
	d.wanRate.update(dd, du, dt)
	d.wanLive[0].push(float64(dd) * 8 / dt)
	d.wanLive[1].push(float64(du) * 8 / dt)
	d.wanToday[0] += dd
	d.wanToday[1] += du
	m := now.Unix() / 60
	if d.minute == 0 {
		d.minute = m
	}
	for n := 0; d.minute < m && n <= tdMinPts; n++ {
		d.wanMin[0].push(float64(d.minAcc[0]))
		d.wanMin[1].push(float64(d.minAcc[1]))
		d.minAcc = [2]uint64{}
		d.minute++
	}
	d.minute = m
	d.minAcc[0] += dd
	d.minAcc[1] += du
}

// classify decides who a flow belongs to; ok is false for traffic that
// does not leave the router's networks.
func (d *trafficd) classify(f ctFlow) (fl tdFlow, ok bool) {
	a := d.addrs.Load()
	o, r := f.orig, f.reply
	o.src, o.dst, r.src = o.src.Unmap(), o.dst.Unmap(), r.src.Unmap()
	internal := func(ip netip.Addr) bool {
		return a.isLAN(ip) || a.isLocal(ip) || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255})
	}
	fl.proto = o.proto
	switch {
	case a.isLAN(o.src) && !a.isLocal(o.src):
		if internal(o.dst) {
			return fl, false
		}
		fl.dev = d.deviceFor(o.src).key
		fl.client, fl.remote, fl.lport, fl.rport = o.src, o.dst, o.sport, o.dport
	case a.isLocal(o.src) || o.src.IsLoopback():
		if internal(o.dst) {
			return fl, false
		}
		fl.dev = "router"
		d.device("router")
		fl.remote, fl.lport, fl.rport = o.dst, o.sport, o.dport
	case r.src.IsValid() && a.isLAN(r.src) && !a.isLocal(r.src) && !internal(o.src):
		// A port forwarded to a LAN device.
		fl.dev = d.deviceFor(r.src).key
		fl.client, fl.remote, fl.lport, fl.rport, fl.inbound = r.src, o.src, r.sport, o.sport, true
	case a.isLocal(o.dst) && !internal(o.src):
		fl.dev = "router"
		d.device("router")
		fl.remote, fl.lport, fl.rport, fl.inbound = o.src, o.dport, o.sport, true
	default:
		return fl, false
	}
	return fl, true
}

func (d *trafficd) nameFlow(fl *tdFlow) {
	name := d.names.lookup(fl.client, fl.remote, fl.lport)
	if name != "" {
		fl.host, fl.named = name, true
	} else if fl.host == "" {
		fl.host = fl.remote.String()
	}
	fl.svc = flowService(name, fl.remote, fl.proto, fl.rport)
}

func entity(m map[string]*tdEntity, name string) *tdEntity {
	e := m[name]
	if e == nil {
		e = &tdEntity{name: name, devs: map[string]uint64{}}
		m[name] = e
	}
	return e
}

func (d *trafficd) sampleFlows(flows []ctFlow, now time.Time, dt float64, first bool) {
	type acc struct {
		down, up uint64
		flows    int
	}
	perDev, perSvc, perHost, perProto := map[string]*acc{}, map[string]*acc{}, map[string]*acc{}, map[string]*acc{}
	add := func(m map[string]*acc, k string, down, up uint64) {
		a := m[k]
		if a == nil {
			a = &acc{}
			m[k] = a
		}
		a.down += down
		a.up += up
		a.flows++
	}
	d.nTCP, d.nUDP, d.nOther = 0, 0, 0
	for _, f := range flows {
		k := tdFlowKey{f.id, f.orig}
		fl := d.flows[k]
		if fl == nil {
			c, ok := d.classify(f)
			fl = &c
			fl.ignore = !ok
			fl.first = now
			fl.pname = protoName(fl.proto, fl.rport)
			if first {
				fl.prev = [2]uint64{f.origBytes, f.replyBytes}
			}
			d.flows[k] = fl
			if ok {
				d.nameFlow(fl)
			}
		}
		fl.tick = d.tick
		if fl.ignore {
			fl.prev = [2]uint64{f.origBytes, f.replyBytes}
			continue
		}
		if !fl.named && (now.Sub(fl.first) < 20*time.Second || d.tick%15 == 0) {
			d.nameFlow(fl)
		}
		dO, dR := delta(f.origBytes, fl.prev[0]), delta(f.replyBytes, fl.prev[1])
		fl.prev = [2]uint64{f.origBytes, f.replyBytes}
		down, up := dR, dO
		if fl.inbound {
			down, up = dO, dR
		}
		fl.rate.update(down, up, dt)
		switch fl.proto {
		case ipTCP:
			d.nTCP++
		case ipUDP:
			d.nUDP++
		default:
			d.nOther++
		}
		add(perDev, fl.dev, down, up)
		add(perSvc, fl.svc.name, down, up)
		add(perHost, fl.host, down, up)
		add(perProto, fl.pname, down, up)
		if b := down + up; b > 0 {
			dev := d.devs[fl.dev]
			if dev != nil {
				if _, ok := dev.svcs[fl.svc.name]; ok || len(dev.svcs) < tdMaxPer {
					dev.svcs[fl.svc.name] += b
				}
				if _, ok := dev.hosts[fl.host]; ok || len(dev.hosts) < tdMaxPer {
					dev.hosts[fl.host] += b
				}
			}
			entity(d.svcs, fl.svc.name).devs[fl.dev] += b
			entity(d.hosts, fl.host).devs[fl.dev] += b
			entity(d.protos, fl.pname).devs[fl.dev] += b
		}
		s := entity(d.svcs, fl.svc.name)
		s.cat = fl.svc.cat
		h := entity(d.hosts, fl.host)
		h.svc, h.cat = fl.svc.name, fl.svc.cat
	}
	for k, fl := range d.flows {
		if fl.tick != d.tick {
			delete(d.flows, k)
		}
	}
	for key, dev := range d.devs {
		a := perDev[key]
		if a == nil {
			a = &acc{}
		}
		dev.rate.update(a.down, a.up, dt)
		dev.live[0].push(float64(a.down) * 8 / dt)
		dev.live[1].push(float64(a.up) * 8 / dt)
		dev.today[0] += a.down
		dev.today[1] += a.up
		dev.flows = a.flows
		if a.down+a.up > 0 {
			dev.active = now
		}
	}
	for _, x := range []struct {
		m   map[string]*tdEntity
		acc map[string]*acc
	}{{d.svcs, perSvc}, {d.hosts, perHost}, {d.protos, perProto}} {
		for name, e := range x.m {
			a := x.acc[name]
			if a == nil {
				a = &acc{}
			}
			e.rate.update(a.down, a.up, dt)
			e.today[0] += a.down
			e.today[1] += a.up
			e.flows = a.flows
			if a.down+a.up > 0 {
				e.active = now
			}
		}
	}
	if len(d.hosts) > tdMaxHosts {
		pruneEntities(d.hosts, tdMaxHosts*3/4)
	}
	if len(d.svcs) > tdMaxHosts { // a name per flow can make as many services
		pruneEntities(d.svcs, tdMaxHosts*3/4)
	}
}

// pruneEntities keeps the n entities with the most traffic today among
// those without traffic now.
func pruneEntities(m map[string]*tdEntity, n int) {
	var idle []*tdEntity
	for _, e := range m {
		if e.rate.total() == 0 && e.flows == 0 {
			idle = append(idle, e)
		}
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].today[0]+idle[i].today[1] < idle[j].today[0]+idle[j].today[1] })
	for _, e := range idle {
		if len(m) <= n {
			break
		}
		delete(m, e.name)
	}
}

// ---- days ----

func topTotals[T any](items []T, n int, key func(T) (tdTotal, uint64)) []tdTotal {
	type kv struct {
		t tdTotal
		v uint64
	}
	var all []kv
	for _, it := range items {
		t, v := key(it)
		if v > 0 {
			all = append(all, kv{t, v})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	var out []tdTotal
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].t)
	}
	return out
}

func entityTotals(m map[string]*tdEntity, n int) []tdTotal {
	var list []*tdEntity
	for _, e := range m {
		list = append(list, e)
	}
	return topTotals(list, n, func(e *tdEntity) (tdTotal, uint64) {
		return tdTotal{Key: e.name, Cat: e.cat, Down: e.today[0], Up: e.today[1]}, e.today[0] + e.today[1]
	})
}

// todayRecord is the summary of the day so far.
func (d *trafficd) todayRecord(n int) tdDay {
	var devs []*tdDevice
	for _, dev := range d.devs {
		devs = append(devs, dev)
	}
	day := tdDay{Day: d.day, WanDown: d.wanToday[0], WanUp: d.wanToday[1], Sim: d.simToday(), SimOK: d.simOK && d.st.SimDay.Day == d.day,
		Devices: topTotals(devs, n, func(dev *tdDevice) (tdTotal, uint64) {
			return tdTotal{Key: dev.key, Name: dev.name, Down: dev.today[0], Up: dev.today[1]}, dev.today[0] + dev.today[1]
		}),
		Services: entityTotals(d.svcs, n), Hosts: entityTotals(d.hosts, n), Protos: entityTotals(d.protos, n)}
	return day
}

// rollDay closes the day at local midnight.
func (d *trafficd) rollDay(now time.Time) {
	today := d.dayOf(now)
	if today == d.day {
		return
	}
	rec := d.todayRecord(12)
	d.st.Days = append(d.st.Days, rec)
	if len(d.st.Days) > tdDays {
		d.st.Days = d.st.Days[len(d.st.Days)-tdDays:]
	}
	d.day = today
	d.wanToday = [2]uint64{}
	for _, dev := range d.devs {
		dev.today = [2]uint64{}
		dev.svcs, dev.hosts = map[string]uint64{}, map[string]uint64{}
	}
	for _, m := range []map[string]*tdEntity{d.svcs, d.hosts, d.protos} {
		for k, e := range m {
			if e.flows == 0 && e.rate.total() == 0 {
				delete(m, k)
				continue
			}
			e.today = [2]uint64{}
			e.devs = map[string]uint64{}
		}
	}
	for k, dev := range d.devs {
		if !dev.router && !dev.online && dev.flows == 0 {
			delete(d.devs, k)
		}
	}
	if d.simOK {
		d.st.SimDay = simMark{Day: today, Total: d.simTotal, Slot: d.simSlot}
	}
	d.dns.newDay(today)
	d.saveLocked()
}

// ---- flash ----

func (d *trafficd) load(path string) {
	st, err := readPersist(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		// Kept for a look, and out of the way of the next save.
		d.log.Printf("traffic: %s: %v; trying its copy", path, err)
		_ = os.Rename(path, path+".broken")
	}
	if err != nil {
		var berr error
		if st, berr = readPersist(path + ".bak"); berr != nil {
			if !errors.Is(berr, fs.ErrNotExist) {
				d.log.Printf("traffic: %s.bak: %v", path, berr)
			}
			return
		}
		d.log.Printf("traffic: the days come from %s.bak", path)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st.Days = st.Days
	if st.Today.Day == d.day {
		d.wanToday = [2]uint64{st.Today.WanDown, st.Today.WanUp}
		for _, t := range st.Today.Devices {
			dev := d.device(t.Key)
			dev.today = [2]uint64{t.Down, t.Up}
			if dev.name == "" {
				dev.name = t.Name
			}
			if !strings.HasPrefix(t.Key, "ip:") && t.Key != "router" {
				dev.mac = t.Key
			}
		}
		for _, x := range []struct {
			m  map[string]*tdEntity
			in []tdTotal
		}{{d.svcs, st.Today.Services}, {d.hosts, st.Today.Hosts}, {d.protos, st.Today.Protos}} {
			for _, t := range x.in {
				e := entity(x.m, t.Key)
				e.cat, e.today = t.Cat, [2]uint64{t.Down, t.Up}
			}
		}
		d.st.SimDay = st.SimDay
	} else if st.Today.Day != "" {
		// The router was off at midnight: close that day with what is known.
		rec := st.Today
		if st.SimLast.Day == rec.Day && st.SimDay.Day == rec.Day && st.SimLast.Slot == st.SimDay.Slot && st.SimLast.Total >= st.SimDay.Total {
			rec.Sim, rec.SimOK = st.SimLast.Total-st.SimDay.Total, true
		}
		d.st.Days = append(d.st.Days, rec)
		if len(d.st.Days) > tdDays {
			d.st.Days = d.st.Days[len(d.st.Days)-tdDays:]
		}
	}
	d.st.SimLast = st.SimLast
}

func readPersist(path string) (tdPersist, error) {
	var st tdPersist
	raw, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(raw, &st)
}

func (d *trafficd) saveLocked() {
	d.st.Version = 1
	d.st.Today = d.todayRecord(40)
	raw, err := json.Marshal(&d.st)
	if err != nil {
		return
	}
	if err := writeAtomic(tdStatePath, raw); err != nil {
		d.log.Printf("traffic: save: %v", err)
	}
	d.lastSave = d.now()
}

// writeAtomic replaces path with b so that a power cut leaves the old file
// or the new one, whole: the data is on flash before the rename, and the
// rename before it returns. A twin goes to path.bak the same way, for a
// file that is broken after all (load falls back on it).
func writeAtomic(path string, b []byte) error {
	if err := writeSynced(path, b); err != nil {
		return err
	}
	return writeSynced(path+".bak", b)
}

func writeSynced(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		df.Close()
	}
	return nil
}

// ---- requests ----

type tdRequest struct {
	Op   string   `json:"op"`
	Page string   `json:"page,omitempty"`
	Lang string   `json:"lang,omitempty"`
	ID   string   `json:"id,omitempty"`
	Args []string `json:"args,omitempty"`
}

func (d *trafficd) serve(path string) (net.Listener, error) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}
			go d.handle(c)
		}
	}()
	return l, nil
}

func (d *trafficd) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var req tdRequest
	if err := json.NewDecoder(io.LimitReader(c, 64<<10)).Decode(&req); err != nil {
		return
	}
	var resp any
	switch req.Op {
	case "view":
		resp = d.view(req.Page, req.Lang)
	case "action":
		msg, err := d.action(req.ID, req.Args, req.Lang)
		if err != nil {
			resp = map[string]string{"error": err.Error()}
		} else {
			resp = map[string]string{"message": msg}
		}
	default:
		resp = map[string]string{"error": "unknown request"}
	}
	_ = json.NewEncoder(c).Encode(resp)
}

func (d *trafficd) action(id string, args []string, lang string) (string, error) {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	t := d.viewer(lang)
	switch id {
	case "range":
		if arg == "live" || arg == "1h" || arg == "24h" {
			d.mu.Lock()
			d.ui.rng = arg
			d.mu.Unlock()
		}
		return "", nil
	case "basis":
		if arg == "now" || arg == "today" {
			d.mu.Lock()
			d.ui.basis = arg
			d.mu.Unlock()
		}
		return "", nil
	case "cap":
		v, err := strconv.ParseFloat(arg, 64)
		if err != nil || v < 0 || v > 100000 {
			return "", errors.New("bad cap")
		}
		if err := saveConfig("cap_gb", strconv.FormatFloat(v, 'f', -1, 64)); err != nil {
			return "", err
		}
		d.readConfig()
		return t.T("Saved"), nil
	case "cycle":
		v, err := strconv.Atoi(arg)
		if err != nil || v < 1 || v > 28 {
			return "", errors.New("bad day")
		}
		if err := saveConfig("cycle_day", strconv.Itoa(v)); err != nil {
			return "", err
		}
		d.readConfig()
		return t.T("Saved"), nil
	}
	return "", errors.New("unknown action " + id)
}

// ---- the service ----

func findAppDir(id string) string {
	for _, dir := range defaultAppDirs {
		p := filepath.Join(dir, id)
		if _, err := os.Stat(filepath.Join(p, "app.json")); err == nil {
			return p
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "apps", id)
	}
	return ""
}

func runTrafficd() int {
	logger, closeLog := openLog(tdLogPath)
	defer closeLog()
	d := newTrafficd(logger, loadLocation())
	d.appDir = findAppDir("traffic")
	d.load(tdStatePath)
	d.readConfig()
	d.refreshAddrs()
	d.refreshClients()
	d.refreshSIM()
	d.dns.newDay(d.day)
	sn := &sniffer{names: d.names, dns: d.dns, log: logger, open: map[string]int{},
		isLAN: func(ip netip.Addr) bool { a := d.addrs.Load(); return a.isLAN(ip) && !a.isLocal(ip) },
		macOf: func(ip netip.Addr) (string, bool) {
			if m := d.macs.Load(); m != nil {
				mac, ok := (*m)[ip]
				return mac, ok
			}
			return "", false
		}}
	sn.refresh()
	l, err := d.serve(tdSockPath)
	if err != nil {
		logger.Printf("traffic: %v", err)
		return 1
	}
	defer l.Close()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	signal.Ignore(syscall.SIGHUP)
	logger.Printf("traffic: collector %s started, pid %d, cap %.0f GB, period from day %d",
		version, os.Getpid(), d.cfg.capGB, d.cfg.cycleDay)
	tick := time.NewTicker(tdTick)
	defer tick.Stop()
	for n := 1; ; n++ {
		select {
		case <-tick.C:
			d.step()
			if n%8 == 0 { // 16 s
				d.refreshClients()
				d.refreshAddrs()
			}
			if n%30 == 0 { // 1 min
				d.refreshSIM()
				sn.refresh()
				d.names.sweep()
				d.mu.Lock()
				if d.now().Sub(d.lastSave) >= time.Hour {
					d.saveLocked()
				}
				d.mu.Unlock()
			}
		case s := <-sig:
			d.mu.Lock()
			d.saveLocked()
			d.mu.Unlock()
			logger.Printf("traffic: %v, saved", s)
			_ = os.Remove(tdSockPath)
			return 0
		}
	}
}
