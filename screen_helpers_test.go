package main

// Helpers for tests that drive the screen: a screen that lays pages out
// with real fonts, a clock the test moves, and short cuts for touches and
// ubus calls.

import (
	"io"
	"log"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/opentype"
)

// testFonts are the router's fonts if dev/fonts has a copy of them, or else
// the Go fonts: the tests need metrics, not the exact glyphs.
func testFonts(t *testing.T) *Fonts {
	t.Helper()
	if f, err := LoadFonts("dev/fonts"); err == nil {
		return f
	}
	f := &Fonts{fonts: map[weight]*opentype.Font{}, faces: map[faceKey]font.Face{}}
	for w, ttf := range map[weight][]byte{Medium: gomedium.TTF, Semibold: gomedium.TTF, Bold: gobold.TTF} {
		ft, err := opentype.Parse(ttf)
		if err != nil {
			t.Fatal(err)
		}
		f.fonts[w] = ft
	}
	return f
}

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time      { return c.t }
func (c *testClock) add(d time.Duration) { c.t = c.t.Add(d) }
func (c *testClock) ev(k touchKind, x, y int) touchEvent {
	return touchEvent{kind: k, x: x, y: y, t: c.t}
}

// testScreen is a screen that is on, unlocked on the home page, without a
// framebuffer; anim makes navigate animate as it does with a framebuffer.
func testScreen(t *testing.T, anim bool) (*Screen, *testClock) {
	t.Helper()
	fonts := testFonts(t)
	o := options{}
	if anim {
		o.anim = "push" // navigate() animates only with a framebuffer or in an -anim preview
	}
	s := newScreen(o, fonts, log.New(io.Discard, "", 0))
	clk := &testClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	s.now = clk.now
	s.loc = time.UTC
	s.webLangFn = func() string { return "en" }
	s.screenOn = true
	s.lastActivity = clk.t
	s.jump("home", nil)
	s.update()
	return s, clk
}

func callUbus(s *Screen, method string, data map[string]any) map[string]any {
	reply := make(chan map[string]any, 1)
	s.onUbus(ubusCall{object: "mudi-screen", method: method, data: data, raw: "(test)", reply: reply})
	return <-reply
}

// catchPanic runs f and returns the panic, if any.
func catchPanic(f func()) (p any) {
	defer func() { p = recover() }()
	f()
	return nil
}
