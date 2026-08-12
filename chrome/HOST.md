# Host merge — embedding `suzuri-chrome` from the Go host

**Product paint path:** this crate (`chrome/`) — native Rust + wgpu.  
**Not product:** `surface/` (Tauri / React / Kussetsu-web spike) — visual R&D only.

The Go binary (`cmd/suzuri` → `internal/ui`, `internal/chrome`) owns PTY policy,
config, MCP, transfer CLI, and platform installers. Native chrome owns the
framebuffer and glass UX. This document is the merge plan so the Go host can
**spawn or embed** chrome without regressing the standalone `suzuri-chrome`
binary.

## Architecture rule

> Anything that isn’t shell output never snaps to a character cell.

| Layer | Owner | Tech |
|-------|--------|------|
| Smooth chrome (title, tabs, glass, rain, modals) | **this crate** | winit + wgpu |
| Cell pane (shell / TUI only) | shared contract | mono grid in the glass well |
| Host policy (config dir, updates, MCP, transfer engine) | Go | `internal/*` |

## Library surface (`suzuri_chrome`)

```rust
use suzuri_chrome::{
    layout, cells, ansi, pty, session, panes, input, settings, commands, shell,
    ChromeSession, Metrics, CellGrid, PtySession, VERSION, HOST_API,
};
```

| Module | Use from host |
|--------|----------------|
| `layout` | Same geometry as the GPU window (title / hole / warp) |
| `cells` / `ansi` | Grid buffer + VT into grid |
| `pty` | Local shell PTY (or keep Go PTY and feed bytes) |
| `session` / `panes` | Tabs + split tree (PTY map stays host-owned) |
| `settings` / `commands` | Prefs + palette action IDs |
| `input` | Hit-test helpers if the host drives input |
| `shell` | Mock shell fallback |

**Binary-only (not in the lib):** `app`, `renderer`, `text`, rain shaders, macOS
window chrome. Those stay behind the `suzuri-chrome` executable so a host can
either spawn that process or later link a present loop.

**Parallel tracks** add `notes`, `workspace_ui`, `transfer_ui`, `chrome_ui` —
export them from `lib.rs` when those modules land so hosts see one crate root.

Optional **C ABI stubs** (`--features ffi`, module `ffi`) pin symbols for cgo.
Default `cargo build --release` does **not** enable `ffi`.

## How the Go host runs chrome today (standalone)

```bash
cd chrome && cargo build --release
./target/release/suzuri-chrome
```

Do **not** use macOS `open` on the bare binary (spawns Terminal.app). Launch the
path directly from the agent shell or from Go via `exec.Command`.

Config / notes / prefs live under the product dir (macOS:
`~/Library/Application Support/suzuri/`). Chrome modules should keep reading
and writing there so host and chrome share state.

## Phases to replace `surface/`

### Phase 0 — Reference only (current)

- `surface/` stays a look/feel spike (Vite / Tauri / xterm hole).
- Product evaluation runs **native** `suzuri-chrome` side by side with Go
  `suzuri` (ebiten / ConPTY host).
- No hard dependency from `cmd/suzuri` on `surface/`.

### Phase 1 — Spawn process (first merge) — **implemented**

Go host treats chrome as an optional UI child. Package:
[`internal/chromehost`](../internal/chromehost/) (`ResolveBinary`, `Start`,
`RunCLI`).

1. Resolve binary: `SUZURI_CHROME` env → next to `suzuri` → dev
   `chrome/target/release/suzuri-chrome` (walk from cwd / exe) → `PATH`.
2. `exec.Command` / CreateProcess (never macOS `open`) with inherited stdio.
3. Pass shared context via env:
   - `SUZURI_CONFIG_DIR` = product `config.Dir()`
   - (later: cwd / initial shell / feature toggles)
4. Lifecycle: `suzuri chrome` starts chrome and **waits** for exit (chrome-only
   mode). Default `suzuri` still opens classic ebiten UI (`internal/ui` +
   `internal/chrome`).
5. Keep Go ebiten face as default until chrome passes parity gates (`PARITY.md`).

```bash
# Classic UI (unchanged)
suzuri

# Native chrome UI (Phase 1)
cd chrome && cargo build --release
suzuri chrome
# or: SUZURI_CHROME=/path/to/suzuri-chrome suzuri chrome
```

**Success criteria:** users can run native chrome from a subcommand without
breaking existing ebiten UI.

### Phase 2 — Shared state files / IPC light — **partial**

- Prefer **files + env** already used by product (notes bank, settings JSON)
  over a custom protocol.
- Transfer still shells out to `suzuri-transfer` / hato (same as product).

#### Control mailbox (implemented)

Light control plane so the host can ask a **running** chrome process to quit or
open a surface without full embed / sockets:

| Side | Path | Behavior |
|------|------|----------|
| Mailbox file | `{config_dir}/chrome_cmd` | One command per line |
| Config dir | Go `config.Dir()` · chrome `SUZURI_CONFIG_DIR` or product default | Same roots as notes/prefs |
| Chrome | `control_mailbox` module | Poll every **250ms** on the frame tick; read + truncate; fail soft if missing |
| Go | `chromehost.SendCommand(cmd)` | Validates verb, writes file (atomic temp+rename) |

**Commands** (exact line text):

| Command | Effect |
|---------|--------|
| `quit` | Exit chrome |
| `open_notes` | Open notes overlay |
| `open_workspace` | Open workspace overlay |
| `open_palette` | Open command palette |

```go
// From the Go host (chrome already running, or about to start):
_ = chromehost.SendCommand(chromehost.CmdOpenNotes)
// or: chromehost.SendCommand("quit")
```

```bash
# Manual poke while chrome is open:
echo open_palette > "$(…/suzuri)/chrome_cmd"
```

**Defaults stay simple:** spawn (`suzuri chrome` / Phase 1) does **not** require
the mailbox file. If the socket/file is unavailable or unreadable, chrome
ignores it and keeps running. No Unix domain socket yet (`chrome.sock` remains
optional future); the file mailbox is the cross-platform path.

**Not yet:** focus-tab / multi-window, bidirectional events, or a long-lived
Unix socket server. Shared data files (notes bank, `chrome_prefs.json`) already
round-trip via the same config dir.

**Success criteria:** notes / settings / workspace data round-trip between Go
host tools and chrome without dual stores; host can `SendCommand` to open
overlays or quit without embedding the GPU loop.

### Phase 3 — Library embed (Rust) or cgo staticlib

Two embed options (pick one per platform later):

**A. Process remains the product** (recommended longer):  
chrome stays a separate binary; Go only orchestrates.

**B. In-process:**

```bash
# Rust host or helper
cargo build --release -p suzuri-chrome

# C ABI stubs for experiment / metrics only
cargo rustc --release --features ffi --crate-type staticlib
# → target/release/libsuzuri_chrome.a  (link from cgo)
```

```c
// sketch — see src/ffi.rs for real symbols
uint32_t suzuri_chrome_abi_version(void);
const char *suzuri_chrome_version(void);
int suzuri_chrome_layout_metrics(float w, float h,
    float *title_h, float *tab_h, float *edge, float *input_strip_h);
```

Full GPU present-in-process needs a host-owned event loop + window; that is
**not** in the stub ABI. Until then, spawn (Phase 1) is the supported path.

**Success criteria:** either stable spawn integration **or** a documented
staticlib link with non-stub session APIs.

### Phase 4 — Retire surface / thin Go chrome

1. Default macOS (and later Windows) install ships `suzuri-chrome` as the UI.
2. Go host becomes engine + CLI: PTY host optional, MCP, transfer, updates,
   config.
3. Delete or archive `surface/` (or keep as screenshot golden reference only).
4. `internal/chrome` glass/modals either call into chrome IPC or shrink to
   headless helpers.

**Success criteria:** no Tauri/React path on the critical product install;
native chrome is the only framebuffer.

## What not to do

- Do not reintroduce CEF / html-in-canvas / Canvas UI capture trees for product
  chrome.
- Do not quantize product chrome to the cell grid.
- Do not break the standalone binary: `cargo build --release` (default
  features) must keep producing `suzuri-chrome`.
- Do not enable `ffi` by default (keeps default builds simple and free of
  unused `cdylib`/`staticlib` crates).

## Build matrix

| Command | Intent |
|---------|--------|
| `cargo build --release` | Product binary + `rlib` (default) |
| `cargo build --release --features ffi` | Same + C ABI stubs in the rlib |
| `cargo rustc --release --features ffi --crate-type staticlib` | `.a` for cgo experiments |
| `cargo run --release` | Standalone window |

## Ownership

| Path | Role |
|------|------|
| `chrome/src/lib.rs` | Host-facing Rust modules + re-exports |
| `chrome/src/control_mailbox.rs` | Phase 2 `chrome_cmd` poller |
| `chrome/src/ffi.rs` | Optional C ABI stubs (`feature = "ffi"`) |
| `chrome/HOST.md` | This document |
| `chrome/src/main.rs` + `app` / `renderer` | Standalone binary only |
| `internal/chromehost` | Go spawn + `SendCommand` mailbox |
| `cmd/suzuri`, `internal/*` | Go host (spawn / policy) |
| `surface/` | Deprecated spike — do not extend for product |

## Related

- [`README.md`](./README.md) — run, shortcuts, 1.0 vertical slice  
- [`PARITY.md`](./PARITY.md) — product parity tracks  
- [`../surface/README.md`](../surface/README.md) — spike status / not product  
