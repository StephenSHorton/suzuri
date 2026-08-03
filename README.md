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
| **Panes** | Split right/down, shared sashes, per-pane Warp bars, focus with Alt/⌥+arrows |
| **Input** | Warp-style bottom bar — local edit, multiline, history, echo filter |
| **Look** | Inkstone / Charmtone / High contrast · bundled Gohu mono · app icon · box-drawing |
| **Polish** | Window placement, matrix/ripple intros (Windows), 猫咪 dim under settings, floating palette/help over live shell |
| **Agents** | Spawn-on-demand MCP (`suzuri mcp`) for diagnostics |
| **Updates** | Auto-update from GitHub Releases on startup; palette **Check for updates** |
| **Install** | Windows: user-scoped setup (`*-setup.exe`) · macOS: `.dmg` / `.app.zip` → Applications |

## Download

**Windows (recommended)**

1. Grab **`suzuri-*-windows-amd64-setup.exe`** from [Releases](https://github.com/StephenSHorton/suzuri/releases/latest).  
2. Run the installer — **no administrator rights**. It installs to `%LOCALAPPDATA%\Programs\suzuri\`, adds **Start Menu** and desktop shortcuts, and an **Apps & features** uninstall entry.  
3. Config and logs stay in `%LOCALAPPDATA%\suzuri\`. In-app updates still replace the installed exe in place.  
4. Builds are **not Authenticode-signed**. SmartScreen may show “Windows protected your PC” — **More info → Run anyway**.

**Windows (portable)**

1. Grab **`suzuri-*-windows-amd64.exe`** (or the `.zip`) if you prefer a single file with no shortcuts.  
2. Double-click — GUI subsystem, no spare console window. Same SmartScreen note as above.

**macOS (Apple Silicon, recommended)**

1. Grab **`suzuri-*-darwin-arm64.dmg`** from [Releases](https://github.com/StephenSHorton/suzuri/releases/latest).  
2. Open the DMG and drag **suzuri** into **Applications** (or `~/Applications`).  
3. Builds are **not Apple-notarized** (no paid Developer ID). First launch may show **“suzuri” Not Opened** — Apple could not verify it is free of malware. Click **Done** (not Move to Trash), then either **System Settings → Privacy & Security → Open Anyway**, or right-click the app → **Open** → **Open**. After the first allow, it opens normally.  
4. Config and logs live in `~/Library/Application Support/suzuri/`. In-app updates replace the binary inside the `.app` (portable payload, not the DMG).

**macOS (portable)**

1. Grab **`suzuri-*-darwin-arm64`** (or the plain `.zip`, not `.app.zip`) if you prefer a single binary.  
2. `chmod +x suzuri-*-darwin-arm64 && ./suzuri-*-darwin-arm64`  
3. Same Gatekeeper note as the `.app` above if the binary is quarantined from a browser download.

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
| `⌘+/⌘-` · `Ctrl++` / `Ctrl+-` | Zoom UI (font size) |
| `⌘0` · `Ctrl+0` | Reset zoom |
| `Ctrl+Shift+T` | New tab |
| `Ctrl+Shift+N` | New window |
| `Ctrl+W` | Close tab |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Next / previous tab |
| `Ctrl+1`…`9` | Jump to tab |
| `Ctrl+Shift+C` / `Ctrl+Shift+V` | Copy / paste |
| `⌘-click` · `Ctrl-click` URL | Open link in browser |

## Config

| OS | Config | Logs |
|----|--------|------|
| Windows | `%LOCALAPPDATA%\suzuri\config.json` | `%LOCALAPPDATA%\suzuri\suzuri.log` |
| macOS | `~/Library/Application Support/suzuri/config.json` | same dir `suzuri.log` |

Fields: font, theme, ANSI map, **intro** (`matrix` \| `ripple` \| `none`), profiles, window placement.

`SUZURI_LOG_LEVEL=info` to quiet debug.

## MCP (agent diagnostics + notes)

Spawn-on-demand stdio MCP — attach to a running GUI over loopback. Tools include terminal diag/submit/logs and **notes bank** CRUD (`suzuri_notes_list` / `_get` / `_create` / `_update` / `_delete`). See [`docs/mcp.md`](docs/mcp.md).

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

Release builds embed a version via `-ldflags -X main.version=…`. On startup (and via palette **Check for updates**), suzuri queries GitHub Releases and toasts progress. If a newer version exists, a **confirmation modal** asks before install; **Update** downloads the portable asset (not the setup installer), verifies `SHA256SUMS` when present, replaces the running binary, and relaunches. **Later** dismisses without installing. Works for portable `.exe` and the install under `%LOCALAPPDATA%\Programs\suzuri\`. Dev builds (`version=dev`) never offer updates.

## Packaging

| Artifact | Purpose |
|----------|---------|
| `*-windows-amd64-setup.exe` | NSIS user installer (Start Menu, desktop, uninstall) |
| `*-windows-amd64.exe` | Portable Windows binary (also the in-app update payload) |
| `*-darwin-arm64.dmg` | macOS disk image — drag `suzuri.app` to Applications |
| `*-darwin-arm64.app.zip` | Same `.app` bundle, zipped |
| `*-darwin-arm64` / `.zip` | Portable macOS binary (in-app update payload) |
| `SHA256SUMS` | Checksums for all release assets |

Installer sources: [`packaging/windows/suzuri.nsi`](packaging/windows/suzuri.nsi), [`packaging/macos/`](packaging/macos/).

## License

[MIT](LICENSE) © Stephen S. Horton.

Bundled **GohuFont uni14 Nerd Font Mono** is WTFPL — see [`assets/fonts`](assets/fonts).

## Name

Romanization: `suzuri` · Japanese: **硯** · English: inkstone
