# Architecture

**suzuri** is one product: a native terminal host. There is no separate
“suzuri-chrome” app and no Charm/ebiten product UI.

## Processes

```
  user runs:  suzuri
                 │
       ┌─────────┴──────────┐
       ▼                    ▼
  Go host binary      Rust UI process
  (cmd/suzuri)        (sidecar binary;
  · CLI / MCP           on-disk name
  · config / update     `suzuri-chrome`)
  · spawn UI          · window / glass
  · bridge proxy      · PTY paint
                      · settings / workspace
       │
       └── optional: suzuri-transfer (Rust P2P engine)
```

| Piece | Language | Role |
|-------|----------|------|
| **`suzuri`** | Go | Product entrypoint. Subcommands: `mcp`, `version`, `transfer`. Default (no args) launches the UI. |
| **UI process** | Rust (`chrome/` crate) | The entire interactive GUI (wgpu glass, rain, tabs, settings, workspace chat, notes UI, …). |
| **`suzuri-transfer`** | Rust (`libs/transfer`) | Peer-to-peer file transfer engine. |

Users only need the name **suzuri**. The UI sidecar’s filename is an
implementation detail for “binary next to host” resolution
(`internal/chromehost`).

## What we do *not* ship

| Removed / abandoned | Why |
|---------------------|-----|
| **Charm + Bubble Tea chrome** (`internal/chrome` UI) | Replaced by native GPU UI |
| **ebiten window host** (`internal/ui`) | Replaced by native GPU UI |
| **`surface/`** (Tauri / React / WebGPU spike) | Visual experiment only; not product |

Notes **data** (disk bank + MCP offline ops) lives in `internal/notes` and is
shared by the host bridge and agents — not a second UI.

## Env

| Variable | Meaning |
|----------|---------|
| `SUZURI_CHROME` | Explicit path to the UI binary (dev / override) |
| `SUZURI_CONFIG_DIR` | Shared config root (host + UI) |
| `SUZURI_VERSION` | Host release version (updater; `dev` never offers) |
| `SUZURI_UI` | **Ignored for product path.** Classic/ebiten is gone. |

## Build (dev)

```bash
# UI
cd chrome && cargo build --release && cd ..

# Host
go build -o suzuri ./cmd/suzuri

# Run (host finds chrome/target/release/suzuri-chrome)
./suzuri
```

Release packaging places `suzuri`, `suzuri-chrome`, and `suzuri-transfer`
side by side (see `.github/workflows/release.yml`).
