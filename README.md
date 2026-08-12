# suzuri（硯）

[![CI](https://github.com/StephenSHorton/suzuri/actions/workflows/ci.yml/badge.svg)](https://github.com/StephenSHorton/suzuri/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-00e676.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/StephenSHorton/suzuri?color=0a5c32)](https://github.com/StephenSHorton/suzuri/releases/latest)

**Suzuri** means *inkstone* — the stone where ink is ground before writing.

A **native terminal host** for macOS and Windows: your window, PTY, GPU chrome
(glass, rain, tabs, settings, workspace). Not a TUI inside someone else’s
emulator, and not a Charm/Bubble Tea app.

**Site:** [stephenshorton.github.io/suzuri](https://stephenshorton.github.io/suzuri/) · **Download:** [latest release](https://github.com/StephenSHorton/suzuri/releases/latest)

---

## Why

Most “pretty terminals” are either a full host (Windows Terminal, WezTerm) or a
TUI that lives *inside* another terminal.

**suzuri is a host.** It owns the native window + PTY + paint. The interactive
UI is a **native GPU shell** (Rust / wgpu) — optical glass, glyph rain, product
chrome — not HTML and not Charm.

## Features

| Area | Highlights |
|------|------------|
| **Host** | Native window, ConPTY / POSIX PTY, scrollback, selection, copy/paste |
| **Chrome** | Tabs, splits, command palette (`⌘K` / `⌘P`), settings (`⌘,`), help (`⌘/`) |
| **Look** | Glass panes · glyph rain · primary + derived accent colors · Gohu mono |
| **Input** | Warp-style compose · echo filter · command blocks |
| **Agents** | Spawn-on-demand MCP (`suzuri mcp`) for diagnostics / control |
| **Workspace** | Shared local channels, presence, tinted chat bubbles |
| **Transfer** | Peer-to-peer send/receive (palette + CLI) |
| **Updates** | Startup check + palette **Check for updates** (GitHub Releases; confirm before install; disabled in Microsoft Store / MSIX builds) |

## Download

**macOS (Apple Silicon)**

1. Grab **`suzuri-*-darwin-arm64.dmg`** from [Releases](https://github.com/StephenSHorton/suzuri/releases/latest).
2. Drag **suzuri** into **Applications**.
3. Config / logs: `~/Library/Application Support/suzuri/`.

**Windows**

1. Grab **`suzuri-*-windows-amd64-setup.exe`** from [Releases](https://github.com/StephenSHorton/suzuri/releases/latest).
2. User install under `%LOCALAPPDATA%\Programs\suzuri\` (no admin).
3. Config / logs: `%LOCALAPPDATA%\suzuri\`.

Portable `.zip` builds ship **host + UI + transfer** side by side. In-app
updates replace the same layout.

## Architecture

One product, small process split:

| Binary | Role |
|--------|------|
| **`suzuri`** | Go host — CLI, MCP, config, update, launches UI |
| **UI process** | Rust GPU shell (packaged next to host; internal name `suzuri-chrome`) |
| **`suzuri-transfer`** | Optional P2P engine |

You only run **`suzuri`**. Details: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Build from source

Requires **Go** (see `go.mod`), **Rust** (stable), and a C toolchain for CGO on
the host where needed for other modules — the **product UI is pure Rust**.

```bash
git clone https://github.com/StephenSHorton/suzuri.git
cd suzuri

# UI (required for the window)
cd chrome && cargo test --bins && cargo build --release && cd ..

# Host
go test ./...
go build -ldflags "-s -w -X main.version=dev" -o suzuri ./cmd/suzuri

# Run — host finds chrome/target/release/suzuri-chrome
./suzuri
```

**Windows GUI subsystem** (no spare console on double-click):

```powershell
go build -ldflags "-H windowsgui -s -w -X main.version=dev" -o suzuri.exe ./cmd/suzuri
```

**Optional transfer engine:**

```bash
./tools/build-transfer.sh
```

### CLI

| Command | Purpose |
|---------|---------|
| `suzuri` | Launch the app |
| `suzuri version` | Print version |
| `suzuri mcp` | Stdio MCP (spawn-on-demand) |
| `suzuri transfer …` | P2P transfer CLI |
| `suzuri chrome …` | Alias that forwards args to the UI process |

## Settings

Open **Settings** (`⌘,`). **Primary color** is the brand color; **Accent** is
derived from it (or set manually). **Reset defaults** restores factory prefs.

## Auto-update

Release builds embed a version via `-ldflags -X main.version=…`. On startup
(and via palette **Check for updates**), suzuri queries GitHub Releases. If a
newer version exists, a confirmation modal offers install. The portable zip
payload updates host + UI + transfer together when present. **Microsoft Store /
MSIX** installs skip GitHub self-update (the Store owns updates).

## Packaging / CI

| Workflow | Purpose |
|----------|---------|
| **CI** | `go test` + build |
| **Release** | Tag `v*.*.*` → macOS / Windows assets + Store MSIX + `SHA256SUMS` (includes UI binary) |
| **Pages** | Site under `docs/site` |

```bash
git tag v0.9.112
git push origin v0.9.112
```

## License

[MIT](LICENSE) © Stephen S. Horton.

Bundled **GohuFont uni14 Nerd Font Mono** is WTFPL — see [`assets/fonts`](assets/fonts).

## Name

Romanization: `suzuri` · Japanese: **硯** · English: inkstone
