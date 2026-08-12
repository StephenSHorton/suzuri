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

### Phase 1 — Spawn process (first merge)

Go host treats chrome as an optional UI child:

1. Resolve binary: next to `suzuri`, or `SUZURI_CHROME` env, or dev
   `chrome/target/release/suzuri-chrome`.
2. `exec.Command(chromePath, args...)` with inherited or logged stdio.
3. Pass shared context via env / flags (not a long-lived wire yet), e.g.:
   - `SUZURI_CONFIG_DIR`
   - cwd / initial shell
   - feature toggles (rain, lens) if needed
4. Lifecycle: host owns process; exit chrome when host quits (or detach for
   “chrome-only” mode).
5. Keep Go `internal/chrome` + `internal/ui` as the **default** face until
   chrome passes parity gates (`PARITY.md`).

**Success criteria:** users can run native chrome from a flag or subcommand
without breaking existing ebiten UI.

### Phase 2 — Shared state files / IPC light

- Prefer **files + env** already used by product (notes bank, settings JSON)
  over a custom protocol.
- Optional: small localhost or Unix-socket control plane for “open notes”,
  “focus tab”, “quit” if spawn alone is insufficient.
- Transfer still shells out to `suzuri-transfer` / hato (same as product).

**Success criteria:** notes / settings / workspace data round-trip between Go
host tools and chrome without dual stores.

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
| `chrome/src/ffi.rs` | Optional C ABI stubs (`feature = "ffi"`) |
| `chrome/HOST.md` | This document |
| `chrome/src/main.rs` + `app` / `renderer` | Standalone binary only |
| `cmd/suzuri`, `internal/*` | Go host (spawn / policy) |
| `surface/` | Deprecated spike — do not extend for product |

## Related

- [`README.md`](./README.md) — run, shortcuts, 1.0 vertical slice  
- [`PARITY.md`](./PARITY.md) — product parity tracks  
- [`../surface/README.md`](../surface/README.md) — spike status / not product  
