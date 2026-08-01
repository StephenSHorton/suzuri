# suzuri（硯）

[![CI](https://github.com/StephenSHorton/suzuri/actions/workflows/ci.yml/badge.svg)](https://github.com/StephenSHorton/suzuri/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-00e676.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/StephenSHorton/suzuri?color=0a5c32)](https://github.com/StephenSHorton/suzuri/releases/latest)

**Suzuri** means *inkstone* — the stone where ink is ground before writing.

A **real terminal host** for Windows: your window, ConPTY, VT cell grid, Charm chrome, Warp-style input bar. Not a TUI inside someone else’s emulator, and not a Warp fork.

**Site:** [stephenshorton.github.io/suzuri](https://stephenshorton.github.io/suzuri/) · **Download:** [latest release](https://github.com/StephenSHorton/suzuri/releases/latest)

---

## Why

Most “pretty terminals” are either:

- a **full host** (Windows Terminal, WezTerm) with deep VT fidelity, or  
- a **TUI app** that lives *inside* another terminal (Crush, many Charm demos).

**suzuri is a host.** It owns Win32 + ConPTY + paint. Charm (Bubble Tea + Lip Gloss) owns the chrome you notice — tabs, palette, settings — composited over a dimmed shell.

## Features

| Area | Highlights |
|------|------------|
| **Host** | Native Win32 window, ConPTY shells, scrollback, selection, copy/paste |
| **Chrome** | Tabs, command palette (`Ctrl+K`), settings (`Ctrl+,`), help, themes |
| **Input** | Warp-style bottom bar — local edit, multiline, history, echo filter |
| **Look** | Inkstone / Charmtone / High contrast · bundled Gohu mono · box-drawing |
| **Polish** | Window placement, matrix/ripple intros, center 硯 watermark |
| **Agents** | Spawn-on-demand MCP (`suzuri mcp`) for diagnostics |
| **Updates** | Checks GitHub Releases on startup; palette **Check for updates** |

## Download

1. Grab **`suzuri-*-windows-amd64.exe`** (or the `.zip`) from [Releases](https://github.com/StephenSHorton/suzuri/releases/latest).  
2. Run it — no installer. Config lives in `%LOCALAPPDATA%\suzuri\`.  
3. Optional: pin the exe or put it on your `PATH`.

## Build from source

```powershell
git clone https://github.com/StephenSHorton/suzuri.git
cd suzuri
go test ./...
go build -ldflags "-s -w -X main.version=dev" -o suzuri.exe ./cmd/suzuri
.\suzuri.exe
```

Windows only (ConPTY). Go 1.26+ recommended (see `go.mod`).

## Keys

| Shortcut | Action |
|----------|--------|
| `Ctrl+K` / `Ctrl+P` | Command palette |
| `Ctrl+,` | Settings |
| `Ctrl+/` | Help |
| `Ctrl+Shift+T` | New tab |
| `Ctrl+W` | Close tab |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Next / previous tab |
| `Ctrl+1`…`9` | Jump to tab |
| `Ctrl+Shift+C` / `Ctrl+Shift+V` | Copy / paste |

## Config

`%LOCALAPPDATA%\suzuri\config.json` — font, theme, ANSI map, **intro** (`matrix` \| `ripple` \| `none`), profiles, window placement.

Logs: `%LOCALAPPDATA%\suzuri\suzuri.log` · `SUZURI_LOG_LEVEL=info` to quiet debug.

## MCP (agent diagnostics)

Spawn-on-demand stdio MCP — attach to a running GUI over loopback. See [`docs/mcp.md`](docs/mcp.md).

```toml
# ~/.grok/config.toml
[mcp_servers.suzuri]
command = 'C:\path\to\suzuri.exe'
args = ["mcp"]
enabled = true
```

## Architecture

```
Win32 window  →  key/mouse
      │
      ├─ Charm chrome (tabs / palette / settings)  → mini VT → GDI
      │
      └─ active tab
           write queue  →  ConPTY  →  shell
                ↑                         ↓
           VT parse (UI thread)  ←  byte queue
                ↓
           cell grid + scrollback → GDI
```

Design notes: [`docs/crush-inspired-plan.md`](docs/crush-inspired-plan.md).

## Releases & CI

| Workflow | Purpose |
|----------|---------|
| **CI** | `go test` + build on PR/push |
| **Release** | Tag `v*.*.*` → portable `.exe` + `.zip` + `SHA256SUMS` |
| **Pages** | Deploys `docs/site` to GitHub Pages |

```powershell
git tag v0.6.0
git push origin v0.6.0
```

## Auto-update

Release builds embed a version via `-ldflags -X main.version=…`. On startup (and via palette **Check for updates**), suzuri queries GitHub Releases, downloads the windows-amd64 asset, verifies `SHA256SUMS` when present, renames the running image, writes the new exe, and relaunches. Dev builds (`version=dev`) never auto-update.

## License

[MIT](LICENSE) © Stephen S. Horton.

Bundled **GohuFont uni14 Nerd Font Mono** is WTFPL — see [`assets/fonts`](assets/fonts).

## Name

Romanization: `suzuri` · Japanese: **硯** · English: inkstone
