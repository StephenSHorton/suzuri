# suzuri-chrome product parity

Branch: `exp/native-chrome`

## Wave 1 ✅
Notes · Workspace · Transfer engine · Terminal selection · Settings persist · Host surface

## Wave 2 ✅
Selection highlight · Transfer drop + ticket copy · `suzuri chrome` host spawn · Notes undo

## Run

```bash
cd chrome && cargo build --release && ./target/release/suzuri-chrome
go build -o suzuri ./cmd/suzuri && ./suzuri chrome
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

## Later
- Multi-window
- In-process FFI / deeper Go embed
- MCP attach polish
- Selection word/line UX polish
