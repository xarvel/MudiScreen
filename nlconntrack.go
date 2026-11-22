package main

// The connection tracking table, read over netlink (ctnetlink) the way
// `conntrack -L` reads it: one dump request, binary answers. Byte counters
// need nf_conntrack_acct=1, which the firmware has. On this router the
// counters stay true for forwarded traffic: GL's IPA acceleration does not
// take flows past them (checked against rmnet_data0 for USB and Wi-Fi
// clients).

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"syscall"
)

const (
	nlmFRequest = 0x1
	nlmFDump    = 0x300
	nlmsgError  = 2
	nlmsgDone   = 3

	nfnlSubsysCtnetlink = 1
	ctMsgNew            = 0
	ctMsgGet            = 1

	ctaTupleOrig     = 1
	ctaTupleReply    = 2
	ctaStatus        = 3
	ctaCountersOrig  = 9
	ctaCountersReply = 10
	ctaID            = 12

	ctaTupleIP    = 1
	ctaTupleProto = 2

	ctaIPv4Src = 1
	ctaIPv4Dst = 2
	ctaIPv6Src = 3
	ctaIPv6Dst = 4

	ctaProtoNum      = 1
	ctaProtoSrcPort  = 2
	ctaProtoDstPort  = 3
	ctaProtoICMPID   = 4
	ctaProtoICMPType = 5
	ctaProtoICMP6ID  = 7
	ctaProtoICMP6Typ = 8

	ctaCountersPackets   = 1
	ctaCountersBytes     = 2
	ctaCounters32Packets = 3
	ctaCounters32Bytes   = 4
)

type ctTuple struct {
	src, dst     netip.Addr
	sport, dport uint16 // ICMP: id and type
	proto        uint8
}

type ctFlow struct {
	id                    uint32
	orig, reply           ctTuple
	origBytes, replyBytes uint64
	origPkts, replyPkts   uint64
	status                uint32
}

// nlAttrs walks netlink attributes; the header is in host byte order.
func nlAttrs(b []byte, fn func(typ uint16, val []byte)) error {
	for len(b) >= 4 {
		l := int(binary.NativeEndian.Uint16(b))
		if l < 4 || l > len(b) {
			return errors.New("netlink: bad attribute")
		}
		fn(binary.NativeEndian.Uint16(b[2:])&0x3fff, b[4:l])
		al := (l + 3) &^ 3
		if al >= len(b) {
			return nil
		}
		b = b[al:]
	}
	return nil
}

func parseCtTuple(b []byte) ctTuple {
	var t ctTuple
	_ = nlAttrs(b, func(typ uint16, v []byte) {
		switch typ {
		case ctaTupleIP:
			_ = nlAttrs(v, func(typ uint16, v []byte) {
				switch {
				case typ == ctaIPv4Src && len(v) == 4:
					t.src = netip.AddrFrom4([4]byte(v))
				case typ == ctaIPv4Dst && len(v) == 4:
					t.dst = netip.AddrFrom4([4]byte(v))
				case typ == ctaIPv6Src && len(v) == 16:
					t.src = netip.AddrFrom16([16]byte(v))
				case typ == ctaIPv6Dst && len(v) == 16:
					t.dst = netip.AddrFrom16([16]byte(v))
				}
			})
		case ctaTupleProto:
			_ = nlAttrs(v, func(typ uint16, v []byte) {
				switch {
				case typ == ctaProtoNum && len(v) >= 1:
					t.proto = v[0]
				case (typ == ctaProtoSrcPort || typ == ctaProtoICMPID || typ == ctaProtoICMP6ID) && len(v) >= 2:
					t.sport = binary.BigEndian.Uint16(v)
				case typ == ctaProtoDstPort && len(v) >= 2:
					t.dport = binary.BigEndian.Uint16(v)
				case (typ == ctaProtoICMPType || typ == ctaProtoICMP6Typ) && len(v) >= 1:
					t.dport = uint16(v[0])
				}
			})
		}
	})
	return t
}

func parseCtCounters(b []byte) (pkts, bytes uint64) {
	_ = nlAttrs(b, func(typ uint16, v []byte) {
		switch {
		case typ == ctaCountersPackets && len(v) >= 8:
			pkts = binary.BigEndian.Uint64(v)
		case typ == ctaCountersBytes && len(v) >= 8:
			bytes = binary.BigEndian.Uint64(v)
		case typ == ctaCounters32Packets && len(v) >= 4:
			pkts = uint64(binary.BigEndian.Uint32(v))
		case typ == ctaCounters32Bytes && len(v) >= 4:
			bytes = uint64(binary.BigEndian.Uint32(v))
		}
	})
	return
}

// parseCtMessage reads one conntrack entry: the payload of a netlink
// message after its header (nfgenmsg, then attributes).
func parseCtMessage(b []byte) (ctFlow, bool) {
	var f ctFlow
	if len(b) < 4 {
		return f, false
	}
	err := nlAttrs(b[4:], func(typ uint16, v []byte) {
		switch typ {
		case ctaTupleOrig:
			f.orig = parseCtTuple(v)
		case ctaTupleReply:
			f.reply = parseCtTuple(v)
		case ctaStatus:
			if len(v) >= 4 {
				f.status = binary.BigEndian.Uint32(v)
			}
		case ctaCountersOrig:
			f.origPkts, f.origBytes = parseCtCounters(v)
		case ctaCountersReply:
			f.replyPkts, f.replyBytes = parseCtCounters(v)
		case ctaID:
			if len(v) >= 4 {
				f.id = binary.BigEndian.Uint32(v)
			}
		}
	})
	return f, err == nil && f.orig.src.IsValid() && f.orig.dst.IsValid()
}

// parseNlBatch reads the netlink messages of one datagram; done is true at
// the end of the dump.
func parseNlBatch(b []byte, flows *[]ctFlow) (done bool, err error) {
	for len(b) >= 16 {
		l := int(binary.NativeEndian.Uint32(b))
		typ := binary.NativeEndian.Uint16(b[4:])
		if l < 16 || l > len(b) {
			return false, errors.New("netlink: bad message")
		}
		msg := b[16:l]
		switch {
		case typ == nlmsgDone:
			return true, nil
		case typ == nlmsgError:
			if len(msg) >= 4 {
				if e := int32(binary.NativeEndian.Uint32(msg)); e != 0 {
					return true, syscall.Errno(-e)
				}
			}
			return true, nil
		case typ == nfnlSubsysCtnetlink<<8|ctMsgNew:
			if f, ok := parseCtMessage(msg); ok {
				*flows = append(*flows, f)
			}
		}
		al := (l + 3) &^ 3
		if al >= len(b) {
			break
		}
		b = b[al:]
	}
	return false, nil
}

// dumpConntrack returns the whole table, IPv4 and IPv6.
func dumpConntrack() ([]ctFlow, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_NETFILTER)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return nil, err
	}
	tv := syscall.Timeval{Sec: 3}
	_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
	_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 1<<20)
	req := make([]byte, 20)
	binary.NativeEndian.PutUint32(req[0:], 20)
	binary.NativeEndian.PutUint16(req[4:], nfnlSubsysCtnetlink<<8|ctMsgGet)
	binary.NativeEndian.PutUint16(req[6:], nlmFRequest|nlmFDump)
	binary.NativeEndian.PutUint32(req[8:], 1)
	// nfgenmsg: family AF_UNSPEC (every family), version 0, res_id 0.
	if err := syscall.Sendto(fd, req, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return nil, err
	}
	buf := make([]byte, 1<<17)
	flows := make([]ctFlow, 0, 512)
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return flows, err
		}
		done, err := parseNlBatch(buf[:n], &flows)
		if done || err != nil {
			return flows, err
		}
	}
}
