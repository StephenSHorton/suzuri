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

## Later
- Workspace presence / attach / MCP
- Themes / multi-window
- In-process FFI
