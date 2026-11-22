package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifests(t *testing.T) {
	etc, share := t.TempDir(), t.TempDir()
	// A directory app with a relative command.
	writeFile(t, filepath.Join(share, "beta", "app.json"), `{"api":1,"id":"beta","order":40,"command":"run.sh"}`)
	writeFile(t, filepath.Join(share, "beta", "run.sh"), "#!/bin/sh\n")
	// The same id in /etc wins over /usr/share.
	writeFile(t, filepath.Join(share, "alpha.json"), `{"id":"alpha","command":"/bin/false","order":20}`)
	writeFile(t, filepath.Join(etc, "alpha.json"), `{"id":"alpha","command":"/bin/true","order":20,"name":"Alpha"}`)
	// Skipped: requirement missing, disabled, reserved id, bad api, bad json.
	writeFile(t, filepath.Join(share, "gamma.json"), `{"id":"gamma","command":"/bin/true","requires":["/nonexistent/x"]}`)
	writeFile(t, filepath.Join(share, "delta.json"), `{"id":"delta","command":"/bin/true","disabled":true}`)
	writeFile(t, filepath.Join(share, "system.json"), `{"id":"system","command":"/bin/true"}`)
	writeFile(t, filepath.Join(share, "future.json"), `{"api":2,"id":"future","command":"/bin/true"}`)
	writeFile(t, filepath.Join(share, "broken.json"), `{"id":`)

	ms, problems := loadManifests([]string{etc, share}, true)
	if len(ms) != 2 || ms[0].ID != "alpha" || ms[1].ID != "beta" {
		t.Fatalf("manifests %+v", ms)
	}
	if ms[0].Command != "/bin/true" || ms[0].Name != "Alpha" {
		t.Fatalf("alpha from /etc expected: %+v", ms[0])
	}
	if ms[1].Command != filepath.Join(share, "beta", "run.sh") || ms[1].Name != "beta" {
		t.Fatalf("beta: %+v", ms[1])
	}
	if len(problems) != 4 { // gamma, system, future, broken (delta is silent)
		t.Fatalf("problems %q", problems)
	}
	if manifestStamp([]string{etc, share}) == "" {
		t.Fatal("empty stamp")
	}
}

func TestParseView(t *testing.T) {
	v, err := parseView([]byte(`{"card":{"title":"T","open":"main","extra":1},
		"tiles":[{},{},{},{},{},{},{},{}],
		"pages":{"main":{"title":"M","items":[{"type":"radio","label":"x","action":{"id":"pick","args":["1"]}}]}},
		"future_field":{"a":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Card.Title != "T" || len(v.Tiles) != maxTiles || v.Pages["main"].Items[0].Action.Args[0] != "1" {
		t.Fatalf("%+v", v)
	}
	if v, err := parseView([]byte("  \n")); err != nil || v.Card != nil {
		t.Fatalf("empty output: %+v %v", v, err)
	}
	if _, err := parseView([]byte(`{"card":`)); err == nil {
		t.Fatal("broken JSON accepted")
	}
	if (&Action{ID: "a", Args: []string{"b"}}).key() == (&Action{ID: "a", Args: []string{"c"}}).key() {
		t.Fatal("action keys collide")
	}
}

func TestParseColor(t *testing.T) {
	if parseColor("green", colText) != colGreen || parseColor("#102030", colText) != rgb(0x10, 0x20, 0x30) ||
		parseColor("nonsense", colDim) != colDim {
		t.Fatal("parseColor")
	}
}

func TestEncodeTableRoundTrip(t *testing.T) {
	in := map[string]any{"s": "x", "b": true, "n": float64(7), "big": float64(1 << 40), "f": 1.5,
		"t": map[string]any{"k": "v"}, "a": []any{"p", float64(2)}}
	out, err := decodeTable(encodeTable(in))
	if err != nil {
		t.Fatal(err)
	}
	if out["s"] != "x" || out["b"] != true || out["n"] != int64(7) || out["big"] != int64(1<<40) || out["f"] != 1.5 {
		t.Fatalf("%#v", out)
	}
	if out["t"].(map[string]any)["k"] != "v" || out["a"].([]any)[1] != int64(2) {
		t.Fatalf("%#v", out)
	}
}
