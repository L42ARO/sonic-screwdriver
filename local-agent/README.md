# local-agent

The dumb executor half of the system. It runs on the Windows machine that has the legacy
software installed, owns that machine's screen, mouse and keyboard, and does exactly what
it is told. No AI lives here: the compute-box decides, the local-agent acts.

The only interface to the target software is the one a person has: pixels in, mouse and
keyboard out. There is deliberately no accessibility-tree or window-message shortcut.

## Build and run

```
cargo build --release
./target/release/SonicScrew-Local-Agent.exe screenshot --out shot.png
./target/release/SonicScrew-Local-Agent.exe windows
./target/release/SonicScrew-Local-Agent.exe exec scripts/dentasoft-smoke.json --out-dir run-out
```

Use the release build. The debug build encodes screenshots several times slower.

## Desktop setup and tray

Double-click `SonicScrew-Local-Agent.exe` with no arguments. The first run opens a black-and-white
setup window for the compute-box URL, the machine token and optional start at sign-in.
Get the **machine token** from the compute-box dashboard's **Add machine** button; it
identifies this computer as that machine. Never enter an OpenAI key here. Test checks the
token without taking over the machine or interrupting the current connection. If the URL
is plain `http://` to another computer, the window warns that the token and screenshots
travel unencrypted.

Preferences live in `%LOCALAPPDATA%\SonicScrewdriver\config.json`; the token lives in
Windows Credential Manager, referenced by an opaque ID in the JSON. Saves replace the
configuration atomically. `SONIC_CONFIG_DIR` can override the preferences directory for
isolated development/testing. This is separate from program installation settings, which
remain in the compute-box database.

With saved settings, launching the executable connects in the background and adds a
monochrome tray icon. Launching it again does nothing. Use the tray icon's **Settings**
menu to edit configuration; closing Settings hides the window. The tray also provides
connection status, pause/resume, connection logs, start at sign-in and Quit. Pause, Quit,
and configuration changes wait for the current job to finish. Unsaved edits do not change
the running connection. Settings errors remain visible; a missing stored credential opens
setup again so it can be repaired.

The machine's name comes from its token: the tray shows "Connected as <name>". To move
the agent to another machine, paste that machine's token. Regenerating or removing the
machine in the dashboard disconnects the agent. Network outages retry with backoff.
Authentication errors wait for a settings change; a machine already connected from
another agent is retried without replacing it.

The executor runs in the signed-in user's desktop session. Start at sign-in is optional;
it does not install a Windows service or enable unattended use of a locked desktop.

## Connected mode

```
./target/release/SonicScrew-Local-Agent.exe connect --url http://localhost:8080 --token sonic_mt_...
# Or, after configuring the desktop app:
./target/release/SonicScrew-Local-Agent.exe connect
```

Dials out to the compute-box (`--url`, saved preference, then localhost), says hello
with its machine token and screen size, then carries out every action it receives
and sends back the result with a base64 screenshot. On network failure it
reconnects after 1 s, doubling to 30 s, with jitter. The compute-box pings every 15 s;
45 s of silence counts as a dead connection. Screen work runs on its own thread, so
heartbeats keep flowing during slow actions.

The overlay appears when the compute-box starts a job and disappears when it ends or the
connection drops. `--url` and `--token` can also come from `SONIC_AGENT_URL` and
`SONIC_MACHINE_TOKEN`.

CLI flags/environment override saved preferences. HTTPS/WSS is recommended for remote
servers; plain HTTP/WS also works, with the warning described above. Base paths are
supported (e.g. `https://example.com/sonic` becomes `wss://example.com/sonic/v1/agent`).
TLS uses the machine's trusted certificate roots. The CLI shares single-instance protection
with the tray application. Release CLI commands attach to their parent console; launching
the desktop app does not leave a terminal open.

The message format is documented at the top of `compute-box/src/services/hub/messages.go`.

## Verification

`cargo test` covers settings persistence, isolated Credential Manager entries, single
instances, URL validation, and deferring a connection change until the active job ends.
The ignored lifecycle helper is invoked by its parent test rather than run directly.

From the repo root, after building both binaries, a process-level smoke test is available:

```powershell
python local-agent/tests/connection_smoke.py --box-exe path/to/compute-box.exe --agent-exe local-agent/target/release/SonicScrew-Local-Agent.exe
```

It uses temporary server data and settings, starts no desktop jobs and makes no model
calls. It verifies authentication, duplicate executable launches, stable server tokens,
and reconnection after a server restart, without printing the generated test secret.

## Scripted mode

`exec` takes a JSON array of actions, runs them in order, writes every resulting
screenshot to `--out-dir` as `step_NN_<action>.png`, and prints one JSON result per
action. `scripts/dentasoft-smoke.json` logs in to the DentaSoft test target, looks up a
patient, edits the balance and saves. It is tied to the window sitting at its default
position on a 1920x1080 display, since scripted mode has no eyes of its own.

## Coordinates

Every coordinate the agent accepts is in *screenshot space*: the pixel grid of the
screenshots it returns. Screenshots are downscaled so they are at most `--max-width`
pixels wide (default 1280); the agent maps back to physical pixels itself and reports the
`scale` with every shot. The process declares per-monitor DPI awareness so Windows display
scaling never shifts a click.

## Actions

| action | fields | notes |
|---|---|---|
| `screenshot` | | capture now, no waiting |
| `zoom` | `x y w h` | full-resolution crop of a screenshot-space rect |
| `click` | `x y [button] [count]` | button: left, right, middle |
| `double_click` / `right_click` | `x y` | |
| `move` | `x y` | |
| `drag` | `from [x,y]`, `to [x,y]` | |
| `scroll` | `x y [dx] [dy]` | wheel notches, dy > 0 scrolls down |
| `type` | `text` | literal unicode text |
| `key` | `keys [count]` | `enter`, `tab`, `f3`, `ctrl+a`, `alt+f4`, single characters; `count` repeats |
| `wait` | `ms` | sleep, then screenshot |
| `focus_window` | `title_contains` | bring a window to the front; returns its rect |
| `list_running_applications` | | process IDs, names, accessible executable paths and visible window metadata in `data`, plus screenshot |
| `launch_program` | `executable`, `window_title_contains`, optional `args`, `working_directory`, `startup_timeout_ms` | executor action resolved by the compute-box from a project program ID |

Every state-changing action waits briefly, then polls until two consecutive captures are
nearly identical (a blinking caret is tolerated) or 2.5 s pass, and returns a fresh
screenshot. If the screen was still changing at the timeout the result carries
`"unsettled": true`.

Application launch uses a direct process spawn with an argument array and no terminal.
It matches existing windows by both executable path (case insensitive) and title substring,
restores minimized windows and focuses the match. Multiple matches return
`application_ambiguous`. A process without a matching window is not considered ready;
startup waits up to the configured timeout (default 30 seconds, maximum 120 seconds).
Launch failures have an `error_code` as well as an error message.

For native installations with no arguments, an existing executable process prevents a
duplicate spawn while waiting for its window. For programs hosted by an interpreter,
executable paths alone cannot distinguish scripts: matching windows and launches tracked
by this agent instance identify them instead. Timed-out launches remain tracked while
their process lives, including across WebSocket reconnects; that tracking is not persisted
across agent restarts. Programs that hand off to a different executable need configuration
for the actual UI executable; shortcuts and launcher handoff discovery are not supported.
Process metadata may have `executable: null` when Windows denies access. Application
contents are still read solely from screenshots.

## "Agent at work" overlay

While `exec` runs, a click-through, always-on-top overlay shows a softly pulsing glow
around the screen edge and a banner near the bottom: "AGENT AT WORK · step 12/30: typing
"P-1004"". It tells whoever is near the machine that the mouse and keyboard are being
driven. The window is flagged as excluded from screen capture, so the agent's own
screenshots never contain it and its animation does not disturb settle detection.

- `--no-overlay` turns it off.
- `AGENT_OVERLAY_CAPTURABLE=1` keeps it visible to capture, to check what a person sees.

## Layout

- `src/protocol.rs` action and result types; this becomes the wire protocol
- `src/screen.rs` capture, downscale, zoom, settle detection
- `src/input.rs` mouse and keyboard injection, key-chord parsing
- `src/win.rs` DPI awareness, top-level window listing and focus
- `src/overlay.rs` the on-screen "agent at work" indicator
- `src/executor.rs` runs one action and returns its result
- `src/agent.rs` connected mode: WebSocket client, reconnects, screen thread
- `src/main.rs` CLI

## Focusing windows

`focus_window` has to work from a background process, which Windows restricts. It tries
the normal call, then attaches to the foreground window's input thread, then clicks the
window's title bar the way a person would.

## Not yet done

- Stop hotkey (the overlay is informational only; it does not take input)
- Multi-monitor selection (primary monitor only for now)
