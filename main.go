// Command mudi-screen is an open replacement user interface for the built-in
// 240×320 touchscreen of the GL.iNet Mudi 7 (GL-E5800).
//
// It takes the place of GL.iNet's closed-source gl_screen: it draws to
// /dev/fb0 (RGB565), reads the capacitive panel on /dev/input/event0 and
// publishes the gl_screen ubus object, so that lpm-monitor's power key and
// battery events reach it. The core shows only what every stock Mudi 7 has
// (network, battery, devices, power, the passcode); everything else comes
// from apps: a manifest and any executable that prints a view as JSON (see
// docs/EXTENSIONS.md).
//
// -preview renders one frame with demo data to a PNG, which works on any
// Linux machine and needs only the fonts.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// version is set at build time (-ldflags -X main.version=...).
var version = "dev"

type options struct {
	apps, lang                  string
	fb, touch, backlight, blank string
	powerKey                    string
	fonts                       string
	logPath, ctlPath            string
	ubusSock, ubusName          string
	ttl                         time.Duration
	preview, page               string
	scroll                      int
	anim                        string
	progress                    float64
	dryRun                      bool
	probeUbus                   bool
}

func main() {
	// Bundled apps run from the same binary: mudi-screen app <name> ...
	if len(os.Args) > 1 && os.Args[1] == "app" {
		os.Exit(runApp(os.Args[2:]))
	}
	var o options
	flag.StringVar(&o.apps, "apps", strings.Join(defaultAppDirs, ":"), "app directories, earlier ones win")
	flag.StringVar(&o.lang, "lang", "", "language: en, ru (default: uci mudi-screen.main.lang, then the web admin's)")
	flag.StringVar(&o.fb, "fb", "/dev/fb0", "framebuffer device")
	flag.StringVar(&o.touch, "touch", "/dev/input/event0", "touch panel input device")
	flag.StringVar(&o.powerKey, "powerkey", "/dev/input/event1", "power key input device, read passively as a fallback (empty: off)")
	flag.StringVar(&o.backlight, "backlight", "/sys/class/backlight/soc:backlight", "backlight sysfs directory")
	flag.StringVar(&o.blank, "blank", "/sys/class/graphics/fb0/blank", "framebuffer blank control")
	flag.StringVar(&o.fonts, "fonts", "/etc/gl_screen/language/ttf", "directory with default_{medium,semibold,bold}.ttf")
	flag.StringVar(&o.logPath, "log", "/tmp/mudi-screen.log", "log file")
	flag.StringVar(&o.ctlPath, "ctl", "/tmp/mudi-screen.ctl", "control FIFO for tests: tap X Y | swipe DX DY | key short|long | page NAME | wake | sleep")
	flag.StringVar(&o.ubusSock, "ubus", "/var/run/ubus/ubus.sock", "ubusd socket")
	flag.StringVar(&o.ubusName, "ubus-name", "gl_screen", "ubus object to publish (empty: none)")
	flag.DurationVar(&o.ttl, "ttl", 0, "exit after this long (0: run until stopped)")
	flag.StringVar(&o.preview, "preview", "", "render one frame with demo data to this PNG and exit")
	flag.StringVar(&o.page, "page", "home", "page for -preview: lock, pin, home, power, confirm, notify or APP:PAGE")
	flag.IntVar(&o.scroll, "scroll", 0, "scroll offset in pixels for -preview")
	flag.StringVar(&o.anim, "anim", "", "for -preview: a frame of a transition from home: push, pop or up (from lock)")
	flag.Float64Var(&o.progress, "progress", 0.5, "for -anim: how far the transition is, 0..1")
	flag.BoolVar(&o.dryRun, "dry-run", false, "log power off, reboot, app actions and the switch to the stock screen instead of running them")
	flag.BoolVar(&o.probeUbus, "probe-ubus", false, "only publish -ubus-name and print its calls until -ttl (default 30s); the screen is not touched")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("mudi-screen", version)
		return
	}

	if o.probeUbus {
		probeUbus(o)
		return
	}
	if o.preview != "" {
		if err := runPreview(o); err != nil {
			fmt.Fprintln(os.Stderr, "mudi-screen:", err)
			os.Exit(1)
		}
		return
	}
	os.Exit(runLive(o))
}

func (o options) appDirs() []string {
	var dirs []string
	for _, d := range strings.Split(o.apps, ":") {
		if d = strings.TrimSpace(d); d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
