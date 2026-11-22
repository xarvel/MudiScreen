package main

// Named icons for apps: drawIcon(name, ...). Unknown names draw a dot, so
// an app written for a newer screen still shows something.
//
//	shield shield-check phone wifi wifi-0..3 signal signal-0..4 power
//	devices sms lock cross check bolt info alert globe gear chart dot

import (
	"image/color"
	"strconv"
	"strings"
)

var iconNames = []string{"shield", "shield-check", "phone", "wifi", "signal", "power", "devices", "sms",
	"lock", "cross", "check", "bolt", "info", "alert", "globe", "gear", "chart", "dot"}

// drawIcon draws the icon centred at (cx, cy) in a box of size s; bg is the
// colour behind it (for cut-outs).
func (c *Canvas) drawIcon(name string, cx, cy, s float32, col, bg color.RGBA) {
	x, y := cx-s/2, cy-s/2
	level := -1
	if i := strings.LastIndexByte(name, '-'); i > 0 {
		if n, err := strconv.Atoi(name[i+1:]); err == nil {
			name, level = name[:i], n
		}
	}
	switch name {
	case "shield":
		c.iconShield(x+s*0.1, y, s*0.8, s, col, false, bg)
	case "shield-check":
		c.iconShield(x+s*0.1, y, s*0.8, s, col, true, bg)
	case "phone":
		c.iconPhone(x, y, s, col)
	case "wifi":
		if level < 0 {
			level = 3
		}
		c.iconWifi(x, y, s, min(level, 3), col, mix(col, bg, 0.3))
	case "signal":
		if level < 0 {
			level = 4
		}
		c.iconBars(x, y+s*0.1, s, s*0.8, min(level, 4), col, mix(col, bg, 0.3))
	case "power":
		c.iconPower(x, y, s, col)
	case "devices":
		c.iconDevices(x, y, s, col, bg)
	case "sms", "message":
		c.iconSMS(x, y, s, col)
	case "lock":
		c.iconLock(x+s*0.2, y, s*0.6, s, col)
	case "cross", "error":
		c.iconCross(x+s*0.1, y+s*0.1, s*0.8, col)
	case "check":
		c.iconCheck(x, y, s, col)
	case "bolt":
		c.iconBolt(x+s*0.2, y, s*0.6, s, col)
	case "info":
		c.Ring(cx, cy, s*0.48, s*0.1, col)
		c.RoundRect(cx-s*0.06, cy-s*0.06, s*0.12, s*0.32, s*0.06, col)
		c.Circle(cx, cy-s*0.2, s*0.07, col)
	case "alert", "warning":
		var p Path
		p.MoveTo(cx, y+s*0.04)
		p.LineTo(x+s*0.98, y+s*0.92)
		p.LineTo(x+s*0.02, y+s*0.92)
		p.Close()
		c.Fill(&p, col)
		c.RoundRect(cx-s*0.055, y+s*0.33, s*0.11, s*0.33, s*0.05, bg)
		c.Circle(cx, y+s*0.78, s*0.065, bg)
	case "globe":
		c.Ring(cx, cy, s*0.48, s*0.09, col)
		c.RoundRectStroke(cx-s*0.2, y+s*0.02, s*0.4, s*0.96, s*0.2, s*0.08, col)
		c.RoundRect(x+s*0.04, cy-s*0.04, s*0.92, s*0.08, s*0.04, col)
	case "chart", "traffic":
		t := s * 0.13
		pts := [][2]float32{{0.08, 0.72}, {0.34, 0.40}, {0.56, 0.58}, {0.92, 0.14}}
		var p Path
		for i := 1; i < len(pts); i++ {
			p.Segment(x+s*pts[i-1][0], y+s*pts[i-1][1], x+s*pts[i][0], y+s*pts[i][1], t)
		}
		c.Fill(&p, col)
		for _, pt := range pts {
			c.Circle(x+s*pt[0], y+s*pt[1], t/2, col)
		}
		c.RoundRect(x+s*0.04, y+s*0.86, s*0.92, t, t/2, col)
	case "gear":
		for i := 0; i < 8; i++ {
			var p Path
			a := float32(i) * 45
			p.ArcBand(cx, cy, s*0.3, s*0.5, a-11, a+11)
			c.Fill(&p, col)
		}
		c.Ring(cx, cy, s*0.36, s*0.14, col)
	default:
		c.Circle(cx, cy, s*0.25, col)
	}
}

// iconCheck draws a tick.
func (c *Canvas) iconCheck(x, y, s float32, col color.Color) {
	t := s * 0.14
	var p Path
	p.Segment(x+s*0.12, y+s*0.52, x+s*0.40, y+s*0.78, t)
	c.Fill(&p, col)
	var q Path
	q.Segment(x+s*0.38, y+s*0.79, x+s*0.88, y+s*0.24, t)
	c.Fill(&q, col)
	c.Circle(x+s*0.39, y+s*0.785, t/2, col)
}
