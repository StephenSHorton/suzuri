# suzuri-chrome (native Kussetsu)

**GPU-owned shell chrome for [suzuri](../README.md), in Rust.**  
No React. No HTML. No Chromium. No Canvas UI capture trees.

This is the long-term paint path the web `surface/` spike was validating.

## Architecture rule (unchanged)

> **Anything that isn’t shell output never snaps to a character cell.**

| Layer | What lives here | Tech |
|-------|-----------------|------|
| **Smooth chrome** | title bar, tabs, warp frame, rain, refractive glass, traffic lights | **this crate** — `winit` + `wgpu` |
| **Cell pane** | shell / TUI only | mono text in the glass well (mock shell now; PTY later) |

### Why not Bevy (yet)

Bevy is a great ECS game engine (and why Rust was attractive). A terminal host needs:

- a thin present loop that can later share the swapchain with cell paint  
- easy embedding next to the existing Go PTY/VT host  
- no game-world overhead for a few glass panels  

**winit + wgpu** is the direct port of “own the framebuffer.” Bevy can still host effects later if we want ECS; the **shaders and layout contract** are what matter.

### Why not the web surface

| Web surface (spike) | Native chrome (this) |
|---------------------|----------------------|
| React + Kussetsu npm | Scene + WGSL in-process |
| WebView / WebGPU | System GPU via wgpu |
| CEF was only for html-in-canvas | Never needed |

Keep `surface/` as a **visual reference** until parity; product path is native.

## Run

```bash
cd ~/projects/suzuri/chrome
cargo run
```

Esc quits.

### What works now

- **Frameless** GPU window with matrix rain + glass panels  
- **macOS traffic lights** (close / minimize / zoom)  
- **Title bar drag** to move the window  
- **Dynamic tabs**: click a chip to select, **+** to open a new tab  
- **Settings** overlay (⌘/, / Ctrl+, / Settings chip; Esc closes)  
- **Live PTY** via `portable-pty` (`$SHELL` / zsh / bash) with minimal ANSI  
  - Click **terminal** to send keys to the shell  
  - **Warp bar**: type a line + Enter → writes to PTY (or mock if spawn fails)  
- **Mock shell fallback** if PTY cannot spawn  
- **Resize** reflows grid + SIGWINCH  

### Layout contract

Same metrics as the surface spike (`layout.rs`):

```
title (44) · tabs (36) · pad
[ glass terminal well + mono cell text ]
pad · [ glass warp bar ]
```

## Roadmap

1. ~~Paint loop, rain, glass~~  
2. ~~Text (glyphon)~~  
3. ~~Input + traffic lights~~  
4. ~~Live PTY + ANSI-lite~~  
5. ~~Richer VT~~ (bg SGR, alt screen, scroll region, cursor hide)  
6. ~~Per-tab PTY~~ (`HashMap<tab_id, PtySession>` + per-tab grid/ANSI)  
7. **Host merge** — Go or pure-Rust host links `suzuri_chrome` lib for layout/cells/pty/ansi  

### Library (`suzuri_chrome`)

```rust
// Cargo.toml dependency path = "chrome"
use suzuri_chrome::{cells, layout, pty, session, ansi};
```

Binary-only: window, wgpu renderer, glyphon. Lib: protocol + state for embedding.

### Shortcuts

| Key | Action |
|-----|--------|
| ⌘/Ctrl+T | New tab (+ PTY) |
| ⌘/Ctrl+W | Close tab (quit if last) |
| ⌘/Ctrl+, | Settings |
| Esc | Close settings / quit |

## Relation to npm `kussetsu`

The TypeScript Kussetsu package remains a **web UI framework**.  
This crate reuses the *ideas* (own framebuffer, glass samples backdrop, layout outside cells) for **suzuri only** — it is not a React port.
