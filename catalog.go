package main

// Translation catalogs. An app may print its texts in English and leave the
// translation to catalogs, which are data, not code: anyone can add a
// language for any app without touching the app.
//
//	<app dir>/i18n/<lang>.tsv              shipped with the app
//	<base>/i18n/<lang>/<app id>.tsv        language packs and local fixes
//
// where <base> is the parent of every app directory (/etc/mudi-screen and
// /usr/share/mudi-screen by default). Earlier directories win, and every
// one of them wins over the app's own file.
//
// A line is "English<TAB>translation". "{}" in the English text matches any
// text; "{}" in the translation takes the matches in order, "{1}".."{9}" by
// number. Lines starting with # and blank lines are skipped. A text that no
// line matches stays as it is.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type catPattern struct {
	re      *regexp.Regexp
	out     string
	literal int // characters outside the placeholders; longer goes first
}

type catalog struct {
	exact    map[string]string
	raw      map[string]string // every line as written, templates included
	patterns []catPattern
}

// catalogPaths lists the catalog files for an app, lowest priority first.
func catalogPaths(m Manifest, lang string, appDirs []string) []string {
	paths := []string{filepath.Join(m.dir, "i18n", lang+".tsv")}
	for i := len(appDirs) - 1; i >= 0; i-- {
		base := filepath.Dir(filepath.Clean(appDirs[i]))
		paths = append(paths, filepath.Join(base, "i18n", lang, m.ID+".tsv"))
	}
	return paths
}

var placeholderRe = regexp.MustCompile(`\\\{\\\}`)

// loadCatalog reads the files in order, later lines overriding earlier
// ones. It returns nil when no file has a usable line.
func loadCatalog(paths []string) *catalog {
	c := &catalog{exact: map[string]string{}, raw: map[string]string{}}
	pats := map[string]catPattern{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			src, dst, ok := strings.Cut(line, "\t")
			if !ok || src == "" {
				continue
			}
			c.raw[src] = dst
			if !strings.Contains(src, "{}") {
				c.exact[src] = dst
				continue
			}
			expr := "^" + placeholderRe.ReplaceAllString(regexp.QuoteMeta(src), "(.+?)") + "$"
			re, err := regexp.Compile(expr)
			if err != nil {
				continue
			}
			pats[src] = catPattern{re: re, out: dst, literal: len(strings.ReplaceAll(src, "{}", ""))}
		}
		f.Close()
	}
	for _, p := range pats {
		c.patterns = append(c.patterns, p)
	}
	sort.SliceStable(c.patterns, func(i, j int) bool {
		if c.patterns[i].literal != c.patterns[j].literal {
			return c.patterns[i].literal > c.patterns[j].literal
		}
		return c.patterns[i].re.String() < c.patterns[j].re.String()
	})
	if len(c.exact) == 0 && len(c.patterns) == 0 {
		return nil
	}
	return c
}

var argRe = regexp.MustCompile(`\{([1-9]?)\}`)

func (c *catalog) tr(s string) string {
	if c == nil || s == "" {
		return s
	}
	if t, ok := c.exact[s]; ok {
		return t
	}
	for _, p := range c.patterns {
		m := p.re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		next := 1
		return argRe.ReplaceAllStringFunc(p.out, func(ph string) string {
			n := next
			if d := ph[1 : len(ph)-1]; d != "" {
				n, _ = strconv.Atoi(d)
			} else {
				next++
			}
			if n < len(m) {
				return m[n]
			}
			return ""
		})
	}
	return s
}

// tmpl translates a text or a template ("{} of {}") as a whole, without
// matching patterns: for programs that fill templates themselves.
func (c *catalog) tmpl(s string) string {
	if c == nil {
		return s
	}
	if t, ok := c.raw[s]; ok {
		return t
	}
	return s
}

func (c *catalog) action(a *Action) {
	if a != nil {
		a.Confirm = c.tr(a.Confirm)
		a.ConfirmLabel = c.tr(a.ConfirmLabel)
	}
}

// apply translates every text of a view in place.
func (c *catalog) apply(v *View) {
	if c == nil {
		return
	}
	if cd := v.Card; cd != nil {
		cd.Title, cd.Subtitle = c.tr(cd.Title), c.tr(cd.Subtitle)
		c.action(cd.Action)
	}
	for i := range v.Tiles {
		t := &v.Tiles[i]
		t.Label, t.Value = c.tr(t.Label), c.tr(t.Value)
		c.action(t.Action)
	}
	for i := range v.Lock {
		v.Lock[i].Text = c.tr(v.Lock[i].Text)
	}
	for _, p := range v.Pages {
		p.Title, p.Empty = c.tr(p.Title), c.tr(p.Empty)
		c.items(p.Items)
	}
	for _, n := range v.SMS {
		n.Preview, n.TextLabel = c.tr(n.Preview), c.tr(n.TextLabel)
		c.items(n.Items)
	}
	c.items(v.Settings)
}

func (c *catalog) items(items []Item) {
	for i := range items {
		it := &items[i]
		it.Label, it.Value, it.Detail, it.Unit = c.tr(it.Label), c.tr(it.Value), c.tr(it.Detail), c.tr(it.Unit)
		it.Sub, it.XStart, it.XEnd = c.tr(it.Sub), c.tr(it.XStart), c.tr(it.XEnd)
		for j := range it.Series {
			it.Series[j].Label = c.tr(it.Series[j].Label)
		}
		for j := range it.Options {
			it.Options[j].Label = c.tr(it.Options[j].Label)
			c.action(it.Options[j].Action)
		}
		for j := range it.Stats {
			it.Stats[j].Label, it.Stats[j].Unit = c.tr(it.Stats[j].Label), c.tr(it.Stats[j].Unit)
		}
		c.action(it.Action)
	}
}
