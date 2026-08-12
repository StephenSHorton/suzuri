# suzuri-chrome product parity

Branch: `exp/native-chrome`

## Wave 1 (merged)

| Track | Status |
|-------|--------|
| Notes bank | ✅ |
| Workspace chat | ✅ |
| Transfer engine | ✅ |
| Terminal selection | ✅ |
| Settings persist | ✅ |
| Host merge surface | ✅ |

## Wave 2 (merged)

| Track | Status |
|-------|--------|
| Selection highlight paint | ✅ |
| Transfer drag-drop + ticket copy | ✅ |
| Go host `suzuri chrome` spawn | ✅ |
| Notes undo + title focus | ✅ |

## Later

- Workspace presence + file attach + MCP agents
- Full theme catalog / multi-window
- In-process FFI embed after spawn
- Selection multi-click word/line polish

## Run

```bash
cd chrome && cargo build --release && ./target/release/suzuri-chrome
# product host:
go build -o suzuri ./cmd/suzuri && ./suzuri chrome
```
