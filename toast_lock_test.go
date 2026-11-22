package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An action may run for up to a minute. If the screen locked meanwhile, its
// toast says only "Done": what the action printed is for whoever unlocks,
// not for whoever wakes the router.
func TestActionToastBehindLock(t *testing.T) {
	bin := t.TempDir()
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("uci", "#!/bin/sh\n"+
		"echo \"gl_screen.generic.ENABLE_PASSCODE='1'\"\n"+
		"echo \"gl_screen.generic.PASSCODE='\\\"1234\\\"'\"\n")
	stub("ubus", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "tr")
	writeFile(t, filepath.Join(appDir, "app.json"), `{"id":"courier","name":"Courier","command":"app.sh"}`)
	writeFile(t, filepath.Join(appDir, "app.sh"), "#!/bin/sh\ncase \"$1\" in\n"+
		"status) echo '{}' ;;\n"+
		"action) sleep 0.5; echo 'Your pickup code is 4821' ;;\nesac\n")
	if err := os.Chmod(filepath.Join(appDir, "app.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	s, clk := testScreen(t, false)
	s.appDirs = []string{filepath.Join(dir, "apps")}
	ms, _ := loadManifests(s.appDirs, true)
	st := &appState{m: ms[0], running: map[string]bool{}}
	s.apps, s.appByID["tr"] = []*appState{st}, st
	v, _ := parseView([]byte(`{"pages":{"main":{"title":"T","items":[
	  {"type":"button","label":"Track","action":{"id":"track"}}]}}}`))
	st.view, st.haveView = v, true
	s.live = true
	s.fb = &Framebuffer{cur: make([]byte, W*H*2), prev: make([]byte, W*H*2)}
	s.disp = &Display{log: log.New(io.Discard, "", 0)}
	s.cfg.PasscodeOn, s.cfg.Passcode = true, "1234"

	s.open("tr", "main")
	sc := s.cur()
	s.renderScene(sc)
	sc.hits[0].fn() // the owner taps Track (behind the passcode)
	key := func() {
		reply := make(chan map[string]any, 1)
		s.onUbus(ubusCall{object: "gl_screen", method: "set", data: map[string]any{"method": "power key short press"}, raw: "(test)", reply: reply})
	}
	key() // and puts the router down: screen off, locked
	clk.add(10 * time.Second)
	key() // someone else wakes it: the lock screen
	if !s.locked() || !s.screenOn {
		t.Fatalf("setup: locked %v on %v", s.locked(), s.screenOn)
	}
	deadline := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case r := <-s.results:
			s.onAppResult(r)
			done = r.action != nil
		case <-deadline:
			t.Fatal("no action result")
		}
	}
	s.update()
	if s.locked() && strings.Contains(s.toast, "4821") {
		t.Fatalf("the action's output is on the lock screen, before the passcode: toast %q (page %q)", s.toast, s.page)
	}
}
