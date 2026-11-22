package main

// Pages of the traffic analyzer, built from the collector's state. Texts
// are English templates; lang/<lang>.tsv of the app translates them here,
// so the screen gets finished text.

import (
	"math"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var catColors = map[string]string{
	catVideo: "#FF5A5F", catMusic: "#FF7AB6", catSocial: "#4C8DFF", catMessaging: "#34C759", catCalls: "#30C8C8",
	catCloud: "#9F7AEA", catWeb: "#5AC8FA", catShopping: "#FF9F0A", catGaming: "#BF5AF2", catAI: "#FFD60A",
	catWork: "#64D2FF", catSystem: "#8C98AD", catAds: "#FF453A", catCDN: "#6E7B91", catVPN: "#FFC40A",
	catDNS: "#A0A8B8", catOther: "#5C677D",
}

func catColor(cat string) string {
	if c, ok := catColors[cat]; ok {
		return c
	}
	return catColors[catOther]
}

type tview struct {
	d    *trafficd
	lang string
	c    *catalog
	now  time.Time
}

func (d *trafficd) cat(lang string) *catalog {
	d.catMu.Lock()
	defer d.catMu.Unlock()
	if c, ok := d.catalog[lang]; ok {
		return c
	}
	var c *catalog
	if d.appDir != "" && lang != "" && lang != "en" {
		c = loadCatalog([]string{filepath.Join(d.appDir, "lang", lang+".tsv")})
	}
	d.catalog[lang] = c
	return c
}

func (d *trafficd) viewer(lang string) *tview {
	return &tview{d: d, lang: lang, c: d.cat(lang), now: d.now()}
}

// fill puts args into "{}" (in order) and "{1}".."{9}" of a template.
func fill(t string, args ...string) string {
	var b strings.Builder
	next := 0
	for i := 0; i < len(t); i++ {
		if t[i] == '{' && i+1 < len(t) {
			if t[i+1] == '}' {
				if next < len(args) {
					b.WriteString(args[next])
				}
				next++
				i++
				continue
			}
			if i+2 < len(t) && t[i+1] >= '1' && t[i+1] <= '9' && t[i+2] == '}' {
				if n := int(t[i+1] - '1'); n < len(args) {
					b.WriteString(args[n])
				}
				i += 2
				continue
			}
		}
		b.WriteByte(t[i])
	}
	return b.String()
}

func (t *tview) T(s string) string                   { return t.c.tmpl(s) }
func (t *tview) F(tpl string, args ...string) string { return fill(t.c.tmpl(tpl), args...) }
func itoa(n int) string                              { return strconv.Itoa(n) }
func (t *tview) dec(s string) string {
	if t.lang == "ru" {
		return strings.Replace(s, ".", ",", 1)
	}
	return s
}

func (t *tview) num(v float64) string {
	if v >= 10 || v == 0 {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	return t.dec(strconv.FormatFloat(v, 'f', 1, 64))
}

// rate formats bit/s: value and unit.
func (t *tview) rate(bps float64) (string, string) {
	switch {
	case bps >= 1e9:
		return t.num(bps / 1e9), t.T("Gbit/s")
	case bps >= 1e6:
		return t.num(bps / 1e6), t.T("Mbit/s")
	}
	return t.num(bps / 1e3), t.T("kbit/s")
}

func (t *tview) rateS(bps float64) string { v, u := t.rate(bps); return v + " " + u }

// ratePair is "↓ 6.0 ↑ 0.3 Mbit/s": both rates in the unit of the larger.
func (t *tview) ratePair(down, up float64) string {
	unit, div := "kbit/s", 1e3
	switch m := max(down, up); {
	case m >= 1e9:
		unit, div = "Gbit/s", 1e9
	case m >= 1e6:
		unit, div = "Mbit/s", 1e6
	}
	return "↓ " + t.num(down/div) + " ↑ " + t.num(up/div) + " " + t.T(unit)
}

// size formats bytes in binary units, the way GL's admin panel shows its
// data counter (205.8 GB is 205.8 × 2^30 bytes).
func (t *tview) size(b uint64) (string, string) {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(b)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	var s string
	switch {
	case i == 0:
		s = strconv.FormatUint(b, 10)
	case i >= 3 && f < 1000, f < 10:
		s = t.dec(strconv.FormatFloat(f, 'f', 1, 64))
	default:
		s = strconv.FormatFloat(math.Round(f), 'f', 0, 64)
	}
	return s, t.T(units[i])
}

func (t *tview) sizeS(b uint64) string { v, u := t.size(b); return v + " " + u }

func (t *tview) pct(part, whole float64) string {
	if whole <= 0 {
		return "0%"
	}
	p := part / whole * 100
	if p > 0 && p < 1 {
		return "<1%"
	}
	return strconv.Itoa(int(math.Round(p))) + "%"
}

func (t *tview) age(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return t.F("{} s", itoa(s))
	case s < 3600:
		return t.F("{} min", itoa(s/60))
	}
	return t.F("{} h {} min", itoa(s/3600), itoa(s%3600/60))
}

func (t *tview) ifaceLabel(s string) string {
	switch strings.ToLower(s) {
	case "2.4g":
		return t.T("Wi-Fi 2.4 GHz")
	case "5g":
		return t.T("Wi-Fi 5 GHz")
	case "6g":
		return t.T("Wi-Fi 6 GHz")
	case "tethering", "usb":
		return "USB"
	case "lan", "cable", "eth":
		return t.T("Cable")
	case "", "unknown":
		return ""
	}
	return s
}

func (t *tview) devName(key string) string {
	if key == "router" {
		return t.T("Router")
	}
	if dev := t.d.devs[key]; dev != nil && dev.name != "" {
		return dev.name
	}
	return strings.TrimPrefix(key, "ip:")
}

func segs(id, cur string, opts ...[2]string) Item {
	it := Item{Type: "segments"}
	for _, o := range opts {
		it.Options = append(it.Options, Option{Label: o[1], Checked: o[0] == cur, Action: &Action{ID: id, Args: []string{o[0]}}})
	}
	return it
}

func section(label string) Item { return Item{Type: "section", Label: label} }

// rateChart makes a chart of download and upload in bit/s.
func (t *tview) rateChart(label string, down, up []float64, span int, xStart, style string) Item {
	peak := 0.0
	for i := range down {
		peak = max(peak, down[i], up[i])
	}
	unit, div := "kbit/s", 1e3
	switch {
	case peak >= 1e9:
		unit, div = "Gbit/s", 1e9
	case peak >= 1e6:
		unit, div = "Mbit/s", 1e6
	}
	scale := func(v []float64) []float64 {
		out := make([]float64, len(v))
		for i := range v {
			out[i] = math.Round(v[i]/div*1000) / 1000
		}
		return out
	}
	return Item{Type: "chart", Label: label, Unit: t.T(unit), Span: span, XStart: xStart, XEnd: t.T("now"), Style: style,
		Series: []Series{{Label: "↓", Color: "blue", Values: scale(down)}, {Label: "↑", Color: "green", Values: scale(up)}}}
}

// ---- the overview ----

// rangeSeries is the internet traffic of a range in bit/s.
func (d *trafficd) rangeSeries(rng string) (down, up []float64, span int, total [2]float64, peak float64) {
	switch rng {
	case "1h", "24h":
		n, group := 60, 1
		if rng == "24h" {
			n, group = tdMinPts, 10
		}
		dm, um := d.wanMin[0].last(n), d.wanMin[1].last(n)
		for i := range dm {
			total[0] += dm[i]
			total[1] += um[i]
			peak = max(peak, dm[i]*8/60, um[i]*8/60)
		}
		total[0] += float64(d.minAcc[0])
		total[1] += float64(d.minAcc[1])
		for end := len(dm); end > 0; end -= group {
			start := max(0, end-group)
			var sd, su float64
			for i := start; i < end; i++ {
				sd += dm[i]
				su += um[i]
			}
			secs := float64(60 * (end - start))
			down = append([]float64{sd * 8 / secs}, down...)
			up = append([]float64{su * 8 / secs}, up...)
		}
		return down, up, n / group, total, peak
	}
	down, up = d.wanLive[0].last(90), d.wanLive[1].last(90)
	for i := range down {
		total[0] += down[i] * tdTick.Seconds() / 8
		total[1] += up[i] * tdTick.Seconds() / 8
		peak = max(peak, down[i], up[i])
	}
	return down, up, 90, total, peak
}

func (t *tview) capBytes() uint64 { return uint64(t.d.cfg.capGB * (1 << 30)) }

// simTodayLabel says how much mobile data today took: since midnight, or
// since the collector began counting on its first day.
func (t *tview) simTodayLabel() string {
	if at := t.d.st.SimDay.At; at != 0 {
		return t.F("{} since {}", t.sizeS(t.d.simToday()), time.Unix(at, 0).In(t.d.loc).Format("15:04"))
	}
	return t.F("today {}", t.sizeS(t.d.simToday()))
}

// emptyList is the text of a list that has nothing to show.
func (t *tview) emptyList(now bool) Item {
	if now {
		return Item{Type: "text", Label: t.T("No traffic right now. Today shows the whole day."), Color: "dim"}
	}
	return Item{Type: "text", Label: t.T("No traffic yet today."), Color: "dim"}
}

func (t *tview) periodName() string {
	if t.d.cfg.cycleDay == 1 {
		return t.T("This month")
	}
	return t.T("This period")
}

func usageColor(frac float64) string {
	switch {
	case frac >= 1:
		return "red"
	case frac >= 0.8:
		return "orange"
	}
	return "green"
}

func (t *tview) card() *Card {
	d := t.d
	today := d.wanToday[0] + d.wanToday[1]
	if d.simOK {
		today = d.simToday()
	}
	c := &Card{Title: t.F("Traffic · {}", t.sizeS(today)), Icon: "chart", Color: "purple", Open: "main",
		Subtitle: t.ratePair(d.wanRate.down, d.wanRate.up)}
	if limit := t.capBytes(); d.simOK && limit > 0 {
		used, _, _ := d.simPeriod()
		if frac := float64(used) / float64(limit); frac >= 0.8 {
			c.Subtitle = t.F("{} of {} used", t.sizeS(used), t.sizeS(limit))
			c.SubtitleColor = usageColor(frac)
		}
	}
	return c
}

func (t *tview) lockLine() *Line {
	d := t.d
	limit := t.capBytes()
	if !d.simOK || limit == 0 {
		return nil
	}
	used, _, _ := d.simPeriod()
	if frac := float64(used) / float64(limit); frac >= 0.9 {
		return &Line{Text: t.F("Mobile data: {} of {}", t.sizeS(used), t.sizeS(limit)), Color: usageColor(frac)}
	}
	return nil
}

func (t *tview) pageMain() *Page {
	d := t.d
	p := &Page{Title: t.T("Traffic")}
	rng := d.ui.rng
	p.Items = append(p.Items, segs("range", rng, [2]string{"live", t.T("Live")}, [2]string{"1h", t.T("1 hour")},
		[2]string{"24h", t.T("24 hours")}))
	down, up, span, total, peak := d.rangeSeries(rng)
	stats := Item{Type: "stats"}
	pv, pu := t.rate(peak)
	if rng == "live" {
		dv, du := t.rate(d.wanRate.down)
		uv, uu := t.rate(d.wanRate.up)
		stats.Stats = []Stat{{Label: t.T("Download"), Value: dv, Unit: du, Color: "blue"},
			{Label: t.T("Upload"), Value: uv, Unit: uu, Color: "green"}, {Label: t.T("Peak"), Value: pv, Unit: pu}}
	} else {
		dv, du := t.size(uint64(total[0]))
		uv, uu := t.size(uint64(total[1]))
		stats.Stats = []Stat{{Label: t.T("Downloaded"), Value: dv, Unit: du, Color: "blue"},
			{Label: t.T("Uploaded"), Value: uv, Unit: uu, Color: "green"}, {Label: t.T("Peak"), Value: pv, Unit: pu}}
	}
	xStart := map[string]string{"live": t.T("−3 min"), "1h": t.T("−1 h"), "24h": t.T("−24 h")}[rng]
	style := ""
	if rng == "24h" {
		style = "bars"
	}
	p.Items = append(p.Items, stats, t.rateChart(t.T("Internet"), down, up, span, xStart, style))
	if len(d.wanIf) > 0 {
		p.Items = append(p.Items, Item{Type: "info", Label: t.T("Interface"), Value: strings.Join(d.wanIf, ", ")})
	}

	if d.simOK {
		p.Items = append(p.Items, section(t.T("Mobile data")))
		used, _, _ := d.simPeriod()
		if limit := t.capBytes(); limit > 0 {
			frac := float64(used) / float64(limit)
			p.Items = append(p.Items, Item{Type: "bar", Label: t.periodName(), Value: t.F("{} of {}", t.sizeS(used), t.sizeS(limit)),
				Sub: t.simTodayLabel(), Fraction: min(frac, 1), Color: usageColor(frac), Open: "mobile"})
		} else {
			p.Items = append(p.Items, Item{Type: "link", Label: t.periodName(), Value: t.sizeS(used),
				Sub: t.simTodayLabel(), Icon: "signal", Color: "blue", Open: "mobile"})
		}
	}

	p.Items = append(p.Items, section(t.T("Details")))
	online := 0
	for _, dev := range d.devs {
		if !dev.router && (dev.online || dev.flows > 0) {
			online++
		}
	}
	topSvc, topProto := "", ""
	if s := t.sortedEntities(d.svcs, true); len(s) > 0 && s[0].rate.total() > 0 {
		topSvc = s[0].name
	} else if s := t.sortedEntities(d.svcs, false); len(s) > 0 {
		topSvc = s[0].name
	}
	if s := t.sortedEntities(d.protos, false); len(s) > 0 {
		topProto = s[0].name
	}
	hosts := 0
	for _, h := range d.hosts {
		if h.today[0]+h.today[1] > 0 || h.flows > 0 {
			hosts++
		}
	}
	d.dns.mu.Lock()
	perMin := d.dns.perMin.last(1)
	d.dns.mu.Unlock()
	qpm := "0"
	if len(perMin) > 0 {
		qpm = itoa(int(perMin[0]))
	}
	p.Items = append(p.Items,
		Item{Type: "link", Label: t.T("Devices"), Value: t.F("{} online", itoa(online)), Icon: "devices", Open: "devices"},
		Item{Type: "link", Label: t.T("Apps & services"), Value: t.T(topSvc), Icon: "globe", Open: "services"},
		Item{Type: "link", Label: t.T("Hosts"), Value: itoa(hosts), Icon: "info", Open: "hosts"},
		Item{Type: "link", Label: t.T("Connections"), Value: itoa(d.nTCP + d.nUDP + d.nOther), Icon: "chart", Open: "flows"},
		Item{Type: "link", Label: t.T("Protocols"), Value: t.T(topProto), Icon: "gear", Open: "protocols"},
		Item{Type: "link", Label: "DNS", Value: t.F("{}/min", qpm), Icon: "check", Open: "dns"},
		Item{Type: "link", Label: t.T("Settings"), Icon: "gear", Open: "settings"})
	if d.ctErr != "" {
		p.Items = append(p.Items, Item{Type: "text", Label: t.F("The connection table cannot be read: {}", d.ctErr), Color: "red"})
	}
	return p
}

// ---- lists ----

func (t *tview) sortedEntities(m map[string]*tdEntity, now bool) []*tdEntity {
	var out []*tdEntity
	for _, e := range m {
		if now && e.rate.total() == 0 || !now && e.today[0]+e.today[1] == 0 {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if now {
			if a.rate.total() != b.rate.total() {
				return a.rate.total() > b.rate.total()
			}
		}
		if a.today[0]+a.today[1] != b.today[0]+b.today[1] {
			return a.today[0]+a.today[1] > b.today[0]+b.today[1]
		}
		return a.name < b.name
	})
	return out
}

// amount is what a list shows for something: its rate now, or its bytes
// today; the value is for comparing.
func (t *tview) amount(now bool, r rate2, today [2]uint64) (string, float64, float64, float64) {
	if now {
		return t.rateS(r.total()), r.total(), r.down, r.up
	}
	return t.sizeS(today[0] + today[1]), float64(today[0] + today[1]), float64(today[0]), float64(today[1])
}

func (t *tview) basisSegs() Item {
	return segs("basis", t.d.ui.basis, [2]string{"now", t.T("Now")}, [2]string{"today", t.T("Today")})
}

func (t *tview) pageDevices() *Page {
	d := t.d
	now := d.ui.basis == "now"
	p := &Page{Title: t.T("Devices")}
	p.Items = append(p.Items, t.basisSegs())
	var list []*tdDevice
	for _, dev := range d.devs {
		if dev.online || dev.flows > 0 || dev.today[0]+dev.today[1] > 0 {
			list = append(list, dev)
		}
	}
	val := func(dev *tdDevice) float64 {
		if now {
			return dev.rate.total()
		}
		return float64(dev.today[0] + dev.today[1])
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if (a.online || a.flows > 0) != (b.online || b.flows > 0) {
			return a.online || a.flows > 0
		}
		if val(a) != val(b) {
			return val(a) > val(b)
		}
		return t.devName(a.key) < t.devName(b.key)
	})
	top := 1.0
	for _, dev := range list {
		top = max(top, val(dev))
	}
	offline := false
	for _, dev := range list {
		if !(dev.online || dev.flows > 0) && !offline {
			offline = true
			p.Items = append(p.Items, section(t.T("Offline today")))
		}
		value, _, dn, upv := t.amount(now, dev.rate, dev.today)
		sub := ""
		if now {
			sub = t.ratePair(dev.rate.down, dev.rate.up) + " · " + t.F("{} conn.", itoa(dev.flows))
		} else {
			sub = t.F("↓ {} ↑ {}", t.sizeS(dev.today[0]), t.sizeS(dev.today[1]))
		}
		if dev.router {
			sub = t.T("own traffic and VPN tunnels")
		}
		p.Items = append(p.Items, Item{Type: "bar", Label: t.devName(dev.key), Sub: sub, Value: value,
			Fraction: dn / top, Fraction2: upv / top, Color: "blue", Color2: "green", Open: "dev:" + dev.key})
	}
	if len(list) == 0 {
		p.Items = append(p.Items, t.emptyList(false))
	} else {
		p.Items = append(p.Items, Item{Type: "text", Label: t.T("Blue: download, green: upload."), Color: "dim"})
	}
	return p
}

func (t *tview) pageDevice(key string) *Page {
	d := t.d
	dev := d.devs[key]
	if dev == nil {
		return &Page{Title: t.T("Device"), Empty: t.T("No traffic from this device yet")}
	}
	p := &Page{Title: t.devName(key)}
	dv, du := t.rate(dev.rate.down)
	uv, uu := t.rate(dev.rate.up)
	tv, tu := t.size(dev.today[0] + dev.today[1])
	p.Items = append(p.Items, Item{Type: "stats", Stats: []Stat{{Label: t.T("Download"), Value: dv, Unit: du, Color: "blue"},
		{Label: t.T("Upload"), Value: uv, Unit: uu, Color: "green"}, {Label: t.T("Today"), Value: tv, Unit: tu}}},
		t.rateChart(t.T("Speed"), dev.live[0].last(90), dev.live[1].last(90), 90, t.T("−3 min"), ""))
	p.Items = append(p.Items, section(t.T("Device")))
	if dev.router {
		p.Items = append(p.Items, Item{Type: "text", Label: t.T("The router's own connections: DNS, time, updates, and VPN tunnels that carry the devices' traffic."), Color: "dim"})
	} else {
		if l := t.ifaceLabel(dev.iface); l != "" {
			p.Items = append(p.Items, Item{Type: "info", Label: t.T("Connection"), Value: l})
		}
		if dev.ip.IsValid() {
			p.Items = append(p.Items, Item{Type: "info", Label: t.T("IP address"), Value: dev.ip.String()})
		}
		if dev.mac != "" {
			p.Items = append(p.Items, Item{Type: "info", Label: "MAC", Value: strings.ToUpper(dev.mac)})
		}
	}
	p.Items = append(p.Items, Item{Type: "info", Label: t.T("Connections now"), Value: itoa(dev.flows)},
		Item{Type: "info", Label: t.T("Today"), Value: t.F("↓ {} ↑ {}", t.sizeS(dev.today[0]), t.sizeS(dev.today[1]))})

	type kv struct {
		k string
		v uint64
	}
	var svcs []kv
	for k, v := range dev.svcs {
		svcs = append(svcs, kv{k, v})
	}
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].v > svcs[j].v || svcs[i].v == svcs[j].v && svcs[i].k < svcs[j].k })
	if len(svcs) > 0 {
		p.Items = append(p.Items, section(t.T("Apps & services today")))
		for i, s := range svcs {
			if i == 8 {
				break
			}
			cat := catOther
			if e := d.svcs[s.k]; e != nil {
				cat = e.cat
			}
			p.Items = append(p.Items, Item{Type: "bar", Label: t.T(s.k), Value: t.sizeS(s.v), Sub: t.T(cat),
				Fraction: float64(s.v) / float64(svcs[0].v), Color: catColor(cat), Open: "svc:" + s.k})
		}
	}
	var flows []*tdFlow
	for _, fl := range d.flows {
		if !fl.ignore && fl.dev == key {
			flows = append(flows, fl)
		}
	}
	sortFlows(flows)
	if len(flows) > 0 {
		p.Items = append(p.Items, section(t.T("Connections now")))
		for i, fl := range flows {
			if i == 12 {
				break
			}
			p.Items = append(p.Items, t.flowItem(fl, false))
		}
	}
	return p
}

func sortFlows(flows []*tdFlow) {
	sort.Slice(flows, func(i, j int) bool {
		a, b := flows[i], flows[j]
		if a.rate.total() != b.rate.total() {
			return a.rate.total() > b.rate.total()
		}
		if a.prev[0]+a.prev[1] != b.prev[0]+b.prev[1] {
			return a.prev[0]+a.prev[1] > b.prev[0]+b.prev[1]
		}
		return a.host < b.host
	})
}

func (t *tview) flowItem(fl *tdFlow, withDevice bool) Item {
	value := "—"
	if fl.rate.total() > 0 {
		value = t.rateS(fl.rate.total())
	}
	sub := t.T(fl.pname) + " · " + t.age(t.now.Sub(fl.first))
	switch {
	case withDevice:
		sub = t.devName(fl.dev) + " · " + sub
	case fl.svc.name != svcUnknown && fl.svc.name != fl.host && !strings.HasSuffix(fl.host, fl.svc.name):
		sub = t.T(fl.svc.name) + " · " + sub
	}
	return Item{Type: "entry", Label: fl.host, Sub: sub, Value: value}
}

func (t *tview) pageServices() *Page {
	d := t.d
	now := d.ui.basis == "now"
	p := &Page{Title: t.T("Apps & services")}
	p.Items = append(p.Items, t.basisSegs())
	list := t.sortedEntities(d.svcs, now)
	if len(list) == 0 {
		p.Items = append(p.Items, t.emptyList(now))
		return p
	}
	// Categories first: where the traffic goes, at a glance.
	type catSum struct {
		cat string
		v   float64
	}
	sums := map[string]float64{}
	whole := 0.0
	for _, e := range list {
		_, v, _, _ := t.amount(now, e.rate, e.today)
		sums[e.cat] += v
		whole += v
	}
	var cats []catSum
	for c, v := range sums {
		cats = append(cats, catSum{c, v})
	}
	sort.Slice(cats, func(i, j int) bool {
		return cats[i].v > cats[j].v || cats[i].v == cats[j].v && cats[i].cat < cats[j].cat
	})
	p.Items = append(p.Items, section(t.T("Categories")))
	for i, c := range cats {
		if i == 6 {
			break
		}
		sub := t.sizeS(uint64(c.v))
		if now {
			sub = t.rateS(c.v)
		}
		p.Items = append(p.Items, Item{Type: "bar", Label: t.T(c.cat), Value: t.pct(c.v, whole), Sub: sub,
			Fraction: c.v / cats[0].v, Color: catColor(c.cat)})
	}
	p.Items = append(p.Items, section(t.T("Apps & services")))
	_, top, _, _ := t.amount(now, list[0].rate, list[0].today)
	for i, e := range list {
		if i == 40 {
			break
		}
		value, v, _, _ := t.amount(now, e.rate, e.today)
		n := 0
		for _, b := range e.devs {
			if b > 0 {
				n++
			}
		}
		p.Items = append(p.Items, Item{Type: "bar", Label: t.T(e.name), Value: value, Sub: t.F("{} · devices: {}", t.T(e.cat), itoa(max(n, 1))),
			Fraction: v / max(top, 1), Color: catColor(e.cat), Open: "svc:" + e.name})
	}
	return p
}

func (t *tview) pageService(name string) *Page {
	d := t.d
	e := d.svcs[name]
	p := &Page{Title: t.T(name)}
	if e == nil {
		p.Empty = t.T("No traffic yet")
		return p
	}
	rv, ru := t.rate(e.rate.total())
	tv, tu := t.size(e.today[0] + e.today[1])
	p.Items = append(p.Items, Item{Type: "stats", Stats: []Stat{{Label: t.T("Now"), Value: rv, Unit: ru, Color: catColor(e.cat)},
		{Label: t.T("Today"), Value: tv, Unit: tu}, {Label: t.T("Connections"), Value: itoa(e.flows)}}},
		Item{Type: "info", Label: t.T("Category"), Value: t.T(e.cat)})
	type kv struct {
		k string
		v uint64
	}
	var devs []kv
	for k, v := range e.devs {
		devs = append(devs, kv{k, v})
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].v > devs[j].v || devs[i].v == devs[j].v && devs[i].k < devs[j].k })
	if len(devs) > 0 {
		p.Items = append(p.Items, section(t.T("Devices today")))
		for _, dv := range devs {
			p.Items = append(p.Items, Item{Type: "bar", Label: t.devName(dv.k), Value: t.sizeS(dv.v),
				Fraction: float64(dv.v) / float64(max(devs[0].v, 1)), Color: catColor(e.cat), Open: "dev:" + dv.k})
		}
	}
	var hosts []*tdEntity
	for _, h := range d.hosts {
		if h.svc == name && (h.today[0]+h.today[1] > 0 || h.flows > 0) {
			hosts = append(hosts, h)
		}
	}
	sort.Slice(hosts, func(i, j int) bool {
		a, b := hosts[i].today[0]+hosts[i].today[1], hosts[j].today[0]+hosts[j].today[1]
		return a > b || a == b && hosts[i].name < hosts[j].name
	})
	if len(hosts) > 0 {
		p.Items = append(p.Items, section(t.T("Hosts")))
		for i, h := range hosts {
			if i == 15 {
				break
			}
			p.Items = append(p.Items, Item{Type: "entry", Label: h.name, Value: t.sizeS(h.today[0] + h.today[1])})
		}
	}
	return p
}

func (t *tview) pageHosts() *Page {
	d := t.d
	now := d.ui.basis == "now"
	p := &Page{Title: t.T("Hosts")}
	p.Items = append(p.Items, t.basisSegs())
	list := t.sortedEntities(d.hosts, now)
	for i, h := range list {
		if i == 50 {
			break
		}
		value, _, _, _ := t.amount(now, h.rate, h.today)
		topDev, topV := "", uint64(0)
		for k, v := range h.devs {
			if v > topV || v == topV && k < topDev {
				topDev, topV = k, v
			}
		}
		sub := t.T(h.svc)
		if topDev != "" {
			sub += " · " + t.devName(topDev)
		}
		p.Items = append(p.Items, Item{Type: "entry", Label: h.name, Sub: sub, Value: value})
	}
	if len(list) == 0 {
		p.Items = append(p.Items, t.emptyList(now))
	}
	return p
}

func (t *tview) pageFlows() *Page {
	d := t.d
	p := &Page{Title: t.T("Connections")}
	p.Items = append(p.Items, Item{Type: "stats", Stats: []Stat{{Label: t.T("Total"), Value: itoa(d.nTCP + d.nUDP + d.nOther)},
		{Label: "TCP", Value: itoa(d.nTCP), Color: "blue"}, {Label: "UDP", Value: itoa(d.nUDP), Color: "purple"}}})
	var flows []*tdFlow
	for _, fl := range d.flows {
		if !fl.ignore {
			flows = append(flows, fl)
		}
	}
	sortFlows(flows)
	p.Items = append(p.Items, section(t.T("Fastest first")))
	for i, fl := range flows {
		if i == 40 {
			break
		}
		p.Items = append(p.Items, t.flowItem(fl, true))
	}
	if len(flows) == 0 {
		p.Items = append(p.Items, Item{Type: "text", Label: t.T("No connections"), Color: "dim"})
	}
	return p
}

func (t *tview) pageProtocols() *Page {
	d := t.d
	now := d.ui.basis == "now"
	p := &Page{Title: t.T("Protocols")}
	p.Items = append(p.Items, t.basisSegs())
	list := t.sortedEntities(d.protos, now)
	whole := 0.0
	for _, e := range list {
		_, v, _, _ := t.amount(now, e.rate, e.today)
		whole += v
	}
	for _, e := range list {
		value, v, _, _ := t.amount(now, e.rate, e.today)
		p.Items = append(p.Items, Item{Type: "bar", Label: t.T(e.name), Value: t.pct(v, whole),
			Sub: t.F("{} · {} conn.", value, itoa(e.flows)), Fraction: v / max(whole, 1), Color: "purple"})
	}
	if len(list) == 0 {
		p.Items = append(p.Items, t.emptyList(now))
	}
	return p
}

func (t *tview) pageDNS() *Page {
	d := t.d
	p := &Page{Title: "DNS"}
	s := d.dns
	s.mu.Lock()
	defer s.mu.Unlock()
	perMin := s.perMin.last(60)
	last := 0
	if len(perMin) > 0 {
		last = int(perMin[len(perMin)-1])
	}
	p.Items = append(p.Items, Item{Type: "stats", Stats: []Stat{{Label: t.T("Per minute"), Value: itoa(last)},
		{Label: t.T("Blocked"), Value: itoa(s.blocked), Color: "red"}, {Label: t.T("Not found"), Value: itoa(s.notFound), Color: "orange"}}},
		Item{Type: "chart", Label: t.T("Answers per minute"), Span: 60, Style: "bars", XStart: t.T("−1 h"), XEnd: t.T("now"),
			Series: []Series{{Color: "blue", Values: perMin}}})
	type kv struct {
		k string
		v int
	}
	top := func(m map[string]int, n int) []kv {
		var out []kv
		for k, v := range m {
			out = append(out, kv{k, v})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].v > out[j].v || out[i].v == out[j].v && out[i].k < out[j].k })
		if len(out) > n {
			out = out[:n]
		}
		return out
	}
	if names := top(s.names, 15); len(names) > 0 {
		p.Items = append(p.Items, section(t.F("Names today: {}", itoa(s.total))))
		for _, n := range names {
			p.Items = append(p.Items, Item{Type: "entry", Label: n.k, Sub: t.T(classifyName(n.k).name), Value: itoa(n.v)})
		}
	}
	if blocked := top(s.blockedN, 8); len(blocked) > 0 {
		p.Items = append(p.Items, section(t.T("Blocked today")))
		for _, n := range blocked {
			p.Items = append(p.Items, Item{Type: "entry", Label: n.k, Value: itoa(n.v), Color: "red"})
		}
	}
	if len(s.clients) > 0 {
		p.Items = append(p.Items, section(t.T("Devices today")))
		type ck struct {
			a netip.Addr
			v int
		}
		var cl []ck
		for a, v := range s.clients {
			cl = append(cl, ck{a, v})
		}
		sort.Slice(cl, func(i, j int) bool { return cl[i].v > cl[j].v })
		for _, c := range cl {
			name := c.a.String()
			if mac := d.macOf[c.a]; mac != "" {
				name = t.devName(mac)
			}
			p.Items = append(p.Items, Item{Type: "entry", Label: name, Value: itoa(c.v)})
		}
	}
	p.Items = append(p.Items, Item{Type: "text", Color: "dim",
		Label: t.T("Names come from the router's DNS answers and from TLS. Devices with encrypted DNS of their own (iCloud Private Relay, DNS over HTTPS) show fewer names.")})
	return p
}

func (t *tview) pageMobile() *Page {
	d := t.d
	p := &Page{Title: t.T("Mobile data")}
	if !d.simOK {
		p.Empty = t.T("This router has no mobile data counter.")
		return p
	}
	used, start, since := d.simPeriod()
	tv, tu := t.size(d.simToday())
	uv, uu := t.size(used)
	capV, capU := t.T("none"), ""
	if limit := t.capBytes(); limit > 0 {
		capV, capU = t.size(limit)
	}
	p.Items = append(p.Items, Item{Type: "stats", Stats: []Stat{{Label: t.T("Today"), Value: tv, Unit: tu, Color: "blue"},
		{Label: t.periodName(), Value: uv, Unit: uu}, {Label: t.T("Cap"), Value: capV, Unit: capU}}})
	if limit := t.capBytes(); limit > 0 {
		frac := float64(used) / float64(limit)
		left := uint64(0)
		if limit > used {
			left = limit - used
		}
		p.Items = append(p.Items, Item{Type: "bar", Label: t.F("{} used", t.pct(float64(used), float64(limit))),
			Value: t.F("{} left", t.sizeS(left)), Fraction: min(frac, 1), Color: usageColor(frac)})
	}
	// Daily use, the last 30 days.
	now := t.now.In(d.loc)
	byDay := map[string]uint64{}
	for _, day := range d.st.Days {
		if day.SimOK {
			byDay[day.Day] = day.Sim
		}
	}
	byDay[d.day] = d.simToday()
	var vals []float64
	peak := 0.0
	first := ""
	for i := 29; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		v, ok := byDay[day]
		if !ok && len(vals) == 0 {
			continue
		}
		if first == "" {
			first = day
		}
		vals = append(vals, float64(v))
		peak = max(peak, float64(v))
	}
	unit, div := "MB", float64(1<<20)
	if peak >= 1<<30 {
		unit, div = "GB", float64(1<<30)
	}
	for i := range vals {
		vals[i] = math.Round(vals[i]/div*100) / 100
	}
	xs := ""
	if f, err := time.ParseInLocation("2006-01-02", first, d.loc); err == nil {
		xs = f.Format("02.01")
	}
	p.Items = append(p.Items, Item{Type: "chart", Label: t.T("Per day"), Unit: t.T(unit), Span: 30, Style: "bars",
		XStart: xs, XEnd: t.T("today"), Series: []Series{{Color: "blue", Values: vals}}})
	end := cycleStart(start.AddDate(0, 1, 0).Add(12*time.Hour), d.cfg.cycleDay).AddDate(0, 0, -1)
	p.Items = append(p.Items, section(t.T("Period")),
		Item{Type: "info", Label: t.T("Dates"), Value: start.Format("02.01") + " – " + end.Format("02.01")},
		Item{Type: "info", Label: t.T("Starts again in"), Value: t.F("{} d.", itoa(int(math.Ceil(end.AddDate(0, 0, 1).Sub(now).Hours()/24))))})
	if since.After(start) {
		p.Items = append(p.Items, Item{Type: "info", Label: t.T("Counted since"), Value: since.Format("02.01")})
	}
	p.Items = append(p.Items, Item{Type: "info", Label: t.F("SIM {}, all time", itoa(d.simSlot)), Value: t.sizeS(d.simTotal)},
		Item{Type: "link", Label: t.T("Cap and period"), Icon: "gear", Open: "settings"},
		Item{Type: "text", Color: "dim", Label: t.T("Counted by the router's modem, the same counter as in GL.iNet's admin panel.")})
	return p
}

func (t *tview) pageSettings() *Page {
	d := t.d
	p := &Page{Title: t.T("Settings")}
	p.Items = append(p.Items, section(t.T("Monthly mobile data cap")))
	for _, gb := range []float64{0, 1, 2, 5, 10, 20, 30, 50, 100, 200} {
		label := t.T("No cap")
		if gb > 0 {
			label = strconv.FormatFloat(gb, 'f', -1, 64) + " " + t.T("GB")
		}
		p.Items = append(p.Items, Item{Type: "radio", Label: label, Checked: d.cfg.capGB == gb,
			Action: &Action{ID: "cap", Args: []string{strconv.FormatFloat(gb, 'f', -1, 64)}}})
	}
	p.Items = append(p.Items, section(t.T("The period starts on day")))
	for _, day := range []int{1, 5, 10, 15, 20, 25, 28} {
		p.Items = append(p.Items, Item{Type: "radio", Label: itoa(day), Checked: d.cfg.cycleDay == day,
			Action: &Action{ID: "cycle", Args: []string{itoa(day)}}})
	}
	return p
}

func (t *tview) page(name string) *Page {
	switch {
	case name == "main":
		return t.pageMain()
	case name == "devices":
		return t.pageDevices()
	case strings.HasPrefix(name, "dev:"):
		return t.pageDevice(strings.TrimPrefix(name, "dev:"))
	case name == "services":
		return t.pageServices()
	case strings.HasPrefix(name, "svc:"):
		return t.pageService(strings.TrimPrefix(name, "svc:"))
	case name == "hosts":
		return t.pageHosts()
	case name == "flows":
		return t.pageFlows()
	case name == "protocols":
		return t.pageProtocols()
	case name == "dns":
		return t.pageDNS()
	case name == "mobile":
		return t.pageMobile()
	case name == "settings":
		return t.pageSettings()
	}
	return nil
}

// view is the app's answer to `status`: the card, the overview and the
// page on the screen now.
func (d *trafficd) view(page, lang string) View {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.viewer(lang)
	v := View{Card: t.card(), Pages: map[string]*Page{"main": t.pageMain()}}
	if l := t.lockLine(); l != nil {
		v.Lock = []Line{*l}
	}
	if page != "" && page != "main" {
		if p := t.page(page); p != nil {
			v.Pages[page] = p
		}
	}
	return v
}
