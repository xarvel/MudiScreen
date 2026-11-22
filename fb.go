package main

// The panel: GC9307C over SPI, driven by fbtft as /dev/fb0, 240×320,
// RGB565 little-endian, stride 480 bytes. fbtft sends only the lines that
// were written, so Push writes just the rows that changed since the last
// frame.
//
// Power: three independent controls. fb0/blank sends the panel to sleep and,
// through the touch driver's fb notifier, the touch controller as well (no
// touches arrive while blanked). bl_power gates the backlight and is what
// lpm-monitor reads to know whether the screen is on. brightness is the PWM
// level, 0..max_brightness (120); GL keeps uci BRIGHTNESS + 20 there.

import (
	"bytes"
	"image"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Framebuffer struct {
	f         *os.File
	cur, prev []byte
	valid     bool
}

func OpenFB(path string) (*Framebuffer, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return &Framebuffer{f: f, cur: make([]byte, W*H*2), prev: make([]byte, W*H*2)}, nil
}

func (fb *Framebuffer) Close() error { return fb.f.Close() }

// Invalidate makes the next Push write the whole frame (after the panel
// slept, or when something else may have drawn on it).
func (fb *Framebuffer) Invalidate() { fb.valid = false }

func toRGB565(dst []byte, img *image.RGBA) {
	p := img.Pix
	for i, j := 0, 0; i+3 < len(p); i, j = i+4, j+2 {
		v := uint16(p[i]&0xF8)<<8 | uint16(p[i+1]&0xFC)<<3 | uint16(p[i+2])>>3
		dst[j] = byte(v)
		dst[j+1] = byte(v >> 8)
	}
}

// changedRows returns the first and last row that differ, or -1, -1.
func changedRows(a, b []byte) (int, int) {
	const stride = W * 2
	y0, y1 := -1, -1
	for y := 0; y < H; y++ {
		if !bytes.Equal(a[y*stride:(y+1)*stride], b[y*stride:(y+1)*stride]) {
			if y0 < 0 {
				y0 = y
			}
			y1 = y
		}
	}
	return y0, y1
}

func (fb *Framebuffer) Push(img *image.RGBA) error {
	toRGB565(fb.cur, img)
	y0, y1 := 0, H-1
	if fb.valid {
		y0, y1 = changedRows(fb.cur, fb.prev)
		if y0 < 0 {
			return nil
		}
	}
	off, end := y0*W*2, (y1+1)*W*2
	if _, err := fb.f.WriteAt(fb.cur[off:end], int64(off)); err != nil {
		fb.valid = false
		return err
	}
	fb.cur, fb.prev = fb.prev, fb.cur
	fb.valid = true
	return nil
}

type Display struct {
	blDir, blank string
	log          *log.Logger
}

func (d *Display) write(path, val string) {
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(val+"\n"), 0o644); err != nil {
		d.log.Printf("display: %s: %v", path, err)
	}
}

func (d *Display) maxBrightness() int {
	raw, err := os.ReadFile(filepath.Join(d.blDir, "max_brightness"))
	if err != nil {
		return 120
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || v <= 0 {
		return 120
	}
	return v
}

// PowerOn wakes the panel and the touch controller, then the backlight
// (at the brightness set before).
func (d *Display) PowerOn() {
	d.write(d.blank, "0")
	if d.blDir != "" {
		d.write(filepath.Join(d.blDir, "bl_power"), "0")
	}
}

// PowerOff turns off the backlight, then sends the panel to sleep.
func (d *Display) PowerOff() {
	if d.blDir != "" {
		d.write(filepath.Join(d.blDir, "bl_power"), "1")
	}
	d.write(d.blank, "1")
}

func (d *Display) SetBrightness(level int) {
	if d.blDir == "" {
		return
	}
	level = min(max(level, 1), d.maxBrightness())
	d.write(filepath.Join(d.blDir, "brightness"), strconv.Itoa(level))
}
