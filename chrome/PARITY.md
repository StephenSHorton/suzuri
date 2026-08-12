# suzuri-chrome product parity

Branch: `exp/native-chrome`

## Agent tracks (merged)

| Track | Commit / merge | Status |
|-------|----------------|--------|
| Notes bank | `6074cc8` + integrate | ✅ |
| Workspace chat | `f76636f` + integrate | ✅ |
| Transfer engine | `6100c2d` / `bdbf199` | ✅ |
| Terminal selection | `47ddb3b` / `d8c450b` + app wire | ✅ |
| Settings persist | `635f301` / `d9154bc` | ✅ |
| Host merge surface | `0ab23fe` / `a82c598` | ✅ |

## Still thinner than product (next waves)

- Go host **spawn/embed** of `suzuri-chrome` (see `HOST.md`)
- Transfer OS drag-drop + ticket copy chip UI
- Notes undo/selection drag / title rename polish
- Workspace presence + file attach + MCP agents
- Full theme catalog / multi-window
- Selection multi-click / right-click / clear-on-focus polish (highlight paint: done)

## Run

```bash
cd chrome && cargo run --release
```
