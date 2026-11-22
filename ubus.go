package main

// A minimal ubus client that publishes objects and answers their calls. The
// screen publishes "gl_screen" with the method "set", as GL's gl_screen does
// (lpm-monitor calls it with the power key and battery events, the web admin
// with "reload" and "config_update", screen_disp_switch with "switch"), and
// its own "mudi-screen" object for apps.
//
// Wire format (libubus ubusmsg.h, libubox blob.h/blobmsg.h): every message
// is an 8-byte header {version 0, type, seq be16, peer be32} followed by one
// blob attribute (id 0) whose payload is a list of UBUS_ATTR_* attributes.
// A blob attribute header is a big-endian u32: bit 31 "extended" (blobmsg),
// bits 24..30 id/type, bits 0..23 length including the header; attributes
// are padded to 4 bytes. A blobmsg attribute adds {namelen be16, name, NUL}
// padded to 4 bytes before its data.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"slices"
	"sort"
	"time"
)

const (
	ubusMsgHello     = 0
	ubusMsgStatus    = 1
	ubusMsgData      = 2
	ubusMsgInvoke    = 5
	ubusMsgAddObject = 6

	ubusAttrStatus    = 1
	ubusAttrObjPath   = 2
	ubusAttrObjID     = 3
	ubusAttrMethod    = 4
	ubusAttrSignature = 6
	ubusAttrData      = 7
	ubusAttrNoReply   = 10

	ubusStatusOK             = 0
	ubusStatusMethodNotFound = 3

	blobExtended = 0x80000000
	blobLenMask  = 0x00ffffff

	blobmsgArray  = 1
	blobmsgTable  = 2
	blobmsgString = 3
	blobmsgInt64  = 4
	blobmsgInt32  = 5
	blobmsgInt16  = 6
	blobmsgInt8   = 7
	blobmsgDouble = 8
)

// ubusObject is an object we publish; every method takes any table.
type ubusObject struct {
	name    string
	methods []string
}

// ubusCall is one call to our objects. The UI answers every call on reply
// (nil: no data), within a second, or the caller gets an empty answer.
type ubusCall struct {
	object, method string
	data           map[string]any
	raw            string // data as JSON, for the log
	reply          chan map[string]any
}

func pad4(n int) int { return (n + 3) &^ 3 }

// appendBlob appends one attribute. dst must end on a 4-byte boundary.
func appendBlob(dst []byte, id int, extended bool, payload []byte) []byte {
	h := uint32(id&0x7f)<<24 | uint32(4+len(payload))
	if extended {
		h |= blobExtended
	}
	dst = binary.BigEndian.AppendUint32(dst, h)
	dst = append(dst, payload...)
	for len(dst)%4 != 0 {
		dst = append(dst, 0)
	}
	return dst
}

func appendBlobString(dst []byte, id int, s string) []byte {
	return appendBlob(dst, id, false, append([]byte(s), 0))
}

func appendBlobU32(dst []byte, id int, v uint32) []byte {
	return appendBlob(dst, id, false, binary.BigEndian.AppendUint32(nil, v))
}

// appendBlobmsg appends a named blobmsg attribute of the given type.
func appendBlobmsg(dst []byte, typ int, name string, data []byte) []byte {
	hdr := binary.BigEndian.AppendUint16(nil, uint16(len(name)))
	hdr = append(hdr, name...)
	hdr = append(hdr, 0)
	for len(hdr)%4 != 0 {
		hdr = append(hdr, 0)
	}
	return appendBlob(dst, typ, true, append(hdr, data...))
}

type blobAttr struct {
	id   int
	ext  bool
	data []byte
}

func parseBlobs(b []byte) ([]blobAttr, error) {
	var out []blobAttr
	for len(b) > 0 {
		if len(b) < 4 {
			return out, errors.New("short blob")
		}
		h := binary.BigEndian.Uint32(b)
		l := int(h & blobLenMask)
		if l < 4 || l > len(b) {
			return out, fmt.Errorf("bad blob length %d of %d", l, len(b))
		}
		out = append(out, blobAttr{id: int(h >> 24 & 0x7f), ext: h&blobExtended != 0, data: b[4:l]})
		b = b[min(pad4(l), len(b)):]
	}
	return out, nil
}

// blobmsgSplit returns the name and value bytes of a blobmsg attribute.
func blobmsgSplit(a blobAttr) (string, []byte, error) {
	if len(a.data) < 3 {
		return "", nil, errors.New("short blobmsg")
	}
	n := int(binary.BigEndian.Uint16(a.data))
	hl := pad4(2 + n + 1)
	if 2+n > len(a.data) || hl > len(a.data) {
		return "", nil, errors.New("bad blobmsg name")
	}
	return string(a.data[2 : 2+n]), a.data[hl:], nil
}

func blobmsgValue(typ int, v []byte) (any, error) {
	switch typ {
	case blobmsgTable:
		return decodeTable(v)
	case blobmsgArray:
		attrs, err := parseBlobs(v)
		if err != nil {
			return nil, err
		}
		arr := make([]any, 0, len(attrs))
		for _, a := range attrs {
			_, val, err := blobmsgSplit(a)
			if err != nil {
				return nil, err
			}
			x, err := blobmsgValue(a.id, val)
			if err != nil {
				return nil, err
			}
			arr = append(arr, x)
		}
		return arr, nil
	case blobmsgString:
		if n := len(v); n > 0 && v[n-1] == 0 {
			v = v[:n-1]
		}
		return string(v), nil
	case blobmsgInt64:
		if len(v) < 8 {
			return nil, errors.New("short int64")
		}
		return int64(binary.BigEndian.Uint64(v)), nil
	case blobmsgInt32:
		if len(v) < 4 {
			return nil, errors.New("short int32")
		}
		return int64(int32(binary.BigEndian.Uint32(v))), nil
	case blobmsgInt16:
		if len(v) < 2 {
			return nil, errors.New("short int16")
		}
		return int64(int16(binary.BigEndian.Uint16(v))), nil
	case blobmsgInt8:
		if len(v) < 1 {
			return nil, errors.New("short int8")
		}
		return v[0] != 0, nil
	case blobmsgDouble:
		if len(v) < 8 {
			return nil, errors.New("short double")
		}
		return math.Float64frombits(binary.BigEndian.Uint64(v)), nil
	}
	return nil, nil
}

// decodeTable decodes the attributes of a blobmsg table.
func decodeTable(b []byte) (map[string]any, error) {
	attrs, err := parseBlobs(b)
	if err != nil {
		return nil, err
	}
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		if !a.ext {
			continue
		}
		name, val, err := blobmsgSplit(a)
		if err != nil {
			return m, err
		}
		x, err := blobmsgValue(a.id, val)
		if err != nil {
			return m, err
		}
		m[name] = x
	}
	return m, nil
}

type ubusConn struct {
	c   net.Conn
	seq uint16
}

func (u *ubusConn) read() (typ uint8, seq uint16, peer uint32, body []byte, err error) {
	var h [12]byte
	if _, err = io.ReadFull(u.c, h[:]); err != nil {
		return
	}
	if h[0] != 0 {
		err = fmt.Errorf("ubus: unknown protocol version %d", h[0])
		return
	}
	typ, seq, peer = h[1], binary.BigEndian.Uint16(h[2:]), binary.BigEndian.Uint32(h[4:])
	l := int(binary.BigEndian.Uint32(h[8:]) & blobLenMask)
	if l < 4 || l > 1<<20 {
		err = fmt.Errorf("ubus: bad message length %d", l)
		return
	}
	body = make([]byte, l-4)
	_, err = io.ReadFull(u.c, body)
	return
}

func (u *ubusConn) write(typ uint8, seq uint16, peer uint32, payload []byte) error {
	msg := make([]byte, 0, 12+len(payload))
	msg = append(msg, 0, typ)
	msg = binary.BigEndian.AppendUint16(msg, seq)
	msg = binary.BigEndian.AppendUint32(msg, peer)
	msg = binary.BigEndian.AppendUint32(msg, uint32(4+len(payload)))
	msg = append(msg, payload...)
	_, err := u.c.Write(msg)
	return err
}

func attrU32(attrs []blobAttr, id int) (uint32, bool) {
	for _, a := range attrs {
		if a.id == id && len(a.data) >= 4 {
			return binary.BigEndian.Uint32(a.data), true
		}
	}
	return 0, false
}

func attrString(attrs []blobAttr, id int) string {
	for _, a := range attrs {
		if a.id == id {
			d := a.data
			if n := len(d); n > 0 && d[n-1] == 0 {
				d = d[:n-1]
			}
			return string(d)
		}
	}
	return ""
}

// serveUbus publishes the objects and forwards their calls; it reconnects
// forever (ubusd restarts, or a name is still taken by gl_screen).
func serveUbus(sock string, objs []ubusObject, out chan<- ubusCall, logger *log.Logger) {
	wait := time.Second
	for {
		err := serveUbusOnce(sock, objs, out, logger)
		logger.Printf("ubus: %v; retrying in %v", err, wait)
		time.Sleep(wait)
		wait = min(wait*2, 10*time.Second)
	}
}

func (u *ubusConn) addObject(o ubusObject) (uint32, error) {
	var sig []byte
	for _, m := range o.methods {
		sig = appendBlobmsg(sig, blobmsgTable, m, nil)
	}
	req := appendBlobString(nil, ubusAttrObjPath, o.name)
	req = appendBlob(req, ubusAttrSignature, false, sig)
	u.seq++
	seq := u.seq
	if err := u.write(ubusMsgAddObject, seq, 0, req); err != nil {
		return 0, err
	}
	var id uint32
	for {
		typ, rseq, _, body, err := u.read()
		if err != nil {
			return 0, err
		}
		if rseq != seq {
			continue
		}
		attrs, err := parseBlobs(body)
		if err != nil {
			return 0, err
		}
		switch typ {
		case ubusMsgData:
			if v, ok := attrU32(attrs, ubusAttrObjID); ok {
				id = v
			}
		case ubusMsgStatus:
			if st, _ := attrU32(attrs, ubusAttrStatus); st != ubusStatusOK {
				return 0, fmt.Errorf("add object %q: status %d", o.name, st)
			}
			return id, nil
		}
	}
}

func serveUbusOnce(sock string, objs []ubusObject, out chan<- ubusCall, logger *log.Logger) error {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return err
	}
	defer c.Close()
	u := &ubusConn{c: c}
	typ, _, _, _, err := u.read()
	if err != nil {
		return err
	}
	if typ != ubusMsgHello {
		return fmt.Errorf("expected hello, got message type %d", typ)
	}
	byID := map[uint32]ubusObject{}
	for _, o := range objs {
		id, err := u.addObject(o)
		if err != nil {
			return err
		}
		byID[id] = o
		logger.Printf("ubus: published %q (id %08x)", o.name, id)
	}

	for {
		typ, seq, peer, body, err := u.read()
		if err != nil {
			return err
		}
		if typ != ubusMsgInvoke {
			continue
		}
		attrs, err := parseBlobs(body)
		if err != nil {
			logger.Printf("ubus: bad invoke: %v", err)
			continue
		}
		method := attrString(attrs, ubusAttrMethod)
		id, _ := attrU32(attrs, ubusAttrObjID)
		noReply := false
		var data []byte
		for _, a := range attrs {
			switch a.id {
			case ubusAttrData:
				data = a.data
			case ubusAttrNoReply:
				noReply = len(a.data) > 0 && a.data[0] != 0
			}
		}
		o, known := byID[id]
		status := uint32(ubusStatusOK)
		var answer map[string]any
		if !known || !slices.Contains(o.methods, method) {
			status = ubusStatusMethodNotFound
		} else {
			m, err := decodeTable(data)
			if err != nil {
				logger.Printf("ubus: bad data: %v", err)
			}
			raw, _ := json.Marshal(m)
			call := ubusCall{object: o.name, method: method, data: m, raw: string(raw), reply: make(chan map[string]any, 1)}
			select {
			case out <- call:
				select {
				case answer = <-call.reply:
				case <-time.After(time.Second):
					logger.Printf("ubus: %s %s: no answer from the UI", o.name, method)
				}
			default:
				logger.Printf("ubus: call dropped: %s %s %s", o.name, method, redact(string(raw)))
			}
		}
		if noReply {
			continue
		}
		if answer != nil {
			d := appendBlobU32(nil, ubusAttrObjID, id)
			d = appendBlob(d, ubusAttrData, false, encodeTable(answer))
			if err := u.write(ubusMsgData, seq, peer, d); err != nil {
				return err
			}
		}
		rep := appendBlobU32(nil, ubusAttrStatus, status)
		rep = appendBlobU32(rep, ubusAttrObjID, id)
		if err := u.write(ubusMsgStatus, seq, peer, rep); err != nil {
			return err
		}
	}
}

// encodeTable encodes a JSON-like map as the attributes of a blobmsg table.
func encodeTable(m map[string]any) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b []byte
	for _, k := range keys {
		b = appendBlobmsgValue(b, k, m[k])
	}
	return b
}

func appendBlobmsgValue(b []byte, name string, v any) []byte {
	switch x := v.(type) {
	case nil:
		return b
	case string:
		return appendBlobmsg(b, blobmsgString, name, append([]byte(x), 0))
	case bool:
		var c byte
		if x {
			c = 1
		}
		return appendBlobmsg(b, blobmsgInt8, name, []byte{c})
	case int:
		return appendBlobmsgInt(b, name, int64(x))
	case int64:
		return appendBlobmsgInt(b, name, x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return appendBlobmsgInt(b, name, int64(x))
		}
		return appendBlobmsg(b, blobmsgDouble, name, binary.BigEndian.AppendUint64(nil, math.Float64bits(x)))
	case map[string]any:
		return appendBlobmsg(b, blobmsgTable, name, encodeTable(x))
	case []any:
		var arr []byte
		for _, e := range x {
			arr = appendBlobmsgValue(arr, "", e)
		}
		return appendBlobmsg(b, blobmsgArray, name, arr)
	case []map[string]any:
		var arr []byte
		for _, e := range x {
			arr = appendBlobmsg(arr, blobmsgTable, "", encodeTable(e))
		}
		return appendBlobmsg(b, blobmsgArray, name, arr)
	case []string:
		var arr []byte
		for _, e := range x {
			arr = appendBlobmsg(arr, blobmsgString, "", append([]byte(e), 0))
		}
		return appendBlobmsg(b, blobmsgArray, name, arr)
	}
	return appendBlobmsg(b, blobmsgString, name, append([]byte(fmt.Sprint(v)), 0))
}

func appendBlobmsgInt(b []byte, name string, v int64) []byte {
	if v >= math.MinInt32 && v <= math.MaxInt32 {
		return appendBlobmsg(b, blobmsgInt32, name, binary.BigEndian.AppendUint32(nil, uint32(int32(v))))
	}
	return appendBlobmsg(b, blobmsgInt64, name, binary.BigEndian.AppendUint64(nil, uint64(v)))
}

// probeUbus publishes the object and prints its calls, without touching the
// display: a protocol check next to a running gl_screen (use another name).
func probeUbus(o options) {
	logger := log.New(os.Stdout, "", log.Lmicroseconds)
	ch := make(chan ubusCall, 16)
	go serveUbus(o.ubusSock, []ubusObject{{o.ubusName, []string{"set", "echo"}}}, ch, logger)
	d := o.ttl
	if d <= 0 {
		d = 30 * time.Second
	}
	end := time.After(d)
	for {
		select {
		case c := <-ch:
			logger.Printf("call %s %s %s", c.object, c.method, redact(c.raw))
			if c.method == "echo" {
				c.reply <- c.data
			} else {
				c.reply <- nil
			}
		case <-end:
			return
		}
	}
}
