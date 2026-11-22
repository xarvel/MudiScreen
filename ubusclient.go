package main

// The client side of ubus: `ubus call OBJECT METHOD ARGS` over the socket,
// without starting the ubus program (which costs more than the call).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	ubusMsgLookup   = 4
	defaultUbusSock = "/var/run/ubus/ubus.sock"
)

var errUbusNotFound = errors.New("ubus: object not found")

// ubusInvoke looks the object up and calls the method, over a connection
// of its own.
func ubusInvoke(sock, obj, method string, args map[string]any, timeout time.Duration) (map[string]any, error) {
	c, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	u := &ubusConn{c: c}
	typ, _, _, _, err := u.read()
	if err != nil {
		return nil, err
	}
	if typ != ubusMsgHello {
		return nil, fmt.Errorf("ubus: expected hello, got message type %d", typ)
	}

	u.seq++
	seq := u.seq
	if err := u.write(ubusMsgLookup, seq, 0, appendBlobString(nil, ubusAttrObjPath, obj)); err != nil {
		return nil, err
	}
	var id uint32
	found := false
	for done := false; !done; {
		typ, rseq, _, body, err := u.read()
		if err != nil {
			return nil, err
		}
		if rseq != seq {
			continue
		}
		attrs, err := parseBlobs(body)
		if err != nil {
			return nil, err
		}
		switch typ {
		case ubusMsgData:
			if v, ok := attrU32(attrs, ubusAttrObjID); ok {
				id, found = v, true
			}
		case ubusMsgStatus:
			if st, _ := attrU32(attrs, ubusAttrStatus); st != ubusStatusOK || !found {
				return nil, errUbusNotFound
			}
			done = true
		}
	}

	u.seq++
	seq = u.seq
	req := appendBlobU32(nil, ubusAttrObjID, id)
	req = appendBlobString(req, ubusAttrMethod, method)
	req = appendBlob(req, ubusAttrData, false, encodeTable(args))
	if err := u.write(ubusMsgInvoke, seq, id, req); err != nil {
		return nil, err
	}
	var out map[string]any
	for {
		typ, rseq, _, body, err := u.read()
		if err != nil {
			return out, err
		}
		if rseq != seq {
			continue
		}
		attrs, err := parseBlobs(body)
		if err != nil {
			return out, err
		}
		switch typ {
		case ubusMsgData:
			for _, a := range attrs {
				if a.id == ubusAttrData {
					if out, err = decodeTable(a.data); err != nil {
						return out, err
					}
				}
			}
		case ubusMsgStatus:
			if st, _ := attrU32(attrs, ubusAttrStatus); st != ubusStatusOK {
				return out, fmt.Errorf("ubus %s %s: status %d", obj, method, st)
			}
			return out, nil
		}
	}
}

// ubusCallJSON calls a method and decodes the answer into v, as the
// output of `ubus call` would decode.
func ubusCallJSON(obj, method string, args map[string]any, v any) error {
	m, err := ubusInvoke(defaultUbusSock, obj, method, args, 5*time.Second)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
