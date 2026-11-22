# Writing apps for MudiScreen

MudiScreen draws only what every stock Mudi 7 has: the network, the battery,
connected devices, power and the passcode. Everything else on the screen
comes from **apps**. An app is a manifest plus any executable: a shell script,
Lua, a Go binary. The screen asks the app what to show and the app answers
with JSON. The screen decides how things look, so every app fits the same
design without drawing a pixel.

There are two directions:

- **Pull.** The screen runs `<command> status` and reads a *view* from
  stdout: a home card, tiles, lock screen lines, status bar icons and pages.
  Taps run `<command> action <id> <args...>`.
- **Push.** The app calls the screen's ubus object `mudi-screen` when
  something happens: `refresh` to be asked again at once, `notify` to show a
  message, `show` to open a page.

API version: **1**.

## Where apps live

| Directory | For |
|---|---|
| `/etc/mudi-screen/apps` | local apps and overrides; wins over the next one |
| `/usr/share/mudi-screen/apps` | apps installed by packages |

An app is either a directory `<dir>/<id>/app.json` (the command may be
relative to that directory) or a single file `<dir>/<id>.json` (absolute
command). The screen rereads the directories within 30 seconds after a
change, or at once on `ubus call mudi-screen reload`. `-apps dir1:dir2`
replaces the list.

A package that brings an app puts it in `/usr/share/mudi-screen/apps/<id>/`
and, so that a firmware update with Keep Settings keeps it, lists that
directory in its own `/lib/upgrade/keep.d` file. Examples: the traffic app
that comes with MudiScreen (`apps/traffic`, written in Go and run from the
`mudi-screen` binary itself), and the phone line of the mudi-volte package
(`/usr/share/mudi-screen/apps/volte`, a shell script).

## Manifest

```json
{
  "api": 1,
  "id": "wireguard",
  "name": "WireGuard",
  "order": 20,
  "command": "app.sh",
  "interval": 5,
  "requires": ["/usr/bin/wg"]
}
```

| Field | Meaning |
|---|---|
| `api` | API version the app was written for; `1` (or absent) |
| `id` | `[a-z0-9][a-z0-9._-]*`, up to 32 characters; `system` and `core*` are reserved |
| `name` | shown when the app has failed before its first answer |
| `order` | place of the card and tiles; the built-in network card is `10`; default `50` |
| `command` | executable; relative paths are taken from the manifest's directory |
| `args` | arguments put before `status` and `action` (`["app", "traffic"]` runs `mudi-screen app traffic status`) |
| `interval` | seconds between `status` runs while the screen is on; default 5, 1..3600 |
| `page_interval` | the same while a page of the app is open; default `interval` |
| `requires` | paths that must exist, otherwise the app is skipped (for example the binary of the package it belongs to) |
| `disabled` | `true` hides the app; put a copy with `disabled` into `/etc/mudi-screen/apps` to switch off a packaged app |

## The command

```
<command> status
<command> action <id> [args...]
```

The command runs as root in the manifest's directory with these
environment variables:

| Variable | Value |
|---|---|
| `MUDI_SCREEN_API` | `1` |
| `MUDI_SCREEN_LANG` | `en`, `ru`, …; answer in this language when you can. It can change while the screen runs (Settings → Language): the screen then asks every app for its status again at once |
| `MUDI_SCREEN_APP` | the app's id |
| `MUDI_SCREEN_PAGE` | the app's page on the screen now, or empty: an app can skip work for pages nobody looks at |

**`status`** prints one JSON object (the view, below) and exits 0. It has
5 seconds; aim for well under one, because it runs every few seconds while
the screen is on. Empty output means "nothing to show right now". When
`status` fails, the screen keeps the last good view; an app that never
answered shows a red "no data" card.

The screen runs `status`:
- every `interval` seconds while the screen is on, every `page_interval`
  seconds while a page of the app is open, and every second while anything
  of the app is busy (an action runs, or an item has `"busy": true`);
- at once when the screen wakes up, when a page of the app opens, after each
  action, and on `refresh`;
- never while the screen is off.

**`action`** does what a tapped item asked for. It has 60 seconds. Exit 0
means success; the first line of stdout, if any, is shown as a short message
(a toast). On a non-zero exit the first line of stderr (or stdout) is shown
as an error. If the screen locked while the action ran, the toast says only
"Done" or "The action failed": what an action prints stays behind the
passcode. Output past 1 MiB is dropped. While the action runs, the tapped item shows as busy. Work that
goes on in the background after the action returns should be reported by
`status` with `"busy": true` until it is done: an action can leave a marker
file that `status` looks for, for example.

## The view

Every field is optional. Unknown fields are ignored, so an app written for a
newer screen still works on an older one.

```json
{
  "card":   {"title": "VPN: home", "subtitle": "WireGuard is up", "subtitle_color": "green",
             "icon": "shield-check", "color": "green", "open": "main"},
  "tiles":  [{"label": "Peers", "value": "2", "icon": "devices", "open": "main"}],
  "lock":   [{"text": "VPN: home", "color": "green"}],
  "status": [{"icon": "shield", "color": "green"}],
  "pages":  {"main": {"title": "WireGuard", "items": [ ... ]}},
  "sms":    {"GMS1.Ab3dEf": {"preview": "…", "items": [ ... ], "text_label": "Original", "text_color": "dim"}},
  "settings": [{"type": "toggle", "label": "Check links in SMS", "checked": true, "action": {"id": "toggle"}}]
}
```

| Part | Shown | Fields |
|---|---|---|
| `card` | a card on the home screen | `title`, `subtitle`, `icon`, `color` (icon), `subtitle_color`, `open` (page name) or `action` |
| `tiles` | small tiles below the cards, three in a row, at most 6 per app | `label`, `value`, `icon`, `color`, `open` or `action` |
| `lock` | lines with a coloured dot on the lock screen, at most 4 | `text`, `color` |
| `status` | icons in the status bar, at most 3 | `icon`, `color` |
| `pages` | pages by name, opened from a card, a tile, an item or `show`, at most 32 | `title`, `items`, `empty` (text for no items) |
| `sms` | notes on messages of the screen's SMS inbox, by message name, at most 500 | `preview`, `items` (up to 40), `text_label`, `text_color` |
| `settings` | items on the screen's Settings page, at most 20 | items |

Where a limit cuts pages or notes, it keeps the first ones in name order,
the same on every run.

#### Notes on messages

The screen shows the SMS inbox itself. An app can add to a message:
`items` go on the message's page between the date and the text,
`text_label` is the heading of the text (default "Text"), `text_color` its
colour, and `preview` is shown in the inbox under the sender in place of
the text's start. A message is named as in GL's list, the `name` of
`modem.get_sms_list` (`curl -H "glinet: 1" http://127.0.0.1/rpc` on the
router itself); the app reads the messages from there. When several apps
note the same message, the screen shows the items of all of them in the
apps' order, and the first preview, heading and colour.

#### Settings

`settings` items go on the screen's Settings page, after the language,
under the app's name unless the first item is a `section` of its own.

Actions and pages of the items an app puts on the screen's pages (a
message, Settings) are the app's: a tap runs `<command> action ...` of
that app, `open` opens a page of that app.

**The lock screen and notifications are visible without the passcode.**
Keep them free of anything private: show counts, not names or numbers.

### Items

| `type` | Looks like | Fields used |
|---|---|---|
| `section` | small heading | `label`, `color` |
| `info` | label on the left, value on the right | `label`, `value`, `color` (value) |
| `text` | wrapped paragraph | `label` (the text), `color` |
| `entry` | a row: small `detail` on the left, `label`, small `value` on the right | `detail`, `label`, `sub`, `value`, `icon`, `color` |
| `radio` | one choice of several | `label`, `value`, `checked`, `color` (the dot) |
| `toggle` | label and a switch | `label`, `sub`, `checked`, `color` (the track when on) |
| `button` | full-width button | `label`, `style` (`primary`, `danger`, `plain`), `color` |
| `link` | row with a chevron | `label`, `sub`, `value`, `icon`, `color` |
| `bar` | row with a filled part, for a top list | `label`, `sub`, `value`, `fraction` (0..1), `color`, `fraction2`, `color2` |
| `chart` | line chart (the first line filled below) or stacked bars | `label`, `unit`, `span`, `max`, `series`, `style`, `x_start`, `x_end` |
| `segments` | a row of choices, one of them selected (tabs) | `options`: `[{"label", "checked", "action"}]`, up to 5 |
| `stats` | up to three big numbers side by side | `stats`: `[{"label", "value", "unit", "color"}]` |

`sub` puts a second, smaller line under the label (the row gets taller).
A `bar` with `fraction2` shows a second part right after the first, in
`color2`: download and upload of one total, for example. `segments` runs
the action of the tapped option and shows it selected at once; the app
reports the new selection with `checked` in its next `status`. `stats` is
for the few numbers a page is about; the unit goes under the value.

A chart has up to four `series`, each `{"label": "↓ 3.9", "color": "blue",
"values": [...]}`, with numbers of one `unit` (shown with the top of the
scale). The values fill the right end of `span` points (default: the longest
series), so a series that is still short grows from the right like a live
chart. The scale rounds the largest value (or `max`) up to 1, 2, 2.5 or 5
times a power of ten. `"style": "bars"` draws a bar per point, the series
stacked; `x_start` and `x_end` label the ends of the time axis ("−1 h",
"now").

Every item can also have `busy` (shows a pending mark and ignores taps),
`disabled` (greyed, ignores taps), `open` (a page of the same app) or
`action`. An item without `open` or `action` is not tappable.

### Actions

```json
{"id": "select", "args": ["11"]}
{"id": "clear", "confirm": "Clear the missed calls?", "confirm_label": "Clear", "danger": true}
```

| Field | Meaning |
|---|---|
| `id` | passed as `<command> action <id> <args...>` |
| `args` | strings passed after the id |
| `confirm` | ask this question first |
| `confirm_label` | text of the yes button (default "Yes") |
| `danger` | the yes button is red |

Ids starting with `core.` are handled by the screen itself: `core.power`
(power menu), `core.lock`, `core.sleep`, `core.home`. These are the only
ones an app's items can use, also on the screen's own pages (notes on
messages, Settings); other `core.` ids from an app are ignored. A
`confirm` question is asked for these too.

### Colours and icons

Colours: `green`, `yellow`, `red`, `blue`, `orange`, `purple`, `text`,
`dim`, `faint`, or `#rrggbb`.

Icons: `shield`, `shield-check`, `phone`, `wifi` (`wifi-0`…`wifi-3`),
`signal` (`signal-0`…`signal-4`), `power`, `devices`, `sms`, `lock`, `cross`,
`check`, `bolt`, `info`, `alert`, `globe`, `gear`, `chart`, `dot`. Unknown
names draw a dot.

## Translations

An app can answer in the language of `MUDI_SCREEN_LANG` itself, or print
English and leave the rest to catalogs. Catalogs are data, not code: anyone
can add a language to any app without touching it.

| File | Priority |
|---|---|
| `/etc/mudi-screen/i18n/<lang>/<id>.tsv` | highest: local fixes |
| `/usr/share/mudi-screen/i18n/<lang>/<id>.tsv` | language packs |
| `<app dir>/i18n/<lang>.tsv` | lowest: shipped with the app |

A line is `English<TAB>translation`; `#` starts a comment. `{}` in the
English text matches any text; `{}` in the translation takes the matches in
order, `{1}`…`{9}` by number. Longer patterns are tried first:

<!-- Cyrillic on purpose: a Russian catalog. -->
```
Calls are on	Звонки на связи
missed: {}	пропущенных: {}
{} h {} min	{} ч {} мин
```

The screen translates every text of the view (titles, labels, values,
confirmations, chart units and legends) once per `status`, and the `title`
and `text` of a `notify` that names the app. A text no line matches stays as
it is. Numbers stay as the app printed them, so an app that cares about the
decimal comma looks at `MUDI_SCREEN_LANG`.

### Running time

`{elapsed:T}` in a text, `T` being a Unix time in seconds, shows the time
since then and keeps running: `0:42`, `12:05`, `1:02:05` past an hour. While
a view has one, the screen draws it again every second, so a call or a timer
counts up without the app being asked more often. It works in the card, the
tiles, the lock lines, page titles and the items of pages. Catalogs keep it:
`{}` matches it as any other text.

```json
{"card": {"title": "In a call · {elapsed:1795000000}"}}
```

### Pages and motion

Pages slide in when opened and out when closed; a swipe to the right goes
back, following the finger, and a vertical swipe scrolls with momentum.
An app does nothing for this. While a page of an app is open, its `status`
sees `MUDI_SCREEN_PAGE` and needs to send only that page (and the ones its
card and tiles open): pages on the way back stay as they were until the app
sends them again, and a page that is not there yet shows "Loading…" until
the app's answer, which the screen asks for at once.

### Size

The screen is 240×320 pixels. Titles get about 20 characters, values on
`info` rows about 15. Longer text is cut with "…". Pages scroll with a
vertical swipe; a swipe to the right goes back.

## Push: the `mudi-screen` ubus object

```sh
ubus call mudi-screen refresh '{"app":"volte"}'      # run status of one app now (no "app": all)
ubus call mudi-screen notify '{"title":"Missed call","text":"1 new","icon":"phone",
  "color":"orange","app":"volte","page":"main","timeout":60}'
ubus call mudi-screen dismiss '{"id":"volte-call"}'   # take back the notice with this id
ubus call mudi-screen show '{"app":"volte","page":"main"}'   # wake up and open (after the passcode)
ubus call mudi-screen apps       # loaded apps, their errors, last status run
ubus call mudi-screen state      # screen on/off, page, locked, language
ubus call mudi-screen reload     # reread the app directories
```

`notify` wakes the screen (unless `"wake": false`) and shows a dialog over
whatever is open, the lock screen included, for `timeout` seconds
(default 30). With `app` and `page` it has an "Open" button, and the app's
catalogs translate the title and the text. Opening a page from the lock
screen asks for the passcode first. The notice is seen before the passcode:
put no names or numbers in it.

A notice with an `"id"` can be taken back before its time: `dismiss` with
the same id closes it if it is still shown, and does nothing if it is gone
or another notice took its place. An incoming call that was answered, for
example.

## A minimal app

`/etc/mudi-screen/apps/load/app.json`:

```json
{"api": 1, "id": "load", "name": "Load", "order": 40, "command": "app.sh", "interval": 10}
```

`/etc/mudi-screen/apps/load/app.sh` (mode 755):

```sh
#!/bin/sh
. /usr/share/libubox/jshn.sh
case "$1" in
status)
	read -r l1 l5 l15 rest < /proc/loadavg
	json_init
	json_add_object card
	json_add_string title "Load $l1"
	json_add_string subtitle "5 min $l5 · 15 min $l15"
	json_add_string icon gear
	json_add_string open main
	json_close_object
	json_add_object pages
	json_add_object main
	json_add_string title "Load"
	json_add_array items
	json_add_object ""
	json_add_string type button
	json_add_string label "Drop caches"
	json_add_object action
	json_add_string id drop
	json_close_object
	json_close_object
	json_close_array
	json_close_object
	json_close_object
	json_dump
	;;
action)
	[ "$2" = drop ] && sync && echo 3 > /proc/sys/vm/drop_caches && echo "Done"
	;;
esac
```

`jshn` (part of OpenWrt) builds JSON with proper escaping; `jsonfilter`
reads it. An app directory may also hold a `demo.json` (a view with made-up
data), which `mudi-screen -preview` shows instead of running the app, so a
page can be designed on a computer:

```sh
mudi-screen -fonts dev/fonts -apps apps -lang ru -preview out.png -page traffic:main -scroll 200
```

`-fonts` is a directory with `default_medium.ttf`, `default_semibold.ttf` and
`default_bold.ttf`: the router's own (`/etc/gl_screen/language/ttf`, IBM Plex
Sans) or IBM Plex Sans Medium, SemiBold and Bold from
<https://github.com/IBM/plex> under those names.
