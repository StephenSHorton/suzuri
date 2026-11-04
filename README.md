# suzuri

A spike of suzuri's successor: the terminal core rebuilt in Rust with a Tauri
UI. It keeps suzuri's look (Inkstone theme, Gohu font) but the chrome is real
UI, not ASCII drawn into the cell grid. Terminal parts only: panes, splits,
shells, VT, rendering, input, resize, scrollback.

- `crates/term`: the core. A `Pane` runs one shell. Its PTY actor owns
  writes, resizes and close; a reader thread feeds the VT engine
  (alacritty_terminal); a frame thread sends dirty rows as binary frames.
- `app`: the Tauri shell. Each pane's frames go to the webview over a Tauri
  Channel as raw bytes.
- `ui`: plain HTML/JS, no bundler. Splits, focus, key encoding and a canvas
  renderer that repaints only the rows in each frame.

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

Tests: `cargo test` and `node --test 'ui/*.test.mjs' 'ui/test/*.test.mjs'`.
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
