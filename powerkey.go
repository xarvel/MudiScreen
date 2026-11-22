package main

// A second, passive look at the power key (pmic_pwrkey, /dev/input/event1).
// lpm-monitor owns the key: it classifies presses and calls gl_screen set
// with "power key short press" / "power key long press", and the screen acts
// on those. The raw key is only a fallback for the case that lpm-monitor's
// call does not come (another firmware, another wording): without it a dark
// screen could not be woken. The device is not grabbed, lpm-monitor keeps
// reading it as before.
//
// The key bounces: one physical press can produce several press/release
// edges within ~200 ms. A press episode therefore ends only after
// keyQuiet without a new press, and its length runs from the first press to
// the last release.

import (
	"encoding/binary"
	"io"
	"log"
	"os"
	"time"
)

const (
	keyPower = 116
	keyQuiet = 250 * time.Millisecond
)

type keyEpisodeDecoder struct {
	down     time.Time // first press of the episode; zero when idle
	released time.Time // last release, while waiting for quiet
	now      func() time.Time
}

// feed handles a KEY_POWER edge (1 press, 0 release, 2 repeat).
func (d *keyEpisodeDecoder) feed(val int32) {
	switch val {
	case 1:
		if d.down.IsZero() {
			d.down = d.now()
		}
		d.released = time.Time{}
	case 0:
		if !d.down.IsZero() {
			d.released = d.now()
		}
	}
}

// done returns the held time of a finished episode.
func (d *keyEpisodeDecoder) done() (time.Duration, bool) {
	if d.down.IsZero() || d.released.IsZero() || d.now().Sub(d.released) < keyQuiet {
		return 0, false
	}
	held := d.released.Sub(d.down)
	d.down, d.released = time.Time{}, time.Time{}
	return held, true
}

func readPowerKey(path string, out chan<- time.Duration, logger *log.Logger) {
	for {
		f, err := os.Open(path)
		if err != nil {
			logger.Printf("power key: %v", err)
			return
		}
		err = decodePowerKey(f, out)
		f.Close()
		logger.Printf("power key: %v; reopening", err)
		time.Sleep(time.Second)
	}
}

func decodePowerKey(f *os.File, out chan<- time.Duration) error {
	d := &keyEpisodeDecoder{now: time.Now}
	evs := make(chan [2]int32, 16)
	errc := make(chan error, 1)
	go func() {
		buf := make([]byte, inputEventSize*16)
		for {
			n, err := f.Read(buf)
			if err != nil {
				errc <- err
				return
			}
			for off := 0; off+inputEventSize <= n; off += inputEventSize {
				typ := binary.LittleEndian.Uint16(buf[off+16:])
				code := binary.LittleEndian.Uint16(buf[off+18:])
				if typ == evKey && code == keyPower {
					evs <- [2]int32{1, int32(binary.LittleEndian.Uint32(buf[off+20:]))}
				}
			}
		}
	}()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case e := <-evs:
			d.feed(e[1])
		case <-tick.C:
			if held, ok := d.done(); ok {
				select {
				case out <- held:
				default:
				}
			}
		case err := <-errc:
			if err == nil {
				err = io.EOF
			}
			return err
		}
	}
}
