package main

// The screen core: the event loop, the apps, GL's gl_screen events and the
// screen's own ubus object. Navigation, touch and motion are in nav.go.

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/image/font"
)

// ScreenConfig is the part of uci gl_screen.generic that we honour; the web
// admin's Display page edits it.
type ScreenConfig struct {
	Brightness int // percent as GL stores it; sysfs level = +20
	AutoLock   time.Duration
	AlwaysOn   bool
	PasscodeOn bool
	Passcode   string
}

func (c ScreenConfig) level() int { return c.Brightness + 20 }

// loadScreenConfig reads the settings. Its defaults have no passcode, so a
// caller never takes them for the settings when the read failed.
func loadScreenConfig() (ScreenConfig, error) {
	cfg := ScreenConfig{Brightness: 75, AutoLock: time.Minute}
	m, err := uciShowErr("gl_screen.generic")
	if err != nil {
		return cfg, err
	}
	get := func(k string) string { return strings.Trim(m["gl_screen.generic."+k], `"`) }
	if v, err := strconv.Atoi(get("BRIGHTNESS")); err == nil {
		cfg.Brightness = min(max(v, 1), 100)
	}
	if v, err := strconv.Atoi(get("AUTO_LOCK_TIME")); err == nil && v > 0 {
		cfg.AutoLock = time.Duration(v) * time.Second
	}
	cfg.AlwaysOn = get("ALWAYS_ON") == "1"
	cfg.PasscodeOn = get("ENABLE_PASSCODE") == "1"
	cfg.Passcode = get("PASSCODE")
	return cfg, nil
}

// reloadScreenConfig reads the settings again. A failed read keeps what the
// screen had: the passcode never comes off because uci did not answer.
func (s *Screen) reloadScreenConfig() {
	cfg, err := loadScreenConfig()
	if err != nil {
		s.log.Printf("config: not read, the previous settings stay: %v", err)
		return
	}
	s.cfg, s.cfgUnknown = cfg, false
}

type route struct{ app, page string }

type Modal struct {
	kind   string // power, confirm, alert, notify, bye
	title  string
	text   string
	icon   string
	color  string
	yes    string
	danger bool
	onYes  func()
	until  time.Time
	target *route
	id     string // notify: what a later dismiss names it by
}

type pressedHit struct {
	r      image.Rectangle
	radius int
	modal  bool
}

type Screen struct {
	o     options
	live  bool
	log   *log.Logger
	fonts *Fonts
	fb    *Framebuffer
	disp  *Display
	cfg   ScreenConfig
	loc   *time.Location
	lang  string // the language in use
	// langPref is the choice of the settings page: a language, or "" for
	// the web admin's.
	langPref  string
	webLangFn func() string // for tests
	now       func() time.Time

	st Status

	appDirs  []string
	apps     []*appState
	appByID  map[string]*appState
	appStamp string
	gen      int
	results  chan appResult

	// Navigation: lock, pin, home, or app with the stack of app pages.
	page  string
	stack []route
	after *route // where to go once unlocked

	// Drawing and motion (render.go, anim.go, nav.go).
	cv         *Canvas
	hitDst     *[]hit
	scenes     map[string]*scene
	sceneGen   int
	frame      *image.RGBA
	needFrame  bool
	trans      *transition
	track      *touchTrack
	fling      float64 // px/s of a page in motion
	flingAt    time.Time
	spring     *scrollAnim
	pressed    *pressedHit
	kbd        *keyboard // the on-screen keyboard, while its page is open
	wifi       wifiState
	wifiCh     chan wifiResult
	pending    *pendingTap
	bl         *blFade
	blLevel    int
	frameT     *time.Ticker
	frameC     <-chan time.Time
	stats      frameStats
	touchCh    chan touchEvent
	modal      *Modal
	modalShown *Modal
	modalDirty bool
	modalLayer *Canvas
	modalHits  []hit
	modalBox   image.Rectangle
	modalA     fader
	toast      string
	toastTo    time.Time
	toastLayer *Canvas
	toastDirty bool
	toastBox   image.Rectangle
	toastA     fader

	screenOn     bool
	awake        atomic.Bool
	wokeAt       time.Time
	lastActivity time.Time
	lastLPMPing  time.Time
	lastMinute   int
	lastStampChk time.Time

	ubusKeyAt  time.Time // last short/long press from lpm-monitor
	rawKeyAt   time.Time // last press episode read from the key itself
	rawKeyHeld time.Duration
	keyCheck   <-chan time.Time
	fallbackAt time.Time

	pin       string
	pinErr    bool
	pinFails  int
	pinWaitTo time.Time
	// The settings could not be read at start: whether there is a passcode
	// is not known, so the screen stays locked until they can.
	cfgUnknown bool

	// SMS (sms.go).
	sms        []smsMsg
	smsLoaded  bool
	smsLoading bool
	smsErr     string
	smsAt      time.Time
	smsCh      chan smsResult
	smsCount   int // the modem's count of new messages, -1 before the first status

	refresh chan struct{}
}

func newScreen(o options, fonts *Fonts, logger *log.Logger) *Screen {
	return &Screen{o: o, log: logger, fonts: fonts, loc: time.Local, now: time.Now, lang: "en",
		page: "lock", refresh: make(chan struct{}, 1), results: make(chan appResult, 32),
		appByID: map[string]*appState{}, cfg: ScreenConfig{Brightness: 75, AutoLock: time.Minute},
		scenes: map[string]*scene{}, frame: image.NewRGBA(image.Rect(0, 0, W, H)), touchCh: make(chan touchEvent, 256),
		smsCh: make(chan smsResult, 8), smsCount: -1, wifiCh: make(chan wifiResult, 8)}
}

func (s *Screen) face(w weight, size float64) font.Face { return s.fonts.Face(w, size) }

func runLive(o options) int {
	logger, closeLog := openLog(o.logPath)
	defer closeLog()
	fonts, err := LoadFonts(o.fonts)
	if err != nil {
		logger.Printf("fonts: %v", err)
		return 1
	}
	fb, err := OpenFB(o.fb)
	if err != nil {
		logger.Printf("framebuffer: %v", err)
		return 1
	}
	defer fb.Close()

	stopBootAnimation(logger)
	s := newScreen(o, fonts, logger)
	s.live = true
	s.fb = fb
	s.disp = &Display{blDir: o.backlight, blank: o.blank, log: logger}
	cfg, err := loadScreenConfig()
	for i := 0; err != nil && i < 5; i++ {
		time.Sleep(time.Second)
		cfg, err = loadScreenConfig()
	}
	s.cfg = cfg
	if err != nil {
		// Whether there is a passcode is not known: the screen stays
		// locked until uci answers (needPIN, pinKey).
		s.cfgUnknown = true
		logger.Printf("config: gl_screen.generic not read (%v): locked until it is", err)
	}
	s.loc = loadLocation()
	s.langPref = langPrefFrom(o.lang)
	if s.langPref == "" {
		s.langPref = langPrefFrom(uciGet("mudi-screen.main.lang"))
	}
	s.lang = s.langPref
	if s.lang == "" {
		s.lang = webLang()
	}
	s.appDirs = o.appDirs()
	s.reloadApps()

	go readTouch(o.touch, s.touchCh, logger)
	ubusCh := make(chan ubusCall, 16)
	var objs []ubusObject
	if o.ubusName != "" {
		objs = append(objs, ubusObject{o.ubusName, []string{"set"}})
	}
	objs = append(objs, ubusObject{"mudi-screen", []string{"refresh", "notify", "dismiss", "show", "apps", "state", "reload"}})
	go serveUbus(o.ubusSock, objs, ubusCh, logger)
	keyCh := make(chan time.Duration, 4)
	if o.powerKey != "" {
		go readPowerKey(o.powerKey, keyCh, logger)
	}
	statusCh := make(chan Status, 1)
	go pollStatus(statusCh, s.refresh, &s.awake)
	ctlCh := make(chan string, 8)
	if o.ctlPath != "" {
		go readCtl(o.ctlPath, ctlCh, logger)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	// Started from an ssh session without nohup: the end of the session
	// must not end the screen.
	signal.Ignore(syscall.SIGHUP)
	var ttl <-chan time.Time
	if o.ttl > 0 {
		ttl = time.After(o.ttl)
	}

	logger.Printf("start: version %s, pid %d, ttl %v, dry-run %v, lang %s, passcode %v, auto-lock %v, zone %s, apps %d",
		version, os.Getpid(), o.ttl, o.dryRun, s.lang, s.cfg.PasscodeOn, s.cfg.AutoLock, s.loc, len(s.apps))
	s.wake("start")
	rc := s.loop(ubusCh, keyCh, statusCh, ctlCh, sig, ttl)
	s.handOver()
	logger.Printf("exit %d", rc)
	return rc
}

// handOver leaves a blank frame for whatever draws next (gl_screen, a QR
// code of MudiVoLTE, or this screen again).
func (s *Screen) handOver() {
	if !s.screenOn || s.fb == nil {
		return
	}
	for i := range s.frame.Pix {
		s.frame.Pix[i] = bgRow[i%(W*4)]
	}
	s.fb.Invalidate()
	_ = s.fb.Push(s.frame)
}

func (s *Screen) loop(ub <-chan ubusCall, keys <-chan time.Duration, stc <-chan Status,
	ctl <-chan string, sig <-chan os.Signal, ttl <-chan time.Time) int {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	stock := time.NewTicker(5 * time.Second)
	defer stock.Stop()
	for {
		s.update()
		select {
		case ev := <-s.touchCh:
			s.onTouch(ev)
			for more := true; more; {
				select {
				case ev := <-s.touchCh:
					s.onTouch(ev)
				default:
					more = false
				}
			}
		case c := <-ub:
			s.onUbus(c)
		case held := <-keys:
			s.onRawKey(held)
		case <-s.keyCheck:
			s.keyCheck = nil
			s.rawKeyFallback()
		case st := <-stc:
			s.onStatus(st)
		case r := <-s.smsCh:
			s.onSMS(r)
		case r := <-s.wifiCh:
			s.onWifi(r)
		case r := <-s.results:
			s.onAppResult(r)
		case c := <-ctl:
			s.onCtl(c)
		case <-tick.C:
			s.onTick()
		case <-s.frameC:
		case <-stock.C:
			if procRunning("gl_screen") {
				s.log.Print("stock gl_screen is running again; giving the screen back")
				return 10
			}
		case v := <-sig:
			s.log.Printf("signal %v", v)
			return 0
		case <-ttl:
			s.log.Print("time to live is over")
			return 0
		}
	}
}

// invalidate makes every page draw again before it is shown next.
func (s *Screen) invalidate() {
	s.sceneGen++
	s.needFrame = true
}

// ---- apps ----

func (s *Screen) reloadApps() {
	ms, problems := loadManifests(s.appDirs, true)
	for _, p := range problems {
		s.log.Printf("apps: %s", p)
	}
	s.gen++
	old := s.appByID
	s.apps = nil
	s.appByID = map[string]*appState{}
	for _, m := range ms {
		st := &appState{m: m, running: map[string]bool{}, cat: loadCatalog(catalogPaths(m, s.lang, s.appDirs))}
		if prev, ok := old[m.ID]; ok && prev.m.file == m.file {
			st.view, st.haveView = prev.view, prev.haveView
		}
		s.apps = append(s.apps, st)
		s.appByID[m.ID] = st
	}
	s.appStamp = manifestStamp(s.appDirs)
	names := make([]string, 0, len(ms))
	for _, m := range ms {
		names = append(names, m.ID)
	}
	s.log.Printf("apps: %s", strings.Join(names, ", "))
	// A page of an app that is gone cannot stay open.
	for i, r := range s.stack {
		if r.app != "system" && s.appByID[r.app] == nil {
			s.stack = s.stack[:i]
			break
		}
	}
	if s.page == "app" && len(s.stack) == 0 {
		s.page = "home"
	}
	s.pollApps(true)
	s.invalidate()
}

func (s *Screen) pollApps(force bool) {
	now := s.now()
	for _, st := range s.apps {
		if st.polling {
			continue
		}
		iv := st.m.interval()
		if s.visiblePage(st.m.ID) != "" {
			iv = st.m.pageInterval()
		}
		if st.busy() {
			iv = time.Second
		}
		if !force && now.Sub(st.lastPoll) < iv {
			continue
		}
		s.pollApp(st)
	}
}

func (s *Screen) pollApp(st *appState) {
	if !s.live {
		return
	}
	st.polling = true
	st.lastPoll = s.now()
	m, gen, lang, page := st.m, s.gen, s.lang, s.visiblePage(st.m.ID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := m.run(ctx, lang, page, "status")
		r := appResult{id: m.ID, gen: gen}
		if err == nil {
			r.view, err = parseView(out)
		}
		r.err = err
		s.results <- r
	}()
}

func (s *Screen) onAppResult(r appResult) {
	st := s.appByID[r.id]
	if r.gen != s.gen || st == nil {
		return
	}
	if r.action != nil {
		delete(st.running, r.action.key())
		msg := ""
		if r.err != nil {
			s.log.Printf("app %s: action %s: %v", r.id, r.action.ID, r.err)
			msg = s.trf("error", strings.TrimPrefix(firstLine(r.err.Error()), "exit status "))
			if s.locked() {
				msg = s.tr("action_failed")
			}
		} else {
			s.log.Printf("app %s: action %s done", r.id, r.action.ID)
			if msg = firstLine(r.out); msg != "" {
				msg = st.cat.tr(msg)
				if s.locked() {
					// The screen locked while the action ran: what it says
					// stays behind the passcode, as everything else does.
					msg = s.tr("action_done")
				}
			}
		}
		if msg != "" {
			s.showToast(msg)
		}
		s.pollApp(st)
		s.invalidate()
		return
	}
	st.polling = false
	if st.repoll {
		// Asked in the old language: drop it, ask again.
		st.repoll = false
		s.pollApp(st)
		return
	}
	if r.err != nil {
		if msg := r.err.Error(); msg != st.err {
			s.log.Printf("app %s: status: %v", r.id, r.err)
			st.err = msg
		}
	} else {
		st.cat.apply(&r.view)
		// Pages on the way back stay until the app sends them again: going
		// back then shows the page at once.
		if st.haveView {
			for _, rt := range s.stack {
				if rt.app != r.id {
					continue
				}
				if _, ok := r.view.Pages[rt.page]; !ok {
					if p := st.view.Pages[rt.page]; p != nil {
						if r.view.Pages == nil {
							r.view.Pages = map[string]*Page{}
						}
						r.view.Pages[rt.page] = p
					}
				}
			}
		}
		st.err, st.view, st.haveView = "", r.view, true
		st.ticking = hasLive(&r.view)
	}
	s.invalidate()
}

// appCoreActions are the core actions an app may use (docs/EXTENSIONS.md).
// The rest of core.* (deleting and marking SMS, the language) belongs to
// the pages the screen builds itself.
var appCoreActions = map[string]bool{"core.power": true, "core.lock": true, "core.sleep": true, "core.home": true}

// act runs an action, asking first when it wants that. app is whose item
// it is: "system" for the screen's own, an app's id for an app's item, also
// on a page of the screen's (Item.app).
func (s *Screen) act(app string, a *Action) {
	if a == nil {
		return
	}
	if strings.HasPrefix(a.ID, "core.") && app != "system" && !appCoreActions[a.ID] {
		s.log.Printf("app %s: %s is not for apps, ignored", app, a.ID)
		return
	}
	if a.Confirm != "" {
		yes := a.ConfirmLabel
		if yes == "" {
			yes = s.tr("yes")
		}
		a := a
		s.setModal(&Modal{kind: "confirm", title: a.Confirm, yes: yes, danger: a.Danger, onYes: func() {
			s.setModal(nil)
			s.doAction(app, a)
		}})
		return
	}
	s.doAction(app, a)
}

func (s *Screen) doAction(app string, a *Action) {
	if strings.HasPrefix(a.ID, "core.") {
		s.coreAction(a.ID, a.Args)
		return
	}
	s.runAction(app, a)
}

func (s *Screen) runAction(app string, a *Action) {
	st := s.appByID[app]
	if st == nil {
		return
	}
	key := a.key()
	if st.running[key] {
		return
	}
	s.log.Printf("app %s: action %s %v", app, a.ID, a.Args)
	if s.o.dryRun || !s.live {
		return
	}
	st.running[key] = true
	s.invalidate()
	m, gen, lang, page := st.m, s.gen, s.lang, s.visiblePage(st.m.ID)
	args := append([]string{"action", a.ID}, a.Args...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		out, err := m.run(ctx, lang, page, args...)
		s.results <- appResult{id: m.ID, gen: gen, action: a, out: string(out), err: err}
	}()
}

func (s *Screen) coreAction(id string, args []string) {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	switch id {
	case "core.lang":
		s.setLangPref(arg)
	case "core.sms_readall", "core.sms_delete":
		s.smsCoreAction(id, arg)
	case "core.wifi_scan", "core.wifi_join", "core.wifi_disconnect", "core.airplane":
		s.wifiCoreAction(id, args)
	case "core.power":
		s.setModal(&Modal{kind: "power"})
	case "core.lock":
		s.lock()
	case "core.sleep":
		s.sleep("core.sleep")
	case "core.home":
		s.goHome()
	}
}

// viewOf returns the current view of an app ("system" is built in).
func (s *Screen) viewOf(app string) (View, bool) {
	if app == "system" {
		return s.systemView(), true
	}
	st := s.appByID[app]
	if st == nil {
		return View{}, false
	}
	return st.view, st.haveView
}

// visiblePage is the page of the app on the screen now, or "".
func (s *Screen) visiblePage(app string) string {
	if !s.screenOn || s.page != "app" || len(s.stack) == 0 {
		return ""
	}
	if r := s.stack[len(s.stack)-1]; r.app == app {
		return r.page
	}
	return ""
}

// pingLPM tells lpm-monitor that someone uses the screen, as gl_screen does
// through platform.sh screen_active.
func (s *Screen) pingLPM(force bool) {
	if !s.live || (!force && s.now().Sub(s.lastLPMPing) < 20*time.Second) {
		return
	}
	s.lastLPMPing = s.now()
	go runCmd(3*time.Second, "ubus", "call", "lpm", "set_screen", `{"action":"active"}`)
}

func (s *Screen) requestRefresh() {
	select {
	case s.refresh <- struct{}{}:
	default:
	}
}

// ---- events ----

func (s *Screen) onTick() {
	now := s.now()
	if now.Sub(s.lastStampChk) >= 30*time.Second {
		s.lastStampChk = now
		if s.live && manifestStamp(s.appDirs) != s.appStamp {
			s.log.Print("apps: manifests changed")
			s.reloadApps()
		}
	}
	if s.modal != nil && s.modal.kind == "notify" && !s.modal.until.IsZero() && now.After(s.modal.until) {
		s.setModal(nil)
	}
	s.wifiTick(now) // a join goes on with the screen off
	if !s.screenOn {
		return
	}
	if s.anyTicking() {
		s.invalidate() // live time ({elapsed:T}) runs on
	}
	if !s.cfg.AlwaysOn && (s.modal == nil || s.modal.kind != "bye") && s.track == nil && now.Sub(s.lastActivity) >= s.cfg.AutoLock {
		s.sleep("auto-lock")
		return
	}
	s.pollApps(false)
	if m := now.In(s.loc).Minute(); m != s.lastMinute {
		s.lastMinute = m
		s.invalidate()
	}
	if s.page == "pin" && now.Before(s.pinWaitTo.Add(time.Second)) {
		s.invalidate() // countdown
	}
	if p := s.visiblePage("system"); (p == "sms" || strings.HasPrefix(p, "sms:")) && now.Sub(s.smsAt) >= 10*time.Second {
		s.loadSMS()
	}
}

func (s *Screen) onStatus(st Status) {
	prev := s.smsCount
	s.st = st
	s.smsCount = st.SMSNew
	if prev >= 0 && st.SMSNew > prev {
		s.log.Printf("sms: %d new", st.SMSNew)
		if s.screenOn {
			s.showToast(s.tr("sms_new_toast"))
		}
		if p := s.visiblePage("system"); p == "sms" || strings.HasPrefix(p, "sms:") {
			s.loadSMS()
		}
	}
	s.invalidate()
}

// onRawKey gets a press episode from the key itself. lpm-monitor normally
// reports the same press through ubus within a moment; only when it does
// not, rawKeyFallback acts.
func (s *Screen) onRawKey(held time.Duration) {
	s.log.Printf("power key: held %v", held.Round(time.Millisecond))
	s.rawKeyAt, s.rawKeyHeld = s.now(), held
	s.keyCheck = time.After(700 * time.Millisecond)
}

func (s *Screen) rawKeyFallback() {
	if s.ubusKeyAt.After(s.rawKeyAt.Add(-6 * time.Second)) {
		return // lpm-monitor reported it
	}
	s.fallbackAt = s.now()
	switch held := s.rawKeyHeld; {
	case held < time.Second:
		s.log.Print("power key: no call from lpm-monitor, short press fallback")
		if s.screenOn {
			s.sleep("power key fallback")
		} else {
			s.wake("power key fallback")
		}
	case held < 5*time.Second:
		s.log.Print("power key: no call from lpm-monitor, long press fallback")
		s.wake("power key fallback")
		if s.modal == nil || s.modal.kind != "bye" {
			s.setModal(&Modal{kind: "power"})
		}
	}
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	return fmt.Sprint(v)
}

func (s *Screen) onUbus(c ubusCall) {
	var answer map[string]any
	defer func() { c.reply <- answer }()
	if c.object == "mudi-screen" {
		answer = s.onScreenCall(c)
		s.needFrame = true
		return
	}
	s.log.Printf("ubus %s %s %s", c.object, c.method, redact(c.raw))
	m := strings.ToLower(str(c.data["method"]))
	switch {
	case strings.Contains(m, "long press"):
		s.ubusKeyAt = s.now()
		if s.now().Sub(s.fallbackAt) < 3*time.Second {
			s.log.Print("power key: already handled by the fallback")
			break
		}
		s.wake("power key long press")
		if s.modal == nil || s.modal.kind != "bye" {
			s.setModal(&Modal{kind: "power"})
		}
	case strings.Contains(m, "short press"):
		s.ubusKeyAt = s.now()
		if s.now().Sub(s.fallbackAt) < 3*time.Second {
			s.log.Print("power key: already handled by the fallback")
			break
		}
		if s.screenOn {
			s.sleep("power key")
		} else {
			s.wake("power key")
		}
	case strings.Contains(m, "power key"):
		// The press edge; the classification follows.
	case m == "reload" || m == "config_update":
		s.applyConfig(c.data)
	case strings.HasPrefix(m, "language"):
		// The web admin's language changed.
		s.checkWebLang()
	case strings.Contains(m, "sms"):
		s.requestRefresh()
		if p := s.visiblePage("system"); p == "sms" || strings.HasPrefix(p, "sms:") {
			s.loadSMS()
		}
	case m == "poweroff":
		s.wake(m)
		s.setModal(&Modal{kind: "bye", title: s.tr("bye_off")})
	case m == "turnoff" || m == "timer_turnoff":
		s.sleep(m)
	case m == "turnon" || m == "timer_turnon":
		s.wake(m)
	case strings.HasPrefix(m, "power connected"):
		if s.screenOn {
			s.showToast(s.tr("charger_on"))
		}
	case m == "power disconnect":
		if s.screenOn {
			s.showToast(s.tr("charger_off"))
		}
	case m == "critical battery":
		s.wake(m)
		s.setModal(&Modal{kind: "alert", title: s.tr("bat_crit_t"), text: s.tr("bat_crit"), color: "red"})
	case m == "low battery":
		s.wake(m)
		s.setModal(&Modal{kind: "alert", title: s.tr("bat_low_t"), text: s.tr("bat_low"), color: "orange"})
	case m == "clear low battery" || m == "clear critical battery":
		if s.modal != nil && s.modal.kind == "alert" {
			s.setModal(nil)
		}
	}
	s.needFrame = true
}

// onScreenCall handles the mudi-screen ubus object, the push side of the
// app API.
func (s *Screen) onScreenCall(c ubusCall) map[string]any {
	d := c.data
	switch c.method {
	case "refresh":
		if id := str(d["app"]); id != "" {
			if st := s.appByID[id]; st != nil && !st.polling {
				s.pollApp(st)
			}
		} else {
			s.pollApps(true)
		}
		s.requestRefresh()
	case "notify":
		s.log.Printf("notify %s", redact(c.raw))
		timeout := 30
		if v, err := strconv.Atoi(str(d["timeout"])); err == nil && v > 0 {
			timeout = min(v, 3600)
		}
		var cat *catalog
		if st := s.appByID[str(d["app"])]; st != nil {
			cat = st.cat
		}
		m := &Modal{kind: "notify", title: cat.tr(str(d["title"])), text: cat.tr(str(d["text"])), icon: str(d["icon"]),
			color: str(d["color"]), until: s.now().Add(time.Duration(timeout) * time.Second), id: str(d["id"])}
		if app, page := str(d["app"]), str(d["page"]); app != "" && page != "" {
			m.target = &route{app, page}
		}
		if d["wake"] != false {
			s.wake("notify")
		}
		if s.modal == nil || s.modal.kind == "notify" {
			s.setModal(m)
		}
	case "dismiss":
		// Takes back the notice of that id, if it is still on the display:
		// an incoming call answered, a thing that is over.
		if id := str(d["id"]); id != "" && s.modal != nil && s.modal.kind == "notify" && s.modal.id == id {
			s.log.Printf("dismiss %s", id)
			s.setModal(nil)
		}
	case "show":
		app, page := str(d["app"]), str(d["page"])
		if _, ok := s.viewOf(app); !ok && app != "system" {
			return map[string]any{"error": "no such app"}
		}
		s.wake("show")
		s.setModal(nil)
		s.open(app, page)
	case "reload":
		s.reloadApps()
	case "apps":
		var list []map[string]any
		for _, st := range s.apps {
			e := map[string]any{"id": st.m.ID, "name": st.m.Name, "order": st.m.Order, "command": st.m.Command,
				"has_view": st.haveView}
			if st.err != "" {
				e["error"] = st.err
			}
			if !st.lastPoll.IsZero() {
				e["polled_ago_ms"] = int(s.now().Sub(st.lastPoll) / time.Millisecond)
			}
			list = append(list, e)
		}
		return map[string]any{"api": apiVersion, "dirs": s.appDirs, "apps": list}
	case "state":
		p := s.page
		if p == "app" && len(s.stack) > 0 {
			r := s.stack[len(s.stack)-1]
			p = r.app + ":" + r.page
		}
		return map[string]any{"screen": s.screenOn, "page": p, "locked": s.locked(), "lang": s.lang, "api": apiVersion}
	}
	return nil
}

// applyConfig takes settings from the web admin (config_update) or re-reads
// uci (reload).
func (s *Screen) applyConfig(data map[string]any) {
	if !s.live {
		return
	}
	cfg, err := loadScreenConfig()
	if err != nil {
		s.log.Printf("config: not read, the previous settings stay: %v", err)
		cfg = s.cfg
	} else {
		s.cfgUnknown = false
	}
	if p, ok := data["params"].(map[string]any); ok {
		if c, ok := p["config"].(map[string]any); ok {
			// gl_screen keeps what the web admin sends in uci; so do we, or
			// the change is gone after a reboot and in the stock screen.
			if err := persistScreenConfig(c); err != nil {
				s.log.Printf("config: not saved: %v", err)
			}
			val := func(v any) string { return strings.Trim(str(v), `"`) }
			if v, ok := c["BRIGHTNESS"]; ok {
				if n, err := strconv.Atoi(val(v)); err == nil {
					cfg.Brightness = min(max(n, 1), 100)
				}
			}
			if v, ok := c["AUTO_LOCK_TIME"]; ok {
				if n, err := strconv.Atoi(val(v)); err == nil && n > 0 {
					cfg.AutoLock = time.Duration(n) * time.Second
				}
			}
			if v, ok := c["ALWAYS_ON"]; ok {
				cfg.AlwaysOn = val(v) == "1" || val(v) == "true"
			}
			if v, ok := c["ENABLE_PASSCODE"]; ok {
				cfg.PasscodeOn = val(v) == "1" || val(v) == "true"
			}
			if v, ok := c["PASSCODE"]; ok && val(v) != "" {
				cfg.Passcode = val(v)
			}
		}
	}
	s.cfg = cfg
	if s.screenOn && s.bl == nil {
		s.disp.SetBrightness(cfg.level())
		s.blLevel = cfg.level()
	}
	s.log.Printf("config: brightness %d, auto-lock %v, always-on %v, passcode %v", cfg.Brightness, cfg.AutoLock, cfg.AlwaysOn, cfg.PasscodeOn)
}

// onCtl takes test commands from the control FIFO. Taps and drags go
// through the same path as the touchscreen's.
func (s *Screen) onCtl(line string) {
	s.log.Printf("ctl: %s", line)
	s.lastActivity = s.now()
	f := strings.Fields(line)
	num := func(i, def int) int {
		if i < len(f) {
			if n, err := strconv.Atoi(f[i]); err == nil {
				return n
			}
		}
		return def
	}
	switch f[0] {
	case "tap":
		s.wokeAt = time.Time{}
		s.inject(num(1, 120), num(2, 160), num(1, 120), num(2, 160), 60*time.Millisecond)
	case "swipe":
		s.wokeAt = time.Time{}
		s.inject(120, 160, 120+num(1, 0), 160+num(2, 0), 180*time.Millisecond)
	case "drag":
		s.wokeAt = time.Time{}
		s.inject(num(1, 120), num(2, 160), num(3, 120), num(4, 160), time.Duration(num(5, 300))*time.Millisecond)
	case "key":
		kind := "short"
		if len(f) > 1 {
			kind = f[1]
		}
		reply := make(chan map[string]any, 1)
		s.onUbus(ubusCall{object: "gl_screen", method: "set", data: map[string]any{"method": "power key " + kind + " press"}, raw: "(ctl)", reply: reply})
	case "page":
		if len(f) > 1 {
			s.setModal(nil)
			switch f[1] {
			case "home", "lock", "pin":
				s.jump(f[1], nil)
			default:
				app, page, _ := strings.Cut(f[1], ":")
				s.jump("app", []route{{app, page}})
				if st := s.appByID[app]; st != nil && !st.polling {
					s.pollApp(st)
				}
			}
		}
	case "wake":
		s.wake("ctl")
	case "sleep":
		s.sleep("ctl")
	}
	s.needFrame = true
}

// inject plays a finger from (x0, y0) to (x1, y1) over d.
func (s *Screen) inject(x0, y0, x1, y1 int, d time.Duration) {
	ch := s.touchCh
	go func() {
		ch <- touchEvent{kind: touchDown, x: x0, y: y0, t: time.Now()}
		steps := max(int(d/(10*time.Millisecond)), 1)
		for i := 1; i <= steps; i++ {
			time.Sleep(10 * time.Millisecond)
			ch <- touchEvent{kind: touchMove, x: x0 + (x1-x0)*i/steps, y: y0 + (y1-y0)*i/steps, t: time.Now()}
		}
		ch <- touchEvent{kind: touchUp, x: x1, y: y1, t: time.Now()}
	}()
}

// ---- power ----

func (s *Screen) power(kind string) {
	s.log.Printf("power: %s", kind)
	title := s.tr("bye_off")
	if kind == "reboot" {
		title = s.tr("bye_reboot")
	}
	s.setModal(&Modal{kind: "bye", title: title})
	s.modalA = fader{v: 1}
	s.update()
	if s.o.dryRun || !s.live {
		s.log.Printf("dry-run: /sbin/%s", kind)
		return
	}
	if err := exec.Command("/sbin/" + kind).Start(); err != nil {
		s.log.Printf("power: %v", err)
		s.setModal(&Modal{kind: "alert", title: s.tr("failed"), text: err.Error(), color: "red"})
	}
}

// ---- settings from the web admin ----

// The gl_screen.generic options GL's web admin may send in config_update, and
// which of them GL keeps as quoted strings (PASSCODE '"1234"').
var (
	screenConfigKeys = map[string]bool{"BRIGHTNESS": true, "AUTO_LOCK_TIME": true, "ALWAYS_ON": true,
		"ENABLE_PASSCODE": true, "PASSCODE": true, "HOUR_FORMAT": true, "WAKE_DISPLAY_STYLE": true,
		"WALLPAPER_PAIR": true, "SIGNATURE": true, "INITIAL_SCREEN": true}
	screenConfigQuoted = map[string]bool{"PASSCODE": true, "SIGNATURE": true}
)

// uciScreenValue renders a config_update value the way gl_screen stores it.
func uciScreenValue(key string, v any) (string, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return "1", true
		}
		return "0", true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		if x == math.Trunc(x) {
			return strconv.FormatInt(int64(x), 10), true
		}
	case string:
		t := strings.Trim(x, `"`)
		if strings.ContainsAny(t, "\\\n'") {
			return "", false
		}
		if screenConfigQuoted[key] {
			return `"` + t + `"`, true
		}
		return t, true
	}
	return "", false
}

func persistScreenConfig(c map[string]any) error {
	keys := make([]string, 0, len(c))
	for k := range c {
		if screenConfigKeys[k] {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	if uciGet("gl_screen.generic") == "" {
		if _, err := runCmd(3*time.Second, "uci", "set", "gl_screen.generic=cover"); err != nil {
			return err
		}
	}
	for _, k := range keys {
		v, ok := uciScreenValue(k, c[k])
		if !ok {
			continue
		}
		if _, err := runCmd(3*time.Second, "uci", "set", "gl_screen.generic."+k+"="+v); err != nil {
			return err
		}
	}
	_, err := runCmd(5*time.Second, "uci", "commit", "gl_screen")
	return err
}

// ---- stock screen ----

const modeTool = "/usr/sbin/mudi-screen-mode"

func haveModeTool() bool {
	_, err := os.Stat(modeTool)
	return err == nil
}

// switchToStock hands the display to GL's gl_screen for good (until
// "mudi-screen-mode custom"). The tool stops this service, so it runs in a
// session of its own and outlives this process.
func (s *Screen) switchToStock() {
	s.log.Print("switching to the stock screen")
	s.setModal(&Modal{kind: "bye", title: s.tr("stock")})
	s.modalA = fader{v: 1}
	s.update()
	if s.o.dryRun || !s.live {
		s.log.Printf("dry-run: %s stock", modeTool)
		return
	}
	cmd := exec.Command(modeTool, "stock")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		s.log.Printf("stock screen: %v", err)
		s.setModal(&Modal{kind: "alert", title: s.tr("failed"), text: err.Error(), color: "red"})
		return
	}
	go cmd.Wait()
}

// ---- preview ----

// runPreview renders one frame with demo data. Apps come from -apps as
// usual, but instead of running them their demo.json is shown.
func runPreview(o options) error {
	fonts, err := LoadFonts(o.fonts)
	if err != nil {
		return err
	}
	s := newScreen(o, fonts, log.New(os.Stderr, "", 0))
	now := time.Date(2026, 11, 21, 15, 7, 0, 0, time.Local)
	s.now = func() time.Time { return now }
	s.lang = pickPreviewLang(o.lang)
	s.langPref = langPrefFrom(o.lang)
	s.webLangFn = func() string { return "en" }
	s.st = demoStatus(now)
	s.screenOn = true
	s.cfg.PasscodeOn, s.cfg.Passcode = true, "0000"
	s.appDirs = o.appDirs()
	ms, problems := loadManifests(s.appDirs, false)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "apps:", p)
	}
	for _, m := range ms {
		st := &appState{m: m, running: map[string]bool{}, cat: loadCatalog(catalogPaths(m, s.lang, s.appDirs))}
		if raw, err := os.ReadFile(filepath.Join(m.dir, "demo.json")); err == nil {
			if v, err := parseView(raw); err == nil {
				st.cat.apply(&v)
				st.view, st.haveView, st.ticking = v, true, hasLive(&v)
			} else {
				return fmt.Errorf("%s/demo.json: %w", m.dir, err)
			}
		}
		s.apps = append(s.apps, st)
		s.appByID[m.ID] = st
	}
	sort.SliceStable(s.apps, func(i, j int) bool { return s.apps[i].m.Order < s.apps[j].m.Order })
	// Made-up messages for the SMS pages.
	for i, m := range []smsMsg{
		{Name: "demo6", Number: "Paketdienst", Status: 0, Date: "26-11-21 14:02:00", Body: "Ihr Paket liegt in der Packstation 104 bereit. Abholcode: 4821. Es wird 7 Tage für Sie aufbewahrt."},
		{Name: "demo1", Number: "Bistro Lumen", Status: 0, Date: "26-11-21 13:20:05", Body: "Your table for two tonight at 19:30 is confirmed. Reply C to cancel."},
		{Name: "demo2", Number: "+447700900123", Status: 0, Date: "26-11-21 09:05:00", Body: "See you at nine at the usual place."},
		{Name: "demo3", Number: "Bank", Status: 1, Date: "26-11-20 18:11:00", Body: "Card *1234: payment of 12.50 EUR at a shop. Balance 420.00 EUR."},
		{Name: "demo4", Number: "Operator", Status: 1, Date: "26-11-12 20:19:00", Body: "Your data package has run out. Send DA100 to 3282 to buy 1 GB."},
		{Name: "demo5", Number: "+447700900456", Status: 3, Date: "26-11-20 18:30:00", Body: "On my way"},
	} {
		m.Bus, m.at = "cpu", parseSMSDate(m.Date, s.loc)
		s.sms = append(s.sms, m)
		_ = i
	}
	s.smsLoaded = true
	// Made-up networks around, for the Wi-Fi pages, and a password being
	// typed, for the keyboard.
	demoNet := func(ssid, band, enc string, signal int, saved bool) wifiNet {
		n := wifiNet{SSID: ssid, Band: band, Signal: signal, Saved: saved}
		n.Encryption.Enabled, n.Encryption.Description = enc != "", enc
		return n
	}
	s.wifi.nets = mergeNets([]wifiNet{
		demoNet("Bistro Lumen Guest", "2g", "", -58, false), demoNet("Bistro Lumen Guest", "5g", "", -61, false),
		demoNet("Nimbus-5G-8F2A", "5g", "PSK", -63, false), demoNet("Harbor Loft", "2g", "PSK", -49, true),
		demoNet("Harbor Loft", "5g", "PSK", -52, true), demoNet("Library Reading Room", "2g", "PSK", -71, false),
		demoNet("Studio 42", "5g", "SAE", -80, false),
	})
	s.wifi.scanned = true
	if o.page == "system:keyboard" {
		s.kbd = &keyboard{title: s.tr("kbd_password"), label: s.trf("kbd_for", "Bistro Lumen"), min: 8, max: 63,
			text: []rune("lumen2026"), press: -1}
	}
	switch o.page {
	case "home", "lock":
		s.jump(o.page, nil)
	case "pin":
		s.jump("pin", nil)
		s.pin = "12"
	case "power":
		s.jump("home", nil)
		s.setModal(&Modal{kind: "power"})
	case "confirm":
		s.jump("home", nil)
		s.setModal(&Modal{kind: "confirm", title: s.tr("confirm_off"), text: s.tr("power_note"), yes: s.tr("power_off"), danger: true})
	case "notify":
		s.jump("lock", nil)
		s.setModal(&Modal{kind: "notify", title: "Missed call", text: "1 new", icon: "phone", color: "orange",
			target: &route{"system", "net"}})
	default:
		app, page, _ := strings.Cut(o.page, ":")
		s.jump("app", []route{{app, page}})
	}
	s.modalA = fader{v: 1}
	if o.scroll > 0 {
		sc := s.cur()
		s.renderScene(sc)
		sc.scroll = min(float64(o.scroll), float64(sc.maxScroll))
	}
	if o.anim != "" {
		// A frame in the middle of a transition from the home page.
		to := s.cur()
		from := s.scene("home", route{})
		if o.anim == "up" {
			from, to = s.scene("lock", route{}), s.scene("home", route{})
		}
		s.renderScene(from)
		s.renderScene(to)
		s.trans = &transition{kind: o.anim, from: from, to: to, drag: true, e: o.progress}
		if o.anim == "pop" {
			s.trans.from, s.trans.to = to, from
		}
	}
	s.needFrame = true
	s.update()
	f, err := os.Create(o.preview)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, s.frame)
}

func pickPreviewLang(l string) string {
	if supportedLang(l) {
		return l
	}
	return "en"
}
