# rust-suzuri spike: report

Irori card #255. The question: is a terminal core rebuilt in Rust with a
Tauri UI worth continuing, or should Go suzuri keep being upgraded (#246)?
Everything below was measured on the same machine (MacBook Pro, M5 Max,
macOS) unless marked Windows. The Windows 11 run is kestrel's step on #255.

## What was built

- `crates/term` is the core.
  - A `Pane` is one shell. Its actor thread alone writes to the PTY,
    resizes it and closes it, so those calls never overlap (the ConPTY
    hazard in suzuri's stability report).
  - A reader thread alone feeds the VT engine. A frame thread alone takes
    dirty-row snapshots.
  - Resize is latest-wins. Close, shell exit and reader EOF go through one
    sequence and produce exactly one Exit.
  - DEC 2026 synchronized updates hold the frame until they end or time out.
  - There are no timing gates.
  - The PTY is portable-pty: openpty on macOS, ConPTY on Windows. On
    Windows it creates the console with `PSEUDOCONSOLE_RESIZE_QUIRK` and
    closes its copies of the console-side pipe ends right after creation.
    It also loads a `conpty.dll` placed next to the exe, so bundling
    OpenConsole is a file copy.
- `app` is the Tauri 2 window. Each pane's frames reach the webview as
  binary. There are two transports, picked with `RSZ_TRANSPORT`:
  - the default per-pane Tauri `Channel`;
  - `ws`: one WebSocket on 127.0.0.1 for every pane.
- `ui` is plain HTML/JS. It has splits, focus, a key encoder and a canvas
  renderer that repaints only the rows in each frame. The look is suzuri's
  Inkstone theme and its bundled Gohu font. The chrome (title bar, pane
  headers, focus ring) is real UI, not cells.
- `tools/fakeagent` is a reproducible Claude-Code-like pane after marble's
  #251 design. It has a live region at 80 ms inside DEC 2026, bursts of
  20–1500 coloured lines with CJK and emoji, an OSC title spinner, and
  input echoed at once.
- The bench tools:
  - `scripts/latprobe.swift` is an external latency probe that works on
    any app.
  - `scripts/bench-mac.sh` runs 8 streaming panes against each app.
  - `crates/term/examples/replay*.rs` replays the #249 corpus through
    alacritty_terminal and through libghostty-vt.

Done on macOS:
- one window, splits, focus and closing;
- 8 panes running Claude Code (`docs/img/claude-8-panes-mac.png`);
- the shell, vim-style alt screen, scrollback, resize reflow, Claude Code's
  dialogs and arrow keys.

Left out on purpose: selection/copy, mouse reporting to apps, IME
composition beyond the basics, tabs, and every suzuri extra.

## Numbers (macOS, M5 Max)

### Input latency, 8 streaming panes

This is an external pixel probe, used identically for both apps.
- A key is posted to the app's pid with a 40 ms hold, alternating "x" and
  Delete every 100 ms.
- ScreenCaptureKit watches the focused pane's input line on the real
  screen.
- A sample runs from the key's post time to the display time of the first
  frame where that line changed.
- 300 samples per run. The focused pane runs a quiet fakeagent (live region
  only) and the other 7 stream bursts.
- Both windows were 1200×800. Suzuri ran with your config minus the rain
  ambient and the intro.

| app | runs | p50 ms | p99 ms | max ms | CPU (cores, app + helpers) |
|---|---|---|---|---|---|
| Go suzuri (main @ 100b8a5) | 3 | 48.8–49.2 | 52.1–55.6 | 58.5 | 0.34–0.35 |
| rust-suzuri, Tauri Channel | 3 | 31.5–32.6 | 36.8–40.9 | 50.3 | 0.20 |
| rust-suzuri, WebSocket frames | 2 | 30.5–30.6 | 33.3–33.4 | — | 0.18 |

Suzuri's floor is its 60 Hz ebiten tick plus vsync. Its p50 is about 49 ms
even with one pane. rust-suzuri paints in the webview's next animation
frame after the bytes arrive (120 Hz display).

### Memory (footprint, app plus its helper processes)

| | Go suzuri | rust-suzuri |
|---|---|---|
| 1 idle pane | 452 MB | 136 MB (app 41, WebKit 95) |
| 8 streaming panes, Channel | 518–520 MB, flat | 526–848 MB, median ~730 |
| 8 streaming panes, WebSocket | — | 500–740 MB, median ~630 |

The swing in rust-suzuri is the webview's heap. WebContent climbs while
frames stream, then drops back to about 200 MB when WebKit reclaims it.
footprint shows 895 MB "reclaimable" at one point.

The Channel transport adds churn for every frame:
- every message runs an `eval`;
- a message over 1 KB also costs a fetch round trip (Tauri 2.12's
  channel.rs).

One socket removes that, and its memory and p99 were lower. The rest is the
webview's fixed cost plus scrollback. rust-suzuri keeps 10,000 lines per
pane. Suzuri's ingest keeps none: reed found on #249 that scrolled-off
lines never reach history.

### VT engines on the #249 replay corpus

The corpus was copied from suzuri PR #95. Throughput is MB/s of PTY bytes:
- "rec": recorded read chunks and resizes;
- "rec+snap": the same plus a dirty-row snapshot after every read (the
  worst case for the frame thread).

| engine | all streams, rec | rec+snap | claude-stream rec | cat-large rec |
|---|---|---|---|---|
| Go suzuri `ingestPTY` (reed, #249) | — | — | 4.07 | 6.51 |
| vt10x alone (reed) | — | — | 61.6 | — |
| alacritty_terminal 0.26 | 135 | 49 | 192 | 140 |
| libghostty-vt (ghostty 7b267cd, ReleaseFast) | 404 | 52 | 463 | 437 |

- Both Rust paths are 30–100× faster than the Go app's ingest. That gap
  is the design, not the language: parsing runs off the UI thread, and
  only dirty rows are snapshotted (orca's point on #255).
- libghostty-vt parses about 3× faster than alacritty_terminal. But its
  per-cell C API (graphemes, fg and bg as separate calls) makes a
  snapshot cost about the same, so the gain shows only on feed-bound
  workloads.
- 1-byte chunks: alacritty runs 60–80 MB/s and ghostty 77–139 MB/s. FFI
  per call isn't a problem.
- Correctness against the vt10x goldens: alacritty's final screen matches
  on 8 of 9 streams. That counts U+00A0, which Claude Code writes and the
  goldens show as a space, as a space.
- The odd one is the synthetic CSI 3J wipe. Two rows differ by one
  trailing box-drawing character, at the last column where vt10x wraps
  differently.
- The libghostty-vt screen text wasn't checked (no text dump in the bench
  yet).

Eight Claude Code panes produce well under 1 MB/s in total, so every engine
here has 100× headroom. Engine speed doesn't decide this.

## The two choices

### VT engine: alacritty_terminal (now), libghostty-vt as an upgrade

| | alacritty_terminal | libghostty-vt | vte + own grid |
|---|---|---|---|
| Build | pure Rust, cargo only | Zig 0.17 toolchain, C ABI; the Windows cross-build is untested | pure Rust |
| Speed | 135 MB/s | 404 MB/s | unknown |
| Grid, scrollback, damage, DEC 2026, reflow | all built in | all built in, plus Kitty graphics and a render-state API | all to write |
| Risk | API churn between minors | young C API, Zig in the release build | a terminal emulator to maintain |

Start with alacritty_terminal: it's in, it's correct on the corpus, and it's
fast enough by two orders of magnitude. Everything above the `Engine` trait
stays the same. Switch to libghostty-vt if Kitty graphics or its reflow
matter more than keeping Zig out of the build. Don't build vte plus an own
grid.

### Rendering: webview canvas (built) vs a native wgpu surface (not built)

The webview path is built and measured. On macOS it beats Go suzuri on
latency (p99 33–41 ms vs 52–56 ms) and on CPU. It costs memory: about
+100–200 MB median under load, with GC swings. The transport matters
(Channel vs socket), and the next steps there are cheap:
- frames from all panes coalesced into one message per tick;
- dirty column ranges instead of whole rows;
- an OffscreenCanvas worker.

A wgpu surface beside the Tauri UI wasn't built. What it would take:
- A child Metal or D3D surface sits under or over the webview, with a
  transparent webview for the chrome.
- Input has to be routed between the two.
- DPI and resize have to be synchronized per OS.
- WebView2's own composition on Windows has to be dealt with.

That is weeks, not days. suzuri's own `chrome/` crate (Rust + wgpu) and
`surface/` (Tauri + xterm.js) already went down both roads as visual
prototypes. The win would be memory, about the webview's 100–200 MB, and
glyph quality control. It probably wouldn't be latency, where the webview path already beats
Go suzuri by about 15–20 ms.

## Windows 11

kestrel's run on the PC (step s6 on #255) fills this in: build, 8 Claude
panes, ConPTY churn, and the same 8-pane bench against Go suzuri's #247
telemetry line.

## Recommendation

(To be finished once the Windows run is in.)
