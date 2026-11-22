package main

// Navigation, the finger and the frames: which page is up, how it changes
// (with motion when the screen is on), what a touch does, the passcode,
// sleep and wake.

import (
	"crypto/subtle"
	"image"
	"math"
	"strings"
	"time"
)

// ---- scenes ----

func (s *Screen) top() route {
	if s.page == "app" && len(s.stack) > 0 {
		return s.stack[len(s.stack)-1]
	}
	return route{}
}

func (s *Screen) scene(page string, rt route) *scene {
	k := sceneKey(page, rt)
	sc := s.scenes[k]
	if sc == nil {
		sc = &scene{key: k, kind: page, rt: rt, gen: -1}
		s.scenes[k] = sc
	}
	return sc
}

func (s *Screen) cur() *scene { return s.scene(s.page, s.top()) }

func (s *Screen) ensureRendered(sc *scene) {
	if sc.gen != s.sceneGen {
		s.renderScene(sc)
	}
}

// dropScenes forgets pages that are no longer on the way back.
func (s *Screen) dropScenes() {
	keep := map[string]bool{"lock": true, "pin": true, "home": true}
	for _, rt := range s.stack {
		keep[sceneKey("app", rt)] = true
	}
	for k := range s.scenes {
		if !keep[k] {
			delete(s.scenes, k)
		}
	}
}

// jump goes to a page at once, without motion.
func (s *Screen) jump(page string, stack []route) {
	s.trans = nil
	s.page = page
	s.stack = append(s.stack[:0], stack...)
	if page == "app" && len(s.stack) == 0 {
		s.page = "home"
	}
	sc := s.cur()
	sc.scroll = 0
	s.dropScenes()
	s.needFrame = true
}

// navigate changes the page with change, and moves the old page off and
// the new one on when the screen is there to show it.
func (s *Screen) navigate(kind string, change func()) {
	from := s.cur()
	change()
	to := s.cur()
	s.fling, s.spring, s.pressed = 0, nil, nil
	s.needFrame = true
	if to == from {
		return
	}
	if kind == "push" {
		to.scroll = 0
	}
	if !s.screenOn || s.fb == nil && !s.o.animPreview() {
		s.trans = nil
		s.dropScenes()
		return
	}
	s.ensureRendered(from)
	s.ensureRendered(to)
	s.trans = &transition{kind: kind, from: from, to: to, drag: true}
	s.trans.animate(s.now(), 1, s.dropScenes)
	s.trans.dur = pushDur
}

func (o options) animPreview() bool { return o.anim != "" }

func (s *Screen) locked() bool { return s.page == "lock" || s.page == "pin" }

func (s *Screen) open(app, page string) {
	if page == "" {
		return
	}
	if s.locked() {
		s.after = &route{app, page}
		s.unlock()
		return
	}
	rt := route{app, page}
	s.navigate("push", func() {
		s.stack = append(s.stack, rt)
		s.page = "app"
	})
	// Ask the app at once: it now knows its page is open (MUDI_SCREEN_PAGE).
	if st := s.appByID[app]; st != nil && !st.polling {
		s.pollApp(st)
	}
	if app == "system" && (page == "sms" || strings.HasPrefix(page, "sms:")) {
		s.onSMSPage(page)
	}
	if app == "system" && page == "wifi" {
		s.onWifiPage()
	}
	if app == "system" && page == "net" {
		s.wifiStatus()
	}
}

func (s *Screen) goHome() {
	if s.page == "home" {
		return
	}
	s.navigate("pop", func() {
		s.stack = s.stack[:0]
		s.page = "home"
	})
}

// backTarget is where back leads from here.
func (s *Screen) backTarget() (string, route, bool) {
	switch {
	case s.page == "app" && len(s.stack) > 1:
		return "app", s.stack[len(s.stack)-2], true
	case s.page == "app":
		return "home", route{}, true
	case s.page == "pin":
		return "lock", route{}, true
	}
	return "", route{}, false
}

func (s *Screen) popState() {
	if s.onKeyboardPage() {
		s.closeKeyboard()
	}
	switch {
	case s.page == "app" && len(s.stack) > 1:
		s.stack = s.stack[:len(s.stack)-1]
	case s.page == "app":
		s.stack = s.stack[:0]
		s.page = "home"
	case s.page == "pin":
		s.page = "lock"
		s.pin = ""
		s.after = nil
	}
	if rt := s.top(); rt.app != "" {
		if st := s.appByID[rt.app]; st != nil && !st.polling {
			s.pollApp(st)
		}
	}
}

func (s *Screen) back() {
	if s.modal != nil && s.modal.kind != "bye" {
		s.setModal(nil)
		return
	}
	if _, _, ok := s.backTarget(); ok {
		s.navigate("pop", s.popState)
	}
}

// ---- passcode ----

func (s *Screen) needPIN() bool {
	return s.cfgUnknown || s.cfg.PasscodeOn && s.cfg.Passcode != ""
}

func (s *Screen) unlock() {
	if s.needPIN() {
		s.navigate("up", s.toPIN)
	} else {
		s.navigate("up", s.unlockedState)
	}
}

func (s *Screen) toPIN() {
	s.page = "pin"
	s.pin = ""
	s.pinErr = false
}

func (s *Screen) unlockedState() {
	s.stack = s.stack[:0]
	s.page = "home"
	if r := s.after; r != nil {
		s.after = nil
		s.stack = append(s.stack, *r)
		s.page = "app"
		if st := s.appByID[r.app]; st != nil && !st.polling {
			s.pollApp(st)
		}
		if r.app == "system" && (r.page == "sms" || strings.HasPrefix(r.page, "sms:")) {
			s.onSMSPage(r.page) // read, as when opened without the passcode
		}
	}
}

func (s *Screen) pinKey(k string) {
	if s.now().Before(s.pinWaitTo) {
		return
	}
	defer s.invalidate()
	switch k {
	case "del":
		if n := len(s.pin); n > 0 {
			s.pin = s.pin[:n-1]
		}
		return
	case "cancel":
		s.back()
		return
	}
	if s.cfgUnknown {
		// No passcode to compare with yet: ask uci again, and stay locked
		// while it does not answer. That is not a wrong passcode.
		s.reloadScreenConfig()
		if s.cfgUnknown {
			s.pin, s.pinErr = "", true
			return
		}
		if !s.needPIN() {
			s.navigate("up", s.unlockedState)
			return
		}
	}
	s.pinErr = false
	if len(s.pin) < len(s.cfg.Passcode) {
		s.pin += k
	}
	if len(s.pin) < len(s.cfg.Passcode) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(s.pin), []byte(s.cfg.Passcode)) == 1 {
		s.pin, s.pinErr, s.pinFails = "", false, 0
		s.navigate("up", s.unlockedState)
		return
	}
	s.pin = ""
	s.pinErr = true
	s.pinFails++
	s.log.Printf("wrong passcode (%d in a row)", s.pinFails)
	if s.pinFails >= 5 {
		s.pinWaitTo = s.now().Add(time.Duration(30*(s.pinFails-4)) * time.Second)
	}
}

// ---- dialogs and messages ----

func (s *Screen) setModal(m *Modal) {
	now := s.now()
	switch {
	case m != nil:
		s.modal, s.modalDirty = m, true
		if s.modalA.v < 1 {
			s.modalA.set(1, now, modalInDur)
		}
	case s.modal != nil:
		s.modal = nil
		s.modalA.set(0, now, modalOutDur)
	}
	s.pressed = nil
	if !s.screenOn {
		s.modalA.active = false
		if m != nil {
			s.modalA.v = 1
		} else {
			s.modalA.v = 0
		}
	}
	s.needFrame = true
}

func (s *Screen) showToast(msg string) {
	s.toast = msg
	s.toastTo = s.now().Add(3 * time.Second)
	s.toastDirty = true
	s.toastA.set(1, s.now(), toastDur)
	s.needFrame = true
}

// ---- sleep and wake ----

func (s *Screen) wake(why string) {
	s.lastActivity = s.now()
	if s.screenOn {
		return
	}
	s.log.Printf("screen on (%s)", why)
	s.screenOn = true
	s.awake.Store(true)
	s.wokeAt = s.now()
	if s.live {
		s.reloadScreenConfig()
		s.fb.Invalidate()
	}
	s.checkWebLang()
	s.invalidate()
	s.update() // the lock screen is on the panel before the light comes on
	if s.live {
		from := s.blLevel
		if s.bl == nil {
			from = 1
			s.disp.SetBrightness(1)
		}
		s.bl = &blFade{from: from, to: s.cfg.level(), start: s.now(), dur: 220 * time.Millisecond}
		s.disp.PowerOn()
	}
	s.pingLPM(true)
	s.requestRefresh()
	s.pollApps(true)
}

func (s *Screen) sleep(why string) {
	if !s.screenOn {
		return
	}
	s.log.Printf("screen off (%s)", why)
	s.lock()
	s.invalidate()
	s.update() // the lock screen fades out, and is there at the next wake
	s.screenOn = false
	s.awake.Store(false)
	if s.live {
		s.bl = &blFade{from: s.blLevel, to: 1, start: s.now(), dur: 160 * time.Millisecond, then: func() { s.disp.PowerOff() }}
	}
}

func (s *Screen) lock() {
	if s.modal != nil && s.modal.kind == "bye" {
		return
	}
	s.trans, s.track, s.fling, s.spring, s.pressed, s.pending = nil, nil, 0, nil, nil, nil
	s.page = "lock"
	s.stack = s.stack[:0]
	s.after = nil
	if s.modal != nil && s.modal.kind != "notify" {
		s.modal = nil
		s.modalA = fader{}
	}
	s.pin = ""
	s.pinErr = false
	s.closeKeyboard() // what was typed does not wait behind the lock
	if s.toast != "" {
		// A message from before is not for whoever holds the router now.
		s.toast, s.toastA, s.toastDirty = "", fader{}, true
	}
	s.dropScenes()
	s.needFrame = true
}

// ---- frames ----

// update makes the screen current: it advances what moves, draws pages
// that changed and puts a new frame on the panel when something changed.
func (s *Screen) update() {
	now := s.now()
	moving := false
	if s.bl != nil {
		moving = true
		s.stepBacklight(now)
	}
	if !s.screenOn {
		s.setTicker(moving)
		return
	}
	if p := s.pending; p != nil {
		if now.Before(p.at) {
			moving = true
		} else {
			s.pending, s.pressed = nil, nil
			s.needFrame = true
			p.fn()
		}
	}
	if tr := s.trans; tr != nil && !tr.drag {
		moving = true
		s.needFrame = true
		if tr.step(now) {
			s.trans = nil
			if tr.done != nil {
				tr.done()
			}
		}
	}
	if s.stepScroll(now) {
		moving = true
		s.needFrame = true
	}
	if s.modalA.step(now) {
		moving = true
		s.needFrame = true
	}
	if s.modal == nil && !s.modalA.active && s.modalA.v == 0 {
		s.modalShown = nil
	}
	if s.toast != "" && !now.Before(s.toastTo) && s.toastA.to != 0 {
		s.toastA.set(0, now, toastDur)
	}
	if s.toastA.step(now) {
		moving = true
		s.needFrame = true
	}
	if s.toast != "" && !s.toastA.active && s.toastA.v == 0 {
		s.toast = ""
	}
	if s.trans != nil {
		s.ensureRendered(s.trans.from)
		s.ensureRendered(s.trans.to)
	} else if sc := s.cur(); sc.gen != s.sceneGen {
		s.renderScene(sc)
		s.needFrame = true
	}
	if s.toast != "" && s.toastDirty {
		s.renderToast()
		s.toastDirty = false
	}
	if s.modal != nil && (s.modalDirty || s.modalShown != s.modal) {
		s.renderModal()
		s.modalDirty = false
		s.needFrame = true
	}
	if s.needFrame {
		s.compose()
		s.needFrame = false
	}
	s.setTicker(moving)
}

func (s *Screen) compose() {
	t0 := time.Now()
	f := s.frame
	if s.trans != nil {
		s.trans.compose(f)
	} else {
		composeScene(f, s.cur(), 0, 0)
	}
	if p := s.pressed; p != nil && !p.modal && s.trans == nil {
		lighten(f, p.r, p.radius)
	}
	s.composeKeyboard(f)
	if s.modalShown != nil && s.modalA.v > 0 {
		a := s.modalA.v
		dy := 0
		if s.modalShown.kind != "bye" {
			dimCols(f, 0, W, 0.62*a)
			dy = int(math.Round(22 * (1 - a)))
		}
		blendLayer(f, s.modalLayer.img, s.modalBox, dy, a)
		if p := s.pressed; p != nil && p.modal {
			lighten(f, p.r, p.radius)
		}
	}
	if s.toast != "" && s.toastA.v > 0 && s.toastLayer != nil {
		blendLayer(f, s.toastLayer.img, s.toastBox, 278+int(math.Round(16*(1-s.toastA.v))), s.toastA.v)
	}
	if s.fb != nil {
		if err := s.fb.Push(f); err != nil {
			s.log.Printf("framebuffer: %v", err)
		}
	}
	d := time.Since(t0)
	s.stats.frames++
	s.stats.total += d
	s.stats.longest = max(s.stats.longest, d)
}

// setTicker runs the frame clock while something moves on its own.
func (s *Screen) setTicker(on bool) {
	switch {
	case on && s.frameT == nil:
		s.frameT = time.NewTicker(frameEvery)
		s.frameC = s.frameT.C
		s.stats = frameStats{start: time.Now()}
	case !on && s.frameT != nil:
		s.frameT.Stop()
		s.frameT, s.frameC = nil, nil
		if st := s.stats; st.frames > 4 {
			el := time.Since(st.start)
			s.log.Printf("motion: %d frames in %v (%.0f fps), compose avg %v, max %v", st.frames, el.Round(time.Millisecond),
				float64(st.frames)/el.Seconds(), (st.total / time.Duration(st.frames)).Round(10*time.Microsecond),
				st.longest.Round(10*time.Microsecond))
		}
		s.stats = frameStats{start: time.Now()}
	}
}

func (s *Screen) stepBacklight(now time.Time) {
	b := s.bl
	t := clamp01(float64(now.Sub(b.start)) / float64(b.dur))
	lvl := b.from + int(math.Round(float64(b.to-b.from)*easeOutCubic(t)))
	if lvl != s.blLevel && s.live {
		s.disp.SetBrightness(lvl)
	}
	s.blLevel = lvl
	if t >= 1 {
		s.bl = nil
		if b.then != nil {
			b.then()
		}
	}
}

// stepScroll moves a page that coasts after a flick, or springs back
// after being pulled past its end.
func (s *Screen) stepScroll(now time.Time) bool {
	sc := s.cur()
	if sp := s.spring; sp != nil {
		t := clamp01(float64(now.Sub(sp.start)) / float64(sp.dur))
		sc.scroll = sp.from + (sp.to-sp.from)*easeOutCubic(t)
		if t >= 1 {
			s.spring = nil
		}
		return true
	}
	if s.fling == 0 {
		return false
	}
	dt := min(now.Sub(s.flingAt).Seconds(), 0.05)
	s.flingAt = now
	sc.scroll += s.fling * dt
	s.fling *= math.Exp(-dt / flingTau)
	maxS := float64(sc.maxScroll)
	switch {
	case sc.scroll < 0 || sc.scroll > maxS:
		edge := 0.0
		if sc.scroll > maxS {
			edge = maxS
		}
		// Overshoot a little, as far as the speed carries, and come back.
		over := min(math.Abs(s.fling)*0.035, 36)
		if edge == 0 {
			sc.scroll = -over
		} else {
			sc.scroll = edge + over
		}
		s.fling = 0
		s.spring = &scrollAnim{from: sc.scroll, to: edge, start: now, dur: 360 * time.Millisecond}
	case math.Abs(s.fling) < 12:
		s.fling = 0
	}
	return true
}

// ---- the finger ----

// hitAt finds what is under a point: the dialog first, then the bar on
// top, then the page.
func (s *Screen) hitAt(x, y int) (hit, image.Rectangle, bool, bool) {
	p := image.Pt(x, y)
	if s.modal != nil {
		for i := len(s.modalHits) - 1; i >= 0; i-- {
			if p.In(s.modalHits[i].r) {
				return s.modalHits[i], s.modalHits[i].r, true, true
			}
		}
		return hit{}, image.Rectangle{}, true, false
	}
	sc := s.cur()
	if y < sc.chromeH {
		for i := len(sc.chromeHits) - 1; i >= 0; i-- {
			if p.In(sc.chromeHits[i].r) {
				return sc.chromeHits[i], sc.chromeHits[i].r, false, true
			}
		}
		return hit{}, image.Rectangle{}, false, false
	}
	off := sc.chromeH - int(math.Round(sc.scroll))
	cp := image.Pt(x, y-off)
	for i := len(sc.hits) - 1; i >= 0; i-- {
		if cp.In(sc.hits[i].r) {
			r := sc.hits[i].r.Add(image.Pt(0, off)).Intersect(image.Rect(0, sc.chromeH, W, H))
			return sc.hits[i], r, false, true
		}
	}
	return hit{}, image.Rectangle{}, false, false
}

func (s *Screen) onTouch(ev touchEvent) {
	if !s.screenOn {
		return
	}
	s.lastActivity = s.now()
	s.pingLPM(false)
	if s.onKeyboardPage() && s.kbdTouch(ev) {
		return
	}
	switch ev.kind {
	case touchDown:
		if s.now().Sub(s.wokeAt) < 300*time.Millisecond || s.pending != nil || s.trans != nil && !s.trans.drag {
			s.track = &touchTrack{mode: dragIgnore}
			return
		}
		t := &touchTrack{x0: ev.x, y0: ev.y, ax: ev.x, ay: ev.y, scroll0: s.cur().scroll}
		if s.fling != 0 || s.spring != nil {
			t.stopFling = true // a touch stops a page that coasts
			s.fling, s.spring = 0, nil
		}
		t.add(ev)
		if h, r, modal, ok := s.hitAt(ev.x, ev.y); ok {
			t.hit, t.hitRect, t.modalHit = &h, r, modal
			if h.flash && !t.stopFling {
				s.pressed = &pressedHit{r: r, radius: h.radius, modal: modal}
				s.needFrame = true
			}
		} else if modal && s.modal != nil && s.modal.kind != "bye" {
			t.modalHit = true // outside the dialog: closes it on release
		}
		s.track = t
	case touchMove:
		t := s.track
		if t == nil || t.mode == dragIgnore {
			return
		}
		t.add(ev)
		dx, dy := ev.x-t.x0, ev.y-t.y0
		if t.mode == dragUndecided {
			if abs(dx) < tapSlop && abs(dy) < tapSlop {
				return
			}
			s.pressed = nil
			s.needFrame = true
			s.startDrag(t, ev, dx, dy)
		}
		switch t.mode {
		case dragScroll:
			sc := s.cur()
			raw := t.scroll0 + float64(t.ay-ev.y)
			maxS, view := float64(sc.maxScroll), float64(H-sc.chromeH)
			switch {
			case raw < 0:
				sc.scroll = -rubber(-raw, view)
			case raw > maxS:
				sc.scroll = maxS + rubber(raw-maxS, view)
			default:
				sc.scroll = raw
			}
			s.needFrame = true
		case dragBack, dragUnlock:
			if s.trans == nil || s.trans != t.trans {
				// Something else moved the page meanwhile (ubus show, a
				// deleted message going back): the finger lets go.
				t.mode = dragIgnore
				return
			}
			if t.mode == dragBack {
				s.trans.e = clamp01(float64(ev.x-t.ax) / W)
			} else {
				s.trans.e = clamp01(float64(t.ay-ev.y) / (H * 0.85))
			}
			s.needFrame = true
		}
	case touchUp:
		t := s.track
		s.track = nil
		if t == nil || t.mode == dragIgnore {
			return
		}
		t.add(ev)
		vx, vy := t.velocity()
		now := s.now()
		if (t.mode == dragBack || t.mode == dragUnlock) && (s.trans == nil || s.trans != t.trans) {
			s.needFrame = true
			return // the page was moved by something else meanwhile
		}
		switch t.mode {
		case dragUndecided:
			s.tapped(t, ev)
		case dragScroll:
			sc := s.cur()
			switch {
			case sc.scroll < 0 || sc.scroll > float64(sc.maxScroll):
				to := 0.0
				if sc.scroll > 0 {
					to = float64(sc.maxScroll)
				}
				s.spring = &scrollAnim{from: sc.scroll, to: to, start: now, dur: 380 * time.Millisecond}
			case math.Abs(vy) > 80 && sc.maxScroll > 0:
				s.fling, s.flingAt = max(min(-vy, 6000), -6000), now
			}
			s.needFrame = true
		case dragBack:
			tr := s.trans
			if tr.e > 0.33 || vx > 450 {
				tr.animate(now, 1, func() {
					s.popState()
					s.dropScenes()
				})
			} else {
				tr.animate(now, 0, nil)
			}
		case dragUnlock:
			tr := s.trans
			if tr.e > 0.22 || vy < -450 {
				state := s.unlockedState
				if s.needPIN() {
					state = s.toPIN
				}
				tr.animate(now, 1, func() {
					state()
					s.dropScenes()
				})
			} else {
				tr.animate(now, 0, nil)
			}
		}
		s.needFrame = true
	}
}

// startDrag decides what a moving finger does.
func (s *Screen) startDrag(t *touchTrack, ev touchEvent, dx, dy int) {
	t.mode = dragIgnore
	if s.modal != nil {
		return
	}
	t.ax, t.ay = ev.x, ev.y
	vertical := abs(dy) >= abs(dx)
	switch {
	case s.page == "lock" && (vertical && dy < 0 || !vertical):
		to := "home"
		var stack []route
		if s.needPIN() {
			to = "pin"
		} else if s.after != nil {
			to, stack = "app", []route{*s.after}
		}
		rt := route{}
		if len(stack) > 0 {
			rt = stack[0]
		}
		dst := s.scene(to, rt)
		s.ensureRendered(s.cur())
		s.ensureRendered(dst)
		s.trans = &transition{kind: "up", from: s.cur(), to: dst, drag: true}
		t.mode, t.trans = dragUnlock, s.trans
		if !vertical {
			// A sideways swipe on the lock screen unlocks as a whole.
			t.ay = ev.y + int(0.3*H)
		}
	case vertical && (s.page == "home" || s.page == "app"):
		t.mode = dragScroll
		t.scroll0 = s.cur().scroll
	case !vertical && dx > 0 && (s.page == "app" || s.page == "pin"):
		page, rt, ok := s.backTarget()
		if !ok {
			return
		}
		dst := s.scene(page, rt)
		s.ensureRendered(s.cur())
		s.ensureRendered(dst)
		s.trans = &transition{kind: "pop", from: s.cur(), to: dst, drag: true}
		t.mode, t.trans = dragBack, s.trans
	}
}

// tapped finishes a touch that did not move.
func (s *Screen) tapped(t *touchTrack, ev touchEvent) {
	if t.stopFling {
		return
	}
	h, r, modal, ok := s.hitAt(ev.x, ev.y)
	switch {
	case !ok && t.modalHit && s.modal != nil && s.modal.kind != "bye" && t.hit == nil:
		s.setModal(nil) // a tap outside the dialog closes it
	case ok && t.hit != nil && r == t.hitRect && modal == t.modalHit:
		if s.page != "pin" && !s.onKeyboardPage() {
			// Not on the passcode page or the keyboard: their keys are
			// always in the same places, so their coordinates would spell
			// what was typed.
			s.log.Printf("tap %d,%d", ev.x, ev.y)
		}
		if h.flash {
			// Let the pressed state be seen before the page changes.
			s.pressed = &pressedHit{r: r, radius: h.radius, modal: modal}
			s.pending = &pendingTap{fn: h.fn, at: s.now().Add(tapFlash)}
		} else {
			s.pressed = nil
			h.fn()
		}
	default:
		s.pressed = nil
	}
	s.needFrame = true
}
