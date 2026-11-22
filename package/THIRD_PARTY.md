# Third-party components in mudi-screen

`mudi-screen` is a single statically linked Go binary (linux/arm64, no cgo).
Everything third-party that is linked into it is listed below; the license
texts are in `licenses/` next to this file (last column). On the router both
are in `/usr/share/doc/mudi-screen/`.

| Component | Version | License | Source | Texts |
|---|---|---|---|---|
| Go standard library and runtime | the Go release that built the package (1.24 or later) | BSD-3-Clause, plus the Go patent grant (PATENTS) | https://go.googlesource.com/go | `licenses/go/` |
| golang.org/x/image | v0.30.0 | BSD-3-Clause, plus PATENTS | https://go.googlesource.com/image | `licenses/golang.org/x/image/` |
| golang.org/x/text | v0.28.0 | BSD-3-Clause, plus PATENTS | https://go.googlesource.com/text | `licenses/golang.org/x/text/` |

`go version -m /usr/bin/mudi-screen` prints the modules and the Go release
of a build.

The screen draws its text with the fonts of the router's firmware
(`/etc/gl_screen/language/ttf`, IBM Plex Sans); the package carries no font.
