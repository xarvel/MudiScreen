# Contributing

Thank you for looking into MudiScreen. Reports from other firmware versions
and ideas for apps are the most useful things you can send; patches are
welcome too. Security problems go the private way described in
[SECURITY.md](SECURITY.md).

## Reporting a problem

Open an issue with:

- the router model, the GL firmware version and `mudi-screen -version`;
- what you did, what happened and what you expected;
- the relevant part of `/tmp/mudi-screen.log` (or
  `/tmp/mudi-screen-traffic.log`), and `logread | grep mudi-screen` for a
  crash. Look them over before pasting: they may hold names of your devices.
- For a drawing problem, a screenshot: `/dev/fb0` is the picture, 240×320
  RGB565 little-endian.

## Building and testing

The README explains the build (`package/build.sh`) and how to render pages
without a router (`-preview`). Before sending a change, run:

```sh
gofmt -l .                      # prints nothing
go vet ./... && go test ./...
sh package/build.sh             # the package still builds
shellcheck -s sh -S warning -e SC2034 package/build.sh package/install.sh.in \
    package/control/postinst package/control/prerm \
    package/files/usr/sbin/mudi-screen-mode package/files/usr/libexec/mudi-screen/run \
    package/files/etc/init.d/mudi-screen package/files/etc/init.d/mudi-screen-traffic \
    docs/screenshots/make.sh
```

The tests run on Linux and macOS. Tests that lay pages out use the fonts in
`dev/fonts` when you put a copy of the router's there, and the Go fonts
otherwise. A change to what the screen shows should come with a picture of
it (`-preview`); a change that touches the display, the touch panel, the
power key or the package scripts should be tried on a Mudi 7 as well; say in
the pull request what you tried it on.

## Code

- Go is formatted with `gofmt`; shell scripts pass `shellcheck` and run in
  BusyBox ash; comments and commit messages are in English. Each commit says
  what changed and why.
- The screen's texts are in `i18n.go` (English and Russian); an app's texts
  go to catalogs (docs/EXTENSIONS.md, "Translations"). A new language is a
  new catalog and a column in `i18n.go`.
- New Go modules need a permissive license (BSD, MIT, Apache-2.0 and the
  like) and an entry in `package/THIRD_PARTY.md` with its text in
  `package/licenses/`.
- Contributions are accepted under the terms of the repository
  ([LICENSE](LICENSE)).
