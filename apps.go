package main

// Apps: everything on the screen that is not part of a stock Mudi 7 comes
// from apps. An app is a manifest plus any executable:
//
//	<apps dir>/<id>/app.json   with "command" relative to that directory, or
//	<apps dir>/<id>.json       with an absolute "command".
//
// The screen runs `<command> status` and reads a View as JSON from stdout,
// and `<command> action <id> <args...>` when an item is tapped. Both get
// MUDI_SCREEN_API, MUDI_SCREEN_LANG and MUDI_SCREEN_APP in the environment.
// Apps that need to show something at once call the screen's ubus object
// (mudi-screen refresh / notify / show). See docs/EXTENSIONS.md.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var defaultAppDirs = []string{"/etc/mudi-screen/apps", "/usr/share/mudi-screen/apps"}

type Manifest struct {
	API      int      `json:"api"`
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Order    int      `json:"order"`    // place among the cards; the built-in network card is 10
	Command  string   `json:"command"`  // absolute, or relative to the manifest's directory
	Args     []string `json:"args"`     // put before "status" and "action"
	Interval float64  `json:"interval"` // seconds between status runs while the screen is on; default 5
	// PageInterval is used instead while a page of the app is open; default
	// Interval. The app learns which page from MUDI_SCREEN_PAGE.
	PageInterval float64  `json:"page_interval"`
	Requires     []string `json:"requires"` // paths that must exist, or the app is skipped
	Disabled     bool     `json:"disabled"`

	dir  string // directory of the manifest
	file string
}

func seconds(v, def float64) time.Duration {
	if v <= 0 {
		v = def
	}
	return time.Duration(min(max(v, 1), 3600) * float64(time.Second))
}

func (m Manifest) interval() time.Duration { return seconds(m.Interval, 5) }

func (m Manifest) pageInterval() time.Duration {
	if m.PageInterval <= 0 {
		return m.interval()
	}
	return seconds(m.PageInterval, 5)
}

var appIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// loadManifests reads the app directories in order; an id found in an
// earlier directory hides the same id in later ones (/etc over /usr/share).
// checkRequires is off for previews, which show demo views on any machine.
func loadManifests(dirs []string, checkRequires bool) ([]Manifest, []string) {
	var out []Manifest
	var problems []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			path := filepath.Join(dir, e.Name())
			switch {
			case e.IsDir():
				path = filepath.Join(path, "app.json")
				if _, err := os.Stat(path); err != nil {
					continue
				}
			case strings.HasSuffix(e.Name(), ".json"):
			default:
				continue
			}
			m, err := readManifest(path)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			if m.Disabled {
				continue
			}
			if missing := missingRequirement(m); checkRequires && missing != "" {
				problems = append(problems, fmt.Sprintf("%s: skipped, %s is missing", m.ID, missing))
				continue
			}
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out, problems
}

func readManifest(path string) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	m.file, m.dir = path, filepath.Dir(path)
	switch {
	case m.API != 0 && m.API != apiVersion:
		return m, fmt.Errorf("api %d is not supported (this screen speaks %d)", m.API, apiVersion)
	case !appIDRe.MatchString(m.ID):
		return m, fmt.Errorf("bad id %q", m.ID)
	case m.ID == "system" || strings.HasPrefix(m.ID, "core"):
		return m, fmt.Errorf("id %q is reserved", m.ID)
	case m.Command == "":
		return m, errors.New("no command")
	}
	if !filepath.IsAbs(m.Command) {
		m.Command = filepath.Join(m.dir, m.Command)
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Order == 0 {
		m.Order = 50
	}
	return m, nil
}

func missingRequirement(m Manifest) string {
	for _, p := range m.Requires {
		if _, err := os.Stat(p); err != nil {
			return p
		}
	}
	return ""
}

// manifestStamp changes when a manifest is added, removed or edited.
func manifestStamp(dirs []string) string {
	var b strings.Builder
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			path := filepath.Join(dir, e.Name())
			if e.IsDir() {
				path = filepath.Join(path, "app.json")
			}
			if fi, err := os.Stat(path); err == nil {
				fmt.Fprintf(&b, "%s %d %d\n", path, fi.Size(), fi.ModTime().UnixNano())
			}
		}
	}
	return b.String()
}

// limitedBuffer keeps at most 1 MiB of output. It holds its buffer in a
// field rather than embedding it: exec copies with io.Copy, which would
// use an embedded bytes.Buffer's ReadFrom and never call this Write.
type limitedBuffer struct{ buf bytes.Buffer }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := 1<<20 - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (l *limitedBuffer) Bytes() []byte  { return l.buf.Bytes() }
func (l *limitedBuffer) String() string { return l.buf.String() }

// run runs the app's command; page is the app's page on the screen now, or
// "".
func (m Manifest) run(ctx context.Context, lang, page string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, m.Command, append(append([]string{}, m.Args...), args...)...)
	cmd.Dir = m.dir
	cmd.Env = append(os.Environ(),
		"MUDI_SCREEN_API="+strconv.Itoa(apiVersion),
		"MUDI_SCREEN_LANG="+lang,
		"MUDI_SCREEN_APP="+m.ID,
		"MUDI_SCREEN_PAGE="+page)
	var out, errb limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// A command may leave a background child holding its stdout (a tunnel
	// it started, say); do not wait for that child.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err != nil {
		msg := firstLine(errb.String())
		if msg == "" {
			msg = firstLine(out.String())
		}
		if msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out.Bytes(), err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}

// appState is the screen's view of one app; it is touched only by the UI
// goroutine.
type appState struct {
	m        Manifest
	cat      *catalog // translations for the screen's language, or nil
	view     View
	haveView bool
	err      string
	lastPoll time.Time
	polling  bool
	repoll   bool            // the answer on its way is stale (the language changed): ask again
	ticking  bool            // the view has live time ({elapsed:T}): drawn again every second
	running  map[string]bool // actions in flight, by Action.key
}

func (st *appState) busy() bool {
	if len(st.running) > 0 {
		return true
	}
	for _, p := range st.view.Pages {
		for _, it := range p.Items {
			if it.Busy {
				return true
			}
		}
	}
	return false
}

type appResult struct {
	id     string
	gen    int
	action *Action // nil for status
	view   View
	out    string
	err    error
}
