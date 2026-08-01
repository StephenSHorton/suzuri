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
| **Settings** (`Ctrl+,` / palette) — font, size, cursor, theme, shell ANSI, profile | ✅ |
| Themes: Inkstone / Charmtone / High contrast + soft/full ANSI remap | ✅ |
| **Profiles** (Default / PowerShell / Cmd + custom in config.json) | ✅ |
| **Help** `Ctrl+/` · first-run splash · command categories | ✅ |
| Status toasts · click `+` · click-out dismiss · last-tab quit confirm | ✅ |
| Config: `%LOCALAPPDATA%\suzuri\config.json` | ✅ |
| Font: **GohuFont uni14 Nerd Font Mono** (bundled, WTFPL) · Cascadia/Consolas fallback | ✅ |
| Seamless box-drawing / block glyphs (WT-style) | ✅ |
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
| `Ctrl+,` | Settings (font, size, cursor, theme, ANSI, profile) |
| `Ctrl+/` | Keyboard shortcuts help |
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

## MCP (agent diagnostics)

Spawn-on-demand **stdio** MCP — Grok starts `suzuri mcp` when needed; it attaches to the **running GUI** over loopback. No always-on daemon. See [`docs/mcp.md`](docs/mcp.md).

```toml
# ~/.grok/config.toml
[mcp_servers.suzuri]
command = 'C:\Users\4step\projects\suzuri\suzuri.exe'
args = ["mcp"]
enabled = true
```

Tools: `suzuri_status`, `suzuri_diag`, `suzuri_snapshot`, `suzuri_submit`, `suzuri_logs` (app log tail).

## Logs

Charm [`log`](https://github.com/charmbracelet/log) writes to:

```
%LOCALAPPDATA%\suzuri\suzuri.log
```

(usually `C:\Users\<you>\AppData\Local\suzuri\suzuri.log`). Level defaults to `debug`; set `SUZURI_LOG_LEVEL=info` (or `warn` / `error`) to quiet it. Panics in the UI thread are recovered and written with a stack trace.

## Architecture

Charm owns **all UI chrome** (tab strip, status line, command palette) via Bubble Tea + Lip Gloss. The shell viewport stays a VT cell grid driven by ConPTY.

**Direction:** make chrome feel more like [Crush](https://github.com/charmbracelet/crush) (dialogs, themes, settings) while remaining a real host — not an AI TUI inside another terminal. See [`docs/crush-inspired-plan.md`](docs/crush-inspired-plan.md).

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
