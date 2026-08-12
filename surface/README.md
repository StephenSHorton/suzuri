# suzuri surface (web spike)

> **Product path is native:** see [`../chrome`](../chrome) — Rust + wgpu, no React/webview.  
> This directory is a **visual / UX reference** from the Kussetsu-on-WebGPU spike.

Experimental **Tauri + web** face for [suzuri](../README.md) — kept for comparison, not the long-term host.

## Architecture rule

> **Anything that isn’t shell output never snaps to a character cell.**

| Layer | What lives here | Tech |
|-------|-----------------|------|
| **Smooth chrome** | titlebar, tabs, settings, Warp bar, rain, refractive glass | **[Kussetsu](https://github.com/StephenSHorton/kussetsu)** (WebGPU) |
| **Cell pane** | shell / TUI only | **xterm.js** in a DOM hole |

Kussetsu owns the framebuffer for chrome: one WebGPU device, glass samples a real backdrop (matrix rain shader), no per-panel capture trees. The terminal is a deliberate **hole** — sharp cells, not refracted through glass (honest PTY surface).

## Vertical slice

| Piece | Status |
|-------|--------|
| Frameless window | ✅ custom GPU titlebar + drag strip + min/max/close |
| Kussetsu chrome | ✅ tabs · settings · warp · glass · rain backdrop |
| Cell-grid terminal | ✅ xterm.js (+ fit); mock shell |
| Canvas UI / CEF / html-in-canvas | ❌ removed (wrong trap for this product) |
| Real PTY / ConPTY | ❌ next |

## Dev

```bash
cd surface
bun install
bun run dev          # Vite only → http://localhost:1420  (needs WebGPU browser)
bun run tauri dev    # frameless native window + Vite HMR
```

Requires **WebGPU** (Chrome 113+, Edge, Safari 18+, recent Firefox), Rust (stable), Bun.

`kussetsu` is linked from `~/projects/kussetsu` (`file:../../kussetsu`) so chrome tracks the local renderer.

## Layout

```
surface/
  src/
    components/
      AppShell.tsx       GpuCanvas + terminal hole + drag strip
      ShellChrome.tsx    home GPU tree (tabs / frame / warp)
      SettingsChrome.tsx settings GPU tree
      TerminalPane.tsx   xterm hole only
    lib/theme.ts         glass params, layout constants, rain WGSL
  src-tauri/             stock Tauri 2 (no CEF)
```

## Relation to the Go host

This does **not** replace `cmd/suzuri`. It explores a second surface so we can decide what to share (config, MCP, PTY engine) before committing.
