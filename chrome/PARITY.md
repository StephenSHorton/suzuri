# suzuri-chrome product parity

Branch: `exp/native-chrome`

## Wave 1 ✅
Notes · Workspace · Transfer engine · Terminal selection · Settings persist · Host surface

## Wave 2 ✅
Selection highlight · Transfer drop + ticket copy · `suzuri chrome` host spawn · Notes undo

## Run

```bash
cd chrome && cargo build --release
# Default UI prefers chrome when binary resolves:
go build -o suzuri ./cmd/suzuri && ./suzuri
# Force classic: SUZURI_UI=classic ./suzuri
# Explicit: ./suzuri chrome
```

## Wave 3 ✅
- Themes + `SUZURI_CONFIG_DIR` (`theme.rs`, prefs.theme, host env path)
- Workspace presence + file attach
- Selection multi-click (word / line)
- Host light IPC (`chrome_cmd` mailbox: quit / open_notes / open_workspace / open_palette)

## Wave 4 ✅
- Terminal URL hover + Cmd/Ctrl+click open browser (`links.rs`)
- Confirm quit modal (`confirm.rs`)
- First-run splash (`splash_seen` in chrome_prefs)
- Tab / pane rename dialog (F2 + palette)

## Wave 5 ✅
- Link hover paint (primary FG + light underlay on span)
- Expanded `chrome_cmd` IPC (settings/help/transfer/new_tab/caffeine)
- Copy toasts (`Copied` / `Ticket copied`)

## Wave 6 ✅
- New window (second process · palette / ⌃⇧N · mailbox `new_window`)
- FFI session create/destroy + tabs (`--features ffi`; GPU still spawn)
- Workspace auto-refresh (~1s) + presence cycle + `refresh_workspace`
- Selection word/line drag modes (500ms multi-click · extend by word/line)

## Wave 7 ✅
- Default UI prefers chrome when binary resolvable (`SUZURI_UI` override)
- MCP bridge proxy while chrome runs (notes/workspace/status/submit)
- `chrome_status.json` + `chrome_submit` mailboxes
- FFI `present` / `present_available` stubs (GPU remains process-spawn)

## Wave 8 ✅
- Rich chrome→bridge snapshot: tabs, viewport, live_lines, history_tail, pty_tail
- Go `SnapshotFromChromeStatus` for MCP diag/status/snapshot
- Legacy thin status still accepted

## Wave 9 ✅
- Echo filter (product `echo_filter.go` port) armed on warp submit
- Host command blocks: scrollback rule + `❯ cmd` + MCP `blocks[]` / history kinds
- Snapshot notes include armed echo + recent blocks for `suzuri_diag`

## Wave 9 polish ✅
- `commit_live` before each block (previous output → history, live blanked)
- Clear pin (`clear`/`cls`/`Clear-Host`) + stick-bottom
- PTY payload uses CR (product `sendBarPayload`), multi-line → CR

## Later
- Full in-process GPU present loop (cgo + host window)
- Package install always ships `suzuri-chrome` sibling binary
