# suzuri（硯）

**Suzuri** means *inkstone* — the stone where ink is ground before writing.

A **real terminal host** for Windows: your window, ConPTY, VT cell grid. Not a TUI inside someone else’s emulator, and not a Warp fork.

## Status (v0.5)

| Feature | State |
|---------|--------|
| Own Win32 window + ConPTY shell | ✅ |
| VT cell emulator (`vt10x`) | ✅ |
| Block caret (opacity pulse) | ✅ |
| PowerShell-friendly Backspace (DEL) | ✅ |
| Scrollback + mouse wheel / PgUp·PgDn | ✅ |
| Multi-row scroll capture | ✅ |
| Drag select + copy/paste | ✅ |
| ANSI 16 / 256 / truecolor + bold | ✅ |
| **Charm chrome** (tabs, status, palette) | ✅ |
| Font: **Cascadia Mono** (fallback Consolas…) | ✅ |
| Splits / richer menus | soon |

### Keys

| Shortcut | Action |
|----------|--------|
| `Ctrl+Shift+T` | New tab |
| `Ctrl+W` | Close tab |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Next / previous tab |
| `Ctrl+1`…`Ctrl+9` | Jump to tab |
| Click tab strip | Activate tab |
| `Ctrl+K` / `Ctrl+P` | Command palette (Charm) |
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

Charm owns **all UI chrome** (tab strip, status line, command palette) via Bubble Tea + Lip Gloss. The shell viewport stays a VT cell grid driven by ConPTY.

```
Win32 window  →  key/mouse
      │
      ├─ Charm chrome (tabs / status / palette)  → View → mini VT → paint
      │
      └─ active tab
           write queue  →  ConPTY  →  shell
                ↑                         ↓
           VT parse (UI thread)  ←  byte queue
                ↓
           cell grid + scrollback → paint
```

## Name

Romanization: `suzuri` · Japanese: 硯 · English: inkstone
