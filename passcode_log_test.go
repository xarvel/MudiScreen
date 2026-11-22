package main

import (
	"bytes"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// The keys of the passcode pad are always in the same places, so the
// coordinates of taps on them would spell the passcode: they stay out of
// the log.
func TestPasscodeTapsNotLogged(t *testing.T) {
	var logBuf bytes.Buffer
	s, clk := testScreen(t, false)
	s.log = log.New(&logBuf, "", 0)
	s.cfg.PasscodeOn, s.cfg.Passcode = true, "2580"
	s.lock()
	s.unlock()
	if s.page != "pin" {
		t.Fatalf("setup: page %q", s.page)
	}
	s.update()

	// The pad as drawPIN lays it out.
	const kw, kh, gx, gy = 64, 42, 12, 6
	kx, ky := (W-3*kw-2*gx)/2, 118
	center := func(k string) (int, int) {
		for r, row := range pinKeys {
			for c, key := range row {
				if key == k {
					return kx + c*(kw+gx) + kw/2, ky + r*(kh+gy) + kh/2
				}
			}
		}
		t.Fatalf("no key %q", k)
		return 0, 0
	}
	for _, d := range s.cfg.Passcode {
		x, y := center(string(d))
		s.onTouch(clk.ev(touchDown, x, y))
		clk.add(40 * time.Millisecond)
		s.onTouch(clk.ev(touchUp, x, y))
		clk.add(100 * time.Millisecond) // the pressed flash, then the key
		s.update()
	}
	if s.page != "home" {
		t.Fatalf("setup: the passcode did not unlock: page %q", s.page)
	}

	// What anyone who reads the log gets back.
	keyAt := func(x, y int) string {
		for r, row := range pinKeys {
			for c, key := range row {
				x0, y0 := kx+c*(kw+gx), ky+r*(kh+gy)
				if x >= x0 && x < x0+kw && y >= y0 && y < y0+kh {
					return key
				}
			}
		}
		return "?"
	}
	got := ""
	for _, m := range regexp.MustCompile(`tap (\d+),(\d+)`).FindAllStringSubmatch(logBuf.String(), -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		got += keyAt(x, y)
	}
	if got == s.cfg.Passcode {
		t.Fatalf("the passcode can be read back from the log: %q\nlog:\n%s", got, fmt.Sprint(logBuf.String()))
	}
}
