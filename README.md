# suzuri（硯）

**Suzuri** means *inkstone* — the stone where ink is ground before writing.

A **real terminal host** for Windows: your window, ConPTY, VT cell grid. Not a TUI inside someone else’s emulator, and not a Warp fork.

## Status (v0.2)

| Feature | State |
|---------|--------|
| Own Win32 window + ConPTY shell | ✅ |
| VT cell emulator (`vt10x`) | ✅ |
| Block caret (opacity pulse) | ✅ |
| PowerShell-friendly Backspace (DEL) | ✅ |
| Scrollback + mouse wheel / PgUp·PgDn | ✅ |
| Drag select + copy/paste | ✅ |
| Tabs / splits / truecolor / Charm menus | soon |

### Keys

| Shortcut | Action |
|----------|--------|
| `Ctrl+Shift+C` / `Ctrl+Insert` | Copy selection |
| `Ctrl+Shift+V` / `Shift+Insert` / `Ctrl+V` | Paste |
| `Ctrl+C` | Copy if selection; else `^C` to shell |
| Right-click | Paste |
| Mouse wheel / `PgUp` `PgDn` | Scroll history |

Shell defaults to `powershell -NoLogo -NoProfile` so themed prompts don’t inject missing-font glyphs.

## Run

```powershell
cd C:\Users\4step\projects\suzuri
go run ./cmd/suzuri
# or
.\suzuri.exe
```

## Architecture

```
Win32 window  →  key/mouse
      ↓
  write queue  →  ConPTY  →  shell
      ↑                         ↓
  VT parse (UI thread)  ←  byte queue
      ↓
  cell grid + scrollback → paint
```

## Name

Romanization: `suzuri` · Japanese: 硯 · English: inkstone
