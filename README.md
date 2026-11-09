# suzuri

A spike of suzuri's successor: the terminal core rebuilt in Rust with a Tauri
UI. It keeps suzuri's look (Inkstone theme, Gohu font) but the chrome is real
UI, not ASCII drawn into the cell grid. Terminal parts only: panes, splits,
shells, VT, rendering, input, resize, scrollback.

- `crates/term`: the core. A `Pane` runs one shell. Its PTY actor owns
  writes, resizes and close; a reader thread feeds the VT engine
  (libghostty-vt, #290); a frame thread sends dirty rows as binary frames.
- `app`: the Tauri shell. Each pane's frames go to the webview over a Tauri
  Channel as raw bytes.
- `ui`: plain HTML/JS, no bundler. Splits, focus, key encoding and a canvas
  renderer that repaints only the rows in each frame.

## Build

The VT engine is libghostty-vt, built from a pinned ghostty commit by
`crates/term/build.rs`. It needs Zig 0.17.0 (`zig` on PATH, or `ZIG` set
to the binary, from https://ziglang.org/download/) and git the first time;
the library is then cached per target in `~/.cache/rsz-ghostty`
(`%LOCALAPPDATA%\rsz-ghostty` on Windows, or `RSZ_GHOSTTY_CACHE`) and
linked statically. `GHOSTTY_VT_LIB=<dir>` links a library built elsewhere.
This holds for anything that builds `rsz-term`, Irori included.

## Run

```sh
cargo run -p suzuri            # one pane
RSZ_PANES=8 cargo run -p suzuri # 8 panes, 4 x 2
```

Keys (macOS / Windows): split right ⌘D / Ctrl+Shift+D, split down ⌘⇧D /
Ctrl+Shift+E, close pane ⌘W / Ctrl+Shift+W, focus ⌘⌥+arrows / Alt+arrows,
stats HUD ⌘⇧H / Ctrl+Shift+H.

Input bar (#285, Go suzuri's Warp bar): zsh, bash and PowerShell start
with a quiet prompt and the command line is an editable bar under each pane,
with the cwd above it. Enter sends the line; Shift+Enter adds a line; ↑/↓
walk history; Option/Ctrl+←/→/⌫ work by word; ⌘⌫ clears; Ctrl+C clears the
bar or interrupts. The bar steps aside for full-screen apps and for any
running program (Claude Code, ssh, a REPL) and returns at the next prompt.
`RSZ_NO_BAR=1` keeps the shell's own prompt.

Completion (#295, Go's complete.go): Tab completes the token from the file
system (in the shell's cwd, ~, absolute and drive paths) or the whole line
from history, and Tab / Shift+Tab walk the matches. Dim ghost text after the
caret shows the newest history line you've started, else the first path
match; → at the end of the line takes it.

Tests: `cargo test` and `node --test 'ui/*.test.mjs' 'ui/test/*.test.mjs' 'ui/view/test/*.test.mjs'`.
The frame fixtures in `testdata/frames` are written by `rsz-term`'s encoder
and decoded by `ui/test/frame.test.mjs`; after a wire format change,
regenerate them with `SUZURI_REGEN_FIXTURES=1 cargo test -p rsz-term --test
frame_fixtures` and bump `FORMAT_VERSION` on both sides.

Embedding `rsz-term` in another app (Irori's inline terminals): depend on
the crate by git tag or rev, and add the same ConPTY patch to your own
workspace, since a `[patch]` only applies to the workspace that declares it:

```toml
[patch.crates-io]
portable-pty = { git = "<this repo's URL>", rev = "<the same rev as rsz-term>" }
```

Cargo finds it in `vendor/portable-pty`. Without it, Windows panes use stock
portable-pty, whose flags misbehave under the bundled OpenConsole. Set
`PaneConfig.term_program` to your app's name, and `keep_prompt` when you
have no input bar.

Releases and auto-update: see [docs/releasing.md](docs/releasing.md).

Dev knobs: `RSZ_CMD` (pane command instead of the shell), `RSZ_PANES`,
`RSZ_BENCH=1` with `RSZ_BENCH_SECS` and `RSZ_BENCH_OUT` (latency probe),
`RSZ_TRACE=1` (log PTY reads).

Logs: `suzuri.log` in `~/Library/Logs/suzuri` (macOS) or
`%LOCALAPPDATA%\suzuri\logs` (Windows), `$RSZ_CONFIG_DIR/logs` when that is
set. It rolls over to `suzuri.log.1` past 5 MB and is mirrored to stderr from
a terminal. `SUZURI_LOG_LEVEL` (or `RSZ_LOG_LEVEL`) sets the level: off,
error, warn, info, debug (default) or trace. A panic writes
`suzuri-crash.txt` beside it (backtrace, version, OS, the last log lines); the
next launch says so once and renames it `suzuri-crash.prev.txt`. Agents can
read the log with `GET /v1/logs?lines=N` on the AI control bridge.
