package main

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// The add-object request must match libubus byte for byte:
// blob_put_string(OBJPATH) + nest(SIGNATURE){ blobmsg_open_table("set") }.
func TestAddObjectEncoding(t *testing.T) {
	sig := appendBlobmsg(nil, blobmsgTable, "set", nil)
	// extended | type 2 (table) | len 4 + hdr(2+3+1 = 6 → 8) = 12
	want := []byte{0x82, 0, 0, 12, 0, 3, 's', 'e', 't', 0, 0, 0}
	if !bytes.Equal(sig, want) {
		t.Fatalf("signature table % x, want % x", sig, want)
	}
	req := appendBlobString(nil, ubusAttrObjPath, "gl_screen")
	// id 2, len 4 + 10 = 14, padded to 16
	if binary.BigEndian.Uint32(req) != 2<<24|14 || len(req) != 16 {
		t.Fatalf("objpath % x", req)
	}
	req = appendBlob(req, ubusAttrSignature, false, sig)
	if len(req) != 16+4+12 || binary.BigEndian.Uint32(req[16:]) != 6<<24|16 {
		t.Fatalf("request % x", req)
	}
}

func TestDecodeTable(t *testing.T) {
	var params []byte
	params = appendBlobmsg(params, blobmsgInt8, "enable", []byte{1})
	params = appendBlobmsg(params, blobmsgString, "mode", []byte("VPN\x00"))
	var tbl []byte
	tbl = appendBlobmsg(tbl, blobmsgString, "method", []byte("power key long press\x00"))
	tbl = appendBlobmsg(tbl, blobmsgTable, "params", params)
	tbl = appendBlobmsg(tbl, blobmsgInt32, "n", binary.BigEndian.AppendUint32(nil, uint32(0xfffffffe)))
	m, err := decodeTable(tbl)
	if err != nil {
		t.Fatal(err)
	}
	if m["method"] != "power key long press" {
		t.Fatalf("method %#v", m["method"])
	}
	p, ok := m["params"].(map[string]any)
	if !ok || p["enable"] != true || p["mode"] != "VPN" {
		t.Fatalf("params %#v", m["params"])
	}
	if m["n"] != int64(-2) {
		t.Fatalf("n %#v", m["n"])
	}
}

func TestRedact(t *testing.T) {
	in := `{"method":"config_update","params":{"config":{"PASSCODE":"\"1234\"","BRIGHTNESS":"50","ENABLE_PASSCODE":1}}}`
	out := redact(in)
	if bytes.Contains([]byte(out), []byte("1234")) {
		t.Fatalf("passcode leaked: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte(`"BRIGHTNESS":"50"`)) {
		t.Fatalf("too much redacted: %s", out)
	}
}

func TestTouchEvents(t *testing.T) {
	d := newTouchDecoder()
	clock := time.Unix(0, 0)
	d.now = func() time.Time { return clock }
	var got []touchEvent
	ev := func(typ, code uint16, v int32) {
		if e, ok := d.feed(typ, code, v); ok {
			got = append(got, e)
		}
	}
	// A contact at (100, 200) that moves to (103, 260) and lifts.
	ev(evAbs, absMTSlot, 0)
	ev(evAbs, absMTTrackingID, 7)
	ev(evAbs, absMTPositionX, 100)
	ev(evAbs, absMTPositionY, 200)
	ev(evSyn, synReport, 0)
	ev(evSyn, synReport, 0) // no change: no event
	clock = clock.Add(30 * time.Millisecond)
	ev(evAbs, absMTPositionX, 103)
	ev(evAbs, absMTPositionY, 260)
	// A second finger in slot 1 changes nothing.
	ev(evAbs, absMTSlot, 1)
	ev(evAbs, absMTTrackingID, 9)
	ev(evAbs, absMTPositionX, 10)
	ev(evAbs, absMTSlot, 0)
	ev(evSyn, synReport, 0)
	ev(evAbs, absMTTrackingID, -1)
	ev(evSyn, synReport, 0)
	want := []touchEvent{{touchDown, 100, 200, time.Unix(0, 0)}, {touchMove, 103, 260, clock}, {touchUp, 103, 260, clock}}
	if len(got) != len(want) {
		t.Fatalf("events %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	// The next contact reports its own position before a down.
	got = nil
	ev(evAbs, absMTTrackingID, 8)
	ev(evSyn, synReport, 0)
	if len(got) != 0 {
		t.Fatalf("down without a position: %+v", got)
	}
	ev(evAbs, absMTPositionX, 5)
	ev(evAbs, absMTPositionY, 6)
	ev(evSyn, synReport, 0)
	if len(got) != 1 || got[0].kind != touchDown || got[0].x != 5 || got[0].y != 6 {
		t.Fatalf("second contact: %+v", got)
	}
}

func TestChangedRows(t *testing.T) {
	a := make([]byte, W*H*2)
	b := make([]byte, W*H*2)
	if y0, y1 := changedRows(a, b); y0 != -1 || y1 != -1 {
		t.Fatalf("equal frames: %d %d", y0, y1)
	}
	b[5*W*2+7] = 1
	b[9*W*2] = 1
	if y0, y1 := changedRows(a, b); y0 != 5 || y1 != 9 {
		t.Fatalf("rows %d %d", y0, y1)
	}
}

func TestPowerKeyEpisodes(t *testing.T) {
	clock := time.Unix(100, 0)
	d := &keyEpisodeDecoder{now: func() time.Time { return clock }}
	step := func(ms int) { clock = clock.Add(time.Duration(ms) * time.Millisecond) }
	// A tap that bounces: press, release 60 ms later, press again 40 ms
	// later, final release at 150 ms.
	d.feed(1)
	step(60)
	d.feed(0)
	step(40)
	d.feed(1)
	step(50)
	d.feed(0)
	step(100)
	if _, ok := d.done(); ok {
		t.Fatal("episode ended before the quiet time")
	}
	step(200)
	held, ok := d.done()
	if !ok || held != 150*time.Millisecond {
		t.Fatalf("tap: %v %v", held, ok)
	}
	// A 2-second hold with key repeat events.
	d.feed(1)
	step(1000)
	d.feed(2)
	step(1000)
	d.feed(0)
	step(300)
	held, ok = d.done()
	if !ok || held != 2*time.Second {
		t.Fatalf("hold: %v %v", held, ok)
	}
}
