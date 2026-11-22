package main

import (
	"fmt"
	"testing"
	"time"
)

// While a finger drags a page back, or the lock screen up, something else
// may move the page: ubus show, a deleted message going back. The finger
// then lets go of the transition it no longer owns, and nothing crashes.
func TestNavigationDuringDrag(t *testing.T) {
	cases := []struct {
		name  string
		setup func(s *Screen)
		x0    int
		y0    int
		dx    int
		dy    int
		mode  dragMode
		call  func(s *Screen)
	}{
		{
			name:  "back swipe + ubus show",
			setup: func(s *Screen) { s.jump("app", []route{{"system", "net"}}) },
			x0:    30, y0: 200, dx: 40, dy: 2, mode: dragBack,
			call: func(s *Screen) { callUbus(s, "show", map[string]any{"app": "system", "page": "settings"}) },
		},
		{
			name:  "lock screen swipe up + ubus show",
			setup: func(s *Screen) { s.lock() },
			x0:    120, y0: 280, dx: 2, dy: -40, mode: dragUnlock,
			call: func(s *Screen) { callUbus(s, "show", map[string]any{"app": "system", "page": "net"}) },
		},
		{
			name:  "back swipe on a message + delete done",
			setup: func(s *Screen) { s.jump("app", []route{{"system", "sms"}, {"system", "sms:GMS1.B"}}) },
			x0:    30, y0: 200, dx: 40, dy: 2, mode: dragBack,
			call: func(s *Screen) { s.onSMS(smsResult{action: "delete", name: "GMS1.B"}) },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, clk := testScreen(t, true)
			c.setup(s)
			s.update()
			s.onTouch(clk.ev(touchDown, c.x0, c.y0))
			clk.add(16 * time.Millisecond)
			s.onTouch(clk.ev(touchMove, c.x0+c.dx, c.y0+c.dy))
			if s.track == nil || s.track.mode != c.mode || s.trans == nil {
				t.Fatalf("drag did not start: track %+v trans %v", s.track, s.trans)
			}
			c.call(s)
			clk.add(400 * time.Millisecond) // the animated transition ends
			s.update()
			p := catchPanic(func() {
				clk.add(16 * time.Millisecond)
				s.onTouch(clk.ev(touchMove, c.x0+c.dx*2, c.y0+c.dy*2))
				clk.add(16 * time.Millisecond)
				s.onTouch(clk.ev(touchUp, c.x0+c.dx*2, c.y0+c.dy*2))
				s.update()
			})
			if p != nil {
				t.Fatalf("the finger that keeps moving crashes the screen: panic: %v", fmt.Sprint(p))
			}
		})
	}
}
