package main

// traffic.json survives a reset at a bad moment: it is written with fsync,
// with a copy beside it, and a file that does not parse is put aside and
// the copy read instead. With it would go the history and the billing
// period's mobile data, and so the cap warning.

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDamagedStateFallsBackOnCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	d := testCollector(t) // 2026-11-21 12:00
	d.cfg = tdConfig{capGB: 10, cycleDay: 1}
	for day := 1; day <= 20; day++ {
		d.st.Days = append(d.st.Days, tdDay{Day: time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
			Sim: 450 << 20, SimOK: true, WanDown: 450 << 20})
	}
	d.applySIM(1, 100<<30, d.now())
	before := d.view("", "en").Card.Subtitle

	// The hourly save, as saveLocked does it, into a test path.
	d.mu.Lock()
	d.st.Today = d.todayRecord(40)
	raw, _ := json.Marshal(&d.st)
	d.mu.Unlock()
	if err := writeAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	// What an unclean reset right after the rename can leave: a cut file.
	if err := os.WriteFile(path, raw[:len(raw)/3], 0o600); err != nil {
		t.Fatal(err)
	}

	var logged strings.Builder
	d2 := newTrafficd(log.New(&logged, "", 0), time.UTC)
	d2.now, d2.day, d2.cfg = d.now, d.day, d.cfg
	d2.load(path)
	d2.applySIM(1, 100<<30, d2.now())
	after := d2.view("", "en").Card.Subtitle
	used, _, _ := d2.simPeriod()

	// The next hourly save goes over the damaged file: nothing to go back to.
	d2.mu.Lock()
	d2.st.Today = d2.todayRecord(40)
	raw2, _ := json.Marshal(&d2.st)
	d2.mu.Unlock()
	_ = writeAtomic(path, raw2)
	_, bak := os.Stat(path + ".bak")
	_ = io.Discard

	if len(d2.st.Days) != len(d.st.Days) {
		t.Fatalf("history lost: %d of %d days after load (%q); card before %q, after %q; period used %d MB; backup copy: %v",
			len(d2.st.Days), len(d.st.Days), strings.TrimSpace(logged.String()), before, after, used>>20, bak == nil)
	}
}
