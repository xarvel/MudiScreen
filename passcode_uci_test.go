package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every wake reads gl_screen.generic again. A read that fails (a timeout
// under load, no memory to fork) keeps the settings the screen had: the
// passcode never comes off because uci did not answer.
func TestPasscodeKeptWhenUCIFails(t *testing.T) {
	bin := t.TempDir()
	uci := filepath.Join(bin, "uci")
	good := "#!/bin/sh\n" +
		"echo \"gl_screen.generic=cover\"\n" +
		"echo \"gl_screen.generic.ENABLE_PASSCODE='1'\"\n" +
		"echo \"gl_screen.generic.PASSCODE='\\\"1234\\\"'\"\n" +
		"echo \"gl_screen.generic.AUTO_LOCK_TIME='60'\"\n"
	bad := "#!/bin/sh\nexit 1\n" // or: sleep 5 (the 3 s timeout), or fork failing
	writeExec := func(body string) {
		if err := os.WriteFile(uci, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec(good)
	if err := os.WriteFile(filepath.Join(bin, "ubus"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	s, clk := testScreen(t, false)
	s.live = true
	s.fb = &Framebuffer{cur: make([]byte, W*H*2), prev: make([]byte, W*H*2)}
	s.disp = &Display{log: log.New(io.Discard, "", 0)}
	s.sleep("test")
	clk.add(time.Second)
	s.wake("power key")
	if !s.needPIN() {
		t.Fatalf("setup: the passcode is not on: %+v", s.cfg)
	}
	s.sleep("power key")

	writeExec(bad) // one failed read of uci
	clk.add(time.Second)
	s.wake("power key")
	clk.add(time.Second)
	s.update()
	// The person holding the router taps the lock screen.
	s.onTouch(clk.ev(touchDown, 120, 160))
	clk.add(50 * time.Millisecond)
	s.onTouch(clk.ev(touchUp, 120, 160))
	s.update()
	if s.page != "pin" {
		t.Fatalf("after one failed uci read the lock screen opens without the passcode: page %q, PasscodeOn %v", s.page, s.cfg.PasscodeOn)
	}
}
