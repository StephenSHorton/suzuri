# suzuri（硯）

**Suzuri** means *inkstone* — the stone where ink is ground before writing.

This is a **real terminal host**: your window, your PTY, your grid. Not a TUI that lives inside someone else’s emulator, and not a Warp fork.

## Intent

| Layer | Responsibility |
|-------|----------------|
| **Host window** | OS window, input, clipboard, drag-and-drop (later) |
| **ConPTY** | Windows pseudo-console + shell process |
| **VT / grid** | Parse escapes, maintain cell buffer, render (growing) |
| **Product UI** | Tabs, command palette, agents — *later*, can use Charm as *overlay* only |

## Status

**v0 (now):** Windows-only spike

- Own desktop window
- Spawns a shell via **ConPTY**
- Forwards keyboard input
- Shows output (ANSI stripped for the early renderer)

Not yet: full VT fidelity, GPU text, tabs, scrollback chrome, Charm menus.

## Run (Windows)

```powershell
cd C:\Users\4step\projects\suzuri
go run ./cmd/suzuri
```

Requires a recent Windows 10/11 (ConPTY).

## Name

Romanization: `suzuri` · Japanese: 硯 · English: inkstone
