# Host merge — Go host + native UI

**Product UI:** this crate (`chrome/`) — Rust + wgpu.  
**Product host:** `cmd/suzuri` → `internal/chromehost` (spawn UI, MCP bridge, update).

There is **no** Charm/ebiten product path and **no** `surface/` web path.

## Architecture rule

> Anything that isn’t shell output never snaps to a character cell.

| Layer | Owner | Tech |
|-------|--------|------|
| Smooth chrome (title, tabs, glass, rain, modals) | **this crate** | winit + wgpu |
| Cell pane (shell / TUI only) | this crate | mono grid in the glass well |
| Host policy (config dir, updates, MCP, transfer) | Go | `internal/*` |

## How launch works

```bash
./suzuri          # Go host resolves UI binary and spawns it
./suzuri chrome   # same path; optional extra args for the UI process
```

Resolution order (`internal/chromehost.ResolveBinary`):

1. `SUZURI_CHROME` env  
2. Sibling of the running `suzuri` executable (`suzuri-chrome`)  
3. Dev: `chrome/target/release/suzuri-chrome` under the repo  
4. `PATH`

## Library surface (`suzuri_chrome`)

```rust
use suzuri_chrome::{
    layout, cells, ansi, pty, session, panes, input, settings, commands, shell,
    ChromeSession, Metrics, CellGrid, PtySession, VERSION, HOST_API,
};
```

See `PARITY.md` for feature status. Release packaging ships the UI binary next
to the host (see repo root `README.md` and `.github/workflows/release.yml`).
