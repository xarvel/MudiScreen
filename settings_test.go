package main

import (
	"io"
	"log"
	"path/filepath"
	"testing"
)

func TestLanguageSetting(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "volte")
	writeFile(t, filepath.Join(appDir, "app.json"), `{"id":"volte","command":"/bin/true"}`)
	writeFile(t, filepath.Join(appDir, "i18n", "ru.tsv"), "Calls are on\tЗвонки на связи\n")
	s := newScreen(options{}, nil, log.New(io.Discard, "", 0))
	s.appDirs = []string{filepath.Join(dir, "apps")}
	ms, _ := loadManifests(s.appDirs, true)
	st := &appState{m: ms[0], running: map[string]bool{}}
	s.apps, s.appByID["volte"] = []*appState{st}, st
	s.webLangFn = func() string { return "en" }
	s.lang, s.langPref = "en", ""

	checked := func() string {
		for _, it := range s.settingsPage().Items {
			if it.Type == "radio" && it.Checked {
				return it.Action.Args[0]
			}
		}
		return "none"
	}
	if checked() != "" {
		t.Fatalf("the web admin's language is not the one checked: %q", checked())
	}

	// An answer on its way when the language changes is dropped.
	st.polling = true
	gen := s.gen
	s.act("system", &Action{ID: "core.lang", Args: []string{"ru"}})
	if s.lang != "ru" || s.langPref != "ru" || checked() != "ru" {
		t.Fatalf("after ru: lang %q pref %q checked %q", s.lang, s.langPref, checked())
	}
	if st.cat.tr("Calls are on") != "Звонки на связи" {
		t.Fatal("the app's catalog was not loaded")
	}
	if !st.repoll {
		t.Fatal("the stale answer is not marked")
	}
	s.onAppResult(appResult{id: "volte", gen: gen, view: View{Card: &Card{Title: "old"}}})
	if st.haveView || st.repoll {
		t.Fatalf("the stale answer was applied: %+v", st.view.Card)
	}
	if s.tr("settings") != "Настройки" {
		t.Fatal("the screen's own texts did not change")
	}

	// Back to the web admin's language; an unknown value means the same.
	s.setLangPref("xx")
	if s.lang != "en" || s.langPref != "" || st.cat != nil || checked() != "" {
		t.Fatalf("after xx: lang %q pref %q cat %v checked %q", s.lang, s.langPref, st.cat, checked())
	}

	// While it follows the web admin, a change there follows on wake.
	s.live = true
	s.webLangFn = func() string { return "ru" }
	s.checkWebLang()
	if s.lang != "ru" || s.langPref != "" {
		t.Fatalf("web admin change: lang %q pref %q", s.lang, s.langPref)
	}
	s.live = false
	if langPrefFrom("ru") != "ru" || langPrefFrom("de") != "" || langPrefFrom("") != "" {
		t.Fatal("langPrefFrom")
	}
}
