package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"os"
	"testing"
	"time"
)

// TestTrafficDemo writes apps/traffic/demo.json (every page of the traffic
// app, from made-up traffic) when MUDI_SCREEN_WRITE_DEMO=1; previews show
// it instead of asking the collector.
func TestTrafficDemo(t *testing.T) {
	if os.Getenv("MUDI_SCREEN_WRITE_DEMO") != "1" {
		t.Skip("set MUDI_SCREEN_WRITE_DEMO=1 to write apps/traffic/demo.json")
	}
	d := testCollector(t)
	d.macOf[netip.MustParseAddr("192.168.8.140")] = "aa:bb:cc:00:00:02"
	d.glc["aa:bb:cc:00:00:02"] = glClient{Name: "laptop", IP: "192.168.8.140", Iface: "5G", Online: true, MAC: "aa:bb:cc:00:00:02"}
	d.macOf[netip.MustParseAddr("192.168.8.150")] = "aa:bb:cc:00:00:03"
	d.glc["aa:bb:cc:00:00:03"] = glClient{Name: "tablet", IP: "192.168.8.150", Iface: "2.4G", Online: true, MAC: "aa:bb:cc:00:00:03"}
	for name, ip := range map[string]string{"www.netflix.com": "45.57.1.1", "api.github.com": "140.82.1.1",
		"chatgpt.com": "104.18.32.47", "edge-chat.instagram.com": "157.240.1.60", "www.wikipedia.org": "185.15.59.224"} {
		d.names.addDNS(netip.MustParseAddr("192.168.8.140"), dnsAnswer{qname: name, addrs: []netip.Addr{netip.MustParseAddr(ip)}, ttl: 300})
	}
	type spec struct {
		id       uint32
		src, dst string
		proto    uint8
		dport    uint16
		down, up float64 // bytes per second
	}
	specs := []spec{
		{1, "192.168.8.137", "173.194.1.1", ipUDP, 443, 520e3, 9e3},
		{2, "192.168.8.137", "149.154.167.51", ipTCP, 443, 18e3, 4e3},
		{3, "192.168.8.140", "45.57.1.1", ipTCP, 443, 260e3, 6e3},
		{4, "192.168.8.140", "140.82.1.1", ipTCP, 443, 12e3, 3e3},
		{5, "192.168.8.140", "104.18.32.47", ipTCP, 443, 9e3, 2e3},
		{6, "192.168.8.150", "157.240.1.60", ipTCP, 443, 40e3, 5e3},
		{7, "192.168.8.150", "185.15.59.224", ipTCP, 443, 3e3, 1e3},
		{8, "10.0.0.2", "176.126.162.110", ipTCP, 8443, 6e3, 1e3},
		{9, "192.168.8.137", "17.57.144.20", ipTCP, 5223, 200, 100},
		{10, "10.0.0.2", "1.1.1.1", ipUDP, 53, 300, 200},
	}
	t0 := d.now()
	acc := map[uint32][2]float64{}
	for k := 0; k < 90; k++ {
		now := t0.Add(time.Duration(k) * 2 * time.Second)
		var flows []ctFlow
		for _, sp := range specs {
			w := 1 + 0.6*math.Sin(float64(k)/6+float64(sp.id))
			a := acc[sp.id]
			a[0] += sp.down * 2 * w
			a[1] += sp.up * 2 * w
			acc[sp.id] = a
			flows = append(flows, flow(sp.id, sp.src, sp.dst, sp.proto, uint16(40000+sp.id), sp.dport, uint64(a[1]), uint64(a[0])))
		}
		d.mu.Lock()
		first := d.last.IsZero()
		d.last = now
		d.tick++
		d.sampleFlows(flows, now, 2, first)
		var dn, up float64
		for _, sp := range specs {
			w := 1 + 0.6*math.Sin(float64(k)/6+float64(sp.id))
			dn += sp.down * 2 * w
			up += sp.up * 2 * w
		}
		d.wanLive[0].push(dn * 8 / 2)
		d.wanLive[1].push(up * 8 / 2)
		d.wanRate.update(uint64(dn), uint64(up), 2)
		d.mu.Unlock()
	}
	d.wanIf = []string{"rmnet_data0"}
	for m := 0; m < tdMinPts; m++ {
		day := 0.5 + 0.5*math.Sin(float64(m)/1440*2*math.Pi-1.4)
		d.wanMin[0].push(day * 60e6 * (0.7 + 0.3*math.Sin(float64(m)/17)))
		d.wanMin[1].push(day * 4e6)
	}
	for i := 1; i <= 20; i++ {
		day := t0.AddDate(0, 0, -i).Format("2006-01-02")
		d.st.Days = append([]tdDay{{Day: day, Sim: uint64((0.6 + 0.5*math.Sin(float64(i))) * (1 << 30)), SimOK: true}}, d.st.Days...)
	}
	d.simOK, d.simSlot, d.simTotal = true, 1, 221<<30
	d.st.SimDay = simMark{Day: d.day, Total: 221<<30 - 1300<<20, Slot: 1}
	d.cfg.capGB = 50
	for i, n := range []string{"www.youtube.com", "rr1.googlevideo.com", "api.telegram.org", "www.netflix.com", "graph.instagram.com",
		"doubleclick.net", "api.github.com", "chatgpt.com"} {
		for k := 0; k < 30-3*i; k++ {
			a := dnsAnswer{qname: n, addrs: []netip.Addr{netip.MustParseAddr("1.2.3.4")}}
			if n == "doubleclick.net" {
				a = dnsAnswer{qname: n, blocked: true}
			}
			d.dns.add(netip.MustParseAddr("192.168.8.140"), a, t0.Add(time.Duration(k)*time.Minute))
		}
	}
	d.dns.tick(t0.Add(61 * time.Minute))

	v := d.view("", "en")
	for _, page := range []string{"devices", "dev:aa:bb:cc:00:00:01", "services", "svc:YouTube", "hosts", "flows", "protocols",
		"dns", "mobile", "settings"} {
		pv := d.view(page, "en")
		v.Pages[page] = pv.Pages[page]
	}
	raw, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("apps/traffic/demo.json", append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	fmt.Println("demo pages:", len(v.Pages))
}
