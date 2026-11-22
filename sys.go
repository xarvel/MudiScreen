package main

import (
	"bufio"
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the router has no zoneinfo; uci keeps the zone name
)

func runCmd(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func uciGet(key string) string {
	out, err := runCmd(3*time.Second, "uci", "-q", "get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// uciShow returns option → value for `uci -q show <what>` (values unquoted).
func uciShow(what string) map[string]string {
	m, _ := uciShowErr(what)
	return m
}

// uciShowErr is uciShow that tells a failed read from an empty one.
func uciShowErr(what string) (map[string]string, error) {
	out, err := runCmd(3*time.Second, "uci", "-q", "show", what)
	m := map[string]string{}
	if err != nil {
		return m, err
	}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[k] = unquoteUCI(v)
	}
	return m, nil
}

// unquoteUCI undoes `uci show` quoting: 'abc' → abc, 'it'\”s' → it's.
func unquoteUCI(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		v = v[1 : len(v)-1]
		v = strings.ReplaceAll(v, `'\''`, `'`)
	}
	return v
}

// ---- log ----

func openLog(path string) (*log.Logger, func()) {
	if path == "" || path == "-" {
		return log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds), func() {}
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds), func() {}
	}
	_ = f.Chmod(0o600) // O_CREATE leaves an older file's mode as it was
	// A service's stderr goes to the system log: only a terminal gets a copy.
	var w io.Writer = f
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		w = io.MultiWriter(f, os.Stderr)
	}
	return log.New(w, "", log.LstdFlags|log.Lmicroseconds), func() { f.Close() }
}

// The web admin sends the lock screen passcode in config_update; it must not
// reach the log.
var secretRe = regexp.MustCompile(`(?i)("[^"]*(pass|pin|key|secret)[^"]*"\s*:\s*)("(?:[^"\\]|\\.)*"|[^,}\]]+)`)

func redact(s string) string { return secretRe.ReplaceAllString(s, `$1"***"`) }

// ---- processes ----

// pidsOf returns the processes with this comm name, this one excluded.
func pidsOf(comm string) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		if readTrim(filepath.Join("/proc", e.Name(), "comm")) == comm {
			pids = append(pids, pid)
		}
	}
	return pids
}

func procRunning(comm string) bool { return len(pidsOf(comm)) > 0 }

// stopBootAnimation ends GL's boot animation (screen_boot, started in
// preinit), as gl_screen does through platform.sh kill_boot once it is up:
// USR1 finishes the animation, then the process goes.
func stopBootAnimation(logger *log.Logger) {
	pids := pidsOf("screen_boot")
	if len(pids) == 0 {
		return
	}
	logger.Printf("boot animation still running (%v): ending it", pids)
	for _, p := range pids {
		_ = syscall.Kill(p, syscall.SIGUSR1)
	}
	time.Sleep(time.Second)
	for _, p := range pidsOf("screen_boot") {
		_ = syscall.Kill(p, syscall.SIGKILL)
	}
}

func pidAlive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

// ---- time zone ----

func loadLocation() *time.Location {
	if zn := uciGet("system.@system[0].zonename"); zn != "" {
		if loc, err := time.LoadLocation(strings.ReplaceAll(zn, " ", "_")); err == nil {
			return loc
		}
	}
	// Fall back to the C library's idea of the offset (from /etc/TZ).
	if out, err := runCmd(2*time.Second, "date", "+%z"); err == nil {
		s := strings.TrimSpace(out)
		if len(s) == 5 {
			h, _ := strconv.Atoi(s[1:3])
			m, _ := strconv.Atoi(s[3:5])
			off := h*3600 + m*60
			if s[0] == '-' {
				off = -off
			}
			return time.FixedZone(s, off)
		}
	}
	return time.Local
}

// ---- control FIFO (tests) ----

func readCtl(path string, out chan<- string, logger *log.Logger) {
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		logger.Printf("ctl: %v", err)
		return
	}
	for {
		f, err := os.Open(path) // blocks until a writer appears
		if err != nil {
			logger.Printf("ctl: %v", err)
			return
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				out <- line
			}
		}
		f.Close()
	}
}
