package main

// Names for addresses, learned passively on the LAN side: the DNS answers
// the router sends to its clients (and to its own processes, on lo), and
// the server name in TLS ClientHellos of the clients. A classic BPF filter
// in the kernel lets only those packets through, so the rest of the
// traffic costs nothing here.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Classic BPF opcodes used by the filter.
const (
	bpfLdHAbs  = 0x28
	bpfLdBAbs  = 0x30
	bpfLdHInd  = 0x48
	bpfLdBInd  = 0x50
	bpfLdxMSH  = 0xb1
	bpfJeqK    = 0x15
	bpfJsetK   = 0x45
	bpfAndK    = 0x54
	bpfRshK    = 0x74
	bpfAddX    = 0x0c
	bpfTax     = 0x07
	bpfRetK    = 0x06
	sniffSnap  = 2048
	packetHost = 0
	packetOut  = 4
)

type bpfIns struct {
	label  string
	op     uint16
	k      uint32
	jt, jf string // labels; "" is the next instruction
}

func assembleBPF(prog []bpfIns) ([]syscall.SockFilter, error) {
	at := map[string]int{}
	for i, in := range prog {
		if in.label != "" {
			at[in.label] = i
		}
	}
	out := make([]syscall.SockFilter, len(prog))
	for i, in := range prog {
		f := syscall.SockFilter{Code: in.op, K: in.k}
		for _, j := range []struct {
			label string
			dst   *uint8
		}{{in.jt, &f.Jt}, {in.jf, &f.Jf}} {
			if j.label == "" {
				continue
			}
			t, ok := at[j.label]
			if !ok || t <= i || t-i-1 > 255 {
				return nil, fmt.Errorf("bpf: bad jump to %q at %d", j.label, i)
			}
			*j.dst = uint8(t - i - 1)
		}
		out[i] = f
	}
	return out, nil
}

// sniffFilter accepts, on an Ethernet-framed interface, UDP packets from
// port 53 (DNS answers) and TCP packets to port 443 whose payload starts a
// TLS ClientHello, over IPv4 and IPv6 (no extension headers).
func sniffFilter() []syscall.SockFilter {
	p, err := assembleBPF([]bpfIns{
		{op: bpfLdHAbs, k: 12},
		{op: bpfJeqK, k: 0x0800, jf: "v6"},
		{op: bpfLdBAbs, k: 23},
		{op: bpfJeqK, k: ipUDP, jf: "tcp4"},
		{op: bpfLdHAbs, k: 20},
		{op: bpfJsetK, k: 0x1fff, jt: "drop"},
		{op: bpfLdxMSH, k: 14},
		{op: bpfLdHInd, k: 14}, // UDP source port
		{op: bpfJeqK, k: 53, jt: "accept", jf: "drop"},
		{label: "tcp4", op: bpfJeqK, k: ipTCP, jf: "drop"},
		{op: bpfLdHAbs, k: 20},
		{op: bpfJsetK, k: 0x1fff, jt: "drop"},
		{op: bpfLdxMSH, k: 14},
		{op: bpfLdHInd, k: 16}, // TCP destination port
		{op: bpfJeqK, k: 443, jf: "drop"},
		{op: bpfLdBInd, k: 26}, // TCP data offset
		{op: bpfAndK, k: 0xf0},
		{op: bpfRshK, k: 2},
		{op: bpfAddX},
		{op: bpfTax},
		{op: bpfLdBInd, k: 14}, // TLS record type
		{op: bpfJeqK, k: 0x16, jf: "drop"},
		{op: bpfLdBInd, k: 19}, // handshake type
		{op: bpfJeqK, k: 1, jt: "accept", jf: "drop"},
		{label: "v6", op: bpfJeqK, k: 0x86dd, jf: "drop"},
		{op: bpfLdBAbs, k: 20},
		{op: bpfJeqK, k: ipUDP, jf: "tcp6"},
		{op: bpfLdHAbs, k: 54},
		{op: bpfJeqK, k: 53, jt: "accept", jf: "drop"},
		{label: "tcp6", op: bpfJeqK, k: ipTCP, jf: "drop"},
		{op: bpfLdHAbs, k: 56},
		{op: bpfJeqK, k: 443, jf: "drop"},
		{op: bpfLdBAbs, k: 66},
		{op: bpfAndK, k: 0xf0},
		{op: bpfRshK, k: 2},
		{op: bpfTax},
		{op: bpfLdBInd, k: 54},
		{op: bpfJeqK, k: 0x16, jf: "drop"},
		{op: bpfLdBInd, k: 59},
		{op: bpfJeqK, k: 1, jt: "accept", jf: "drop"},
		{label: "accept", op: bpfRetK, k: sniffSnap},
		{label: "drop", op: bpfRetK, k: 0},
	})
	if err != nil {
		panic(err) // a fixed program: a test catches this
	}
	return p
}

// ---- packets ----

type sniffed struct {
	dns     bool // a DNS answer; otherwise a ClientHello
	src     netip.Addr
	dst     netip.Addr
	sport   uint16
	dport   uint16
	payload []byte
}

// parseFrame takes an Ethernet frame the filter let through.
func parseFrame(b []byte) (sniffed, bool) {
	var s sniffed
	if len(b) < 14 {
		return s, false
	}
	et := binary.BigEndian.Uint16(b[12:])
	b = b[14:]
	var proto uint8
	switch et {
	case 0x0800:
		if len(b) < 20 || b[0]>>4 != 4 {
			return s, false
		}
		ihl := int(b[0]&0x0f) * 4
		total := int(binary.BigEndian.Uint16(b[2:]))
		if ihl < 20 || total < ihl || len(b) < ihl {
			return s, false
		}
		if total < len(b) {
			b = b[:total]
		}
		proto = b[9]
		s.src = netip.AddrFrom4([4]byte(b[12:16]))
		s.dst = netip.AddrFrom4([4]byte(b[16:20]))
		b = b[ihl:]
	case 0x86dd:
		if len(b) < 40 {
			return s, false
		}
		plen := int(binary.BigEndian.Uint16(b[4:]))
		proto = b[6]
		s.src = netip.AddrFrom16([16]byte(b[8:24]))
		s.dst = netip.AddrFrom16([16]byte(b[24:40]))
		b = b[40:]
		if plen < len(b) {
			b = b[:plen]
		}
	default:
		return s, false
	}
	switch proto {
	case ipUDP:
		if len(b) < 8 {
			return s, false
		}
		s.sport, s.dport = binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
		s.dns = s.sport == 53
		s.payload = b[8:]
		return s, s.dns
	case ipTCP:
		if len(b) < 20 {
			return s, false
		}
		s.sport, s.dport = binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
		off := int(b[12]>>4) * 4
		if off < 20 || off > len(b) {
			return s, false
		}
		s.payload = b[off:]
		return s, s.dport == 443
	}
	return s, false
}

// ---- DNS ----

type dnsAnswer struct {
	qname   string
	qtype   uint16
	rcode   int
	addrs   []netip.Addr
	ttl     uint32 // smallest TTL of the addresses
	blocked bool   // an address answer of 0.0.0.0 or :: (a blocking resolver)
}

func dnsName(msg []byte, off int) (string, int, error) {
	var parts []string
	end := -1
	n := 0
	for jumps := 0; ; {
		if off >= len(msg) {
			return "", 0, errors.New("dns: name out of range")
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(parts, "."), end, nil
		case l&0xc0 == 0xc0:
			if off+1 >= len(msg) {
				return "", 0, errors.New("dns: short pointer")
			}
			if end < 0 {
				end = off + 2
			}
			if jumps++; jumps > 16 {
				return "", 0, errors.New("dns: pointer loop")
			}
			off = int(binary.BigEndian.Uint16(msg[off:]) & 0x3fff)
		case l&0xc0 != 0:
			return "", 0, errors.New("dns: bad label")
		default:
			if off+1+l > len(msg) {
				return "", 0, errors.New("dns: label out of range")
			}
			if n += l + 1; n > 255 {
				return "", 0, errors.New("dns: name too long")
			}
			parts = append(parts, string(msg[off+1:off+1+l]))
			off += 1 + l
		}
	}
}

// parseDNSResponse reads the question and the address answers of a DNS
// response.
func parseDNSResponse(msg []byte) (dnsAnswer, bool) {
	var a dnsAnswer
	if len(msg) < 12 {
		return a, false
	}
	flags := binary.BigEndian.Uint16(msg[2:])
	if flags&0x8000 == 0 {
		return a, false
	}
	a.rcode = int(flags & 0x0f)
	qd, an := int(binary.BigEndian.Uint16(msg[4:])), int(binary.BigEndian.Uint16(msg[6:]))
	if qd < 1 {
		return a, false
	}
	off := 12
	for i := 0; i < qd; i++ {
		name, next, err := dnsName(msg, off)
		if err != nil || next+4 > len(msg) {
			return a, false
		}
		if i == 0 {
			a.qname = normalizeHost(name)
			a.qtype = binary.BigEndian.Uint16(msg[next:])
		}
		off = next + 4
	}
	if a.qname == "" {
		return a, false
	}
	a.ttl = 1 << 31
	for i := 0; i < an && off < len(msg); i++ {
		_, next, err := dnsName(msg, off)
		if err != nil || next+10 > len(msg) {
			break
		}
		typ := binary.BigEndian.Uint16(msg[next:])
		ttl := binary.BigEndian.Uint32(msg[next+4:])
		rdl := int(binary.BigEndian.Uint16(msg[next+8:]))
		rd := next + 10
		if rd+rdl > len(msg) {
			break
		}
		switch {
		case typ == 1 && rdl == 4:
			ip := netip.AddrFrom4([4]byte(msg[rd : rd+4]))
			if ip.IsUnspecified() {
				a.blocked = true
			} else {
				a.addrs = append(a.addrs, ip)
				a.ttl = min(a.ttl, ttl)
			}
		case typ == 28 && rdl == 16:
			ip := netip.AddrFrom16([16]byte(msg[rd : rd+16]))
			if ip.IsUnspecified() {
				a.blocked = true
			} else {
				a.addrs = append(a.addrs, ip)
				a.ttl = min(a.ttl, ttl)
			}
		}
		off = rd + rdl
	}
	if len(a.addrs) == 0 {
		a.ttl = 0
	}
	return a, true
}

// ---- TLS ----

// clientHelloSNI returns the server name of a TLS ClientHello, if it is in
// the bytes seen (a ClientHello may span two segments; the name may be in
// the second).
func clientHelloSNI(b []byte) (string, bool) {
	if len(b) < 9 || b[0] != 0x16 || b[5] != 1 {
		return "", false
	}
	rec := int(binary.BigEndian.Uint16(b[3:]))
	b = b[5:]
	if len(b) > rec {
		b = b[:rec]
	}
	if len(b) < 4 { // a record shorter than a handshake header
		return "", false
	}
	hs := int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	b = b[4:]
	if len(b) > hs {
		b = b[:hs]
	}
	if len(b) < 35 {
		return "", false
	}
	b = b[34:] // version, random
	skip := func(lenBytes int) bool {
		if len(b) < lenBytes {
			return false
		}
		l := int(b[0])
		if lenBytes == 2 {
			l = int(binary.BigEndian.Uint16(b))
		}
		if len(b) < lenBytes+l {
			return false
		}
		b = b[lenBytes+l:]
		return true
	}
	if !skip(1) || !skip(2) || !skip(1) || len(b) < 2 { // session id, cipher suites, compression
		return "", false
	}
	el := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if len(b) > el {
		b = b[:el]
	}
	for len(b) >= 4 {
		typ, l := binary.BigEndian.Uint16(b), int(binary.BigEndian.Uint16(b[2:]))
		b = b[4:]
		if l > len(b) {
			return "", false
		}
		if typ == 0 { // server_name
			d := b[:l]
			if len(d) < 2 {
				return "", false
			}
			d = d[2:]
			for len(d) >= 3 {
				nt, nl := d[0], int(binary.BigEndian.Uint16(d[1:]))
				d = d[3:]
				if nl > len(d) {
					return "", false
				}
				if nt == 0 {
					n := normalizeHost(string(d[:nl]))
					return n, n != ""
				}
				d = d[nl:]
			}
			return "", false
		}
		b = b[l:]
	}
	return "", false
}

// ---- names ----

type nameKey struct{ client, ip netip.Addr }

type sniKey struct {
	client, server netip.Addr
	port           uint16 // the client's port
}

type nameVal struct {
	name  string
	until time.Time
}

type nameCache struct {
	mu     sync.Mutex
	byPair map[nameKey]nameVal
	byIP   map[netip.Addr]nameVal
	bySNI  map[sniKey]nameVal
	now    func() time.Time
}

const (
	maxNames    = 40000          // per map, at any time
	maxAnswerIP = 16             // addresses taken from one answer
	maxNameTTL  = 24 * time.Hour // a TTL past this is taken as this
)

// putName adds to a map of names without letting it grow past maxNames:
// a new key at the bound takes the place of an arbitrary one.
func putName[K comparable](m map[K]nameVal, k K, v nameVal) {
	if _, ok := m[k]; !ok && len(m) >= maxNames {
		for old := range m {
			delete(m, old)
			break
		}
	}
	m[k] = v
}

func newNameCache() *nameCache {
	return &nameCache{byPair: map[nameKey]nameVal{}, byIP: map[netip.Addr]nameVal{}, bySNI: map[sniKey]nameVal{}, now: time.Now}
}

// addDNS remembers the addresses of an answer. client is the address the
// answer went to; the zero address stands for the router itself.
func (c *nameCache) addDNS(client netip.Addr, a dnsAnswer) {
	if len(a.addrs) == 0 {
		return
	}
	now := c.now()
	ttl := min(time.Duration(a.ttl)*time.Second, maxNameTTL)
	addrs := a.addrs
	if len(addrs) > maxAnswerIP {
		addrs = addrs[:maxAnswerIP]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ip := range addrs {
		ip = ip.Unmap()
		// Clients keep connections far longer than the TTL; a client's own
		// answer stays longest, because a CDN address serves many names.
		putName(c.byPair, nameKey{client, ip}, nameVal{a.qname, now.Add(max(ttl, 3*time.Hour))})
		putName(c.byIP, ip, nameVal{a.qname, now.Add(max(ttl, 30*time.Minute))})
	}
}

func (c *nameCache) addSNI(client, server netip.Addr, port uint16, name string) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	putName(c.bySNI, sniKey{client.Unmap(), server.Unmap(), port}, nameVal{name, now.Add(time.Hour)})
	putName(c.byPair, nameKey{client.Unmap(), server.Unmap()}, nameVal{name, now.Add(3 * time.Hour)})
}

// lookup names the remote end of a flow: the flow's own TLS name first,
// then the client's DNS answer, then anyone's.
func (c *nameCache) lookup(client, remote netip.Addr, cport uint16) string {
	client, remote = client.Unmap(), remote.Unmap()
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.bySNI[sniKey{client, remote, cport}]; ok && now.Before(v.until) {
		return v.name
	}
	if v, ok := c.byPair[nameKey{client, remote}]; ok && now.Before(v.until) {
		return v.name
	}
	if v, ok := c.byIP[remote]; ok && now.Before(v.until) {
		return v.name
	}
	return ""
}

// sweep drops expired names, and some more when a map is still too big.
func (c *nameCache) sweep() {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	sweepMap(c.byPair, now)
	sweepMap(c.byIP, now)
	sweepMap(c.bySNI, now)
}

func sweepMap[K comparable](m map[K]nameVal, now time.Time) {
	for k, v := range m {
		if now.After(v.until) {
			delete(m, k)
		}
	}
	for k := range m { // random order: drops an arbitrary part
		if len(m) <= maxNames {
			break
		}
		delete(m, k)
	}
}

// ---- DNS statistics ----

type dnsStats struct {
	mu       sync.Mutex
	day      string
	perMin   *series // answers per minute, the last hour
	minute   int64
	count    int
	total    int
	blocked  int
	notFound int
	names    map[string]int     // answers by name, today
	clients  map[netip.Addr]int // answers by client, today
	blockedN map[string]int     // blocked names, today
}

func newDNSStats() *dnsStats {
	return &dnsStats{perMin: newSeries(60), names: map[string]int{}, clients: map[netip.Addr]int{},
		blockedN: map[string]int{}}
}

func (s *dnsStats) add(client netip.Addr, a dnsAnswer, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance(now)
	s.count++
	s.total++
	if len(s.clients) < 5000 || s.clients[client] > 0 {
		s.clients[client]++
	}
	if len(s.names) < 5000 || s.names[a.qname] > 0 {
		s.names[a.qname]++
	}
	switch {
	case a.blocked:
		s.blocked++
		if len(s.blockedN) < 2000 || s.blockedN[a.qname] > 0 {
			s.blockedN[a.qname]++
		}
	case a.rcode == 3:
		s.notFound++
	}
}

// advance closes the minutes that have passed; called with s.mu held.
func (s *dnsStats) advance(now time.Time) {
	m := now.Unix() / 60
	if s.minute == 0 {
		s.minute = m
		return
	}
	for ; s.minute < m && m-s.minute <= 61; s.minute++ {
		s.perMin.push(float64(s.count))
		s.count = 0
	}
	s.minute = m
}

func (s *dnsStats) tick(now time.Time) {
	s.mu.Lock()
	s.advance(now)
	s.mu.Unlock()
}

func (s *dnsStats) newDay(day string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.day == day {
		return
	}
	s.day = day
	s.total, s.blocked, s.notFound = 0, 0, 0
	s.names = map[string]int{}
	s.clients = map[netip.Addr]int{}
	s.blockedN = map[string]int{}
}

// ---- the capture ----

type sniffer struct {
	names *nameCache
	dns   *dnsStats
	log   *log.Logger
	isLAN func(netip.Addr) bool // a LAN host other than the router
	// macOf is the MAC address the router knows for a LAN address (ARP,
	// GL's client list); nil or !ok when it knows none.
	macOf func(netip.Addr) (string, bool)
	mu    sync.Mutex
	open  map[string]int // interface name → index of the open socket

	lastPanic atomic.Int64 // Unix time of the last packet the parser choked on
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// sniffInterfaces are the LAN bridges and lo.
func sniffInterfaces() map[string]int {
	out := map[string]int{}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		if ifc.Name == "lo" || strings.HasPrefix(ifc.Name, "br-") && ifc.Name != "br-wan" {
			out[ifc.Name] = ifc.Index
		}
	}
	return out
}

// refresh opens sockets on new interfaces (a network restart makes new
// bridges); the sockets of interfaces that went away end by themselves.
func (sn *sniffer) refresh() {
	want := sniffInterfaces()
	sn.mu.Lock()
	defer sn.mu.Unlock()
	for name, idx := range want {
		if sn.open[name] == idx {
			continue
		}
		fd, err := openPacketSocket(idx)
		if err != nil {
			sn.log.Printf("traffic: capture on %s: %v", name, err)
			continue
		}
		sn.open[name] = idx
		go sn.read(name, idx, fd)
	}
}

func openPacketSocket(ifindex int) (int, error) {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, int(htons(syscall.ETH_P_ALL)))
	if err != nil {
		return -1, err
	}
	if err := syscall.AttachLsf(fd, sniffFilter()); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_ALL), Ifindex: ifindex}); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

func (sn *sniffer) read(name string, idx, fd int) {
	defer syscall.Close(fd)
	defer func() {
		sn.mu.Lock()
		if sn.open[name] == idx {
			delete(sn.open, name)
		}
		sn.mu.Unlock()
	}()
	buf := make([]byte, sniffSnap+64)
	for {
		n, from, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			sn.log.Printf("traffic: capture on %s ended: %v", name, err)
			return
		}
		ll, ok := from.(*syscall.SockaddrLinklayer)
		// Packets queued before the socket was bound come from anywhere.
		if !ok || ll.Ifindex != idx {
			continue
		}
		// lo shows every packet twice, as sent and as received.
		if name == "lo" && ll.Pkttype == packetOut {
			continue
		}
		sn.handleSafe(buf[:n], name == "lo", ll.Pkttype == packetOut)
	}
}

// handleSafe is handle that a packet cannot take the collector down with:
// whatever its parsers missed costs that packet, not the day's numbers.
func (sn *sniffer) handleSafe(frame []byte, lo, out bool) {
	defer func() {
		if r := recover(); r != nil {
			if now := time.Now().Unix(); sn.lastPanic.Swap(now) < now-60 {
				sn.log.Printf("traffic: a packet the parser could not take: %v", r)
			}
		}
	}()
	sn.handle(frame, lo, out)
}

// handle takes a frame seen on lo or on a LAN bridge (out: one the router
// sent there). Anyone on the LAN can send anything, so only what the
// router itself vouches for names the flows of others:
//
//   - a DNS answer counts if the router sent it (dnsmasq, or an answer it
//     passed on from the internet), and not if it came from another LAN
//     host: the router passes on what a guest sends to another device,
//     forged source and all, as frames of its own;
//   - a ClientHello names the flows of the address it came from only if
//     the frame came from the device the router knows at that address: a
//     guest can put anyone's address on a packet, not anyone's MAC.
func (sn *sniffer) handle(frame []byte, lo, out bool) {
	p, ok := parseFrame(frame)
	if !ok {
		return
	}
	now := time.Now()
	if p.dns {
		if !lo && (!out || sn.isLAN(p.src)) {
			return
		}
		a, ok := parseDNSResponse(p.payload)
		if !ok {
			return
		}
		client := p.dst.Unmap()
		switch {
		case client.IsLoopback():
			sn.names.addDNS(netip.Addr{}, a)
		case sn.isLAN(client):
			sn.names.addDNS(client, a)
			sn.dns.add(client, a, now)
		}
		return
	}
	if !lo {
		if out {
			return // the router passing a frame on: not a LAN client's hello
		}
		if sn.macOf != nil {
			if mac, ok := sn.macOf(p.src.Unmap()); ok && mac != net.HardwareAddr(frame[6:12]).String() {
				return
			}
		}
	}
	if name, ok := clientHelloSNI(p.payload); ok {
		sn.names.addSNI(p.src, p.dst, p.sport, name)
	}
}
