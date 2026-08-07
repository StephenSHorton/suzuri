# Suzuri MCP

Agent-facing diagnostics and control for the **live** suzuri window.

## Mental model (not an always-on server)

Per the vault note *MCP does not require an always-on background process*:

| Piece | Who starts it | Lifecycle |
|-------|----------------|-----------|
| **suzuri GUI** (`suzuri.exe`) | You | Runs while you use the terminal |
| **stdio MCP** (`suzuri mcp`) | Grok / MCP client | Spawned on demand, exits when the client is done |

The MCP process is **not** a daemon. It attaches to the GUI over a **loopback** bridge (`%LOCALAPPDATA%\suzuri\bridge.json` → `http://127.0.0.1:<port>` + bearer token). No public port, no “start the MCP server first.”

```
Grok  --spawns stdio-->  suzuri mcp  --HTTP 127.0.0.1-->  suzuri GUI (bridge)
```

If the GUI is not running, tools return a clear error: launch `suzuri.exe` first.

## Tools

| Tool | Purpose |
|------|---------|
| `suzuri_status` | GUI up? tab count, pid, bridge URL |
| `suzuri_diag` | Full report: viewport, blocks, input bar, echo filter, PTY tail, dual-display *diagnostic* notes |
| `suzuri_snapshot` | Same structured snapshot without the extra diagnostic notes pass |
| `suzuri_submit` | Submit a line via Warp bar path (block + echo arm + ConPTY) |
| `suzuri_logs` | Tail app log (`suzuri.log`). Works even if GUI is down. |
| `suzuri_notes_list` | List the **user notes bank** (Ctrl+Shift+M): id, title, body, active |
| `suzuri_notes_get` | Get one note by `id` (omit = active) |
| `suzuri_notes_create` | Create a note (`title?`, `body?`); becomes active |
| `suzuri_notes_update` | Partial update (`id?`, `title?`, `body?`, `set_active?`) |
| `suzuri_notes_delete` | Delete by `id` (omit = active; last note is cleared, not removed). **No UI confirm** — agents skip the interactive prompt. |
| `workspace_guide` | **Start here** — how the shared room works + paste-ready agent/user phrases |
| `workspace_status` | Shared workspace path + channel/member counts (offline OK) |
| `workspace_join` | Register as agent/human in the shared workspace |
| `workspace_leave` | Leave workspace |
| `workspace_set_status` | Publish availability: `idle` / `working` / `waiting` / `blocked` / `away` (+ optional note) |
| `workspace_members` | List members (includes `status` + `status_note`) |
| `workspace_channels` | List channels |
| `workspace_channel_create` | Create a channel |
| `workspace_channel_delete` | Delete a channel + history + files (not #general) |
| `workspace_post` | Post a message to a channel |
| `workspace_history` | Read recent messages from a channel |
| `workspace_upload` | Attach a local file to a channel (max 64MiB) |
| `workspace_download` | Resolve a file attachment to a local path |

**Shell output vs app log vs notes**

| What you want | Tool |
|---------------|------|
| What the user sees in the terminal | `suzuri_diag` → `viewport`, `blocks`, `live_lines` |
| Raw ConPTY bytes (escape sequences) | `suzuri_diag` → `pty_tail` |
| Full-screen TUI (bar hidden) | `suzuri_diag` → `tabs[].alt_screen` |
| Host events (tabs, bridge, panics, key path) | `suzuri_logs` |
| User scratch notes (Ctrl+Shift+M) | `suzuri_notes_*` |
| Shared channels (humans + AIs) | `workspace_*` — see [`workspace.md`](workspace.md) |

### Notes bank details

- Live GUI path: flushes the open editor, mutates the bank, saves `notes.json`, reloads the notes UI if open.
- Interactive **d / Delete** in the notes list asks for confirmation; MCP `suzuri_notes_delete` never prompts.
- Offline path: reads/writes `notes.json` under the OS config dir when the GUI is not running.
  - Windows: `%LOCALAPPDATA%\suzuri\notes.json`
  - macOS: `~/Library/Application Support/suzuri/notes.json`
  - Linux: `~/.config/suzuri/notes.json`
- `suzuri_diag`’s `notes` field is **agent diagnostics** (e.g. dual-command hints), not this bank.

## Wire Grok Build

### Windows

```toml
[mcp_servers.suzuri]
command = 'C:\Users\you\projects\suzuri\suzuri.exe'
args = ["mcp"]
enabled = true
startup_timeout_sec = 30
```

```powershell
cd C:\Users\you\projects\suzuri
go build -o suzuri.exe ./cmd/suzuri
```

### macOS

```toml
[mcp_servers.suzuri]
command = "/Applications/suzuri.app/Contents/MacOS/suzuri"
# or a local build:
# command = "/Users/you/projects/suzuri/suzuri"
args = ["mcp"]
enabled = true
startup_timeout_sec = 30
```

```bash
cd ~/projects/suzuri
CGO_ENABLED=1 go build -o suzuri ./cmd/suzuri
```

Use the **built** binary path (`go run` is awkward for MCP). The GUI must be running for live terminal tools; notes also work offline via `notes.json`.

## Manual smoke

```powershell
# Windows — Terminal A: GUI (writes bridge.json)
.\suzuri.exe
# Terminal B:
Get-Content $env:LOCALAPPDATA\suzuri\bridge.json
```

```bash
# macOS — Terminal A: GUI
./suzuri
# Terminal B:
cat "$HOME/Library/Application Support/suzuri/bridge.json"
```

## Security

- Bridge listens on **127.0.0.1 only**
- Random bearer token in `bridge.json` (mode 0600)
- Token required on every request
- Endpoint file removed when the GUI exits

## Why not “MCP owns ConPTY”?

suzuri’s value for agents is **what the user sees** (command blocks, echo filter, layout). Spawning a second headless shell would not show dual-prompt or bar bugs. Attach-to-live-window is intentional for v1.
