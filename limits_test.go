package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Past maxNotes, the same notes stay on every poll (in name order), not
// whichever the map gives first.
func TestNotesPastTheCapAreStable(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"sms":{`)
	for i := 0; i < 600; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"GMS1.%04d":{"preview":"Note %d"}`, i, i)
	}
	b.WriteString(`}}`)
	kept := func() string {
		v, err := parseView([]byte(b.String()))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for n := range v.SMS {
			names = append(names, n)
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}
	first := kept()
	for i := 0; i < 5; i++ {
		if kept() != first {
			t.Fatalf("600 notes: the %d kept differ between two polls of the same output", maxNotes)
		}
	}
}

// An app's output is read up to 1 MiB, also through exec's io.Copy.
func TestAppOutputCap(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(out, bytes.Repeat([]byte("Your bill is paid. "), 100000), 0o644); err != nil {
		t.Fatal(err)
	}
	m := Manifest{ID: "notes", Command: "/bin/cat", Args: []string{out}, dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, _ := m.run(ctx, "ru", "")
	if len(raw) > 1<<20 {
		t.Fatalf("an app printed %d bytes, the screen kept %d (cap %d)", 3200000, len(raw), 1<<20)
	}
}

// A huge preview costs no more to fit into its inbox row than a short one:
// it is drawn on the UI goroutine in every frame that shows the row.
func TestHugePreviewIsCheap(t *testing.T) {
	f := testFonts(t).Face(Medium, 12)
	sub := smsPreview(strings.Repeat("Your bill is paid. ", 80000)) // what the app sends as preview
	start := time.Now()
	_ = fit(f, sub, W-60)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("one inbox row with a %d-byte preview: fit took %v", len(sub), d.Round(time.Millisecond))
	}
}
