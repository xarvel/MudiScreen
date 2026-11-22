package main

// Touch: chsc_cap_touch on /dev/input/event0 speaks multitouch protocol B
// with coordinates in screen pixels (ABS_MT_POSITION_X/Y 0..239/0..319,
// ABS_MT_TRACKING_ID -1 on lift). Only slot 0 is followed: a second finger
// must not move a page. The decoder turns the events of one contact into
// down, move and up; the screen makes taps, scrolls and swipes of them.

import (
	"encoding/binary"
	"io"
	"log"
	"os"
	"time"
)

const (
	evSyn = 0
	evKey = 1
	evAbs = 3

	synReport = 0

	absX            = 0x00
	absY            = 0x01
	absMTSlot       = 0x2f
	absMTPositionX  = 0x35
	absMTPositionY  = 0x36
	absMTTrackingID = 0x39

	inputEventSize = 24 // struct input_event on 64-bit: timeval 16 + type 2 + code 2 + value 4
)

type touchKind int

const (
	touchDown touchKind = iota
	touchMove
	touchUp
)

type touchEvent struct {
	kind touchKind
	x, y int
	t    time.Time
}

// touchDecoder turns input events into touch events.
type touchDecoder struct {
	slot        int32
	down        bool
	x, y        int32
	sent        bool // a down was sent for this contact
	lx, ly      int32
	singleTouch bool // the device reports ABS_X/ABS_Y without MT slots
	now         func() time.Time
}

func newTouchDecoder() *touchDecoder {
	return &touchDecoder{x: -1, y: -1, now: time.Now}
}

// feed handles one input event; at the end of a report it may return a
// touch event.
func (d *touchDecoder) feed(typ, code uint16, val int32) (touchEvent, bool) {
	switch typ {
	case evAbs:
		switch code {
		case absMTSlot:
			d.slot = val
		case absMTTrackingID:
			if d.slot == 0 {
				d.down = val >= 0
				if d.down && !d.sent {
					d.x, d.y = -1, -1 // the position of this contact follows
				}
			}
		case absMTPositionX:
			if d.slot == 0 {
				d.x = val
			}
		case absMTPositionY:
			if d.slot == 0 {
				d.y = val
			}
		case absX:
			if d.singleTouch {
				d.x = val
			}
		case absY:
			if d.singleTouch {
				d.y = val
			}
		}
	case evKey:
		if code == 0x14a && d.singleTouch { // BTN_TOUCH
			d.down = val != 0
		}
	case evSyn:
		if code != synReport {
			return touchEvent{}, false
		}
		switch {
		case d.down && d.x >= 0 && d.y >= 0:
			ev := touchEvent{kind: touchMove, x: int(d.x), y: int(d.y), t: d.now()}
			if !d.sent {
				ev.kind = touchDown
				d.sent = true
			} else if d.x == d.lx && d.y == d.ly {
				return touchEvent{}, false
			}
			d.lx, d.ly = d.x, d.y
			return ev, true
		case !d.down && d.sent:
			d.sent = false
			return touchEvent{kind: touchUp, x: int(d.lx), y: int(d.ly), t: d.now()}, true
		}
	}
	return touchEvent{}, false
}

func readTouch(path string, out chan<- touchEvent, logger *log.Logger) {
	for {
		f, err := os.Open(path)
		if err != nil {
			logger.Printf("touch: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		err = decodeTouch(f, out)
		f.Close()
		logger.Printf("touch: %v; reopening", err)
		time.Sleep(time.Second)
	}
}

func decodeTouch(r io.Reader, out chan<- touchEvent) error {
	d := newTouchDecoder()
	buf := make([]byte, inputEventSize*64)
	for {
		n, err := r.Read(buf)
		if err != nil {
			return err
		}
		for off := 0; off+inputEventSize <= n; off += inputEventSize {
			typ := binary.LittleEndian.Uint16(buf[off+16:])
			code := binary.LittleEndian.Uint16(buf[off+18:])
			val := int32(binary.LittleEndian.Uint32(buf[off+20:]))
			ev, ok := d.feed(typ, code, val)
			if !ok {
				continue
			}
			if ev.kind == touchMove {
				select {
				case out <- ev:
				default: // the UI is behind; the next move carries the position anyway
				}
				continue
			}
			select {
			case out <- ev:
			case <-time.After(time.Second):
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
