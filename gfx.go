package main

// Drawing: an RGBA canvas the size of the panel, anti-aliased vector shapes
// (golang.org/x/image/vector) and text (opentype faces from fonts.go).

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

const (
	W = 240
	H = 320
)

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

// mix blends a over b: t = 0 gives b, t = 1 gives a.
func mix(a, b color.RGBA, t float64) color.RGBA {
	f := func(x, y uint8) uint8 { return uint8(float64(x)*t + float64(y)*(1-t) + 0.5) }
	return color.RGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 255}
}

type Canvas struct {
	img  *image.RGBA
	z    *vector.Rasterizer
	mask *image.Alpha
}

func NewCanvas() *Canvas { return NewCanvasSize(W, H) }

func NewCanvasSize(w, h int) *Canvas {
	return &Canvas{img: image.NewRGBA(image.Rect(0, 0, w, h)), z: vector.NewRasterizer(1, 1)}
}

// Resize gives the canvas a new height, keeping its memory when it fits.
func (c *Canvas) Resize(h int) {
	w := c.img.Rect.Dx()
	if n := w * h * 4; n <= cap(c.img.Pix) {
		c.img.Pix = c.img.Pix[:n]
		c.img.Rect = image.Rect(0, 0, w, h)
		return
	}
	c.img = image.NewRGBA(image.Rect(0, 0, w, h))
}

func (c *Canvas) Clear(col color.RGBA) {
	draw.Draw(c.img, c.img.Bounds(), &image.Uniform{col}, image.Point{}, draw.Src)
}

func (c *Canvas) FillRect(x, y, w, h int, col color.Color) {
	r := image.Rect(x, y, x+w, y+h).Intersect(c.img.Bounds())
	draw.Draw(c.img, r, &image.Uniform{col}, image.Point{}, draw.Over)
}

// Path collects a vector path in canvas coordinates. Every sub-path has to
// be closed; a sub-path in the opposite direction cuts a hole.
type Path struct {
	ops                    []pathOp
	minX, minY, maxX, maxY float32
	n                      int
	fx, fy                 float32 // start of the current sub-path
}

type pathOp struct {
	kind byte // 'M', 'L', 'Q', 'C'
	p    [6]float32
}

func (p *Path) see(x, y float32) {
	if p.n == 0 {
		p.minX, p.maxX, p.minY, p.maxY = x, x, y, y
	}
	p.n++
	p.minX = min(p.minX, x)
	p.maxX = max(p.maxX, x)
	p.minY = min(p.minY, y)
	p.maxY = max(p.maxY, y)
}

func (p *Path) MoveTo(x, y float32) {
	p.see(x, y)
	p.fx, p.fy = x, y
	p.ops = append(p.ops, pathOp{kind: 'M', p: [6]float32{x, y}})
}

func (p *Path) LineTo(x, y float32) {
	p.see(x, y)
	p.ops = append(p.ops, pathOp{kind: 'L', p: [6]float32{x, y}})
}

func (p *Path) QuadTo(cx, cy, x, y float32) {
	p.see(cx, cy)
	p.see(x, y)
	p.ops = append(p.ops, pathOp{kind: 'Q', p: [6]float32{cx, cy, x, y}})
}

func (p *Path) CubeTo(c1x, c1y, c2x, c2y, x, y float32) {
	p.see(c1x, c1y)
	p.see(c2x, c2y)
	p.see(x, y)
	p.ops = append(p.ops, pathOp{kind: 'C', p: [6]float32{c1x, c1y, c2x, c2y, x, y}})
}

func (p *Path) Close() { p.LineTo(p.fx, p.fy) }

// RoundRect adds a rounded rectangle, clockwise, or counter-clockwise when
// ccw is set (a hole in a shape drawn clockwise).
func (p *Path) RoundRect(x, y, w, h, r float32, ccw bool) {
	r = min(r, w/2, h/2)
	const k = 0.5523 // cubic Bézier approximation of a quarter circle
	kr := k * r
	if !ccw {
		p.MoveTo(x+r, y)
		p.LineTo(x+w-r, y)
		p.CubeTo(x+w-r+kr, y, x+w, y+r-kr, x+w, y+r)
		p.LineTo(x+w, y+h-r)
		p.CubeTo(x+w, y+h-r+kr, x+w-r+kr, y+h, x+w-r, y+h)
		p.LineTo(x+r, y+h)
		p.CubeTo(x+r-kr, y+h, x, y+h-r+kr, x, y+h-r)
		p.LineTo(x, y+r)
		p.CubeTo(x, y+r-kr, x+r-kr, y, x+r, y)
	} else {
		p.MoveTo(x+r, y)
		p.CubeTo(x+r-kr, y, x, y+r-kr, x, y+r)
		p.LineTo(x, y+h-r)
		p.CubeTo(x, y+h-r+kr, x+r-kr, y+h, x+r, y+h)
		p.LineTo(x+w-r, y+h)
		p.CubeTo(x+w-r+kr, y+h, x+w, y+h-r+kr, x+w, y+h-r)
		p.LineTo(x+w, y+r)
		p.CubeTo(x+w, y+r-kr, x+w-r+kr, y, x+w-r, y)
	}
	p.Close()
}

func (p *Path) Circle(cx, cy, r float32, ccw bool) {
	p.RoundRect(cx-r, cy-r, 2*r, 2*r, r, ccw)
}

// arcPoints appends points along an arc; angles in degrees, 0 = east,
// 90 = south (screen coordinates).
func (p *Path) arcPoints(cx, cy, r, a0, a1 float32, first bool) {
	n := int(math.Ceil(float64(r*abs32(a1-a0)) / 90))
	n = max(n, 6)
	for i := 0; i <= n; i++ {
		a := float64(a0+(a1-a0)*float32(i)/float32(n)) * math.Pi / 180
		x := cx + r*float32(math.Cos(a))
		y := cy + r*float32(math.Sin(a))
		if i == 0 && first {
			p.MoveTo(x, y)
		} else {
			p.LineTo(x, y)
		}
	}
}

// ArcBand adds a ring sector between radii r0 < r1 from angle a0 to a1.
func (p *Path) ArcBand(cx, cy, r0, r1, a0, a1 float32) {
	p.arcPoints(cx, cy, r1, a0, a1, true)
	p.arcPoints(cx, cy, r0, a1, a0, false)
	p.Close()
}

// Segment adds a thick line from (x0, y0) to (x1, y1).
func (p *Path) Segment(x0, y0, x1, y1, width float32) {
	dx, dy := x1-x0, y1-y0
	l := float32(math.Hypot(float64(dx), float64(dy)))
	if l == 0 {
		return
	}
	nx, ny := -dy/l*width/2, dx/l*width/2
	p.MoveTo(x0+nx, y0+ny)
	p.LineTo(x1+nx, y1+ny)
	p.LineTo(x1-nx, y1-ny)
	p.LineTo(x0-nx, y0-ny)
	p.Close()
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// Fill draws the path with anti-aliased edges.
func (c *Canvas) Fill(p *Path, col color.Color) {
	if p.n == 0 {
		return
	}
	x0 := int(math.Floor(float64(p.minX)))
	y0 := int(math.Floor(float64(p.minY)))
	x1 := int(math.Ceil(float64(p.maxX)))
	y1 := int(math.Ceil(float64(p.maxY)))
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 {
		return
	}
	ox, oy := float32(x0), float32(y0)
	c.z.Reset(w, h)
	c.z.DrawOp = draw.Src
	for _, op := range p.ops {
		q := op.p
		switch op.kind {
		case 'M':
			c.z.MoveTo(q[0]-ox, q[1]-oy)
		case 'L':
			c.z.LineTo(q[0]-ox, q[1]-oy)
		case 'Q':
			c.z.QuadTo(q[0]-ox, q[1]-oy, q[2]-ox, q[3]-oy)
		case 'C':
			c.z.CubeTo(q[0]-ox, q[1]-oy, q[2]-ox, q[3]-oy, q[4]-ox, q[5]-oy)
		}
	}
	if c.mask == nil || len(c.mask.Pix) < w*h {
		c.mask = image.NewAlpha(image.Rect(0, 0, w, h))
	}
	m := &image.Alpha{Pix: c.mask.Pix[:w*h], Stride: w, Rect: image.Rect(0, 0, w, h)}
	c.z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	draw.DrawMask(c.img, image.Rect(x0, y0, x1, y1), &image.Uniform{col}, image.Point{}, m, image.Point{}, draw.Over)
}

type rrKey struct{ w, h, r int }

// rrMasks keeps the masks of rounded rectangles: a page draws the same few
// sizes again and again. Used from the UI goroutine only.
var rrMasks = map[rrKey]*image.Alpha{}

func isWhole(v float32) bool { return v == float32(int(v)) }

func (c *Canvas) RoundRect(x, y, w, h, r float32, col color.Color) {
	if isWhole(x) && isWhole(y) && isWhole(w) && isWhole(h) && isWhole(r) && w > 0 && h > 0 && w*h <= 240*360 {
		k := rrKey{int(w), int(h), int(r)}
		m := rrMasks[k]
		if m == nil {
			var p Path
			p.RoundRect(0, 0, w, h, r, false)
			m = image.NewAlpha(image.Rect(0, 0, k.w, k.h))
			c.z.Reset(k.w, k.h)
			c.z.DrawOp = draw.Src
			for _, op := range p.ops {
				q := op.p
				switch op.kind {
				case 'M':
					c.z.MoveTo(q[0], q[1])
				case 'L':
					c.z.LineTo(q[0], q[1])
				case 'C':
					c.z.CubeTo(q[0], q[1], q[2], q[3], q[4], q[5])
				}
			}
			c.z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
			if len(rrMasks) < 600 {
				rrMasks[k] = m
			}
		}
		draw.DrawMask(c.img, image.Rect(int(x), int(y), int(x)+k.w, int(y)+k.h), &image.Uniform{col}, image.Point{}, m, image.Point{}, draw.Over)
		return
	}
	var p Path
	p.RoundRect(x, y, w, h, r, false)
	c.Fill(&p, col)
}

// RoundRectStroke draws the outline of a rounded rectangle.
func (c *Canvas) RoundRectStroke(x, y, w, h, r, t float32, col color.Color) {
	var p Path
	p.RoundRect(x, y, w, h, r, false)
	p.RoundRect(x+t, y+t, w-2*t, h-2*t, max(r-t, 0), true)
	c.Fill(&p, col)
}

func (c *Canvas) Circle(cx, cy, r float32, col color.Color) {
	var p Path
	p.Circle(cx, cy, r, false)
	c.Fill(&p, col)
}

func (c *Canvas) Ring(cx, cy, r, t float32, col color.Color) {
	var p Path
	p.Circle(cx, cy, r, false)
	p.Circle(cx, cy, r-t, true)
	c.Fill(&p, col)
}

// Overlay darkens the whole canvas.
func (c *Canvas) Overlay(alpha uint8) {
	b := c.img.Bounds()
	c.FillRect(0, 0, b.Dx(), b.Dy(), color.RGBA{0, 0, 0, alpha})
}

// ---- text ----

// Text draws s with its line box starting at (x, y) and returns the
// advance width.
func (c *Canvas) Text(f font.Face, x, y int, col color.Color, s string) int {
	m := f.Metrics()
	d := font.Drawer{Dst: c.img, Src: &image.Uniform{col}, Face: f, Dot: fixed.P(x, y+m.Ascent.Ceil())}
	d.DrawString(s)
	return (d.Dot.X - fixed.I(x)).Ceil()
}

func textWidth(f font.Face, s string) int { return font.MeasureString(f, s).Ceil() }

func lineHeight(f font.Face) int {
	m := f.Metrics()
	return m.Ascent.Ceil() + m.Descent.Ceil()
}

// fit shortens s with an ellipsis until it fits into width pixels.
// cutRunes keeps the first n runes of s. No glyph is narrower than a
// pixel, so a text cut to its width in runes still fills the width, and a
// long text costs no more to fit than a short one.
func cutRunes(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	k := 0
	for i := range s {
		if k == n {
			return s[:i], true
		}
		k++
	}
	return s, false
}

func fit(f font.Face, s string, width int) string {
	s, cut := cutRunes(s, max(width, 1))
	if !cut && textWidth(f, s) <= width {
		return s
	}
	runes := []rune(s)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if textWidth(f, string(runes[:mid])+"…") <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(runes[:lo]) + "…"
}

// TextIn draws s inside the box (x, y, w, h): align 0 = left, 1 = center,
// 2 = right; vertically centered. Too long text is shortened.
func (c *Canvas) TextIn(f font.Face, x, y, w, h, align int, col color.Color, s string) {
	s = fit(f, s, w)
	tw := textWidth(f, s)
	tx := x
	switch align {
	case 1:
		tx = x + (w-tw)/2
	case 2:
		tx = x + w - tw
	}
	m := f.Metrics()
	// Center the cap height rather than ascent+descent: Cyrillic and Latin
	// capitals then sit visually in the middle of buttons.
	capH := capHeight(f)
	baseline := y + (h+capH)/2
	d := font.Drawer{Dst: c.img, Src: &image.Uniform{col}, Face: f, Dot: fixed.P(tx, baseline)}
	_ = m
	d.DrawString(s)
}

func capHeight(f font.Face) int {
	if b, _, ok := f.GlyphBounds('H'); ok {
		return (-b.Min.Y).Ceil()
	}
	return f.Metrics().Ascent.Ceil() * 7 / 10
}

func runeCount(s string) int { return utf8.RuneCountInString(s) }

// ---- icons (all sized by the box they are given) ----

// iconBars draws n of 4 signal bars in a box of w×h at (x, y).
func (c *Canvas) iconBars(x, y, w, h float32, n int, on, off color.Color) {
	bw := w / 5.5
	gap := (w - 4*bw) / 3
	for i := 0; i < 4; i++ {
		bh := h * float32(i+1) / 4
		col := off
		if i < n {
			col = on
		}
		c.RoundRect(x+float32(i)*(bw+gap), y+h-bh, bw, bh, bw/3, col)
	}
}

// iconBattery draws a battery w×h (nub included) filled to pct.
func (c *Canvas) iconBattery(x, y, w, h float32, pct int, body, fill color.Color) {
	nub := max(w/12, 2)
	bw := w - nub - 1
	c.RoundRectStroke(x, y, bw, h, h/4, 1.4, body)
	c.RoundRect(x+bw+0.5, y+h*0.3, nub, h*0.4, nub/2, body)
	inner := bw - 4.6
	fw := inner * float32(max(min(pct, 100), 0)) / 100
	if fw > 0.5 {
		c.RoundRect(x+2.3, y+2.3, fw, h-4.6, (h-4.6)/4, fill)
	}
}

// iconBolt draws a lightning bolt in a box w×h.
func (c *Canvas) iconBolt(x, y, w, h float32, col color.Color) {
	var p Path
	p.MoveTo(x+w*0.62, y)
	p.LineTo(x+w*0.08, y+h*0.58)
	p.LineTo(x+w*0.47, y+h*0.58)
	p.LineTo(x+w*0.36, y+h)
	p.LineTo(x+w*0.92, y+h*0.40)
	p.LineTo(x+w*0.53, y+h*0.40)
	p.Close()
	c.Fill(&p, col)
}

// iconWifi draws three arcs and a dot in a box of size s.
func (c *Canvas) iconWifi(x, y, s float32, bars int, on, off color.Color) {
	cx, cy := x+s/2, y+s*0.92
	t := s * 0.13
	for i := 0; i < 3; i++ {
		r := s * (0.36 + 0.29*float32(i))
		col := off
		if i < bars {
			col = on
		}
		var p Path
		p.ArcBand(cx, cy, r-t, r, -135, -45)
		c.Fill(&p, col)
	}
	col := off
	if bars > 0 {
		col = on
	}
	c.Circle(cx, cy-s*0.06, s*0.1, col)
}

// iconShield draws a shield in a box w×h; check draws a tick inside.
func (c *Canvas) iconShield(x, y, w, h float32, col color.Color, check bool, checkCol color.Color) {
	var p Path
	p.MoveTo(x+w/2, y)
	p.LineTo(x+w, y+h*0.17)
	p.LineTo(x+w, y+h*0.45)
	p.QuadTo(x+w, y+h*0.85, x+w/2, y+h)
	p.QuadTo(x, y+h*0.85, x, y+h*0.45)
	p.LineTo(x, y+h*0.17)
	p.Close()
	c.Fill(&p, col)
	if check {
		var q Path
		t := w * 0.13
		q.Segment(x+w*0.28, y+h*0.50, x+w*0.45, y+h*0.66, t)
		q.Segment(x+w*0.43, y+h*0.67, x+w*0.74, y+h*0.34, t)
		c.Fill(&q, checkCol)
		c.Circle(x+w*0.44, y+h*0.665, t/2, checkCol)
	}
}

// iconPhone draws a telephone handset in a box of size s.
func (c *Canvas) iconPhone(x, y, s float32, col color.Color) {
	cx, cy := x+s*0.62, y+s*0.38
	var p Path
	p.ArcBand(cx, cy, s*0.42, s*0.58, 100, 172)
	c.Fill(&p, col)
	// Earpiece and mouthpiece: rounded blocks at both ends of the band.
	blk := func(a float64) {
		r := float64(s) * 0.5
		bx := cx + float32(r*math.Cos(a*math.Pi/180))
		by := cy + float32(r*math.Sin(a*math.Pi/180))
		var q Path
		ang := a * math.Pi / 180
		// Block oriented along the radius.
		ux, uy := float32(math.Cos(ang)), float32(math.Sin(ang))
		vx, vy := -uy, ux
		hl, hw := s*0.17, s*0.13
		q.MoveTo(bx+ux*hl+vx*hw, by+uy*hl+vy*hw)
		q.LineTo(bx-ux*hl*0.4+vx*hw, by-uy*hl*0.4+vy*hw)
		q.LineTo(bx-ux*hl*0.4-vx*hw, by-uy*hl*0.4-vy*hw)
		q.LineTo(bx+ux*hl-vx*hw, by+uy*hl-vy*hw)
		q.Close()
		c.Fill(&q, col)
	}
	blk(92)
	blk(180)
}

// iconPower draws the power symbol in a box of size s.
func (c *Canvas) iconPower(x, y, s float32, col color.Color) {
	cx, cy := x+s/2, y+s*0.55
	t := s * 0.13
	r := s * 0.42
	var p Path
	p.ArcBand(cx, cy, r-t, r, -60, 240)
	c.Fill(&p, col)
	var q Path
	q.Segment(cx, y+s*0.02, cx, cy-s*0.05, t)
	c.Fill(&q, col)
	c.Circle(cx, y+s*0.02+t/2, t/2, col)
	c.Circle(cx, cy-s*0.05, t/2, col)
}

// iconLock draws a padlock in a box w×h.
func (c *Canvas) iconLock(x, y, w, h float32, col color.Color) {
	bodyY := y + h*0.45
	c.RoundRect(x, bodyY, w, h-h*0.45, w*0.15, col)
	var p Path
	r := w * 0.32
	t := w * 0.13
	p.ArcBand(x+w/2, bodyY, r-t, r, 180, 360)
	c.Fill(&p, col)
	var q Path
	q.MoveTo(x+w/2-r, bodyY)
	q.LineTo(x+w/2-r+t, bodyY)
	q.LineTo(x+w/2-r+t, bodyY+1)
	q.LineTo(x+w/2-r, bodyY+1)
	q.Close()
	c.Fill(&q, col)
}

// iconDevices draws a laptop with a phone in front of it.
func (c *Canvas) iconDevices(x, y, s float32, col, bg color.Color) {
	c.RoundRectStroke(x+s*0.08, y+s*0.16, s*0.72, s*0.5, s*0.06, s*0.09, col)
	c.RoundRect(x, y+s*0.68, s*0.88, s*0.1, s*0.04, col)
	c.RoundRect(x+s*0.58, y+s*0.36, s*0.4, s*0.6, s*0.08, bg)
	c.RoundRectStroke(x+s*0.62, y+s*0.40, s*0.34, s*0.54, s*0.07, s*0.08, col)
}

// iconSMS draws a speech bubble.
func (c *Canvas) iconSMS(x, y, s float32, col color.Color) {
	c.RoundRect(x, y+s*0.1, s, s*0.66, s*0.2, col)
	var p Path
	p.MoveTo(x+s*0.22, y+s*0.7)
	p.LineTo(x+s*0.46, y+s*0.7)
	p.LineTo(x+s*0.16, y+s*0.95)
	p.Close()
	c.Fill(&p, col)
}

// iconCross draws an ×.
func (c *Canvas) iconCross(x, y, s float32, col color.Color) {
	var p Path
	t := s * 0.16
	p.Segment(x+s*0.15, y+s*0.15, x+s*0.85, y+s*0.85, t)
	c.Fill(&p, col)
	var q Path
	q.Segment(x+s*0.85, y+s*0.15, x+s*0.15, y+s*0.85, t)
	c.Fill(&q, col)
}

// iconBackspace draws a key with an × for deleting a digit.
// iconShift draws the shift arrow in a box w×h: an outline, filled when
// shift is on.
func (c *Canvas) iconShift(x, y, w, h float32, filled bool, col color.Color) {
	shape := func(p *Path, x, y, w, h float32, ccw bool) {
		pts := [][2]float32{{0.5, 0}, {1, 0.55}, {0.72, 0.55}, {0.72, 1}, {0.28, 1}, {0.28, 0.55}, {0, 0.55}}
		if ccw {
			for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
				pts[i], pts[j] = pts[j], pts[i]
			}
		}
		for i, q := range pts {
			if i == 0 {
				p.MoveTo(x+q[0]*w, y+q[1]*h)
			} else {
				p.LineTo(x+q[0]*w, y+q[1]*h)
			}
		}
		p.Close()
	}
	var p Path
	shape(&p, x, y, w, h, false)
	if !filled {
		t := w * 0.14
		shape(&p, x+t, y+t*1.3, w-2*t, h-2*t, true)
	}
	c.Fill(&p, col)
}

func (c *Canvas) iconBackspace(x, y, w, h float32, col color.Color) {
	var p Path
	p.MoveTo(x, y+h/2)
	p.LineTo(x+w*0.3, y)
	p.LineTo(x+w, y)
	p.LineTo(x+w, y+h)
	p.LineTo(x+w*0.3, y+h)
	p.Close()
	t := h * 0.12
	p.RoundRect(x+w*0.3+t, y+t, w*0.7-2*t, h-2*t, 1, true)
	c.Fill(&p, col)
	c.iconCross(x+w*0.47, y+h*0.27, h*0.46, col)
}
