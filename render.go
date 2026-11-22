package main

// Drawing of the pages into their layers (anim.go puts the layers on the
// screen): the status bar or a page header on top, the page's content below
// it, dialogs and messages in layers of their own.

import (
	"image"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font"
)

var (
	colBG     = rgb(0x0B, 0x12, 0x20)
	colCard   = rgb(0x17, 0x21, 0x33)
	colCard2  = rgb(0x22, 0x2E, 0x46)
	colText   = rgb(0xEC, 0xF0, 0xF6)
	colDim    = rgb(0x8C, 0x98, 0xAD)
	colFaint  = rgb(0x45, 0x50, 0x66)
	colGreen  = rgb(0x34, 0xC7, 0x59)
	colYellow = rgb(0xFF, 0xC4, 0x0A)
	colRed    = rgb(0xFF, 0x45, 0x3A)
	colBlue   = rgb(0x2F, 0x8C, 0xFF)
	colPurple = rgb(0xB0, 0x6A, 0xF0)
	colOrange = rgb(0xFF, 0x9F, 0x0A)
)

const (
	statusBarH = 28
	headerH    = 40
	maxContent = 8000 // px of a page's content
)

func (s *Screen) local() time.Time { return s.now().In(s.loc) }

// addHit registers a tap area in the coordinates of the layer being drawn.
func (s *Screen) addHit(x, y, w, h int, fn func()) { s.addHitR(x, y, w, h, 10, fn) }

func (s *Screen) addHitR(x, y, w, h, radius int, fn func()) {
	*s.hitDst = append(*s.hitDst, hit{r: image.Rect(x, y, x+w, y+h), fn: fn, flash: true, radius: radius})
}

func (s *Screen) addHitNoFlash(x, y, w, h int, fn func()) {
	*s.hitDst = append(*s.hitDst, hit{r: image.Rect(x, y, x+w, y+h), fn: fn})
}

// appView is the view of an app (or of the built-in system part).
type appView struct {
	app   string
	order int
	v     View
}

// views returns the views of the built-in system app and all apps that
// have one, in card order. An app that failed before its first status shows
// an error card.
func (s *Screen) views() []appView {
	out := []appView{{"system", systemOrder, s.systemView()}}
	for _, st := range s.apps {
		v := st.view
		if st.ticking {
			v = liveView(v, s.now())
		}
		if !st.haveView {
			if st.err == "" {
				continue
			}
			v = View{Card: &Card{Title: st.m.Name, Subtitle: s.tr("no_data"), SubtitleColor: "red", Icon: "alert", Color: "red"}}
		}
		out = append(out, appView{st.m.ID, st.m.Order, v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].order < out[j].order })
	return out
}

// ---- scenes ----

func sceneKey(page string, rt route) string {
	if page == "app" {
		return "app:" + rt.app + ":" + rt.page
	}
	return page
}

// renderScene draws a page into its layers.
func (s *Screen) renderScene(sc *scene) {
	sc.hits, sc.chromeHits = sc.hits[:0], sc.chromeHits[:0]
	views := s.views()
	setChrome := func(h int) {
		sc.chromeH = h
		if h == 0 {
			sc.chrome = nil
			return
		}
		if sc.chrome == nil {
			sc.chrome = NewCanvasSize(W, h)
		} else {
			sc.chrome.Resize(h)
		}
		sc.chrome.Clear(colBG)
		s.cv, s.hitDst = sc.chrome, &sc.chromeHits
	}
	setContent := func(h int) {
		h = min(max(h, H-sc.chromeH), maxContent)
		sc.contentH = h
		if sc.content == nil {
			sc.content = NewCanvasSize(W, h)
		} else {
			sc.content.Resize(h)
		}
		sc.content.Clear(colBG)
		s.cv, s.hitDst = sc.content, &sc.hits
	}
	switch sc.kind {
	case "lock":
		setChrome(0)
		setContent(H)
		s.drawLock(views)
	case "pin":
		setChrome(0)
		setContent(H)
		s.drawPIN()
	case "home":
		setChrome(statusBarH)
		s.drawStatusBar(views)
		setContent(s.homeHeight(views))
		s.drawHome(views)
	case "app":
		if sc.rt.app == "system" && sc.rt.page == "keyboard" {
			title := ""
			if s.kbd != nil {
				title = s.kbd.title
			}
			setChrome(headerH)
			s.header(title)
			setContent(H - headerH)
			s.drawKeyboard()
			break
		}
		title, items, empty := s.appContent(sc.rt.app, sc.rt.page)
		setChrome(headerH)
		s.header(title)
		h := 8
		for i := range items {
			h += s.itemHeight(&items[i])
		}
		if len(items) == 0 {
			h = 140
		}
		setContent(h)
		s.drawItems(sc.rt.app, items, empty)
	}
	sc.maxScroll = max(0, sc.contentH-(H-sc.chromeH))
	if s.track == nil || s.track.mode != dragScroll {
		sc.scroll = min(max(sc.scroll, 0), float64(sc.maxScroll))
	}
	sc.gen = s.sceneGen
}

// ---- status bar and header ----

func (s *Screen) drawStatusBar(views []appView) {
	c := s.cv
	st := s.st
	c.FillRect(0, 0, W, statusBarH, colBG)
	c.Text(s.face(Semibold, 15), 10, 3, colText, s.local().Format("15:04"))

	x := float32(W - 8)
	if st.BatPct >= 0 {
		x -= 24
		fill := colText
		switch {
		case st.BatCharging || st.BatPlugged:
			fill = colGreen
		case st.BatPct <= 15:
			fill = colRed
		}
		c.iconBattery(x, 6, 24, 12, st.BatPct, colDim, fill)
		if st.BatCharging {
			c.iconBolt(x+6, 3.5, 9, 17, colBG)
			c.iconBolt(x+7, 5, 7, 14, colText)
		}
		f := s.face(Medium, 13)
		pct := strconv.Itoa(st.BatPct) + "%"
		x -= float32(textWidth(f, pct)) + 4
		c.Text(f, int(x), 4, colText, pct)
	}
	x -= 10
	switch st.WANKind {
	case "wifi":
		x -= 18
		c.iconWifi(x, 5, 17, 3, colText, colFaint)
	case "cable", "usb":
		f := s.face(Semibold, 12)
		t := "LAN"
		if st.WANKind == "usb" {
			t = "USB"
		}
		x -= float32(textWidth(f, t))
		c.Text(f, int(x), 5, colText, t)
	}
	if !st.CellAirplane && (st.Tech != "" || st.CellOK) {
		if st.WANKind == "wifi" || st.WANKind == "cable" || st.WANKind == "usb" {
			x -= 8
		}
		x -= 17
		c.iconBars(x, 6, 17, 12, st.Bars, colText, colFaint)
		if st.Tech != "" {
			f := s.face(Semibold, 12)
			x -= float32(textWidth(f, st.Tech)) + 3
			c.Text(f, int(x), 5, colText, st.Tech)
		}
	}
	for i := len(views) - 1; i >= 0; i-- {
		for j := len(views[i].v.Status) - 1; j >= 0; j-- {
			ind := views[i].v.Status[j]
			if x < 70 {
				return
			}
			x -= 20
			c.drawIcon(ind.Icon, x+7, 12, 14, parseColor(ind.Color, colText), colBG)
		}
	}
}

// header draws a page title with a back arrow; the whole bar goes back.
func (s *Screen) header(title string) {
	c := s.cv
	c.FillRect(0, 0, W, headerH, colBG)
	c.Text(s.face(Semibold, 28), 8, -2, colBlue, "‹")
	c.Text(s.face(Semibold, 17), 28, 9, colText, fit(s.face(Semibold, 17), title, W-40))
	s.addHitNoFlash(0, 0, W, headerH, s.back)
}

// ---- lock ----

func (s *Screen) drawLock(views []appView) {
	c := s.cv
	s.drawStatusBar(views)
	t := s.local()
	c.TextIn(s.face(Bold, 76), 0, 52, W, 80, 1, colText, t.Format("15:04"))
	c.TextIn(s.face(Medium, 17), 0, 136, W, 24, 1, colDim, s.dateLine(t))

	y := 180
	f := s.face(Medium, 15)
	for _, av := range views {
		for _, l := range av.v.Lock {
			if y > 262 {
				break
			}
			text := fit(f, l.Text, W-60)
			tw := textWidth(f, text) + 16
			x0 := (W - tw) / 2
			c.Circle(float32(x0)+4, float32(y)+10, 4, parseColor(l.Color, colDim))
			c.Text(f, x0+16, y, colText, text)
			y += 26
		}
	}

	hf := s.face(Medium, 14)
	hint := s.tr("tap_to_open")
	tw := textWidth(hf, hint) + 20
	x0 := (W - tw) / 2
	c.iconLock(float32(x0), 289, 11, 14, colDim)
	c.Text(hf, x0+20, 287, colDim, hint)
	s.addHitNoFlash(0, 0, W, H, s.unlock)
}

// ---- passcode ----

var pinKeys = [][3]string{{"1", "2", "3"}, {"4", "5", "6"}, {"7", "8", "9"}, {"cancel", "0", "del"}}

func (s *Screen) drawPIN() {
	c := s.cv
	c.TextIn(s.face(Semibold, 18), 0, 22, W, 26, 1, colText, s.tr("enter_code"))
	n := max(len(s.cfg.Passcode), 4)
	gap := float32(22)
	x0 := float32(W)/2 - gap*float32(n-1)/2
	for i := 0; i < n; i++ {
		col := colText
		if s.pinErr {
			col = colRed
		}
		if i < len(s.pin) {
			c.Circle(x0+gap*float32(i), 70, 6, col)
		} else {
			c.Ring(x0+gap*float32(i), 70, 6, 1.6, col)
		}
	}
	if wait := s.pinWaitTo.Sub(s.now()); wait > 0 {
		c.TextIn(s.face(Medium, 13), 0, 86, W, 20, 1, colRed, s.trf("wait_n", int(wait.Seconds()+0.99)))
	} else if s.pinErr {
		c.TextIn(s.face(Medium, 13), 0, 86, W, 20, 1, colRed, s.tr("wrong_code"))
	}
	const kw, kh, gx, gy = 64, 42, 12, 6
	kx := (W - 3*kw - 2*gx) / 2
	ky := 118
	for r, row := range pinKeys {
		for col, k := range row {
			x := kx + col*(kw+gx)
			y := ky + r*(kh+gy)
			k := k
			switch k {
			case "cancel":
				c.TextIn(s.face(Medium, 14), x, y, kw, kh, 1, colDim, s.tr("cancel"))
			case "del":
				c.iconBackspace(float32(x+kw/2-13), float32(y+kh/2-9), 26, 18, colDim)
			default:
				c.RoundRect(float32(x), float32(y), kw, kh, 12, colCard)
				c.TextIn(s.face(Semibold, 22), x, y, kw, kh, 1, colText, k)
			}
			s.addHitR(x, y, kw, kh, 12, func() { s.pinKey(k) })
		}
	}
}

// ---- home ----

type tileOf struct {
	app string
	t   Tile
}

func (s *Screen) homeTiles(views []appView) []tileOf {
	var tiles []tileOf
	for _, av := range views {
		for _, t := range av.v.Tiles {
			tiles = append(tiles, tileOf{av.app, t})
		}
	}
	return append(tiles, tileOf{"core", Tile{Label: s.tr("power"), Icon: "power", Action: &Action{ID: "core.power"}}})
}

func (s *Screen) homeHeight(views []appView) int {
	cards := 0
	for _, av := range views {
		if av.v.Card != nil {
			cards++
		}
	}
	return 2 + cards*66 + (len(s.homeTiles(views))+2)/3*68 + 4
}

func (s *Screen) drawHome(views []appView) {
	y := 2
	for _, av := range views {
		if av.v.Card != nil {
			s.drawCard(y, av.app, av.v.Card)
			y += 66
		}
	}
	const tw, th = 70, 62
	gap := (W - 16 - 3*tw) / 2
	for i, t := range s.homeTiles(views) {
		s.drawTile(8+(i%3)*(tw+gap), y+(i/3)*(th+6), tw, th, t.app, t.t)
	}
}

func (s *Screen) drawCard(y int, app string, cd *Card) {
	c := s.cv
	const x, w, h = 8, W - 16, 60
	tint := parseColor(cd.Color, colBlue)
	disc := mix(tint, colCard, 0.22)
	c.RoundRect(x, float32(y), w, h, 14, colCard)
	c.Circle(x+30, float32(y)+30, 19, disc)
	icon := cd.Icon
	if icon == "" {
		icon = "dot"
	}
	c.drawIcon(icon, x+30, float32(y)+30, 20, tint, disc)
	tw := w - 62 - 8
	if cd.Open != "" || cd.Action != nil {
		tw -= 12
		c.TextIn(s.face(Semibold, 22), x+w-20, y, 14, h, 1, colFaint, "›")
		s.addHitR(x, y, w, h, 14, func() { s.tapTarget(app, cd.Open, cd.Action) })
	}
	c.TextIn(s.face(Semibold, 15), x+62, y+9, tw, 20, 0, colText, cd.Title)
	c.TextIn(s.face(Medium, 13), x+62, y+32, tw, 18, 0, parseColor(cd.SubtitleColor, colDim), cd.Subtitle)
}

func (s *Screen) drawTile(x, y, w, h int, app string, t Tile) {
	c := s.cv
	c.RoundRect(float32(x), float32(y), float32(w), float32(h), 14, colCard)
	icon := t.Icon
	if icon == "" {
		icon = "dot"
	}
	c.drawIcon(icon, float32(x+w/2), float32(y+20), 20, parseColor(t.Color, colDim), colCard)
	if t.Value != "" {
		c.TextIn(s.face(Semibold, 15), x+2, y+34, w-4, 20, 1, colText, t.Value)
	} else {
		c.TextIn(s.face(Medium, 12), x+2, y+36, w-4, 18, 1, colDim, t.Label)
	}
	if t.Open != "" || t.Action != nil {
		s.addHitR(x, y, w, h, 14, func() { s.tapTarget(app, t.Open, t.Action) })
	}
}

func (s *Screen) tapTarget(app, open string, a *Action) {
	switch {
	case open != "":
		s.open(app, open)
	case a != nil:
		s.act(app, a)
	}
}

// ---- app pages ----

// appContent is what a page of an app holds: its title, items, and the
// text for an empty page.
func (s *Screen) appContent(app, name string) (string, []Item, string) {
	v, _ := s.viewOf(app)
	p := v.Pages[name]
	if p == nil {
		if st := s.appByID[app]; st != nil && (st.polling || !st.haveView) {
			return "", nil, s.tr("loading")
		}
		return name, nil, s.tr("no_page")
	}
	empty := ""
	if len(p.Items) == 0 {
		empty = p.Empty
		if empty == "" {
			empty = s.tr("empty")
		}
	}
	items := p.Items
	if st := s.appByID[app]; st != nil && st.ticking {
		items = liveItems(items, s.now())
	}
	return liveText(p.Title, s.now()), items, empty
}

func (s *Screen) drawItems(app string, items []Item, empty string) {
	if len(items) == 0 {
		lines := wrapText(s.face(Medium, 14), empty, W-32)
		for i, l := range lines {
			s.cv.TextIn(s.face(Medium, 14), 16, 50+i*20, W-32, 20, 1, colDim, l)
		}
		return
	}
	y := 4
	for i := range items {
		it := &items[i]
		h := s.itemHeight(it)
		s.drawItem(app, it, y, h)
		y += h
	}
}

func (s *Screen) itemHeight(it *Item) int {
	switch it.Type {
	case "section":
		return 28
	case "text":
		return len(wrapText(s.face(Medium, 13), it.Label, W-24))*18 + 8
	case "toggle", "link":
		if it.Sub != "" {
			return 46
		}
		return 36
	case "button":
		return 44
	case "bar", "entry":
		if it.Sub != "" {
			return 44
		}
		return 32
	case "chart":
		if it.XStart != "" || it.XEnd != "" {
			return 140
		}
		return 124
	case "segments":
		return 40
	case "stats":
		return 64
	}
	return 32
}

func (s *Screen) running(app string, a *Action) bool {
	st := s.appByID[app]
	return st != nil && a != nil && st.running[a.key()]
}

// rowText draws the label of a row, and its sub line under it when there
// is one.
func (s *Screen) rowText(x, y, w, rh int, label, sub string, col color.RGBA) {
	if sub == "" {
		s.cv.TextIn(s.face(Medium, 15), x, y, w, rh, 0, col, label)
		return
	}
	s.cv.TextIn(s.face(Medium, 14), x, y+5, w, 18, 0, col, label)
	s.cv.TextIn(s.face(Medium, 12), x, y+23, w, 15, 0, colDim, sub)
}

func (s *Screen) drawItem(app string, it *Item, y, h int) {
	if it.app != "" {
		app = it.app // an app's item on a page of the screen's own
	}
	c := s.cv
	busy := it.Busy || s.running(app, it.Action)
	col := parseColor(it.Color, colText)
	rh := h - 3
	tappable := !it.Disabled && !busy && (it.Open != "" || it.Action != nil)
	labelCol := colText
	if it.Disabled {
		labelCol = colDim
	}
	radius := 10
	switch it.Type {
	case "section":
		c.TextIn(s.face(Semibold, 13), 14, y+8, W-28, 18, 0, parseColor(it.Color, colDim), it.Label)
		return
	case "text":
		f := s.face(Medium, 13)
		for i, l := range wrapText(f, it.Label, W-24) {
			c.Text(f, 12, y+4+i*18, parseColor(it.Color, colText), l)
		}
		return
	case "segments":
		s.drawSegments(app, it, y, rh)
		return
	case "stats":
		s.drawStats(it, y, rh)
		return
	case "chart":
		s.drawChart(it, y, h)
		return
	case "info":
		radius = 9
		c.RoundRect(8, float32(y), W-16, float32(rh), 9, colCard)
		lf, vf := s.face(Medium, 13), s.face(Semibold, 14)
		inner := W - 32
		lw := min(textWidth(lf, it.Label), inner*55/100)
		c.TextIn(lf, 16, y, lw, rh, 0, colDim, it.Label)
		c.TextIn(vf, 16+lw+8, y, inner-lw-8, rh, 2, col, it.Value)
	case "radio":
		bg := colCard
		if it.Checked {
			bg = colCard2
		}
		c.RoundRect(8, float32(y), W-16, float32(rh), 10, bg)
		cx, cy := float32(26), float32(y)+float32(rh)/2
		accent := parseColor(it.Color, colGreen)
		switch {
		case busy:
			c.Ring(cx, cy, 7, 2, colYellow)
		case it.Checked:
			c.Ring(cx, cy, 7, 2, accent)
			c.Circle(cx, cy, 3.5, accent)
		default:
			c.Ring(cx, cy, 7, 1.6, colFaint)
		}
		right := s.drawRight(y, rh, busy, it.Value)
		// The label ends 6 px before the value (drawRight starts it at W-24-vw).
		c.TextIn(s.face(Medium, 15), 42, y, W-64-right, rh, 0, labelCol, it.Label)
	case "toggle":
		c.RoundRect(8, float32(y), W-16, float32(rh), 10, colCard)
		tx, ty := float32(W-16-8-38), float32(y)+float32(rh-22)/2
		track := colFaint
		if it.Checked {
			track = parseColor(it.Color, colGreen)
		}
		c.RoundRect(tx, ty, 38, 22, 11, track)
		kx := tx + 11
		if it.Checked {
			kx = tx + 27
		}
		knob := colText
		if busy {
			knob = colYellow
		}
		c.Circle(kx, ty+11, 8.5, knob)
		s.rowText(18, y, W-16-8-38-22, rh, it.Label, it.Sub, labelCol)
	case "button":
		radius = 12
		bg, fg := colCard2, colText
		switch it.Style {
		case "danger":
			bg, fg = colCard, colRed
		case "primary":
			bg, fg = colBlue, colText
		}
		if it.Color != "" {
			fg = col
		}
		if it.Disabled {
			bg, fg = colCard, colDim
		}
		label := it.Label
		if busy {
			label += " …"
			fg = colYellow
		}
		c.RoundRect(8, float32(y+2), W-16, float32(h-6), 12, bg)
		c.TextIn(s.face(Semibold, 15), 12, y+2, W-24, h-6, 1, fg, label)
	case "link":
		c.RoundRect(8, float32(y), W-16, float32(rh), 10, colCard)
		x := 18
		if it.Icon != "" {
			c.drawIcon(it.Icon, 28, float32(y)+float32(rh)/2, 16, parseColor(it.Color, colDim), colCard)
			x = 44
		}
		right := 18
		if busy {
			c.TextIn(s.face(Semibold, 15), W-44, y, 28, rh, 2, colYellow, "…")
			right = 30
		} else {
			c.TextIn(s.face(Semibold, 22), W-30, y, 14, rh, 1, colFaint, "›")
			if it.Value != "" {
				vf := s.face(Medium, 13)
				vw := min(textWidth(vf, it.Value), 96)
				c.TextIn(vf, W-34-vw, y, vw, rh, 2, colDim, it.Value)
				right += vw + 8
			}
		}
		s.rowText(x, y, W-16-(x-8)-right-6, rh, it.Label, it.Sub, labelCol)
	case "bar":
		c.RoundRect(8, float32(y), W-16, float32(rh), 9, colCard)
		f1 := min(max(it.Fraction, 0), 1)
		f2 := min(max(it.Fraction2, 0), 1-f1)
		if fw := float32(W-16) * float32(f1); fw >= 1 {
			c.RoundRect(8, float32(y), float32(math.Round(float64(fw))), float32(rh), 9, mix(parseColor(it.Color, colBlue), colCard, 0.32))
		}
		if fw2 := float32(W-16) * float32(f2); fw2 >= 1 {
			x2 := 8 + float32(W-16)*float32(f1)
			c.RoundRect(float32(math.Round(float64(x2))), float32(y), float32(math.Round(float64(fw2))), float32(rh), 9,
				mix(parseColor(it.Color2, colGreen), colCard, 0.32))
		}
		vw := 0
		if it.Value != "" {
			vf := s.face(Semibold, 13)
			vw = min(textWidth(vf, it.Value), 120)
			c.TextIn(vf, W-16-vw, y, vw, rh, 2, colText, it.Value)
			vw += 8
		}
		s.rowText(16, y, W-16-16-vw, rh, it.Label, it.Sub, labelCol)
	default: // entry, and anything unknown
		radius = 9
		c.RoundRect(8, float32(y), W-16, float32(rh), 9, colCard)
		x := 16
		if it.Icon != "" {
			c.drawIcon(it.Icon, 25, float32(y)+float32(rh)/2, 14, parseColor(it.Color, colDim), colCard)
			x = 38
		}
		if it.Detail != "" {
			df := s.face(Medium, 12)
			dw := min(textWidth(df, it.Detail), 80)
			c.TextIn(df, x, y, dw, rh, 0, colDim, it.Detail)
			x += dw + 8
		}
		right := s.drawRight(y, rh, busy, it.Value)
		lw := W - 22 - right - x // 6 px before the value
		if it.Sub != "" {
			c.TextIn(s.face(Semibold, 14), x, y+5, lw, 18, 0, col, it.Label)
			c.TextIn(s.face(Medium, 12), x, y+23, lw, 15, 0, colDim, it.Sub)
		} else {
			c.TextIn(s.face(Semibold, 14), x, y, lw, rh, 0, col, it.Label)
		}
	}
	if tappable {
		s.addHitR(8, y, W-16, rh, radius, func() { s.tapTarget(app, it.Open, it.Action) })
	}
}

// drawRight draws the busy mark or a small value at the right edge of a row
// and returns the width it took.
func (s *Screen) drawRight(y, rh int, busy bool, value string) int {
	c := s.cv
	if busy {
		c.TextIn(s.face(Semibold, 15), W-44, y, 28, rh, 2, colYellow, "…")
		return 30
	}
	if value == "" {
		return 0
	}
	f := s.face(Medium, 12)
	vw := min(textWidth(f, value), 100)
	c.TextIn(f, W-16-8-vw, y, vw, rh, 2, colDim, value)
	return vw + 8
}

func (s *Screen) drawSegments(app string, it *Item, y, rh int) {
	c := s.cv
	n := len(it.Options)
	if n == 0 {
		return
	}
	c.RoundRect(8, float32(y), W-16, float32(rh), 10, colCard)
	sel := -1
	for i, o := range it.Options {
		if s.running(app, o.Action) {
			sel = i
		}
	}
	if sel < 0 {
		for i, o := range it.Options {
			if o.Checked {
				sel = i
				break
			}
		}
	}
	inner := W - 16 - 6
	for i, o := range it.Options {
		x0 := 11 + inner*i/n
		x1 := 11 + inner*(i+1)/n
		col := colDim
		if i == sel {
			c.RoundRect(float32(x0), float32(y+3), float32(x1-x0), float32(rh-6), 8, colCard2)
			col = colText
		}
		c.TextIn(s.face(Semibold, 13), x0+2, y, x1-x0-4, rh, 1, col, o.Label)
		if o.Action != nil && !it.Disabled {
			o := o
			s.addHitR(x0, y+3, x1-x0, rh-6, 8, func() { s.tapTarget(app, "", o.Action) })
		}
	}
}

func (s *Screen) drawStats(it *Item, y, rh int) {
	c := s.cv
	n := len(it.Stats)
	if n == 0 {
		return
	}
	c.RoundRect(8, float32(y), W-16, float32(rh), 12, colCard)
	cw := (W - 16) / n
	lf, uf := s.face(Medium, 10), s.face(Medium, 11)
	for i, st := range it.Stats {
		x := 8 + i*cw
		if i > 0 {
			c.FillRect(x, y+12, 1, rh-24, colCard2)
		}
		c.TextIn(lf, x+9, y+6, cw-12, 13, 0, colDim, st.Label)
		vf := s.face(Semibold, 21)
		if textWidth(vf, st.Value) > cw-12 {
			vf = s.face(Semibold, 16)
		}
		vy := y + 20
		if st.Unit == "" {
			vy = y + 25
		}
		c.TextIn(vf, x+9, vy, cw-12, 26, 0, parseColor(st.Color, colText), st.Value)
		if st.Unit != "" {
			c.TextIn(uf, x+9, y+44, cw-12, 14, 0, colDim, st.Unit)
		}
	}
}

// wrapText breaks s into lines no wider than width (at most 12 lines).
func wrapText(f font.Face, s string, width int) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			w, _ = cutRunes(w, max(width, 1)) // one word wider than a line
			try := w
			if line != "" {
				try = line + " " + w
			}
			if line != "" && textWidth(f, try) > width {
				lines = append(lines, line)
				if len(lines) >= 12 {
					return lines
				}
				line = w
			} else {
				line = try
			}
		}
		lines = append(lines, fit(f, line, width))
		if len(lines) >= 12 {
			return lines[:12]
		}
	}
	return lines
}

// ---- dialogs and messages (layers of their own) ----

func (s *Screen) button(x, y, w, h int, bg color.RGBA, fg color.RGBA, label string, fn func()) {
	s.cv.RoundRect(float32(x), float32(y), float32(w), float32(h), 12, bg)
	s.cv.TextIn(s.face(Semibold, 15), x+4, y, w-8, h, 1, fg, label)
	s.addHitR(x, y, w, h, 12, fn)
}

// dialogBox draws a dialog with a title and text, sized to fit; it returns
// the y where the buttons go.
func (s *Screen) dialogBox(buttons int, title string, titleCol color.RGBA, text string, icon string, iconCol color.RGBA) int {
	c := s.cv
	const dx, dw = 18, W - 36
	tf, xf := s.face(Semibold, 17), s.face(Medium, 13)
	tl := wrapText(tf, title, dw-28)
	if len(tl) > 3 {
		tl = tl[:3]
	}
	var xl []string
	if text != "" {
		xl = wrapText(xf, text, dw-28)
		if len(xl) > 5 {
			xl = xl[:5]
		}
	}
	h := 18 + len(tl)*23 + len(xl)*18 + 14 + buttons*48
	iconH := 0
	if icon != "" {
		iconH = 34
		h += iconH
	}
	top := (H - h) / 2
	c.RoundRect(dx, float32(top), dw, float32(h), 18, colCard)
	s.modalBox = image.Rect(dx, top, dx+dw, top+h)
	y := top + 14
	if icon != "" {
		c.drawIcon(icon, W/2, float32(y)+13, 26, iconCol, colCard)
		y += iconH
	}
	for _, l := range tl {
		c.TextIn(tf, dx+10, y, dw-20, 22, 1, titleCol, l)
		y += 23
	}
	for _, l := range xl {
		c.TextIn(xf, dx+10, y, dw-20, 18, 1, colDim, l)
		y += 18
	}
	return y + 12
}

// renderModal draws the open dialog into the dialog layer.
func (s *Screen) renderModal() {
	if s.modalLayer == nil {
		s.modalLayer = NewCanvasSize(W, H)
	}
	c := s.modalLayer
	clear(c.img.Pix)
	s.cv, s.hitDst = c, &s.modalHits
	s.modalHits = s.modalHits[:0]
	m := s.modal
	const bx, bw = 18 + 16, W - 36 - 32
	switch m.kind {
	case "bye":
		c.Clear(colBG)
		c.iconPower(W/2-14, 110, 28, colDim)
		c.TextIn(s.face(Semibold, 17), 0, 152, W, 30, 1, colText, m.title)
		s.modalBox = image.Rect(0, 0, W, H)
	case "power":
		n := 3
		if haveModeTool() {
			n = 4
		}
		y := s.dialogBox(n, s.tr("power"), colText, "", "", colText)
		s.button(bx, y, bw, 42, colRed, colText, s.tr("power_off"), func() {
			s.setModal(&Modal{kind: "confirm", title: s.tr("confirm_off"), text: s.tr("power_note"), yes: s.tr("power_off"), danger: true,
				onYes: func() { s.power("poweroff") }})
		})
		s.button(bx, y+48, bw, 42, colBlue, colText, s.tr("reboot"), func() {
			s.setModal(&Modal{kind: "confirm", title: s.tr("confirm_rb"), text: s.tr("power_note"), yes: s.tr("reboot"),
				onYes: func() { s.power("reboot") }})
		})
		if n == 4 {
			s.button(bx, y+96, bw, 42, colCard2, colText, s.tr("stock_screen"), func() {
				s.setModal(&Modal{kind: "confirm", title: s.tr("confirm_stock"), text: s.tr("stock_note"), yes: s.tr("stock_screen"),
					onYes: s.switchToStock})
			})
			y += 48
		}
		s.button(bx, y+96, bw, 42, colCard2, colText, s.tr("cancel"), func() { s.setModal(nil) })
	case "confirm":
		y := s.dialogBox(2, m.title, colText, m.text, "", colText)
		bg := colBlue
		if m.danger {
			bg = colRed
		}
		yes := m.onYes
		s.button(bx, y, bw, 42, bg, colText, m.yes, func() {
			if yes != nil {
				yes()
			}
		})
		s.button(bx, y+48, bw, 42, colCard2, colText, s.tr("cancel"), func() { s.setModal(nil) })
	case "alert":
		col := parseColor(m.color, colOrange)
		y := s.dialogBox(1, m.title, col, m.text, "alert", col)
		s.button(bx, y, bw, 42, colCard2, colText, s.tr("ok"), func() { s.setModal(nil) })
	case "notify":
		col := parseColor(m.color, colBlue)
		n := 1
		if m.target != nil {
			n = 2
		}
		y := s.dialogBox(n, m.title, colText, m.text, m.icon, col)
		if m.target != nil {
			t := *m.target
			s.button(bx, y, bw, 42, colBlue, colText, s.tr("open"), func() {
				s.setModal(nil)
				s.open(t.app, t.page)
			})
			y += 48
		}
		s.button(bx, y, bw, 42, colCard2, colText, s.tr("ok"), func() { s.setModal(nil) })
	}
	s.modalShown = m
}

func (s *Screen) renderToast() {
	f := s.face(Medium, 13)
	t := fit(f, s.toast, W-56)
	w := textWidth(f, t) + 28
	x := (W - w) / 2
	if s.toastLayer == nil {
		s.toastLayer = NewCanvasSize(W, 30)
	}
	clear(s.toastLayer.img.Pix)
	s.toastLayer.RoundRect(float32(x), 0, float32(w), 30, 15, colCard2)
	s.toastLayer.TextIn(f, x, 0, w, 30, 1, colText, t)
	s.toastBox = image.Rect(x, 0, x+w, 30)
}

// ---- charts ----

var seriesDefault = []color.RGBA{colBlue, colGreen, colOrange, colPurple}

// niceCeil rounds v up to 1, 2, 2.5 or 5 times a power of ten.
func niceCeil(v float64) float64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, f := range []float64{1, 2, 2.5, 5, 10} {
		if f*p >= v*0.999999 {
			return f * p
		}
	}
	return 10 * p
}

// number formats a chart value with up to three significant digits.
func (s *Screen) number(v float64) string {
	var t string
	switch {
	case v >= 100:
		t = strconv.FormatFloat(v, 'f', 0, 64)
	case v >= 10:
		t = strconv.FormatFloat(v, 'f', 1, 64)
	default:
		t = strconv.FormatFloat(v, 'f', 2, 64)
	}
	if strings.Contains(t, ".") {
		t = strings.TrimRight(strings.TrimRight(t, "0"), ".")
	}
	if s.lang == "ru" {
		t = strings.Replace(t, ".", ",", 1)
	}
	return t
}

func (s *Screen) drawChart(it *Item, y, h int) {
	c := s.cv
	const x0, w = 8, W - 16
	c.RoundRect(x0, float32(y), w, float32(h-4), 10, colCard)

	gf := s.face(Medium, 12)
	right := x0 + w - 10
	for i := len(it.Series) - 1; i >= 0; i-- {
		se := it.Series[i]
		if se.Label == "" {
			continue
		}
		tw := min(textWidth(gf, se.Label), 90)
		right -= tw
		c.TextIn(gf, right, y+4, tw, 20, 0, colText, se.Label)
		right -= 9
		c.Circle(float32(right)+3, float32(y)+14, 3.5, parseColor(se.Color, seriesDefault[i%len(seriesDefault)]))
		right -= 8
	}
	if it.Label != "" && right-(x0+10) > 20 {
		c.TextIn(s.face(Semibold, 13), x0+10, y+4, right-(x0+10), 20, 0, colText, it.Label)
	}

	bottomPad := 10
	if it.XStart != "" || it.XEnd != "" {
		bottomPad = 26
		xf := s.face(Medium, 11)
		c.TextIn(xf, x0+10, y+h-4-21, 100, 16, 0, colDim, it.XStart)
		c.TextIn(xf, x0+w-110, y+h-4-21, 100, 16, 2, colDim, it.XEnd)
	}
	px0, px1 := float32(x0+10), float32(x0+w-10)
	top, bottom := float32(y+30), float32(y+h-4-bottomPad)
	bars := it.Style == "bars"
	top0 := it.Max
	if top0 <= 0 {
		if bars {
			for k := 0; ; k++ {
				sum, any := 0.0, false
				for _, se := range it.Series {
					if k < len(se.Values) {
						sum += max(se.Values[k], 0)
						any = true
					}
				}
				if !any {
					break
				}
				top0 = max(top0, sum)
			}
		} else {
			for _, se := range it.Series {
				for _, v := range se.Values {
					top0 = max(top0, v)
				}
			}
		}
	}
	scale := niceCeil(top0)
	grid := mix(colFaint, colCard, 0.55)
	c.FillRect(int(px0), int(top), int(px1-px0), 1, grid)
	c.FillRect(int(px0), int((top+bottom)/2), int(px1-px0), 1, grid)
	c.FillRect(int(px0), int(bottom), int(px1-px0), 1, mix(colFaint, colCard, 0.9))
	label := s.number(scale)
	if it.Unit != "" {
		label += " " + it.Unit
	}
	defer func() {
		lf := s.face(Medium, 11)
		lw := textWidth(lf, label)
		c.RoundRect(px0, top+2, float32(lw+6), 15, 4, colCard)
		c.Text(lf, int(px0)+3, int(top)+2, colDim, label)
	}()

	span := max(it.Span, 2)
	for _, se := range it.Series {
		span = max(span, len(se.Values))
	}
	if bars {
		// Stacked bars: one per point, the first series at the bottom.
		slot := (px1 - px0) / float32(span)
		bw := max(slot*0.72, 1)
		maxN := 0
		for _, se := range it.Series {
			maxN = max(maxN, len(se.Values))
		}
		for k := 0; k < maxN; k++ {
			base := bottom
			for i, se := range it.Series {
				idx := k - (maxN - len(se.Values))
				if idx < 0 || idx >= len(se.Values) {
					continue
				}
				v := min(max(se.Values[idx], 0), scale)
				hgt := float32(v/scale) * (bottom - top)
				if hgt < 0.5 {
					continue
				}
				x := px0 + float32(span-maxN+k)*slot + (slot-bw)/2
				c.RoundRect(float32(math.Round(float64(x))), float32(math.Round(float64(base-hgt))),
					float32(math.Max(1, math.Round(float64(bw)))), float32(math.Max(1, math.Round(float64(hgt)))), 0,
					parseColor(se.Color, seriesDefault[i%len(seriesDefault)]))
				base -= hgt
			}
		}
		return
	}
	dx := (px1 - px0) / float32(span-1)
	for i, se := range it.Series {
		n := len(se.Values)
		if n == 0 {
			continue
		}
		col := parseColor(se.Color, seriesDefault[i%len(seriesDefault)])
		start := span - n
		pt := func(k int) (float32, float32) {
			v := se.Values[k]
			if math.IsNaN(v) || v < 0 {
				v = 0
			}
			v = min(v, scale)
			return px0 + float32(start+k)*dx, bottom - float32(v/scale)*(bottom-top)
		}
		if n == 1 {
			x, yy := pt(0)
			c.Circle(x, yy, 2.2, col)
			continue
		}
		if i == 0 {
			var area Path
			xa, _ := pt(0)
			area.MoveTo(xa, bottom)
			for k := 0; k < n; k++ {
				area.LineTo(pt(k))
			}
			xb, _ := pt(n - 1)
			area.LineTo(xb, bottom)
			area.Close()
			c.Fill(&area, mix(col, colCard, 0.26))
		}
		var line Path
		for k := 1; k < n; k++ {
			xa, ya := pt(k - 1)
			xb, yb := pt(k)
			line.Segment(xa, ya, xb, yb, 1.8)
		}
		c.Fill(&line, col)
	}
}
