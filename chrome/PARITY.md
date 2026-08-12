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

## Wave 3 (partial)
- Themes + `SUZURI_CONFIG_DIR` ✅ (`theme.rs`, prefs.theme, host env path)

## Later
- Workspace presence / attach / MCP
- Multi-window
- In-process FFI
