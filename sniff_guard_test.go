package main

// What a LAN client could do to the traffic analyzer, and must not: crash
// it with a packet, name the flows of other devices, or grow its memory.

import (
	"encoding/binary"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// Fuzz targets for the parsers of bytes from the network:
//
//	go test -run XXX -fuzz FuzzClientHelloSNI -fuzztime 30s .
func FuzzClientHelloSNI(f *testing.F) {
	f.Add(clientHello("www.example.com"))
	f.Fuzz(func(t *testing.T, b []byte) { clientHelloSNI(b) })
}

func FuzzDNSResponse(f *testing.F) {
	f.Add(guardDNS("www.youtube.com", 300, "142.250.1.1", "142.250.1.2"))
	f.Fuzz(func(t *testing.T, b []byte) {
		parseDNSResponse(b)
		if p, ok := parseFrame(b); ok && p.dns {
			parseDNSResponse(p.payload)
		}
	})
}

// guardDNS is a DNS response for name with A records of the given TTL.
func guardDNS(name string, ttl uint32, ips ...string) []byte {
	b := []byte{0x12, 0x34, 0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(b[6:], uint16(len(ips)))
	for _, l := range strings.Split(name, ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0, 0, 1, 0, 1)
	for _, s := range ips {
		a := netip.MustParseAddr(s).As4()
		b = append(b, 0xc0, 12, 0, 1, 0, 1)
		b = binary.BigEndian.AppendUint32(b, ttl)
		b = append(b, 0, 4)
		b = append(b, a[:]...)
	}
	return b
}

func fromMAC(frame []byte, mac string) []byte {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		panic(err)
	}
	copy(frame[6:12], hw)
	return frame
}

func guardSniffer(d *trafficd) *sniffer {
	return &sniffer{names: d.names, dns: d.dns, log: log.New(io.Discard, "", 0), open: map[string]int{},
		isLAN: func(ip netip.Addr) bool { a := d.addrs.Load(); return a.isLAN(ip) && !a.isLocal(ip) },
		macOf: func(ip netip.Addr) (string, bool) { mac, ok := d.macOf[ip]; return mac, ok }}
}

// A TLS record shorter than a handshake header: 9 bytes from any guest,
// which the kernel filter lets through.
func TestShortClientHello(t *testing.T) {
	payload := []byte{0x16, 0x03, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	frame := ether(0x0800, ipv4(ipTCP, "192.168.8.66", "1.2.3.4", tcp(40000, 443, payload)))
	if runBPF(t, sniffFilter(), frame) == 0 {
		t.Fatal("the filter drops the packet; the test means nothing")
	}
	if _, ok := clientHelloSNI(payload); ok {
		t.Fatal("a name from a record of length 0")
	}
	d := testCollector(t)
	guardSniffer(d).handle(frame, false, false) // must not panic
}

func TestSnifferTrustsOnlyTheRouter(t *testing.T) {
	d := testCollector(t) // the phone 192.168.8.137 (aa:bb:cc:00:00:01), the router 192.168.8.1
	clock := d.now()
	d.names.now = func() time.Time { return clock }
	sn := guardSniffer(d)
	phone, cdn := netip.MustParseAddr("192.168.8.137"), netip.MustParseAddr("142.250.1.1")
	answer := func(src, name string, ttl uint32) []byte {
		return ether(0x0800, ipv4(ipUDP, src, "192.168.8.137", udp(53, 50000, guardDNS(name, ttl, "142.250.1.1"))))
	}

	// dnsmasq answers the phone: the router sends that out on the bridge.
	sn.handle(answer("192.168.8.1", "www.youtube.com", 300), false, true)
	if n := d.names.lookup(phone, cdn, 40001); n != "www.youtube.com" {
		t.Fatalf("the router's answer: %q", n)
	}
	// A guest sends a forged answer to the phone: as it reaches the router,
	// and as the router passes it on.
	sn.handle(answer("192.168.8.66", "casino.example", 300), false, false)
	sn.handle(answer("192.168.8.66", "casino.example", 300), false, true)
	sn.handle(answer("8.8.8.8", "casino.example", 300), false, false)
	if n := d.names.lookup(phone, cdn, 40002); n != "www.youtube.com" {
		t.Fatalf("a guest renamed the phone's flow: %q", n)
	}
	// An answer from the internet the router passes on counts.
	sn.handle(answer("8.8.8.8", "dns.google.example", 300), false, true)
	if n := d.names.lookup(phone, cdn, 40003); n != "dns.google.example" {
		t.Fatalf("a passed-on answer: %q", n)
	}

	// A ClientHello with the phone's address from another device's MAC.
	hello := func(name string) []byte {
		return ether(0x0800, ipv4(ipTCP, "192.168.8.137", "104.16.1.1", tcp(40010, 443, clientHello(name))))
	}
	sn.handle(fromMAC(hello("forged.example"), "aa:bb:cc:00:00:66"), false, false)
	if n := d.names.lookup(phone, netip.MustParseAddr("104.16.1.1"), 40010); n != "" {
		t.Fatalf("a forged ClientHello named the phone's flow: %q", n)
	}
	sn.handle(fromMAC(hello("www.netflix.com"), "aa:bb:cc:00:00:01"), false, false)
	if n := d.names.lookup(phone, netip.MustParseAddr("104.16.1.1"), 40010); n != "www.netflix.com" {
		t.Fatalf("the phone's own ClientHello: %q", n)
	}

	// A TTL of 68 years is taken as a day.
	sn.handle(answer("192.168.8.1", "long.example", 0x7fffffff), false, true)
	clock = clock.Add(25 * time.Hour)
	if n := d.names.lookup(phone, cdn, 40004); n != "" {
		t.Fatalf("a name outlived a day: %q", n)
	}
}

// Names stay within their bound between sweeps, however fast they come.
func TestNameCacheBound(t *testing.T) {
	c := newNameCache()
	client := netip.MustParseAddr("192.168.8.137")
	for i := 0; i < 3000; i++ {
		a := dnsAnswer{qname: "x.example", ttl: 0x7fffffff}
		for j := 0; j < 90; j++ {
			a.addrs = append(a.addrs, netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), byte(j)}))
		}
		c.addDNS(client, a)
	}
	if len(c.byPair) > maxNames || len(c.byIP) > maxNames {
		t.Fatalf("byPair %d, byIP %d past %d", len(c.byPair), len(c.byIP), maxNames)
	}
	if v, ok := c.byIP[netip.AddrFrom4([4]byte{10, 0, 0, 20})]; ok {
		t.Fatalf("an address past the first %d of an answer was taken: %+v", maxAnswerIP, v)
	}
}
