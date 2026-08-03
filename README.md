# suzuri（硯）

[![CI](https://github.com/StephenSHorton/suzuri/actions/workflows/ci.yml/badge.svg)](https://github.com/StephenSHorton/suzuri/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-00e676.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/StephenSHorton/suzuri?color=0a5c32)](https://github.com/StephenSHorton/suzuri/releases/latest)

**Suzuri** means *inkstone* — the stone where ink is ground before writing.

A **real terminal host** for Windows and macOS: your window, PTY, VT cell grid, Charm chrome, Warp-style input bar. Not a TUI inside someone else’s emulator, and not a Warp fork.

**Site:** [stephenshorton.github.io/suzuri](https://stephenshorton.github.io/suzuri/) · **Download:** [latest release](https://github.com/StephenSHorton/suzuri/releases/latest)

---

## Why

Most “pretty terminals” are either:

- a **full host** (Windows Terminal, WezTerm) with deep VT fidelity, or  
- a **TUI app** that lives *inside* another terminal (Crush, many Charm demos).

**suzuri is a host.** It owns the native window + PTY + paint. Charm (Bubble Tea + Lip Gloss) owns the chrome you notice — tabs, palette, settings — composited over a dimmed shell.

## Features

| Area | Highlights |
|------|------------|
| **Host** | Native window (Win32 / macOS), ConPTY or POSIX PTY shells, scrollback, selection, copy/paste |
| **Chrome** | Tabs, command palette (`Ctrl+K`), settings (`Ctrl+,`), help, themes |
| **Panes** | Split right/down, shared sashes, per-pane Warp bars, focus with Alt+arrows |
| **Input** | Warp-style bottom bar — local edit, multiline, history, echo filter |
| **Look** | Inkstone / Charmtone / High contrast · bundled Gohu mono · app icon · box-drawing |
| **Polish** | Window placement, matrix/ripple intros (Windows), 猫咪 dim under settings, floating palette/help over live shell |
| **Agents** | Spawn-on-demand MCP (`suzuri mcp`) for diagnostics |
| **Updates** | Auto-update from GitHub Releases on startup; palette **Check for updates** |
| **Install** | Windows: user-scoped setup (`*-setup.exe`) → Start Menu + uninstall |

## Download

**Windows (recommended)**

1. Grab **`suzuri-*-windows-amd64-setup.exe`** from [Releases](https://github.com/StephenSHorton/suzuri/releases/latest).  
2. Run the installer — **no administrator rights**. It installs to `%LOCALAPPDATA%\Programs\suzuri\`, adds **Start Menu** and desktop shortcuts, and an **Apps & features** uninstall entry.  
3. Config and logs stay in `%LOCALAPPDATA%\suzuri\`. In-app updates still replace the installed exe in place.

**Windows (portable)**

1. Grab **`suzuri-*-windows-amd64.exe`** (or the `.zip`) if you prefer a single file with no shortcuts.  
2. Double-click — GUI subsystem, no spare console window.

**macOS (Apple Silicon)**

1. Grab **`suzuri-*-darwin-arm64`** (or the `.zip`) from [Releases](https://github.com/StephenSHorton/suzuri/releases/latest).  
2. `chmod +x suzuri-*-darwin-arm64 && ./suzuri-*-darwin-arm64`  
3. Config lives in `~/Library/Application Support/suzuri/`.  
4. First launch may need **System Settings → Privacy & Security** if Gatekeeper blocks an unsigned binary.

## Build from source

**Windows**

```powershell
git clone https://github.com/StephenSHorton/suzuri.git
cd suzuri
go test ./...
# -H windowsgui: no spare console on double-click / Start Menu (GUI host only).
# Logs still go to %LOCALAPPDATA%\suzuri\suzuri.log. `suzuri version` reattaches
# to the parent console when run from a shell.
go build -ldflags "-H windowsgui -s -w -X main.version=dev" -o suzuri.exe ./cmd/suzuri
.\suzuri.exe
```

**macOS**

```bash
git clone https://github.com/StephenSHorton/suzuri.git
cd suzuri
go test ./...
# CGO required (ebiten window host)
CGO_ENABLED=1 go build -ldflags "-s -w -X main.version=dev" -o suzuri ./cmd/suzuri
./suzuri
```

Go 1.26+ recommended (see `go.mod`). Supported hosts: **Windows** (ConPTY) and **macOS** (POSIX PTY).

## Keys

| Shortcut | Action |
|----------|--------|
| `Ctrl+K` / `Ctrl+P` | Command palette |
| `Ctrl+,` | Settings |
| `Ctrl+/` | Help |
| `Ctrl+Shift+T` | New tab |
| `Ctrl+Shift+N` | New window |
| `Ctrl+W` | Close tab |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Next / previous tab |
| `Ctrl+1`…`9` | Jump to tab |
| `Ctrl+Shift+C` / `Ctrl+Shift+V` | Copy / paste |

## Config

| OS | Config | Logs |
|----|--------|------|
| Windows | `%LOCALAPPDATA%\suzuri\config.json` | `%LOCALAPPDATA%\suzuri\suzuri.log` |
| macOS | `~/Library/Application Support/suzuri/config.json` | same dir `suzuri.log` |

Fields: font, theme, ANSI map, **intro** (`matrix` \| `ripple` \| `none`), profiles, window placement.

`SUZURI_LOG_LEVEL=info` to quiet debug.

## MCP (agent diagnostics)

Spawn-on-demand stdio MCP — attach to a running GUI over loopback. See [`docs/mcp.md`](docs/mcp.md).

```toml
# ~/.grok/config.toml
[mcp_servers.suzuri]
command = '/path/to/suzuri'   # or C:\path\to\suzuri.exe on Windows
args = ["mcp"]
enabled = true
```

## Architecture

```
Native window  →  key/mouse
      │
      ├─ Charm chrome (tabs / palette / settings)  → mini VT → paint
      │
      └─ active tab
           write queue  →  PTY (ConPTY / POSIX)  →  shell
                ↑                                    ↓
           VT parse (UI thread)  ←  byte queue
                ↓
           cell grid + scrollback → paint (GDI / software+Metal)
```

Design notes: [`docs/crush-inspired-plan.md`](docs/crush-inspired-plan.md).

## Releases & CI

| Workflow | Purpose |
|----------|---------|
| **CI** | `go test` + build on Windows and macOS |
| **Release** | Tag `v*.*.*` → windows-amd64 + darwin-arm64 assets + `SHA256SUMS` |
| **Pages** | Deploys `docs/site` to GitHub Pages |

```bash
git tag v0.6.0
git push origin v0.6.0
```

## Auto-update

Release builds embed a version via `-ldflags -X main.version=…`. On startup (and via palette **Check for updates**), suzuri queries GitHub Releases, downloads the **portable** asset for the running OS/arch (not the setup installer), verifies `SHA256SUMS` when present, replaces the running binary, and relaunches. That works for both the portable `.exe` and the installed copy under `%LOCALAPPDATA%\Programs\suzuri\`. Dev builds (`version=dev`) never auto-update.

## Windows packaging

| Artifact | Purpose |
|----------|---------|
| `*-setup.exe` | NSIS user installer (Start Menu, desktop, uninstall) |
| `*-windows-amd64.exe` | Portable binary (also the in-app update payload) |
| `*.zip` | Same portable binary, zipped |

Installer sources: [`packaging/windows/suzuri.nsi`](packaging/windows/suzuri.nsi).

## License

[MIT](LICENSE) © Stephen S. Horton.

Bundled **GohuFont uni14 Nerd Font Mono** is WTFPL — see [`assets/fonts`](assets/fonts).

## Name

Romanization: `suzuri` · Japanese: **硯** · English: inkstone
