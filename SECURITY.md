# Security

MudiScreen runs as root on a home router and shows what it knows on a screen
anyone can pick up: a flaw in it can open the SMS inbox or the traffic
history without the passcode, crash or take over the screen, or let a
device on the network harm the traffic collector. Reports of such flaws are
welcome and are handled in private first.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting: the **Security** tab of
<https://github.com/xarvel/MudiScreen>, "Report a vulnerability". If that
form is not available, open an issue that says only that you have a security
report and how to reach you; keep the details out of the issue.

Please include the version (`mudi-screen -version`), the firmware of the
router, what an attacker needs (the router in hand, a place on the Wi-Fi,
the admin password...) and how to reproduce it. `/tmp/mudi-screen.log` and
the traffic pages can show names and numbers: mask them before sending.

MudiScreen is a personal project without a security team. You should hear
back within a week; fixes go out as a new release and the report is credited
there unless you prefer not to be named.

## What is in scope

- The lock screen and the passcode: any way to reach a page, the SMS or an
  app's action without it, and anything private that shows before it.
- The traffic collector (`sniff.go`, `nlconntrack.go`, `trafficd.go`):
  anything a device on the LAN or the guest network can do with packets
  beyond being counted, such as crashing the collector, growing its memory
  without bound or putting names on the connections of other devices.
- The on-screen keyboard and joining Wi-Fi networks: anything that lets a
  password typed on the screen reach the log, another app or anyone but
  GL's RPC on the router.
- The app interface: anything an app's output or an ubus caller can do to
  the screen beyond what docs/EXTENSIONS.md allows, such as running the
  screen's own SMS actions or crashing it.
- The package scripts and the supervisor: anything that leaves the screen
  dark or the router in a state that `mudi-screen-mode` cannot undo.

## What the project does not protect against

- Someone with the router's admin password, an SSH login or the passcode
  sees everything the screen shows.
- Apps run as root, like everything the router's owner installs: an app is
  trusted as far as its code, not by the screen.
- Lock screen lines and notifications are seen before the passcode; an app
  that puts private data there shows it to anyone (docs/EXTENSIONS.md says
  not to).

## Supported versions

Only the latest release gets fixes.
