# suzuri-chrome 1.0.0

**Native GPU shell chrome for [suzuri](../README.md), in Rust.**  
No React. No HTML. No Chromium. No Canvas UI capture trees.

This is the product paint path. The web `surface/` spike was the visual reference.

## Architecture rule

> **Anything that isn’t shell output never snaps to a character cell.**

| Layer | What lives here | Tech |
|-------|-----------------|------|
| **Smooth chrome** | title bar, tabs, warp frame, rain, refractive glass, lens, modal | **this crate** — `winit` + `wgpu` |
| **Cell pane** | shell / TUI only | mono text in the glass well (live PTY + ANSI) |

## Run

```bash
cd ~/projects/suzuri/chrome
cargo run --release
```

## 1.0.0 vertical slice

- **Frameless** GPU window, macOS rounded corners + traffic lights  
- **Canvas UI glass** on panes, nav chips, and settings modal (shared optics + darken)  
- **Canvas UI glyph rain** (phase reveal, tip light, stratified columns, soft head orbs)  
- **Mouse glass lens** (same model as panes; toggle in settings)  
- **Live multi-tab PTY** (`portable-pty` + ANSI-lite) with mock fallback  
- **Warp bar** — type + Enter into PTY, history ↑/↓, paste ⌘V  
- **Scrollback** — mouse wheel over the terminal  
- **Settings glass modal** — agility dialog spring; toggles rain / lens / glass darken  
- **8-point spacing** — edge 16, stack/inset/cluster 8  
- **Cursor blink**, middle-click close tab, ⌘T / ⌘W  

### Layout (spacing system)

```
title 32 (clear bar + lights + title)
tabs 40
stack 8
┌ single glass well ─────────────────┐
│  PTY / history (cell grid)         │
│  ──────────────────── ASCII line   │
│  ❯ local command input             │
└────────────────────────────────────┘
edge 16 bottom / sides
```

One glass pane only — input is text at the bottom of the well (not a second refractive panel).

### Shortcuts

| Key | Action |
|-----|--------|
| ⌘/Ctrl+T | New tab (+ PTY) |
| ⌘/Ctrl+W | Close tab (quit if last) |
| Middle-click tab | Close tab |
| ⌘/Ctrl+, | Settings |
| ⌘/Ctrl+V | Paste |
| Esc | Close settings / quit |
| ↑/↓ (warp) | Command history |
| Wheel | Scrollback |
| Settings **1** / **2** | Toggle rain / lens |
| Settings **[** / **]** | Glass darken |

### Library (`suzuri_chrome`)

```rust
use suzuri_chrome::{cells, layout, pty, session, ansi, settings};
```

Binary-only: window, wgpu renderer, glyphon. Lib: protocol + state for embedding / host merge.

## Roadmap (post-1.0.0)

1. Host merge — Go or pure-Rust host links `suzuri_chrome` for layout/cells/pty/ansi  
2. True cell-bg quads (replace █ underpaint)  
3. Selection + copy  
4. OSC title / cwd tab labels  
5. Bracketed paste, fuller VT  

## Relation to npm `kussetsu`

The TypeScript Kussetsu package remains a **web UI framework**.  
This crate reuses the *ideas* (own framebuffer, glass samples backdrop, layout outside cells) for **suzuri only**.
