//go:build ignore

// frames turns the screen's 240×320 previews into the images of README.md:
//
//	go run frames.go scale IN.png OUT.png
//	go run frames.go gif OUT.gif FRAME.png:DELAY ...
//
// scale doubles a preview pixel for pixel, so that it stays sharp on a
// high-density display. gif makes an animation of the frames, doubled the
// same way; DELAY is in hundredths of a second. Only the standard library
// is used: `go run` needs nothing else.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fail("usage: frames scale IN OUT | frames gif OUT FRAME:DELAY ...")
	}
	switch os.Args[1] {
	case "scale":
		if len(os.Args) != 4 {
			fail("usage: frames scale IN OUT")
		}
		writePNG(os.Args[3], double(readPNG(os.Args[2])))
	case "gif":
		makeGIF(os.Args[2], os.Args[3:])
	default:
		fail("unknown command " + os.Args[1])
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "frames:", msg)
	os.Exit(2)
}

func readPNG(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		fail(err.Error())
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		fail(path + ": " + err.Error())
	}
	return img
}

func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		fail(err.Error())
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, img); err != nil {
		fail(err.Error())
	}
	if err := f.Close(); err != nil {
		fail(err.Error())
	}
}

// double scales an image to twice its size, each pixel a 2×2 block.
func double(src image.Image) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, 2*b.Dx(), 2*b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y))
			dst.Set(2*x, 2*y, c)
			dst.Set(2*x+1, 2*y, c)
			dst.Set(2*x, 2*y+1, c)
			dst.Set(2*x+1, 2*y+1, c)
		}
	}
	return dst
}

// makeGIF writes the frames as a looping animation. The palette is the 256
// colours the frames use most; the screen's flat colours are all among them,
// and the rare in-between shades of the text edges take their nearest.
func makeGIF(out string, specs []string) {
	var frames []*image.NRGBA
	var delays []int
	count := map[color.NRGBA]int{}
	for _, spec := range specs {
		i := strings.LastIndexByte(spec, ':')
		if i < 0 {
			fail("FRAME:DELAY expected, got " + spec)
		}
		delay, err := strconv.Atoi(spec[i+1:])
		if err != nil || delay < 2 {
			fail("bad delay in " + spec)
		}
		img := double(readPNG(spec[:i]))
		for p := 0; p < len(img.Pix); p += 4 {
			count[color.NRGBA{img.Pix[p], img.Pix[p+1], img.Pix[p+2], 255}]++
		}
		frames = append(frames, img)
		delays = append(delays, delay)
	}
	colors := make([]color.NRGBA, 0, len(count))
	for c := range count {
		colors = append(colors, c)
	}
	sort.Slice(colors, func(i, j int) bool {
		if count[colors[i]] != count[colors[j]] {
			return count[colors[i]] > count[colors[j]]
		}
		a, b := colors[i], colors[j]
		return uint32(a.R)<<16|uint32(a.G)<<8|uint32(a.B) < uint32(b.R)<<16|uint32(b.G)<<8|uint32(b.B)
	})
	if len(colors) > 256 {
		colors = colors[:256]
	}
	pal := make(color.Palette, len(colors))
	for i, c := range colors {
		pal[i] = c
	}
	anim := &gif.GIF{LoopCount: 0}
	nearest := map[color.NRGBA]uint8{}
	for n, img := range frames {
		pm := image.NewPaletted(img.Bounds(), pal)
		for p, q := 0, 0; p < len(img.Pix); p, q = p+4, q+1 {
			c := color.NRGBA{img.Pix[p], img.Pix[p+1], img.Pix[p+2], 255}
			idx, ok := nearest[c]
			if !ok {
				idx = uint8(pal.Index(c))
				nearest[c] = idx
			}
			pm.Pix[q] = idx
		}
		anim.Image = append(anim.Image, pm)
		anim.Delay = append(anim.Delay, delays[n])
		anim.Disposal = append(anim.Disposal, gif.DisposalNone)
	}
	f, err := os.Create(out)
	if err != nil {
		fail(err.Error())
	}
	if err := gif.EncodeAll(f, anim); err != nil {
		fail(err.Error())
	}
	if err := f.Close(); err != nil {
		fail(err.Error())
	}
}
