# Rust suzuri spike: report

Irori card #255. The question: is a terminal core rebuilt in Rust with a
Tauri UI worth continuing, or should Go suzuri keep being upgraded (#246)?
Everything below was measured on one machine (MacBook Pro, M5 Max, macOS)
unless marked Windows. The Windows 11 run is kestrel's step on #255. Raw
results are in `docs/bench/*.json`.

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
- `ui` is plain HTML/JS with no bundler. It has splits, focus, a key
  encoder and a canvas renderer that repaints only the rows in each frame.
  The look is suzuri's Inkstone theme and its bundled Gohu font. The
  chrome (title bar, pane headers, focus ring) is real UI, not cells.
- `tools/fakeagent` is a reproducible Claude-Code-like pane after marble's
  #251 design. It has a live region redrawn every 80 ms inside DEC 2026,
  bursts of 20–1500 coloured lines with CJK and emoji, an OSC title
  spinner, and input echoed at once.
- The bench tools:
  - `scripts/latprobe.swift` is an external latency probe for any macOS app.
  - `scripts/bench-mac.sh` runs both apps the same way.
  - `scripts/bench-windows.ps1` and `scripts/sendkeys.ps1` are for the PC.
  - `crates/term/examples/replay*.rs` replays the #249 corpus.

Done on macOS:
- one window, splits, focus and closing;
- 8 panes running Claude Code (`docs/img/claude-8-panes-mac.png`);
- the shell, alt-screen apps, scrollback, resize reflow, and Claude Code's
  dialogs and arrow-key menus.

Left out on purpose: selection/copy, mouse reporting to apps, IME beyond
the basics, tabs, and every suzuri extra.

## Numbers (macOS, M5 Max)

### Input latency, 8 streaming panes

This is an external pixel probe, used identically for both apps.
- A key is posted to the app's pid with a 40 ms hold, alternating "x" and
  Delete every 100 ms.
- ScreenCaptureKit watches the focused pane's input line on the real
  screen.
- A sample runs from the key's post time to the display time of the first
  captured frame where that line changed. 300 samples per run.
- The focused pane runs a quiet fakeagent (live region only) and the other
  7 stream bursts.
- Both windows are 1200×800, split 4×2 into equal panes:
  - `docs/img/bench-rsz-8-fakeagent.png`
  - `docs/img/bench-suzuri-8-fakeagent.png`
- Suzuri runs with your config minus the rain ambient and the intro, under
  a private HOME.

| app | runs | p50 ms | p90 ms | p99 ms | max ms | CPU cores (app + helpers) |
|---|---|---|---|---|---|---|
| Go suzuri (main @ 100b8a5) | 2 | 48.5–49.4 | 51.2–52.0 | 52.8–53.9 | 57.8 | 0.35 |
| Rust suzuri, Tauri Channel | 4 | 25.4–32.6 | 32.4–35.5 | 35.3–40.9 | 50.3 | 0.20 |
| Rust suzuri, WebSocket frames | 2 | 30.5–30.6 | 32.7–33.1 | 33.3–33.4 | 33.5 | 0.18 |
| Rust suzuri, scrollback off | 1 | 25.0 | 31.9 | 33.3 | 33.6 | 0.20 |
| Rust suzuri f06a673 (final: socket, ack flow control, key bypass) | 2 | 30.0–31.2 | 32.9–34.4 | 33.3–36.5 | 37.1 | n/a* |

- Rust suzuri's p50 drifted between runs, 25 to 33 ms, with the machine's
  other load. Suzuri's didn't, about 49 ms every time.
- Channel against WebSocket is within that drift on latency.
- \*The final runs also had the app time itself (RSZ_BENCH=1). It quits
  after its window, so the CPU figure for those runs isn't comparable.
  In-app p50 was 10 ms in both runs. p99 was 17 and 19 ms.
- Three earlier suzuri runs had an uneven layout (the key sequence put
  three panes in one column). They gave the same numbers (p99 52–56 ms) and
  aren't in the table.
- One pane, before the release build: suzuri p50 44 ms / p99 58 ms.

**In-app vs on-screen.** In one run Rust suzuri also timed the same keys
itself, from the key event's timestamp to the end of the paint after it.
That is oak's definition, used by Go suzuri's #247 telemetry line.
- In-app it read p50 9 ms / p99 19 ms. On screen the same run read p50
  25 ms / p99 33 ms.
- So in-app numbers leave out about 15 ms of compositing and scan-out on
  macOS.
- The Windows section compares in-app with in-app. Don't mix it with the
  pixel numbers above.

### Memory (footprint: the app plus its own helper processes)

| | Go suzuri | Rust suzuri |
|---|---|---|
| 1 idle pane | 452 MB | 136 MB (app 41, WebKit WebContent 75, GPU 16, network 4) |
| 8 streaming, Channel, 10k-line scrollback | 518–524 MB, flat | median 650–750, range 526–848 MB |
| 8 streaming, WebSocket | — | median 600–656, range 500–740 MB |
| 8 streaming, scrollback off | — | median 609, range 504–701 MB |

Sampled every 5 s through each run.
- **The webview heap.** Rust suzuri's swing is the webview's heap.
  WebContent climbs while frames stream (one sample was 1 GB), then drops
  back to about 200 MB when WebKit reclaims it. footprint showed 895 MB of
  WebKit malloc "reclaimable".
- **The transport.** The Channel transport adds churn for every frame:
  every message runs an `eval`, and a message over 1 KB adds a fetch round
  trip (Tauri 2.12 `ipc/channel.rs`). One socket removes that, and its
  median was lower.
- **Scrollback.** Turning scrollback off saves about 100 MB median at
  these pane sizes. Suzuri keeps no scrollback through its ingest at all:
  reed found on #249 that scrolled-off lines never reach history. Its flat
  number isn't like for like.
- **Idle cost.** Idle, Rust suzuri is a third of Go suzuri. Go suzuri's ebiten
  window holds about 300 MB of graphics memory.

### VT engines on the #249 replay corpus

The corpus was copied from suzuri PR #95. Throughput is MB/s of PTY bytes:
- "rec": recorded read chunks and resizes;
- "rec+snap": the same plus a dirty-row snapshot after every read (the
  worst case for the frame thread).

| engine | claude-stream rec | cat-large rec | all streams rec | all streams rec+snap |
|---|---|---|---|---|
| Go suzuri `ingestPTY` (reed, #249) | 4.07 | 6.51 | — | — |
| vt10x alone (reed) | 61.6 | — | — | — |
| alacritty_terminal 0.26 | 192 | 140 | 135 | 49 |
| libghostty-vt (ghostty 7b267cd, ReleaseFast) | 463 | 437 | 404 | 52 |

- On claude-stream, alacritty_terminal ingests 47× faster than Go
  suzuri's path and libghostty-vt 114× faster. On cat-large it's 21× and
  67×.
- Most of that gap is the design, not the language. vt10x alone does
  61.6 MB/s. The Go app's time goes into re-reading the whole screen
  around every batch (orca's point on #255). The Rust core parses off the
  UI thread and snapshots dirty rows only.
- libghostty-vt parses about 3× faster than alacritty_terminal. But its
  per-cell C API (graphemes, fg and bg as separate calls) makes a snapshot
  cost about the same, so the gain shows only when feeding is the
  bottleneck.
- 1-byte chunks: alacritty runs 60–80 MB/s and ghostty 77–139 MB/s. FFI
  per call isn't a problem.
- Correctness against the vt10x goldens: alacritty's final screen matches
  on 8 of 9 streams. That counts U+00A0, which Claude Code writes and the
  goldens show as a space, as a space.
- The odd one is the synthetic CSI 3J wipe. Two rows differ by one
  trailing box-drawing character at the last column.
- The libghostty-vt screen wasn't checked against the goldens.

In the bench, 8 fakeagent panes produce 0.12 MB/s in total. The Rust core
spends 1.4 ms/s parsing it and 0.3 ms/s snapshotting it. The webview
spends 10 ms/s painting it. Engine speed doesn't decide this; rendering
does.

## The two choices

### VT engine: alacritty_terminal now, libghostty-vt as an upgrade

| | alacritty_terminal | libghostty-vt | vte + own grid |
|---|---|---|---|
| Build | pure Rust, cargo only | Zig 0.17 toolchain, C ABI; the Windows build is untested | pure Rust |
| Speed (corpus) | 135 MB/s | 404 MB/s | not measured |
| Grid, scrollback, damage, DEC 2026, reflow | built in | built in, plus Kitty graphics and a render-state API | all to write |
| Risk | API churn between minors | a young C API, Zig in the release build | a terminal emulator to maintain |

Keep alacritty_terminal: it's in, it matches the goldens and it's fast
enough by two orders of magnitude. Everything above the `Engine` trait
stays put if that changes. Switch to libghostty-vt when Kitty graphics or
its reflow matter more than keeping Zig out of the build. Don't build vte
plus an own grid.

### Rendering: webview canvas (built) vs a native wgpu surface (not built)

The webview path is built and measured. On macOS it beats Go suzuri on
latency (p99 33–41 ms vs 53–54 ms) and on CPU (0.2 vs 0.35 cores). It
costs memory under load: about +80–230 MB median over suzuri, with GC
swings. The next steps are cheap and each is a day or less:
- the socket transport, already built (`RSZ_TRANSPORT=ws`);
- one message per tick for all panes;
- dirty column ranges instead of whole rows;
- painting in an OffscreenCanvas worker.

A wgpu surface beside the Tauri UI wasn't built. What it would take:
- A child Metal or D3D surface sits under or over the webview, with a
  transparent webview for the chrome.
- Input has to be routed between the two.
- DPI and resize have to be synchronized per OS.
- WebView2's own composition on Windows has to be dealt with.

That is weeks, not days. suzuri's `chrome/` crate (Rust + wgpu) and
`surface/` (Tauri + xterm.js) already went down both roads as visual
prototypes. The win would be the webview's memory and full control of
glyphs. It probably wouldn't be latency, where the webview path already
beats Go suzuri by 15–20 ms.

## What continuing costs

Rust suzuri has the terminal and none of the product around it. Go suzuri's
features that would have to be rebuilt:

| feature | rough size |
|---|---|
| selection, copy, mouse, IME, links | about a week |
| tabs, the command palette and settings | about a week |
| the 16 themes and fonts | days |
| the Warp-style input bar | about a week |
| intros and ambient effects | optional |
| the MCP bridge, workspace, transfer | about a week each, or reuse the Go pieces as sidecars |
| auto-update, signed and notarized macOS and Windows packaging | about a week; Tauri has an updater |
| notices, inline images, Kitty graphics | about a week |

That's on the order of 6–8 weeks to parity, less if some extras stay
behind. On Go suzuri, the #246 cards fix the same Windows problems: a
session actor, an off-thread VT, damage-based paint. Its baseline ingest is
47× slower, and its paint floor sits behind ebiten's 60 Hz tick.

## Windows 11 (kestrel, the PC: Ryzen 9850X3D, RTX 5080)

First run, main @ b28fa7e:

- **Build.** `cargo build --release -p suzuri -p fakeagent` worked the
  first time, in 1m28s.
- **Smoke test: passed.** In PowerShell 5.1: typing, arrows, Ctrl+C,
  split right and down, Alt+arrow focus, close, and wheel scrollback.
- **Churn: no crash.** 8 streaming panes for 4 minutes. 645 window
  resizes (900–1800 × 600–1100) mixed with splits, closes and focus moves.
  The app's working set was 58.7 MB at the end.
- **8 Claude Code panes: failed.**
  - Panes stayed blank. 2 panes worked.
  - With 8 panes, the layout didn't follow a window resize.
  - Two causes were found and fixed in 3ce10a1:
    - A frame-thread deadlock. Replies to queries inside a timed-out DEC
      2026 block went nowhere, so the frame thread blocked and the program
      waited forever. A regression test now covers it.
    - No flow control. Every frame was an eval on WebView2's UI thread.
  - Rerun on 3ce10a1: all 8 Claude panes drew within 20 s, and moving the
    window to 1400×900 relaid them out. It also held with the PC at 100%
    CPU (f06a673). With `RSZ_TRANSPORT=channel` only 4 of 8 drew and the
    layout didn't follow, so the socket transport is needed on Windows,
    not just the deadlock fix (screenshots `rsz2-claude8-ws.png` and
    `rsz2-claude8-channel.png` are on card #255).
- **Bench, 8 fakeagent panes, real SendInput keys.** Both apps are timed
  in-app, from key event to paint (oak's definition):

| | Go suzuri (oak/hang-watchdog, dev build) | Rust suzuri (b28fa7e, Channel) |
|---|---|---|
| input p99 | 31.4–32.9 ms (one 10 s window 142.6 ms) | 8.6 ms (p50 4.5, max 8.8, n=330) |
| CPU over the ~50 s run | 47 s | 5.4 s |
| memory | 67 MB private / 47 MB working set | 274 MB private / 448 MB working set (app + 7 WebView2 processes) |

Caveats for that run:
- Go's 8 panes were an uneven layout, and all 8 streamed, about 2.5 MB per
  10 s. Rust suzuri had one quiet probe pane, about 1.6 MB per 10 s.
- In Go's screenshot, text from the right-hand columns draws across pane
  borders.

**Latency after the fixes.** The ack flow control and the socket first
cost about 7 ms at p50 (p50 about 12 / p99 about 18). Two causes, both fixed:
- The key's echo frame waited for the previous frame's paint ack. Fixed in
  7480288: for 100 ms after a key, frames skip the ack limit and pacing.
- The socket loop waited with 2 ms timeouts, which Windows rounds up to its
  15.6 ms tick. Fixed in f06a673: blocking reads on their own thread, with
  no short timeouts.

On f06a673, quiet PC, interleaved 40 s runs, in-app key_event_ms:

| build | p50 ms | p90 ms | p99 ms | max ms | over 16 ms |
|---|---|---|---|---|---|
| f06a673, WebSocket (default) | 5.9–6.3 | 9.6–9.8 | 11.6–12.3 | 13.9 | 0 |
| f06a673, Tauri Channel | 4.4–4.5 | 7.6–7.7 | 8.3–8.4 | 8.8 | 0 |
| b28fa7e (old build, same session) | 4.7–5.2 | 7.6–7.7 | 8.5–8.6 | 10.6 | 0 |

The socket is still about 1.5 ms (p50) to 3.5 ms (p99) behind the Channel.
That is the loopback hop. The UI also paints in requestAnimationFrame,
so a frame waits up to one display refresh after it arrives. That is the
remaining floor. Painting straight from onmessage wouldn't reach the
screen any sooner. It stays the default because the Channel starves
8 Claude panes. Memory on the rerun: 262 MB private / 419 MB working set.
Go suzuri rerun with the committed script (`scripts/windows/bench-go.ps1`):
p99 30–45 ms in the windows under keys, with one 201 ms window at the start.

Replay corpus on the PC: 73.5 MB/s feed, 33.9 MB/s feed+snapshot overall,
and claude-stream at 141 MB/s. The screens match the goldens as on the Mac.

## Recommendation

**Continue Rust suzuri** as suzuri's successor (renamed to suzuri later),
and stop putting performance work into Go suzuri beyond fixes users need
before the switch.

Why:
- **Latency.** On the Mac, on-screen p99 is 33 ms against 53 ms. On
  Windows, in-app p99 is about 12 ms against 30–45 ms, with no spikes over
  16 ms. Go suzuri had 142 ms and 201 ms windows.
- **CPU.** About half of Go's on the Mac and about a ninth on Windows
  (5.4 s vs 47 s over a run).
- **Parsing.** 135–400 MB/s against Go's 4 MB/s, 30–100× faster. That is
  where Claude Code's bursts land.
- **Windows.** The deadlock and the transport starvation that 8 Claude panes
  exposed are fixed. The deadlock has a regression test. The transport fix
  was confirmed by kestrel's 8-Claude rerun, including under full CPU
  load. The app survived 645-resize churn. ConPTY comes from portable-pty, the same code WezTerm ships.
- **Architecture.** The #246 goals (a session actor, an off-thread VT,
  damage-based paint) are the baseline here, not a retrofit.

Against it:
- **Memory.** WebView2 and WebKit cost about 260–275 MB private on Windows
  against Go's 67 MB. On the Mac it's lower idle (136 vs 452 MB) but higher
  under load (600–750 vs about 520 MB). Turning scrollback off saves about
  100 MB. If that matters, the fix is the native wgpu renderer described
  above, at the same core.
- **Parity.** 6–8 weeks of product work, as listed above. Go suzuri stays
  the daily driver until then.

Next steps, if you agree:
1. Selection, copy and mouse, then tabs and settings: what daily use needs.
2. Swap in libghostty-vt when its API settles (about 3× the feed speed).
3. Port features over from Go one card at a time, and close or retarget the
   #246 performance cards.
