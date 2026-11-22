package main

// Fonts: IBM Plex Sans as shipped with the router in
// /etc/gl_screen/language/ttf (it covers Latin and Cyrillic). Faces are
// created on first use and cached; they are used only from the UI goroutine.

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type weight int

const (
	Medium weight = iota
	Semibold
	Bold
)

var fontFiles = map[weight]string{
	Medium:   "default_medium.ttf",
	Semibold: "default_semibold.ttf",
	Bold:     "default_bold.ttf",
}

type faceKey struct {
	w    weight
	size float64
}

type Fonts struct {
	fonts map[weight]*opentype.Font
	faces map[faceKey]font.Face
}

func LoadFonts(dir string) (*Fonts, error) {
	f := &Fonts{fonts: map[weight]*opentype.Font{}, faces: map[faceKey]font.Face{}}
	for w, name := range fontFiles {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		ft, err := opentype.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		f.fonts[w] = ft
	}
	return f, nil
}

// Face returns a face of the given weight; size is in pixels.
func (f *Fonts) Face(w weight, size float64) font.Face {
	k := faceKey{w, size}
	if fc, ok := f.faces[k]; ok {
		return fc
	}
	fc, err := opentype.NewFace(f.fonts[w], &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err) // only on a broken font, which LoadFonts already parsed
	}
	cf := &cachedFace{Face: fc, glyphs: map[glyphKey]*glyphMask{}}
	f.faces[k] = cf
	return cf
}

// cachedFace keeps rasterized glyphs: drawing text is then copying small
// masks, which is what makes a full page render fast enough to animate.
// Positions are kept to a quarter pixel.
type cachedFace struct {
	font.Face
	glyphs map[glyphKey]*glyphMask
}

type glyphKey struct {
	r  rune
	fx int8
}

type glyphMask struct {
	dr   image.Rectangle // relative to the dot, rounded down
	mask *image.Alpha
	adv  fixed.Int26_6
	ok   bool
}

var emptyMask = image.NewAlpha(image.Rectangle{})

func (f *cachedFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	k := glyphKey{r, int8((dot.X & 63) >> 4)}
	g := f.glyphs[k]
	if g == nil {
		dr, mask, mp, adv, ok := f.Face.Glyph(fixed.Point26_6{X: fixed.Int26_6(k.fx) << 4}, r)
		g = &glyphMask{dr: dr, adv: adv, ok: ok, mask: emptyMask}
		if ok && !dr.Empty() {
			m := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
			draw.Draw(m, m.Bounds(), mask, mp, draw.Src)
			g.mask = m
		}
		if len(f.glyphs) < 4096 {
			f.glyphs[k] = g
		}
	}
	if !g.ok {
		return image.Rectangle{}, emptyMask, image.Point{}, g.adv, false
	}
	return g.dr.Add(image.Pt(int(dot.X>>6), int(dot.Y>>6))), g.mask, image.Point{}, g.adv, true
}
