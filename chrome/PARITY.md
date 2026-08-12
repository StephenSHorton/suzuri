# suzuri-chrome product parity tracks

Branch: `exp/native-chrome`  
Repo: `~/projects/suzuri`  
Chrome crate: `chrome/`

## Done (baseline)

- Glass chrome, rain, magnifier, Gohu, multi-tab PTY, splits, caffeine
- Palette / settings / help glass modals
- Notes (list + editor, minimal)
- Workspace (local channels/messages, minimal)
- Transfer prompts (needs `suzuri-transfer` binary)

## Parallel tracks

| ID | Track | Primary files | Goal |
|----|--------|---------------|------|
| A | notes | `notes.rs` | Full bank UX: title edit, new/delete, dirty save, better caret |
| B | workspace | `workspace_ui.rs` | Channels create, scroll history, compose polish |
| C | transfer | `transfer_ui.rs` | Find binary, send/receive status, ticket display, progress parse |
| D | terminal | `ansi.rs`, `cells.rs`, new `selection.rs` | Selection + copy, OSC title, more CSI |
| E | settings | `settings.rs`, new `config_store.rs` | Persist prefs JSON next to product config |
| F | host-merge | `lib.rs`, `ffi.rs`, `HOST.md` | C-ABI / embed surface for Go host |

## Live agents (worktrees)

| Track | Agent |
|-------|--------|
| A notes | `019ff3b4-708b-7480-941b-380da1020cea` |
| B workspace | `019ff3b4-708b-7480-941b-381e77f48f33` |
| C transfer | `019ff3b4-708b-7480-941b-3823921321b1` |
| D terminal | `019ff3b4-708b-7480-941b-383def14442e` |
| E settings | `019ff3b4-708c-7610-8e3e-0fa6164fd420` |
| F host-merge | `019ff3b4-708c-7610-8e3e-0fbd4b2e9d87` |

Orchestrator will merge worktrees when tracks report done.

## Integration rules

1. Prefer logic in track-owned modules; keep `app.rs` / `renderer.rs` thin.
2. Public API: methods on track state types (`NotesState`, `WorkspaceUi`, …).
3. After tracks land, orchestrator wires palette actions + glass UI in renderer.
4. `cargo build --release -p suzuri-chrome` (or `cd chrome && cargo build --release`) must pass.
5. Match product paths under `~/Library/Application Support/suzuri/` on macOS.

## Product references

- Notes: `internal/chrome/notes*.go`, `notes_store.go`
- Workspace: `internal/workspace/`, `internal/chrome/workspace.go`
- Transfer: `internal/chrome/transfer.go`, `libs/transfer/`
- Cwd/path: `internal/ui/cwd.go`
- Config dir: `internal/config/config.go`
