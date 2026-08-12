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

Optional **C ABI** (`--features ffi`, module `ffi`): version/layout probes plus
real session create/destroy/size/tab helpers (see Phase 3). Default
`cargo build --release` does **not** enable `ffi`.

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
   mode). Also starts MCP bridge proxy (Phase 2).
5. **Default UI:** bare `suzuri` launches chrome when `PreferChromeUI()` is true:
   - `SUZURI_UI=chrome` / `native` → always chrome
   - `SUZURI_UI=classic` / `ebiten` / `legacy` → classic ebiten
   - unset → chrome if `ResolveBinary()` succeeds (sibling install or cargo release), else classic

```bash
# Native chrome when binary is resolvable (install layout or cargo release)
cd chrome && cargo build --release
suzuri
# or force:
SUZURI_UI=chrome suzuri
suzuri chrome

# Classic ebiten
SUZURI_UI=classic suzuri
```

**Success criteria:** users can run native chrome as default or subcommand without
breaking classic ebiten via `SUZURI_UI=classic`.

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

**Commands** (exact line text, snake_case):

| Command | Effect |
|---------|--------|
| `quit` | Exit chrome |
| `open_notes` | Open notes overlay |
| `open_workspace` | Open workspace overlay |
| `open_palette` | Open command palette |
| `open_settings` | Open settings overlay |
| `open_transfer_send` | Open transfer send overlay |
| `open_transfer_receive` | Open transfer receive overlay |
| `open_help` | Open help overlay |
| `new_tab` | Create a new tab |
| `new_window` | Spawn second chrome process |
| `toggle_caffeine` | Toggle caffeine / keep-awake |
| `refresh_workspace` | Soft-reload workspace panel if open |

```go
// From the Go host (chrome already running, or about to start):
_ = chromehost.SendCommand(chromehost.CmdOpenNotes)
// or: chromehost.SendCommand("quit")
```

```bash
# Manual poke while chrome is open:
echo open_palette > "$(…/suzuri)/chrome_cmd"
```

#### MCP bridge proxy (implemented)

While `suzuri chrome` (or default chrome UI) runs, the **Go parent** starts the
same loopback MCP bridge as classic ebiten:

| Piece | Role |
|-------|------|
| `bridge.json` | Written under `config.Dir()` (product path) |
| Notes / workspace | Disk ops via `chrome.ApplyNotesDiskOp` / `workspace.Apply` |
| Live refresh | `SendCommand(refresh_workspace)` / open notes after mutations |
| Status / snapshot | From `{config}/chrome_status.json` (chrome publishes ~750ms) |
| Submit | `{config}/chrome_submit` one line → chrome warp/PTY path |

#### Rich `chrome_status.json` (diag / snapshot)

Chrome writes a **bridge.Snapshot-compatible** document:

| Field | Content |
|-------|---------|
| `pid`, `cols`, `rows`, `active_tab`, `version` | Process + grid |
| `tabs[]` | Per-tab: `title`, `alive`, `input` (warp draft), `alt_screen` |
| `tabs[].live_lines` | Live grid rows (trailing spaces trimmed) |
| `tabs[].viewport` | What the user sees (`view_offset` respected) |
| `tabs[].history_tail` | Tagged lines (`normal` / `rule` / `cmd`) from host blocks + scrollback |
| `tabs[].blocks[]` | Recent warp-submitted commands (`{command}`) — newest last |
| `tabs[].echo` | `{armed, cmd, phase}` local-echo suppressor state |
| `tabs[].pty_tail` | Debug-quoted recent raw PTY bytes (~2 KiB) |
| `notes[]` | Agent tags (`ui=chrome`, version, armed echo, recent blocks) |

On warp submit chrome follows product `applyBarSubmitToTab` / `sendBarPayload`:

1. **`commit_live`** — non-blank live rows → scrollback, blank host live grid  
   (skipped on alt-screen)
2. **`push_block`** — spacer + rule + `❯ cmd` (optional cwd)
3. **`pin_here`** if command is `clear` / `cls` / `Clear-Host`
4. **Arm echo filter** on the submitted line
5. **PTY write** with newlines → CR and trailing CR (not LF)

Echo-filtered PTY bytes then feed the VT decoder so local shell echo does not
double-print the command.

Go `chromehost.SnapshotFromChromeStatus` maps this into `bridge.Snapshot` for
`/v1/status`, `/v1/snapshot`, `/v1/diag`. Legacy thin status (`tabs` as a count)
is still accepted.

`suzuri mcp` attaches unchanged: notes/workspace work offline; status/diag/submit
work when chrome is running with the bridge proxy.

**Defaults stay simple:** spawn does **not** require the mailbox file. Chrome
fails soft if files are missing.

**Success criteria:** notes / settings / workspace data round-trip between Go
host tools and chrome without dual stores; host can `SendCommand` to open
overlays or quit without embedding the GPU loop; MCP status works with chrome UI.

### Phase 3 — Library embed (Rust) or cgo staticlib — **partial**

Two embed options (pick one per platform later):

**A. Process remains the product** (recommended longer):  
chrome stays a separate binary; Go only orchestrates. **GPU UI stays here.**

**B. In-process (session + metrics only today):**

```bash
# Rust host or helper
cargo build --release -p suzuri-chrome

# C ABI for cgo / static link experiments
cargo rustc --release --features ffi --crate-type staticlib
# → target/release/libsuzuri_chrome.a  (link from cgo)
```

Build with `--features ffi` to export symbols from `src/ffi.rs`. Default
`cargo build --release` does **not** enable `ffi`.

#### Real C symbols (Phase 3 partial)

Probe / layout (no session required):

| Symbol | Notes |
|--------|--------|
| `suzuri_chrome_abi_version` | `uint32_t`; bump on ABI breaks |
| `suzuri_chrome_version` | static NUL-terminated package version |
| `suzuri_chrome_is_ready` | `1` when built with `ffi` |
| `suzuri_chrome_layout_metrics` | default title/tab/edge/input strip (CSS-px) |

Session handles (thread-safe process-wide registry, `std::sync::Mutex`):

| Symbol | Notes |
|--------|--------|
| `suzuri_chrome_session_create(cols, rows)` | → non-zero `usize` handle; `0` dims clamp to `1` |
| `suzuri_chrome_session_destroy(handle)` | remove; unknown/0 is no-op |
| `suzuri_chrome_session_size(handle, *cols, *rows)` | `0` ok, `-1` bad handle / null |
| `suzuri_chrome_session_cols` / `_rows` | size getters; `0` if invalid |
| `suzuri_chrome_session_tab_count(handle)` | ≥0 tabs, or `-1` bad handle |
| `suzuri_chrome_session_new_tab(handle)` | `0` ok, `-1` bad handle |
| `suzuri_chrome_session_write_banner(handle)` | mock boot banner on active pane |
| `suzuri_chrome_present(handle)` | always `-1` (GPU not in-process) |
| `suzuri_chrome_present_available` | always `0` |

```c
// see src/ffi.rs for full signatures
uint32_t suzuri_chrome_abi_version(void);
const char *suzuri_chrome_version(void);
int suzuri_chrome_is_ready(void);
int suzuri_chrome_layout_metrics(float w, float h,
    float *title_h, float *tab_h, float *edge, float *input_strip_h);

size_t suzuri_chrome_session_create(unsigned cols, unsigned rows);
void   suzuri_chrome_session_destroy(size_t handle);
int    suzuri_chrome_session_size(size_t handle, unsigned *cols, unsigned *rows);
unsigned suzuri_chrome_session_cols(size_t handle);
unsigned suzuri_chrome_session_rows(size_t handle);
int suzuri_chrome_present(size_t handle);          /* always -1 */
int suzuri_chrome_present_available(void);         /* always 0 */
int    suzuri_chrome_session_tab_count(size_t handle);
int    suzuri_chrome_session_new_tab(size_t handle);
int    suzuri_chrome_session_write_banner(size_t handle);
```

**Not in this ABI:** GPU present loop, winit window, wgpu surface, rain, or
host event-loop embed. Product framebuffer remains **process-spawned**
(`suzuri-chrome` / Phase 1–2 mailbox). Session handles are for hosts that want
in-process tab/grid state (or cgo experiments) without linking the renderer.

**Success criteria:** either stable spawn integration **or** a documented
staticlib link with non-stub session APIs — **session create/destroy/size/tabs
landed**; GPU embed still future.

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
| `cargo build --release --features ffi` | Same + C ABI (session + metrics) in the rlib |
| `cargo rustc --release --features ffi --crate-type staticlib` | `.a` for cgo experiments |
| `cargo test --features ffi` | Includes `ffi` session registry tests |
| `cargo run --release` | Standalone window |

## Ownership

| Path | Role |
|------|------|
| `chrome/src/lib.rs` | Host-facing Rust modules + re-exports |
| `chrome/src/control_mailbox.rs` | Phase 2 `chrome_cmd` poller |
| `chrome/src/ffi.rs` | Optional C ABI: metrics + session handles (`feature = "ffi"`) |
| `chrome/HOST.md` | This document |
| `chrome/src/main.rs` + `app` / `renderer` | Standalone binary only |
| `internal/chromehost` | Go spawn + `SendCommand` mailbox |
| `cmd/suzuri`, `internal/*` | Go host (spawn / policy) |
| `surface/` | Deprecated spike — do not extend for product |

## Related

- [`README.md`](./README.md) — run, shortcuts, 1.0 vertical slice  
- [`PARITY.md`](./PARITY.md) — product parity tracks  
- [`../surface/README.md`](../surface/README.md) — spike status / not product  
