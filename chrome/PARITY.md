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

## Wave 2 (in flight)

| Track | Goal |
|-------|------|
| G | Selection **highlight** paint in renderer |
| H | Transfer OS **drag-drop** + ticket **copy chip** |
| I | Go host **spawn** `suzuri-chrome` (`HOST.md` phase 1) |
| J | Notes **undo** + title focus polish |

## Later waves

- Workspace presence + file attach + MCP agents
- Full theme catalog / multi-window
- In-process FFI embed (after spawn)

## Run

```bash
cd chrome && cargo run --release
```
