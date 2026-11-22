package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ---- a classic BPF interpreter, enough for sniffFilter ----

func runBPF(t *testing.T, prog []syscall.SockFilter, pkt []byte) uint32 {
	t.Helper()
	var a, x uint32
	load := func(off uint32, size int) (uint32, bool) {
		if int(off)+size > len(pkt) {
			return 0, false
		}
		switch size {
		case 1:
			return uint32(pkt[off]), true
		case 2:
			return uint32(binary.BigEndian.Uint16(pkt[off:])), true
		}
		return binary.BigEndian.Uint32(pkt[off:]), true
	}
	for pc := 0; pc < len(prog); pc++ {
		in := prog[pc]
		ok := true
		switch in.Code {
		case bpfLdHAbs:
			a, ok = load(in.K, 2)
		case bpfLdBAbs:
			a, ok = load(in.K, 1)
		case bpfLdHInd:
			a, ok = load(x+in.K, 2)
		case bpfLdBInd:
			a, ok = load(x+in.K, 1)
		case bpfLdxMSH:
			var v uint32
			v, ok = load(in.K, 1)
			x = 4 * (v & 0xf)
		case bpfJeqK:
			if a == in.K {
				pc += int(in.Jt)
			} else {
				pc += int(in.Jf)
			}
		case bpfJsetK:
			if a&in.K != 0 {
				pc += int(in.Jt)
			} else {
				pc += int(in.Jf)
			}
		case bpfAndK:
			a &= in.K
		case bpfRshK:
			a >>= in.K
		case bpfAddX:
			a += x
		case bpfTax:
			x = a
		case bpfRetK:
			return in.K
		default:
			t.Fatalf("opcode %#x", in.Code)
		}
		if !ok {
			return 0 // a load past the end drops the packet, as in the kernel
		}
	}
	t.Fatal("program fell off the end")
	return 0
}

func ether(etype uint16, payload []byte) []byte {
	b := make([]byte, 14, 14+len(payload))
	binary.BigEndian.PutUint16(b[12:], etype)
	return append(b, payload...)
}

func ipv4(proto uint8, src, dst string, l4 []byte) []byte {
	h := make([]byte, 20)
	h[0] = 0x45
	binary.BigEndian.PutUint16(h[2:], uint16(20+len(l4)))
	h[8] = 64
	h[9] = proto
	s, d := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	copy(h[12:], s[:])
	copy(h[16:], d[:])
	return append(h, l4...)
}

func ipv6(proto uint8, src, dst string, l4 []byte) []byte {
	h := make([]byte, 40)
	h[0] = 0x60
	binary.BigEndian.PutUint16(h[4:], uint16(len(l4)))
	h[6] = proto
	h[7] = 64
	s, d := netip.MustParseAddr(src).As16(), netip.MustParseAddr(dst).As16()
	copy(h[8:], s[:])
	copy(h[24:], d[:])
	return append(h, l4...)
}

func udp(sport, dport uint16, payload []byte) []byte {
	h := make([]byte, 8)
	binary.BigEndian.PutUint16(h, sport)
	binary.BigEndian.PutUint16(h[2:], dport)
	binary.BigEndian.PutUint16(h[4:], uint16(8+len(payload)))
	return append(h, payload...)
}

func tcp(sport, dport uint16, payload []byte) []byte {
	h := make([]byte, 32) // 12 bytes of options
	binary.BigEndian.PutUint16(h, sport)
	binary.BigEndian.PutUint16(h[2:], dport)
	h[12] = 8 << 4
	return append(h, payload...)
}

// dnsAnswerMsg is a response for name with a CNAME and two A records, the
// names compressed.
func dnsAnswerMsg(name string, ips ...string) []byte {
	b := []byte{0x12, 0x34, 0x81, 0x80, 0, 1, 0, byte(1 + len(ips)), 0, 0, 0, 0}
	for _, l := range strings.Split(name, ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0, 0, 1, 0, 1)
	// CNAME: name → edge.example.net
	b = append(b, 0xc0, 12, 0, 5, 0, 1, 0, 0, 0, 60)
	target := []byte{4, 'e', 'd', 'g', 'e', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'n', 'e', 't', 0}
	b = append(b, 0, byte(len(target)))
	cname := len(b)
	b = append(b, target...)
	for _, s := range ips {
		a := netip.MustParseAddr(s).As4()
		b = append(b, 0xc0, byte(cname), 0, 1, 0, 1, 0, 0, 1, 44, 0, 4)
		b = append(b, a[:]...)
	}
	return b
}

func clientHello(sni string) []byte {
	var ext []byte
	if sni != "" {
		name := []byte(sni)
		list := append([]byte{0, byte(len(name) >> 8), byte(len(name))}, name...)
		sn := append([]byte{byte(len(list) >> 8), byte(len(list))}, list...)
		ext = append([]byte{0, 0, byte(len(sn) >> 8), byte(len(sn))}, sn...)
	}
	// A padding extension in front, as browsers send.
	ext = append([]byte{0, 21, 0, 4, 0, 0, 0, 0}, ext...)
	body := []byte{3, 3}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0)                // session id
	body = append(body, 0, 2, 0x13, 0x01) // cipher suites
	body = append(body, 1, 0)             // compression
	body = append(body, byte(len(ext)>>8), byte(len(ext)))
	body = append(body, ext...)
	hs := append([]byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	return append([]byte{0x16, 3, 1, byte(len(hs) >> 8), byte(len(hs))}, hs...)
}

func TestSniffFilter(t *testing.T) {
	prog := sniffFilter()
	dns := dnsAnswerMsg("www.youtube.com", "142.250.1.1")
	hello := clientHello("www.netflix.com")
	cases := []struct {
		name string
		pkt  []byte
		want bool
	}{
		{"dns answer v4", ether(0x0800, ipv4(ipUDP, "192.168.8.1", "192.168.8.137", udp(53, 5353, dns))), true},
		{"dns query v4", ether(0x0800, ipv4(ipUDP, "192.168.8.137", "192.168.8.1", udp(5353, 53, dns))), false},
		{"client hello v4", ether(0x0800, ipv4(ipTCP, "192.168.8.137", "1.2.3.4", tcp(40000, 443, hello))), true},
		{"tls data v4", ether(0x0800, ipv4(ipTCP, "192.168.8.137", "1.2.3.4", tcp(40000, 443, []byte{0x17, 3, 3, 0, 5, 1, 2, 3, 4, 5}))), false},
		{"server hello v4", ether(0x0800, ipv4(ipTCP, "1.2.3.4", "192.168.8.137", tcp(443, 40000, hello))), false},
		{"empty ack v4", ether(0x0800, ipv4(ipTCP, "192.168.8.137", "1.2.3.4", tcp(40000, 443, nil))), false},
		{"quic v4", ether(0x0800, ipv4(ipUDP, "192.168.8.137", "1.2.3.4", udp(40000, 443, []byte{0xc0, 0, 0, 1}))), false},
		{"dns answer v6", ether(0x86dd, ipv6(ipUDP, "fd00::1", "fd00::2", udp(53, 4000, dns))), true},
		{"client hello v6", ether(0x86dd, ipv6(ipTCP, "fd00::2", "2a00::1", tcp(40000, 443, hello))), true},
		{"arp", ether(0x0806, make([]byte, 28)), false},
	}
	for _, c := range cases {
		got := runBPF(t, prog, c.pkt) > 0
		if got != c.want {
			t.Errorf("%s: accepted %v, want %v", c.name, got, c.want)
		}
		if c.want {
			p, ok := parseFrame(c.pkt)
			if !ok {
				t.Errorf("%s: parseFrame failed", c.name)
				continue
			}
			if p.dns {
				a, ok := parseDNSResponse(p.payload)
				if !ok || a.qname != "www.youtube.com" || len(a.addrs) != 1 || a.addrs[0] != netip.MustParseAddr("142.250.1.1") || a.ttl != 300 {
					t.Errorf("%s: dns %+v %v", c.name, a, ok)
				}
			} else if n, ok := clientHelloSNI(p.payload); !ok || n != "www.netflix.com" {
				t.Errorf("%s: sni %q %v", c.name, n, ok)
			}
		}
	}
}

func TestDNSBlockedAndNotFound(t *testing.T) {
	a, ok := parseDNSResponse(dnsAnswerMsg("ads.example.com", "0.0.0.0"))
	if !ok || !a.blocked || len(a.addrs) != 0 {
		t.Fatalf("blocked: %+v", a)
	}
	nx := []byte{0, 1, 0x81, 0x83, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'f', 'o', 'o', 3, 'b', 'a', 'r', 0, 0, 1, 0, 1}
	a, ok = parseDNSResponse(nx)
	if !ok || a.rcode != 3 || a.qname != "foo.bar" {
		t.Fatalf("nxdomain: %+v %v", a, ok)
	}
	// A pointer loop must not hang.
	loop := []byte{0, 1, 0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 12}
	if _, ok := parseDNSResponse(loop); ok {
		t.Fatal("pointer loop accepted")
	}
	if _, ok := clientHelloSNI(clientHello("")); ok {
		t.Fatal("name without SNI")
	}
	if _, ok := clientHelloSNI(clientHello("x.example.com")[:40]); ok {
		t.Fatal("name from a cut hello")
	}
}

func TestNameCache(t *testing.T) {
	c := newNameCache()
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	phone, laptop := netip.MustParseAddr("192.168.8.10"), netip.MustParseAddr("192.168.8.11")
	cdn := netip.MustParseAddr("104.16.1.1")
	c.addDNS(phone, dnsAnswer{qname: "a.example.com", addrs: []netip.Addr{cdn}, ttl: 60})
	c.addDNS(laptop, dnsAnswer{qname: "b.example.org", addrs: []netip.Addr{cdn}, ttl: 60})
	if n := c.lookup(phone, cdn, 1); n != "a.example.com" {
		t.Fatalf("phone: %q", n)
	}
	if n := c.lookup(laptop, cdn, 1); n != "b.example.org" {
		t.Fatalf("laptop: %q", n)
	}
	c.addSNI(phone, cdn, 5555, "c.example.net")
	if n := c.lookup(phone, cdn, 5555); n != "c.example.net" {
		t.Fatalf("sni: %q", n)
	}
	now = now.Add(4 * time.Hour)
	if n := c.lookup(phone, cdn, 1); n != "" {
		t.Fatalf("expired: %q", n)
	}
	c.sweep()
	if len(c.byPair)+len(c.byIP)+len(c.bySNI) != 0 {
		t.Fatal("sweep kept expired names")
	}
}

// ---- netlink ----

func nlAttr(typ uint16, val []byte) []byte {
	b := make([]byte, 4, 4+len(val)+3)
	binary.NativeEndian.PutUint16(b, uint16(4+len(val)))
	binary.NativeEndian.PutUint16(b[2:], typ)
	b = append(b, val...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func nlNest(typ uint16, parts ...[]byte) []byte {
	var v []byte
	for _, p := range parts {
		v = append(v, p...)
	}
	return nlAttr(typ|0x8000, v)
}

func be16(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func be64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

func ctTupleAttr(typ uint16, src, dst string, proto uint8, sp, dp uint16) []byte {
	s, d := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	return nlNest(typ,
		nlNest(ctaTupleIP, nlAttr(ctaIPv4Src, s[:]), nlAttr(ctaIPv4Dst, d[:])),
		nlNest(ctaTupleProto, nlAttr(ctaProtoNum, []byte{proto}), nlAttr(ctaProtoSrcPort, be16(sp)), nlAttr(ctaProtoDstPort, be16(dp))))
}

func ctMsg(id uint32, src, dst, wan string, proto uint8, sp, dp uint16, ob, rb uint64) []byte {
	body := []byte{2, 0, 0, 0} // nfgenmsg
	body = append(body, ctTupleAttr(ctaTupleOrig, src, dst, proto, sp, dp)...)
	body = append(body, ctTupleAttr(ctaTupleReply, dst, wan, proto, dp, sp)...)
	body = append(body, nlAttr(ctaStatus, be32(0x0e))...)
	body = append(body, nlNest(ctaCountersOrig, nlAttr(ctaCountersPackets, be64(10)), nlAttr(ctaCountersBytes, be64(ob)))...)
	body = append(body, nlNest(ctaCountersReply, nlAttr(ctaCountersPackets, be64(20)), nlAttr(ctaCountersBytes, be64(rb)))...)
	body = append(body, nlAttr(ctaID, be32(id))...)
	h := make([]byte, 16)
	binary.NativeEndian.PutUint32(h, uint32(16+len(body)))
	binary.NativeEndian.PutUint16(h[4:], nfnlSubsysCtnetlink<<8|ctMsgNew)
	binary.NativeEndian.PutUint16(h[6:], 2) // NLM_F_MULTI
	return append(h, body...)
}

func nlDone() []byte {
	h := make([]byte, 20)
	binary.NativeEndian.PutUint32(h, 20)
	binary.NativeEndian.PutUint16(h[4:], nlmsgDone)
	return h
}

func TestParseConntrackNetlink(t *testing.T) {
	batch := append(ctMsg(7, "192.168.8.137", "142.250.1.1", "10.0.0.2", ipTCP, 40000, 443, 1000, 50000),
		ctMsg(8, "192.168.8.137", "8.8.8.8", "10.0.0.2", ipUDP, 5000, 53, 60, 90)...)
	var flows []ctFlow
	done, err := parseNlBatch(batch, &flows)
	if err != nil || done || len(flows) != 2 {
		t.Fatalf("batch: %v %v %d", done, err, len(flows))
	}
	f := flows[0]
	if f.id != 7 || f.orig.src != netip.MustParseAddr("192.168.8.137") || f.orig.dport != 443 || f.orig.proto != ipTCP ||
		f.reply.dst != netip.MustParseAddr("10.0.0.2") || f.origBytes != 1000 || f.replyBytes != 50000 || f.replyPkts != 20 {
		t.Fatalf("flow %+v", f)
	}
	done, err = parseNlBatch(nlDone(), &flows)
	if !done || err != nil {
		t.Fatalf("done: %v %v", done, err)
	}
}

// ---- ubus client against a fake ubusd ----

func TestUbusInvoke(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ubus.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		u := &ubusConn{c: c}
		_ = u.write(ubusMsgHello, 0, 0x99, nil)
		for {
			typ, seq, peer, body, err := u.read()
			if err != nil {
				return
			}
			attrs, _ := parseBlobs(body)
			switch typ {
			case ubusMsgLookup:
				if attrString(attrs, ubusAttrObjPath) != "gl-clients" {
					_ = u.write(ubusMsgStatus, seq, 0, appendBlobU32(nil, ubusAttrStatus, 4))
					continue
				}
				d := appendBlobString(nil, ubusAttrObjPath, "gl-clients")
				d = appendBlobU32(d, ubusAttrObjID, 0x1234)
				_ = u.write(ubusMsgData, seq, 0, d)
				_ = u.write(ubusMsgStatus, seq, 0, appendBlobU32(nil, ubusAttrStatus, 0))
			case ubusMsgInvoke:
				var args map[string]any
				for _, a := range attrs {
					if a.id == ubusAttrData {
						args, _ = decodeTable(a.data)
					}
				}
				id, _ := attrU32(attrs, ubusAttrObjID)
				if id != 0x1234 || peer != 0x1234 || attrString(attrs, ubusAttrMethod) != "status" {
					_ = u.write(ubusMsgStatus, seq, 0, appendBlobU32(nil, ubusAttrStatus, 3))
					continue
				}
				reply := map[string]any{"cable_total": int64(1), "wireless_total": int64(2), "echo": args["bus"],
					"list": []any{"a", int64(5)}}
				d := appendBlobU32(nil, ubusAttrObjID, id)
				d = appendBlob(d, ubusAttrData, false, encodeTable(reply))
				_ = u.write(ubusMsgData, seq, 0, d)
				_ = u.write(ubusMsgStatus, seq, 0, appendBlobU32(nil, ubusAttrStatus, 0))
			}
		}
	}()
	out, err := ubusInvoke(sock, "gl-clients", "status", map[string]any{"bus": "cpu"}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out["cable_total"] != int64(1) || out["wireless_total"] != int64(2) || out["echo"] != "cpu" {
		t.Fatalf("answer %#v", out)
	}
	if _, err := ubusInvoke(sock, "nothing", "status", nil, time.Second); err == nil {
		t.Fatal("lookup of a missing object succeeded")
	}
}

// ---- the collector ----

func testCollector(t *testing.T) *trafficd {
	t.Helper()
	d := newTrafficd(log.New(io.Discard, "", 0), time.UTC)
	clock := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return clock }
	d.day = d.dayOf(clock)
	d.addrs.Store(&addrSet{lan: []netip.Prefix{netip.MustParsePrefix("192.168.8.0/24")},
		local: map[netip.Addr]bool{netip.MustParseAddr("192.168.8.1"): true, netip.MustParseAddr("10.0.0.2"): true,
			netip.MustParseAddr("127.0.0.1"): true}})
	d.macOf[netip.MustParseAddr("192.168.8.137")] = "aa:bb:cc:00:00:01"
	d.glc["aa:bb:cc:00:00:01"] = glClient{Name: "phone", IP: "192.168.8.137", Iface: "5G", Online: true, MAC: "aa:bb:cc:00:00:01"}
	d.names.addDNS(netip.MustParseAddr("192.168.8.137"), dnsAnswer{qname: "rr1.googlevideo.com",
		addrs: []netip.Addr{netip.MustParseAddr("173.194.1.1")}, ttl: 300})
	return d
}

func flow(id uint32, src, dst string, proto uint8, sp, dp uint16, ob, rb uint64) ctFlow {
	s, d := netip.MustParseAddr(src), netip.MustParseAddr(dst)
	return ctFlow{id: id, orig: ctTuple{s, d, sp, dp, proto}, reply: ctTuple{d, netip.MustParseAddr("10.0.0.2"), dp, sp, proto},
		origBytes: ob, replyBytes: rb}
}

func TestCollectorFlows(t *testing.T) {
	d := testCollector(t)
	t0 := d.now()
	sample := func(dt time.Duration, flows ...ctFlow) {
		d.mu.Lock()
		defer d.mu.Unlock()
		first := d.last.IsZero()
		now := t0.Add(dt)
		secs := 2.0
		d.last = now
		d.tick++
		d.sampleFlows(flows, now, secs, first)
	}
	// The first sample is a baseline: what flowed before is not counted.
	sample(0,
		flow(1, "192.168.8.137", "173.194.1.1", ipTCP, 40000, 443, 1000, 100000),
		flow(2, "192.168.8.137", "192.168.8.1", ipUDP, 5000, 53, 60, 90), // to the router: not counted
		flow(3, "10.0.0.2", "1.1.1.1", ipUDP, 4000, 53, 70, 120))
	if d.devs["aa:bb:cc:00:00:01"].today != [2]uint64{} {
		t.Fatalf("baseline counted: %+v", d.devs["aa:bb:cc:00:00:01"].today)
	}
	sample(2*time.Second,
		flow(1, "192.168.8.137", "173.194.1.1", ipTCP, 40000, 443, 3000, 2100000),
		flow(2, "192.168.8.137", "192.168.8.1", ipUDP, 5000, 53, 120, 180),
		flow(3, "10.0.0.2", "1.1.1.1", ipUDP, 4000, 53, 140, 240),
		flow(4, "192.168.8.137", "149.154.167.51", ipTCP, 40001, 443, 500, 7000))
	phone := d.devs["aa:bb:cc:00:00:01"]
	if phone == nil || phone.today != [2]uint64{2000000 + 7000, 2000 + 500} || phone.name != "phone" || phone.flows != 2 {
		t.Fatalf("phone %+v", phone)
	}
	if r := d.devs["router"]; r == nil || r.today != [2]uint64{120, 70} {
		t.Fatalf("router %+v", r)
	}
	yt := d.svcs["YouTube"]
	if yt == nil || yt.today[0] != 2000000 || yt.cat != catVideo || yt.devs["aa:bb:cc:00:00:01"] != 2002000 {
		t.Fatalf("youtube %+v", yt)
	}
	if tg := d.svcs["Telegram"]; tg == nil || tg.today[0] != 7000 {
		t.Fatalf("telegram by address %+v", tg)
	}
	if h := d.hosts["rr1.googlevideo.com"]; h == nil || h.svc != "YouTube" {
		t.Fatalf("host %+v", h)
	}
	if p := d.protos["HTTPS"]; p == nil || p.flows != 2 {
		t.Fatalf("https %+v", p)
	}
	if d.nTCP != 2 || d.nUDP != 1 {
		t.Fatalf("counts %d %d", d.nTCP, d.nUDP)
	}
	if phone.rate.down <= 0 {
		t.Fatal("no rate")
	}
	// A flow that ends is forgotten; one that starts over is counted anew.
	sample(4*time.Second, flow(9, "192.168.8.137", "173.194.1.1", ipUDP, 40002, 443, 100, 200))
	if len(d.flows) != 1 || phone.today[0] != 2007200 {
		t.Fatalf("flows %d, phone %+v", len(d.flows), phone.today)
	}
	if p := d.protos["QUIC"]; p == nil || p.today[0] != 200 {
		t.Fatalf("quic %+v", p)
	}

	// Every page builds and encodes, in both languages.
	d.simOK, d.simSlot, d.simTotal = true, 1, 5<<30
	d.st.SimDay = simMark{Day: d.day, Total: 4 << 30, Slot: 1}
	d.cfg.capGB = 1.2
	for _, lang := range []string{"en", "ru"} {
		for _, page := range []string{"", "main", "devices", "dev:aa:bb:cc:00:00:01", "dev:router", "services", "svc:YouTube",
			"hosts", "flows", "protocols", "dns", "mobile", "settings", "dev:nope", "svc:nope"} {
			v := d.view(page, lang)
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("%s %s: %v", lang, page, err)
			}
			back, err := parseView(raw)
			if err != nil || back.Card == nil {
				t.Fatalf("%s %s: %v", lang, page, err)
			}
			if page != "" && back.Pages[page] == nil && page != "main" {
				t.Fatalf("%s: no page %s", lang, page)
			}
		}
	}
	v := d.view("", "en")
	if !strings.Contains(v.Card.Subtitle, "1.0 GB of 1.2 GB used") || v.Card.SubtitleColor != "orange" {
		t.Fatalf("card near the cap: %q %q", v.Card.Subtitle, v.Card.SubtitleColor)
	}
}

func TestCollectorDayAndSIM(t *testing.T) {
	d := testCollector(t)
	now := d.now()
	d.applySIM(1, 1000, now)
	d.applySIM(1, 1500, now.Add(time.Minute))
	if d.simToday() != 500 {
		t.Fatalf("today %d", d.simToday())
	}
	d.wanToday = [2]uint64{900, 100}
	// Midnight: the day goes to the history with its mobile data.
	d.mu.Lock()
	d.rollDay(now.Add(13 * time.Hour))
	d.mu.Unlock()
	if len(d.st.Days) != 1 || d.st.Days[0].Sim != 500 || !d.st.Days[0].SimOK || d.st.Days[0].WanDown != 900 {
		t.Fatalf("days %+v", d.st.Days)
	}
	if d.wanToday != [2]uint64{} || d.simToday() != 0 {
		t.Fatal("today not reset")
	}
	d.now = func() time.Time { return now.Add(14 * time.Hour) }
	d.applySIM(1, 1800, d.now())
	used, start, since := d.simPeriod()
	if used != 800 || start.Format("2006-01-02") != "2026-11-01" || since.Format("2006-01-02") != "2026-11-21" {
		t.Fatalf("period %d %v %v", used, start, since)
	}
	if c := cycleStart(time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), 15); c.Format("2006-01-02") != "2026-02-15" {
		t.Fatalf("cycle %v", c)
	}
}

func TestTemplates(t *testing.T) {
	if got := fill("{} of {}", "1 GB", "2 GB"); got != "1 GB of 2 GB" {
		t.Fatal(got)
	}
	if got := fill("{2} из {1}", "a", "b"); got != "b из a" {
		t.Fatal(got)
	}
	c := loadCatalog([]string{writeTemp(t, "Traffic\tТрафик\n{} of {}\t{} из {}\n")})
	if c.tmpl("{} of {}") != "{} из {}" || c.tmpl("Traffic") != "Трафик" || c.tmpl("x") != "x" {
		t.Fatal("tmpl")
	}
}

func writeTemp(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "c.tsv")
	writeFile(t, p, body)
	return p
}

func TestClassify(t *testing.T) {
	for name, want := range map[string]string{
		"r3---sn-4g5e6nsz.googlevideo.com": "YouTube", "music.youtube.com": "YouTube Music", "www.google.rs": "Google",
		"api.telegram.org": "Telegram", "bbc.co.uk": "bbc.co.uk", "cdn.habr.com": "Habr", "foo.bar.example.org": "example.org",
		"mask.icloud.com": "iCloud Private Relay", "p48-caldav.icloud.com": "iCloud",
	} {
		if got := classifyName(name).name; got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	if s := flowService("", netip.MustParseAddr("17.253.1.1"), ipTCP, 443); s.name != "Apple" {
		t.Fatalf("apple range: %+v", s)
	}
	if s := flowService("dns.google", netip.MustParseAddr("8.8.8.8"), ipUDP, 53); s.name != "DNS" {
		t.Fatalf("port first: %+v", s)
	}
	if protoName(ipUDP, 443) != "QUIC" || protoName(ipTCP, 12345) != "Other TCP" || protoName(ipESP, 0) != "IPsec" {
		t.Fatal("protoName")
	}
}
