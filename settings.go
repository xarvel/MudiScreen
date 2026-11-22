package main

// The screen's own settings page (a page of the built-in system app): the
// language, what apps put there, and what this build is. A choice applies
// at once, without a restart, and stays in uci (mudi-screen.main.lang).

import (
	"strconv"
	"strings"
	"time"
)

// langNames are the languages in their own words, as a language list shows
// them.
//
// Cyrillic on purpose: a language's own name.
var langNames = map[string]string{"en": "English", "ru": "Русский"}

// webLang is the language of GL's web admin, if the screen speaks it.
func webLang() string {
	if l := uciGet("oui-httpd.main.lang"); supportedLang(l) {
		return l
	}
	return "en"
}

// langPrefFrom turns the uci value into a choice: a language, or "" for
// the web admin's.
func langPrefFrom(v string) string {
	if supportedLang(v) {
		return v
	}
	return ""
}

// setLangPref applies a choice from the settings page and keeps it.
func (s *Screen) setLangPref(pref string) {
	pref = langPrefFrom(pref)
	s.langPref = pref
	eff := pref
	if eff == "" {
		eff = s.webLang()
	}
	s.log.Printf("language: %q (%s)", pref, eff)
	s.applyLang(eff)
	if s.live {
		go func() {
			_, err := runCmd(3*time.Second, "uci", "set", "mudi-screen.main.lang="+pref)
			if err == nil {
				_, err = runCmd(5*time.Second, "uci", "commit", "mudi-screen")
			}
			if err != nil {
				s.log.Printf("language: not saved: %v", err)
			}
		}()
	}
}

// applyLang switches the screen to a language: its own texts at once, the
// apps' texts as soon as they answer, asked again right away.
func (s *Screen) applyLang(l string) {
	if l == s.lang {
		return
	}
	s.lang = l
	for _, st := range s.apps {
		st.cat = loadCatalog(catalogPaths(st.m, l, s.appDirs))
		if st.polling {
			// An answer on its way is in the old language: ask again
			// when it comes.
			st.repoll = true
		}
	}
	s.pollApps(true)
	s.requestRefresh()
	s.invalidate()
	s.modalDirty = true
}

// checkWebLang follows a change of the web admin's language while the
// screen follows it.
func (s *Screen) checkWebLang() {
	if s.langPref != "" || !s.live {
		return
	}
	if l := s.webLang(); l != s.lang {
		s.log.Printf("language: the web admin's is now %s", l)
		s.applyLang(l)
	}
}

func (s *Screen) webLang() string {
	if s.webLangFn != nil {
		return s.webLangFn()
	}
	return webLang()
}

func (s *Screen) settingsPage() *Page {
	p := &Page{Title: s.tr("settings")}
	lang := func(label, value, pref string) Item {
		return Item{Type: "radio", Label: label, Value: value, Checked: s.langPref == pref,
			Action: &Action{ID: "core.lang", Args: []string{pref}}}
	}
	web := s.lang
	if s.langPref != "" {
		web = s.webLang()
	}
	p.Items = append(p.Items, Item{Type: "section", Label: s.tr("language")},
		lang(s.tr("lang_web"), langNames[web], ""),
		lang(langNames["en"], "", "en"),
		lang(langNames["ru"], "", "ru"),
		Item{Type: "text", Label: s.tr("lang_note"), Color: "dim"})

	// What apps put on this page, each under its name unless it brings a
	// heading of its own.
	for _, st := range s.apps {
		if !st.haveView || len(st.view.Settings) == 0 {
			continue
		}
		if st.view.Settings[0].Type != "section" {
			p.Items = append(p.Items, Item{Type: "section", Label: st.m.Name})
		}
		p.Items = append(p.Items, owned(st.m.ID, st.view.Settings)...)
	}

	var names []string
	for _, st := range s.apps {
		names = append(names, st.m.Name)
	}
	apps := "—"
	if len(names) > 0 {
		apps = strings.Join(names, ", ")
	}
	p.Items = append(p.Items, Item{Type: "section", Label: s.tr("about")},
		Item{Type: "info", Label: "MudiScreen", Value: version},
		Item{Type: "info", Label: s.tr("apps"), Value: strconv.Itoa(len(names))},
		Item{Type: "text", Label: apps, Color: "dim"})
	return p
}
