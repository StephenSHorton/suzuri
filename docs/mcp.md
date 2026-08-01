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
| `suzuri_diag` | Full report: viewport, blocks, input bar, echo filter, PTY tail, dual-display notes |
| `suzuri_snapshot` | Same structured snapshot without the extra notes pass |
| `suzuri_submit` | Submit a line via Warp bar path (block + echo arm + ConPTY) |
| `suzuri_logs` | Tail `%LOCALAPPDATA%\suzuri\suzuri.log` (app/host log). Works even if GUI is down. |

**Shell output vs app log**

| What you want | Tool |
|---------------|------|
| What the user sees in the terminal | `suzuri_diag` → `viewport`, `blocks`, `live_lines` |
| Raw ConPTY bytes (escape sequences) | `suzuri_diag` → `pty_tail` |
| Full-screen TUI (bar hidden) | `suzuri_diag` → `tabs[].alt_screen` |
| Host events (tabs, bridge, panics, key path) | `suzuri_logs` |

## Wire Grok Build

```toml
[mcp_servers.suzuri]
command = 'C:\Users\4step\projects\suzuri\suzuri.exe'
args = ["mcp"]
enabled = true
startup_timeout_sec = 30
```

Use the **built** `suzuri.exe` path (or `go run` is awkward for MCP). Rebuild after pulling:

```powershell
cd C:\Users\4step\projects\suzuri
go build -o suzuri.exe ./cmd/suzuri
```

## Manual smoke

```powershell
# Terminal A — GUI (writes bridge.json)
.\suzuri.exe

# Terminal B — status via MCP-ish HTTP (or just run the MCP under a client)
# After GUI is up:
Get-Content $env:LOCALAPPDATA\suzuri\bridge.json
```

## Security

- Bridge listens on **127.0.0.1 only**
- Random bearer token in `bridge.json` (mode 0600)
- Token required on every request
- Endpoint file removed when the GUI exits

## Why not “MCP owns ConPTY”?

suzuri’s value for agents is **what the user sees** (command blocks, echo filter, layout). Spawning a second headless shell would not show dual-prompt or bar bugs. Attach-to-live-window is intentional for v1.
