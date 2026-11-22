package main

// Motion: pages are drawn once into layers (a fixed bar on top, content of
// any height below it) and every frame is put together from them, so a
// frame costs a few row copies however busy the page is. On top of that:
// scrolling that follows the finger, with momentum and rubber-band edges;
// pages that slide in and out, also under the finger (swipe right to go
// back, swipe the lock screen up); dialogs and messages that fade and rise;
// a pressed state for whatever the finger is on; and a backlight that fades
// in and out.

import (
	"image"
	"image/color"
	"math"
	"time"
)

const (
	tapSlop     = 10 // px a finger may move and still tap
	frameEvery  = 16 * time.Millisecond
	pushDur     = 280 * time.Millisecond
	modalInDur  = 200 * time.Millisecond
	modalOutDur = 150 * time.Millisecond
	toastDur    = 200 * time.Millisecond
	flingTau    = 0.33 // s: momentum decays by e in this time
	tapFlash    = 70 * time.Millisecond
)

type hit struct {
	r      image.Rectangle
	fn     func()
	flash  bool // show the pressed state
	radius int
}

// scene is a page drawn into layers.
type scene struct {
	key        string
	kind       string // lock, pin, home, app
	rt         route
	chrome     *Canvas // the fixed bar on top, or nil
	chromeH    int
	content    *Canvas
	contentH   int
	hits       []hit // content coordinates
	chromeHits []hit // screen coordinates
	scroll     float64
	maxScroll  int
	gen        int
}

func (sc *scene) row(r int) []uint8 {
	if r < sc.chromeH {
		st := sc.chrome.img.Stride
		return sc.chrome.img.Pix[r*st : r*st+W*4]
	}
	cr := r - sc.chromeH + int(math.Round(sc.scroll))
	if cr < 0 || cr >= sc.contentH {
		return nil
	}
	st := sc.content.img.Stride
	return sc.content.img.Pix[cr*st : cr*st+W*4]
}

var bgRow = func() []uint8 {
	b := make([]uint8, W*4)
	for i := 0; i < W; i++ {
		b[i*4], b[i*4+1], b[i*4+2], b[i*4+3] = colBG.R, colBG.G, colBG.B, 255
	}
	return b
}()

// composeScene draws a scene shifted by (dx, dy); what a shift leaves
// uncovered keeps its pixels.
func composeScene(dst *image.RGBA, sc *scene, dx, dy int) {
	if dx <= -W || dx >= W {
		return
	}
	for y := 0; y < H; y++ {
		r := y - dy
		if r < 0 || r >= H {
			continue
		}
		src := sc.row(r)
		if src == nil {
			src = bgRow
		}
		d := dst.Pix[y*dst.Stride : y*dst.Stride+W*4]
		switch {
		case dx == 0:
			copy(d, src)
		case dx > 0:
			copy(d[dx*4:], src[:(W-dx)*4])
		default:
			copy(d, src[-dx*4:])
		}
	}
}

// dimCols darkens columns [x0, x1) by k (0..1).
func dimCols(dst *image.RGBA, x0, x1 int, k float64) {
	x0, x1 = max(x0, 0), min(x1, W)
	if x1 <= x0 || k <= 0 {
		return
	}
	m := uint32((1 - k) * 256)
	for y := 0; y < H; y++ {
		p := dst.Pix[y*dst.Stride+x0*4 : y*dst.Stride+x1*4]
		for i := 0; i+3 < len(p); i += 4 {
			p[i] = uint8(uint32(p[i]) * m >> 8)
			p[i+1] = uint8(uint32(p[i+1]) * m >> 8)
			p[i+2] = uint8(uint32(p[i+2]) * m >> 8)
		}
	}
}

// edgeShadow shades a few columns left of x, under a page that slides.
func edgeShadow(dst *image.RGBA, x int, k float64) {
	for i := 1; i <= 8; i++ {
		c := x - i
		if c < 0 || c >= W {
			continue
		}
		dimCols(dst, c, c+1, k*(1-float64(i)/9))
	}
}

// blendLayer draws rect r of a premultiplied layer onto dst, moved down by
// dy, with an overall opacity a (0..1).
func blendLayer(dst *image.RGBA, layer *image.RGBA, r image.Rectangle, dy int, a float64) {
	r = r.Intersect(layer.Bounds())
	ga := uint32(math.Round(a * 256))
	if ga == 0 {
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		ty := y + dy
		if ty < 0 || ty >= dst.Rect.Dy() {
			continue
		}
		s := layer.Pix[y*layer.Stride+r.Min.X*4 : y*layer.Stride+r.Max.X*4]
		d := dst.Pix[ty*dst.Stride+r.Min.X*4 : ty*dst.Stride+r.Max.X*4]
		for i := 0; i+3 < len(s); i += 4 {
			sa := uint32(s[i+3]) * ga >> 8
			if sa == 0 {
				continue
			}
			inv := 256 - sa
			d[i] = uint8((uint32(s[i])*ga + uint32(d[i])*inv) >> 8)
			d[i+1] = uint8((uint32(s[i+1])*ga + uint32(d[i+1])*inv) >> 8)
			d[i+2] = uint8((uint32(s[i+2])*ga + uint32(d[i+2])*inv) >> 8)
			d[i+3] = 255
		}
	}
}

func easeOutCubic(t float64) float64 { return 1 - math.Pow(1-t, 3) }

func clamp01(v float64) float64 { return min(max(v, 0), 1) }

// fader animates a value between 0 and 1.
type fader struct {
	v, from, to float64
	start       time.Time
	dur         time.Duration
	active      bool
}

func (f *fader) set(to float64, now time.Time, dur time.Duration) {
	f.from, f.to, f.start, f.dur = f.v, to, now, dur
	f.active = f.v != to
}

func (f *fader) step(now time.Time) bool {
	if !f.active {
		return false
	}
	t := float64(now.Sub(f.start)) / float64(f.dur)
	if t >= 1 {
		f.v, f.active = f.to, false
	} else {
		f.v = f.from + (f.to-f.from)*easeOutCubic(t)
	}
	return true
}

// transition moves one page off and another on.
type transition struct {
	kind     string // push: to comes from the right; pop: from leaves to the right; up: from leaves upwards
	from, to *scene
	drag     bool // the finger sets e
	e        float64
	e0, e1   float64
	start    time.Time
	dur      time.Duration
	done     func()
}

func (tr *transition) animate(now time.Time, to float64, done func()) {
	tr.drag = false
	tr.e0, tr.e1, tr.start, tr.done = tr.e, to, now, done
	tr.dur = time.Duration(float64(pushDur)*math.Abs(to-tr.e)) + 60*time.Millisecond
}

// step advances the transition; finished is true at its end.
func (tr *transition) step(now time.Time) (finished bool) {
	if tr.drag {
		return false
	}
	t := float64(now.Sub(tr.start)) / float64(tr.dur)
	if t >= 1 {
		tr.e = tr.e1
		return true
	}
	tr.e = tr.e0 + (tr.e1-tr.e0)*easeOutCubic(t)
	return false
}

func (tr *transition) compose(dst *image.RGBA) {
	e := tr.e
	switch tr.kind {
	case "push":
		xTo := int(math.Round(W * (1 - e)))
		composeScene(dst, tr.from, -int(math.Round(0.3*W*e)), 0)
		dimCols(dst, 0, xTo, 0.35*e)
		edgeShadow(dst, xTo, 0.4)
		composeScene(dst, tr.to, xTo, 0)
	case "pop":
		xFrom := int(math.Round(W * e))
		composeScene(dst, tr.to, -int(math.Round(0.3*W*(1-e))), 0)
		dimCols(dst, 0, xFrom, 0.35*(1-e))
		edgeShadow(dst, xFrom, 0.4)
		composeScene(dst, tr.from, xFrom, 0)
	case "up":
		composeScene(dst, tr.to, 0, 0)
		dimCols(dst, 0, W, 0.5*(1-e))
		composeScene(dst, tr.from, 0, -int(math.Round(H*e)))
	}
}

// ---- the finger ----

type dragMode int

const (
	dragUndecided dragMode = iota
	dragIgnore
	dragScroll
	dragBack
	dragUnlock
	dragKeys // a finger on the on-screen keyboard
)

type touchSample struct {
	t    time.Time
	x, y int
}

type touchTrack struct {
	mode      dragMode
	x0, y0    int // where the finger went down
	ax, ay    int // where the drag was anchored
	scroll0   float64
	hit       *hit
	hitRect   image.Rectangle // on screen
	modalHit  bool
	samples   []touchSample
	stopFling bool        // the touch stopped a moving page: not a tap
	trans     *transition // the transition the finger moves (dragBack, dragUnlock)
}

func (t *touchTrack) add(ev touchEvent) {
	t.samples = append(t.samples, touchSample{ev.t, ev.x, ev.y})
	cut := 0
	for cut < len(t.samples)-1 && ev.t.Sub(t.samples[cut].t) > 100*time.Millisecond {
		cut++
	}
	t.samples = t.samples[cut:]
}

// velocity of the finger in px/s over the last 100 ms.
func (t *touchTrack) velocity() (vx, vy float64) {
	if len(t.samples) < 2 {
		return 0, 0
	}
	a, b := t.samples[0], t.samples[len(t.samples)-1]
	dt := b.t.Sub(a.t).Seconds()
	if dt < 0.008 {
		return 0, 0
	}
	return float64(b.x-a.x) / dt, float64(b.y-a.y) / dt
}

// rubber is how far content moves when pulled d px past its end.
func rubber(d, dim float64) float64 { return (1 - 1/(d*0.55/dim+1)) * dim }

type scrollAnim struct {
	from, to float64
	start    time.Time
	dur      time.Duration
}

type pendingTap struct {
	fn func()
	at time.Time
}

type blFade struct {
	from, to int
	start    time.Time
	dur      time.Duration
	then     func()
}

type frameStats struct {
	start          time.Time
	frames         int
	total, longest time.Duration
}

// lighten draws the pressed state over a rectangle of the frame.
func lighten(dst *image.RGBA, r image.Rectangle, radius int) {
	r = r.Intersect(dst.Bounds())
	if r.Empty() {
		return
	}
	c := &Canvas{img: dst, z: pressRaster}
	c.RoundRect(float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), float32(radius), color.RGBA{34, 34, 34, 34})
}

var pressRaster = NewCanvasSize(1, 1).z
