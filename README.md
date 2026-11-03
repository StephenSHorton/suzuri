# rust-suzuri

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
cargo run -p rust-suzuri            # one pane
RSZ_PANES=8 cargo run -p rust-suzuri # 8 panes, 4 x 2
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

Tests: `cargo test` and `node --test 'ui/*.test.mjs'`.

Dev knobs: `RSZ_CMD` (pane command instead of the shell), `RSZ_PANES`,
`RSZ_BENCH=1` with `RSZ_BENCH_SECS` and `RSZ_BENCH_OUT` (latency probe),
`RSZ_TRACE=1` (log PTY reads).
