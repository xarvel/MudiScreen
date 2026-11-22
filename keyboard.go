package main

// An on-screen keyboard for the text the screen asks for: the password of a
// Wi-Fi network to join. It is a page of the built-in system app
// ("system:keyboard"): a field on top and five rows of keys below, digits on
// the first row because passwords have them. The keys are narrow (20 px),
// so a key acts when the finger leaves the glass, and the finger may slide
// to the right key first: a bubble above it shows which key it is on.
// Letters, then two layers of symbols: every printable ASCII character can
// be typed. Nothing typed is ever logged.

import (
	"image"
	"strings"
	"unicode/utf8"
)

type keyboard struct {
	title  string // the page's title
	label  string // above the field
	text   []rune
	hidden bool // the field shows dots
	layer  int  // 0 letters, 1 symbols, 2 more symbols
	shift  int  // 0 off, 1 the next letter in capitals, 2 capitals until switched off
	min    int  // Done needs at least this many characters
	max    int
	done   func(text string)

	keys  []kbdKey // where the last render put the keys, in content coordinates
	field image.Rectangle
	eye   image.Rectangle
	press int // the key under the finger, -1: none

	bubble    *Canvas // the magnified key above the finger
	bubbleBox image.Rectangle
	bubbleAt  image.Point
}

type kbdKey struct {
	r     image.Rectangle
	label string // what the key shows
	value string // what it types; "" for the keys that do something else
	fn    string // shift, back, layer0, layer1, layer2, space, done
}

const (
	kbdKeyH   = 38
	kbdGap    = 4
	kbdTop    = 64 // the first row, in content coordinates
	kbdMargin = 3
)

// kbdRows are the rows of each layer. "|" separates keys; a key in braces is
// a function key: {shift} {back} {123} {#+=} {abc} {space} {done}.
var kbdRows = [3][5]string{
	{
		"1|2|3|4|5|6|7|8|9|0",
		"q|w|e|r|t|y|u|i|o|p",
		"a|s|d|f|g|h|j|k|l",
		"{shift}|z|x|c|v|b|n|m|{back}",
		"{123}|{space}|.|{done}",
	},
	{
		"1|2|3|4|5|6|7|8|9|0",
		"-|/|:|;|(|)|$|&|@|\"",
		".|,|?|!|'|#|%|*|+",
		"{#+=}|_|~|\\|<|>|=|{back}",
		"{abc}|{space}|.|{done}",
	},
	{
		"1|2|3|4|5|6|7|8|9|0",
		"[|]|{|}|^|=|_|\\|||~",
		"<|>|`|-|/|:|;|@|\"",
		"{123}|$|&|(|)|'|*|{back}",
		"{abc}|{space}|.|{done}",
	},
}

// openKeyboard shows the keyboard; done gets the text when Done is tapped,
// and the keyboard page closes first.
func (s *Screen) openKeyboard(k *keyboard) {
	if k.max == 0 {
		k.max = 63
	}
	k.press = -1
	s.kbd = k
	s.open("system", "keyboard")
}

// closeKeyboard drops what was typed: a password stays in memory no longer
// than its page.
func (s *Screen) closeKeyboard() {
	if s.kbd != nil {
		for i := range s.kbd.text {
			s.kbd.text[i] = 0
		}
		s.kbd = nil
	}
}

func (s *Screen) onKeyboardPage() bool {
	rt := s.top()
	return s.page == "app" && rt.app == "system" && rt.page == "keyboard" && s.kbd != nil
}

// layoutKeys places the keys of the current layer.
func (k *keyboard) layoutKeys() {
	k.keys = k.keys[:0]
	full := (W - 2*kbdMargin - 9*kbdGap) / 10 // the width of a key of a full row
	for r, row := range kbdRows[k.layer] {
		parts := splitKeys(row)
		y := kbdTop + r*(kbdKeyH+kbdGap)
		// Widths: plain keys are a key wide; on the last row the space bar
		// takes what is left; on the fourth row the two function keys share
		// what the letters leave.
		widths := make([]int, len(parts))
		fixed, flex := 0, 0
		for i, p := range parts {
			switch {
			case p == "{space}":
				flex++
			case r == 3 && strings.HasPrefix(p, "{"):
				flex++
			case r == 4 && p == "{done}":
				widths[i] = full*2 + kbdGap + 14 // room for its word
				fixed += widths[i]
			case r == 4 && strings.HasPrefix(p, "{"):
				widths[i] = full*2 + kbdGap
				fixed += widths[i]
			default:
				widths[i] = full
				fixed += full
			}
		}
		total := W - 2*kbdMargin - (len(parts)-1)*kbdGap
		if flex > 0 {
			each := (total - fixed) / flex
			for i, p := range parts {
				if widths[i] == 0 && (p == "{space}" || strings.HasPrefix(p, "{")) {
					widths[i] = each
				}
			}
		}
		used := (len(parts) - 1) * kbdGap
		for _, w := range widths {
			used += w
		}
		x := (W - used) / 2
		for i, p := range parts {
			key := kbdKey{r: image.Rect(x, y, x+widths[i], y+kbdKeyH)}
			switch p {
			case "{shift}":
				key.fn = "shift"
			case "{back}":
				key.fn = "back"
			case "{123}":
				key.fn, key.label = "layer1", "123"
			case "{#+=}":
				key.fn, key.label = "layer2", "#+="
			case "{abc}":
				key.fn, key.label = "layer0", "abc"
			case "{space}":
				key.fn, key.value = "space", " "
			case "{done}":
				key.fn = "done"
			default:
				key.value, key.label = p, p
				if k.layer == 0 && k.shift > 0 {
					key.value, key.label = strings.ToUpper(p), strings.ToUpper(p)
				}
			}
			k.keys = append(k.keys, key)
			x += widths[i] + kbdGap
		}
	}
}

// splitKeys splits a row at "|", where a "|" between two others is the key
// "|" itself.
func splitKeys(row string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(row); i++ {
		c := row[i]
		if c == '|' && cur != "" {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(c)
	}
	return append(out, cur)
}

// keyAt is the key at a point in content coordinates; a finger between two
// keys gets the nearer one, so the gaps do not swallow taps.
func (k *keyboard) keyAt(x, y int) int {
	best, bestD := -1, 1<<30
	for i, key := range k.keys {
		r := key.r.Inset(-kbdGap/2 - 1)
		if !(image.Point{x, y}).In(r) {
			continue
		}
		cx, cy := (key.r.Min.X+key.r.Max.X)/2, (key.r.Min.Y+key.r.Max.Y)/2
		if d := (x-cx)*(x-cx) + (y-cy)*(y-cy); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// apply does what a key does.
func (s *Screen) kbdApply(i int) {
	k := s.kbd
	if k == nil || i < 0 || i >= len(k.keys) {
		return
	}
	key := k.keys[i]
	switch key.fn {
	case "shift":
		k.shift = (k.shift + 1) % 3
	case "back":
		if n := len(k.text); n > 0 {
			k.text[n-1] = 0
			k.text = k.text[:n-1]
		}
	case "layer0", "layer1", "layer2":
		k.layer = int(key.fn[5] - '0')
	case "done":
		if len(k.text) < k.min {
			return
		}
		text, done := string(k.text), k.done
		s.back()
		s.closeKeyboard()
		if done != nil {
			done(text)
		}
		return
	default: // a character or the space
		r, _ := utf8.DecodeRuneInString(key.value)
		if len(k.text) < k.max {
			k.text = append(k.text, r)
		}
		if k.shift == 1 && key.fn == "" {
			k.shift = 0
		}
	}
	s.invalidate()
}

// kbdTouch takes the touches on the keys; it reports false for the rest of
// the page (the header, the field), which go the usual way.
func (s *Screen) kbdTouch(ev touchEvent) bool {
	k := s.kbd
	sc := s.cur()
	cy := ev.y - sc.chromeH + int(sc.scroll)
	switch ev.kind {
	case touchDown:
		if s.modal != nil || s.trans != nil || ev.y < sc.chromeH || cy < kbdTop-kbdGap {
			return false
		}
		s.track = &touchTrack{mode: dragKeys, x0: ev.x, y0: ev.y}
		s.kbdPress(k.keyAt(ev.x, cy))
		return true
	case touchMove:
		if s.track == nil || s.track.mode != dragKeys {
			return false
		}
		s.kbdPress(k.keyAt(ev.x, cy))
		return true
	case touchUp:
		if s.track == nil || s.track.mode != dragKeys {
			return false
		}
		s.track = nil
		i := k.press
		s.kbdPress(-1)
		s.kbdApply(i)
		return true
	}
	return false
}

// kbdPress marks the key under the finger, and draws the bubble above it.
func (s *Screen) kbdPress(i int) {
	k := s.kbd
	if k == nil || i == k.press {
		return
	}
	k.press = i
	s.needFrame = true
	if i < 0 {
		s.pressed = nil
		return
	}
	key := k.keys[i]
	off := image.Pt(0, s.cur().chromeH-int(s.cur().scroll))
	s.pressed = &pressedHit{r: key.r.Add(off), radius: 6}
	if key.label == "" || key.fn != "" && key.fn != "space" {
		k.bubbleBox = image.Rectangle{}
		return
	}
	if key.fn == "space" {
		k.bubbleBox = image.Rectangle{}
		return
	}
	// The bubble: a key twice as tall, over the pressed one.
	const bw, bh = 40, 48
	if k.bubble == nil {
		k.bubble = NewCanvasSize(bw, bh)
	}
	clear(k.bubble.img.Pix)
	k.bubble.RoundRect(0, 0, bw, bh, 10, colCard2)
	k.bubble.TextIn(s.face(Semibold, 26), 0, 2, bw, bh-8, 1, colText, key.label)
	r := key.r.Add(off)
	x := min(max((r.Min.X+r.Max.X)/2-bw/2, 1), W-bw-1)
	y := r.Min.Y - bh - 2
	if y < 0 {
		y = 0
	}
	k.bubbleBox = image.Rect(0, 0, bw, bh)
	k.bubbleAt = image.Pt(x, y)
}

// composeKeyboard puts the bubble on the frame.
func (s *Screen) composeKeyboard(f *image.RGBA) {
	k := s.kbd
	if k == nil || k.press < 0 || k.bubble == nil || k.bubbleBox.Empty() || s.trans != nil || !s.onKeyboardPage() {
		return
	}
	// blendLayer draws a layer's rectangle at the same place shifted by dy;
	// the bubble's canvas is small, so it is drawn row by row here.
	b := k.bubble.img
	for y := 0; y < b.Rect.Dy(); y++ {
		ty := k.bubbleAt.Y + y
		if ty < 0 || ty >= f.Rect.Dy() {
			continue
		}
		for x := 0; x < b.Rect.Dx(); x++ {
			tx := k.bubbleAt.X + x
			if tx < 0 || tx >= f.Rect.Dx() {
				continue
			}
			si := y*b.Stride + x*4
			sa := uint32(b.Pix[si+3])
			if sa == 0 {
				continue
			}
			di := ty*f.Stride + tx*4
			inv := 255 - sa
			f.Pix[di] = uint8((uint32(b.Pix[si])*sa + uint32(f.Pix[di])*inv) / 255)
			f.Pix[di+1] = uint8((uint32(b.Pix[si+1])*sa + uint32(f.Pix[di+1])*inv) / 255)
			f.Pix[di+2] = uint8((uint32(b.Pix[si+2])*sa + uint32(f.Pix[di+2])*inv) / 255)
			f.Pix[di+3] = 255
		}
	}
}

// drawKeyboard draws the field and the keys into the page's content.
func (s *Screen) drawKeyboard() {
	k := s.kbd
	c := s.cv
	if k == nil {
		return
	}
	k.layoutKeys()
	// The label and the field.
	c.TextIn(s.face(Medium, 13), 10, 4, W-20, 18, 0, colDim, fit(s.face(Medium, 13), k.label, W-20))
	k.field = image.Rect(6, 24, W-6, 24+34)
	c.RoundRect(float32(k.field.Min.X), float32(k.field.Min.Y), float32(k.field.Dx()), float32(k.field.Dy()), 10, colCard)
	eyeW := 52
	k.eye = image.Rect(k.field.Max.X-eyeW, k.field.Min.Y, k.field.Max.X, k.field.Max.Y)
	label := s.tr("kbd_hide")
	if k.hidden {
		label = s.tr("kbd_show")
	}
	c.TextIn(s.face(Medium, 12), k.eye.Min.X, k.eye.Min.Y, eyeW-6, k.eye.Dy(), 1, colBlue, label)
	s.addHitNoFlash(k.eye.Min.X, k.eye.Min.Y, k.eye.Dx(), k.eye.Dy(), func() {
		if s.kbd != nil {
			s.kbd.hidden = !s.kbd.hidden
			s.invalidate()
		}
	})
	shown := string(k.text)
	if k.hidden {
		shown = strings.Repeat("•", len(k.text))
	}
	f := s.face(Medium, 16)
	room := k.field.Dx() - eyeW - 14
	// The end of the text, where the typing is, stays in view.
	for textWidth(f, shown+"|") > room && shown != "" {
		_, n := utf8.DecodeRuneInString(shown)
		shown = shown[n:]
	}
	tx := k.field.Min.X + 10
	if len(k.text) == 0 {
		c.TextIn(s.face(Medium, 15), tx, k.field.Min.Y, room, k.field.Dy(), 0, colFaint, s.tr("kbd_empty"))
	} else {
		c.TextIn(f, tx, k.field.Min.Y, room, k.field.Dy(), 0, colText, shown)
	}
	cx := tx + textWidth(f, shown)
	if len(k.text) == 0 {
		cx = tx
	}
	c.FillRect(cx+1, k.field.Min.Y+8, 2, k.field.Dy()-16, colBlue)
	// The keys.
	for _, key := range k.keys {
		r := key.r
		bg := colCard
		if key.fn != "" && key.fn != "space" {
			bg = colCard2
		}
		fg := colText
		if key.fn == "done" {
			bg = colBlue
			if len(k.text) < k.min {
				bg, fg = colCard2, colFaint
			}
		}
		if key.fn == "shift" && k.shift > 0 {
			bg, fg = colText, colBG
		}
		c.RoundRect(float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 6, bg)
		switch key.fn {
		case "back":
			c.iconBackspace(float32((r.Min.X+r.Max.X)/2-10), float32((r.Min.Y+r.Max.Y)/2-7), 20, 14, fg)
		case "done":
			c.TextIn(s.face(Semibold, 13), r.Min.X+1, r.Min.Y, r.Dx()-2, r.Dy(), 1, fg, fit(s.face(Semibold, 13), s.tr("kbd_join"), r.Dx()-6))
		case "space":
			c.TextIn(s.face(Medium, 12), r.Min.X, r.Min.Y, r.Dx(), r.Dy(), 1, colDim, s.tr("kbd_space"))
		case "shift":
			cx, cy := float32(r.Min.X+r.Max.X)/2, float32(r.Min.Y+r.Max.Y)/2
			c.iconShift(cx-8, cy-9, 16, 16, k.shift > 0, fg)
			if k.shift == 2 {
				c.FillRect(int(cx)-5, int(cy)+9, 10, 2, fg)
			}
		case "layer0", "layer1", "layer2":
			c.TextIn(s.face(Semibold, 13), r.Min.X, r.Min.Y, r.Dx(), r.Dy(), 1, fg, key.label)
		default:
			c.TextIn(s.face(Medium, 17), r.Min.X, r.Min.Y, r.Dx(), r.Dy()-2, 1, fg, key.label)
		}
	}
}
