package main

import (
	"path/filepath"
	"testing"
)

func TestCatalog(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "share", "apps", "volte")
	writeFile(t, filepath.Join(app, "i18n", "ru.tsv"), "# app's own\nCalls are on\tЗвонки на связи (app)\nMissed call\tПропущенный звонок\n")
	// A language pack wins over the app's own file; /etc wins over both.
	writeFile(t, filepath.Join(dir, "share", "i18n", "ru", "volte.tsv"),
		"Calls are on\tЗвонки на связи\nmissed: {}\tпропущенных: {}\n{} h {} min\t{} ч {} мин\n{} min\t{} мин\n")
	writeFile(t, filepath.Join(dir, "etc", "i18n", "ru", "volte.tsv"), "{} of {}\t{2} из {1}\n")
	m := Manifest{ID: "volte", dir: app}
	paths := catalogPaths(m, "ru", []string{filepath.Join(dir, "etc", "apps"), filepath.Join(dir, "share", "apps")})
	c := loadCatalog(paths)
	if c == nil {
		t.Fatal("no catalog")
	}
	for in, want := range map[string]string{
		"Calls are on":  "Звонки на связи",
		"Missed call":   "Пропущенный звонок",
		"missed: 6":     "пропущенных: 6",
		"3 h 10 min":    "3 ч 10 мин", // the longer pattern goes first
		"10 min":        "10 мин",
		"2 of 7":        "7 из 2",
		"something new": "something new",
		"":              "",
	} {
		if got := c.tr(in); got != want {
			t.Errorf("tr(%q) = %q, want %q", in, got, want)
		}
	}
	v := View{Card: &Card{Title: "Calls are on", Subtitle: "missed: 2"},
		Pages: map[string]*Page{"main": {Title: "Missed call", Items: []Item{
			{Type: "info", Label: "10 min", Action: &Action{ID: "x", Confirm: "Missed call"}}}}}}
	c.apply(&v)
	if v.Card.Title != "Звонки на связи" || v.Card.Subtitle != "пропущенных: 2" || v.Pages["main"].Title != "Пропущенный звонок" ||
		v.Pages["main"].Items[0].Label != "10 мин" || v.Pages["main"].Items[0].Action.Confirm != "Пропущенный звонок" {
		t.Fatalf("apply: %+v %+v", v.Card, v.Pages["main"].Items[0])
	}
	if loadCatalog([]string{filepath.Join(dir, "missing.tsv")}) != nil {
		t.Fatal("catalog from nothing")
	}
	var none *catalog
	if none.tr("x") != "x" {
		t.Fatal("nil catalog must keep the text")
	}
	none.apply(&v)
}

func TestUCIScreenValue(t *testing.T) {
	for _, c := range []struct {
		key  string
		in   any
		want string
		ok   bool
	}{
		{"BRIGHTNESS", float64(60), "60", true},
		{"BRIGHTNESS", int64(60), "60", true},
		{"ALWAYS_ON", true, "1", true},
		{"ENABLE_PASSCODE", false, "0", true},
		{"PASSCODE", "1234", `"1234"`, true},
		{"PASSCODE", `"0042"`, `"0042"`, true},
		{"SIGNATURE", "it's", "", false},
		{"AUTO_LOCK_TIME", "120", "120", true},
		{"BRIGHTNESS", 1.5, "", false},
	} {
		got, ok := uciScreenValue(c.key, c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("uciScreenValue(%s, %#v) = %q %v, want %q %v", c.key, c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNiceCeilAndNumber(t *testing.T) {
	for in, want := range map[float64]float64{0: 1, 0.7: 1, 1: 1, 1.3: 2, 2.2: 2.5, 3: 5, 7: 10, 12: 20, 230: 250, 0.031: 0.05} {
		if got := niceCeil(in); got < want*0.9999 || got > want*1.0001 {
			t.Errorf("niceCeil(%v) = %v, want %v", in, got, want)
		}
	}
	s := &Screen{lang: "ru"}
	if s.number(2.5) != "2,5" || s.number(250) != "250" || s.number(0.05) != "0,05" || s.number(20) != "20" {
		t.Fatalf("number: %q %q %q %q", s.number(2.5), s.number(250), s.number(0.05), s.number(20))
	}
}

func TestParseViewChartLimits(t *testing.T) {
	v, err := parseView([]byte(`{"pages":{"p":{"title":"T","items":[{"type":"chart","span":9999,
		"series":[{"values":[1,2,3]},{},{},{},{},{}]}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	it := v.Pages["p"].Items[0]
	if len(it.Series) != maxSeries || it.Span != maxPoints || len(it.Series[0].Values) != 3 {
		t.Fatalf("%+v", it)
	}
}
