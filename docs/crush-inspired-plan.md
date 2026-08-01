# Crush-inspired suzuri — research synthesis

**Date:** 2026-07-31  
**Repos:** [charmbracelet/crush](https://github.com/charmbracelet/crush) (cloned at `C:\Users\4step\projects\crush`)  
**Method:** Multi-agent code observation (styles, dialogs, config, shell, Charm stack, suzuri gap) + product/architecture council.  
**Goal:** Use Charm the way Crush *feels*, without turning suzuri into an AI coding app or abandoning being a **real terminal host**.

---

## TL;DR

| | **Crush** | **suzuri today** |
|--|-----------|------------------|
| **Product** | AI coding “bestie” TUI | Real Windows terminal (inkstone) |
| **Where it runs** | *Inside* someone else’s terminal | *Is* the window + ConPTY |
| **UI stack** | Bubble Tea **v2** + Lip Gloss **v2** + Ultraviolet screen buffer | Charm **v1** chrome only; Win32 owns the app |
| **Shell** | Embedded mvdan POSIX interp + `os/exec` pipes (not a PTY) | PowerShell/etc. in **ConPTY** → vt10x cells |
| **Look** | Charmtone Pantera, gradients, dialog stack, command palette | Neon tab cards + 4-item palette (sticker, not product shell) |

**North star:** suzuri should look like a **Charm app wearing a real terminal**, not a terminal wearing a Charm sticker.

- **Host owns:** window, ConPTY, VT fidelity, selection, scrollback, GDI fonts.  
- **Charm owns:** every non-shell surface users notice — tabs, palette, **settings**, confirms — with tokenized neon cards and one dialog primitive.  
- **Compositor owns:** card floats on dimmed shell (Crush-screenshot energy without Crush’s product).

---

## Critical architecture truth

Crush is **not** a terminal emulator. From `internal/shell`:

- Commands go through `mvdan.cc/sh` + pipes.
- `RunAndCapturePTY` is a **legacy name** — no real PTY; color is forced via env vars.
- Children are isolated from Crush’s TTY so Bubble Tea isn’t corrupted.

Suzuri is the opposite: **Win32 → ConPTY → VT grid → GDI**. Charm cannot replace that stack. It can (and should) own **all app chrome and modals**, composited over the shell cells — which is already the direction of `internal/chrome` + `paintChrome`, just too thin.

Blunt gap analysis: README says “Charm owns all UI chrome.” Paint of the strip/palette: yes. **Product chrome: no.** WndProc still owns tabs, shortcuts, sessions, and half the UX.

---

## What Crush does that we should steal

### 1. Theme factory (not random hex)

Crush: semantic palette → `quickStyle` → mega `Styles`, then overrides.

- Tokens: primary / secondary / accent, fg/bg ladders, status colors, **ANSI 16 remap**.
- Default theme: **Charmtone Pantera** (`charmtone.Charple`, `Pepper`, `Sash`, …).
- Gradients: `lipgloss.Blend1D` for titles/logo.

**Suzuri:** `internal/chrome/theme.go` hardcodes inkstone neon. Next: `tokens → styles` so Settings → Theme is real.

### 2. Dialog overlay stack (not nested tea.Models)

Crush UI rule (`internal/ui/AGENTS.md`):

- **One** root Bubble Tea model.
- Dialogs: `ID` / `HandleMsg` → typed **Action** / `Draw`.
- Overlay stack; front dialog owns keys.
- **Input grace** (~200ms quiet) when async modals open so leftover keystrokes don’t auto-confirm.

**Suzuri:** palette is extra View rows that **grow chrome height** and push the shell. Next: true float-over-shell dim + card; host applies actions (`ApplyFont`, `SetTheme`, `CloseTab`, …).

### 3. Command palette as registry

Crush: filterable list, categories, shortcuts, typed actions.

**Suzuri:** four hardcoded items (new/close/next/prev tab). Expand to Settings, About, theme quick-pick, etc.

### 4. Config persistence pattern

Crush writes user prefs under data dir (`%LOCALAPPDATA%\crush\crush.json` on Windows) with:

- Nested `options.tui.*`
- Atomic write + typed mutators
- Schema for fields

Themes stay **code** (factories); prefs are **string/bool ids** in JSON. Font is **not** in Crush (host terminal owns font) — but **suzuri is the host**, so font **must** be our config.

### 5. Charm v2 imports

Crush: `charm.land/bubbletea/v2`, `lipgloss/v2`, `bubbles/v2`, `glamour/v2`.  
Suzuri: still `github.com/charmbracelet/*` v1.

Upgrade when we grow chrome; not a blocker for settings v1.

### Explicitly do **not** steal

- AI agents, model picker, MCP, LSP, session DB, permissions-for-tools UX  
- Replacing WndProc with a pure Bubble Tea app  
- Embedding mvdan shell instead of ConPTY  
- Ultraviolet as the shell renderer (GDI + VT stays)

---

## Target architecture

```
┌─────────────────────────────────────────────────────────┐
│  Win32 window (message loop, LockOSThread)              │
│    keys/mouse → focus router                            │
│      ├─ overlay open? → chrome.Update / dialog.Handle   │
│      └─ else → ConPTY write                             │
│                                                         │
│    paint:                                               │
│      shell VT grid (scrollback + cells)                 │
│      + dim matte (if overlay)                           │
│      + Charm chrome strip (tabs)                        │
│      + Charm overlay card (palette / settings / confirm)│
└─────────────────────────────────────────────────────────┘
         │                              │
         ▼                              ▼
   host.Session (ConPTY)         chrome package
   vt10x + GDI boxdraw           tokens, dialogs, commands
                                 config load/save
```

**Input ownership matrix**

| Focus | Keys go to |
|-------|------------|
| Shell | ConPTY |
| Palette / settings / confirm | Charm model only |
| Esc | Close top overlay (or clear selection) |

**Paint rule:** shell fills client under chrome strip; overlays **composite on top** (do not permanently steal rows except the fixed tab strip).

---

## Settings menu design

### Open

- `Ctrl+,`  
- Command palette → **Settings…**  
- (Later) tray / menu bar if we add one  

### UX

Single reusable **dialog card** (rounded neon border, void fill, title, body, footer `esc` cancel · `enter` apply):

```
┌─ Settings ─────────────────────────────╮
│  › Appearance                          │
│    Font face        Cascadia Mono    ▸ │
│    Font size        16               ▸ │
│    Cursor           Block            ▸ │
│    Theme            Inkstone         ▸ │
│    Shell ANSI map   Soft Charm       ▸ │
│  Session                               │
│    Default shell    powershell …     ▸ │
│    Scrollback lines 5000             ▸ │
│                                        │
│  esc cancel   enter apply              │
╰────────────────────────────────────────╯
```

Drill-in pages are the same card with a list (filter optional). **Live preview** for font size + theme while focused; **Esc** restores snapshot; **Enter** persists.

### Config file

`%LOCALAPPDATA%\suzuri\config.json` (or `suzuri.toml` later):

```json
{
  "font_face": "Cascadia Mono",
  "font_size_px": 16,
  "cursor": "block",
  "theme": "inkstone",
  "shell_ansi_map": "soft",
  "default_shell": "",
  "scrollback_lines": 5000
}
```

Extend `internal/config` with load/save (atomic write). Theme id → palette factory in `chrome` / shared `theme` package. Applying font recreates GDI HFONT and invalidates metrics (existing `createFontFor` path).

### Themes v1

| ID | Intent |
|----|--------|
| `inkstone` | Current void + hot pink (brand) |
| `charmtone` | Crush-adjacent Pepper/Charple/Dolly |
| `high_contrast` | Accessibility |
| (later) `custom` | user ANSI16 overrides |

---

## Visual language to steal

1. Semantic tokens + ladders (base → subtle → most subtle).  
2. Contrast pairs (`onPrimary` on brand chips).  
3. Gradients only on titles/wordmarks (optional 硯 / “suzuri”).  
4. One dialog chrome for palette + settings + confirm.  
5. Optional ANSI-16 remap for shell so `ls`/`git` colors match chrome.  
6. Keep geometric box-drawing for `╭╮╯╰` (already fixed for outer fillets).

---

## Phased roadmap

### Phase 0 — Hygiene (small)

- [ ] Command registry interface (`id`, title, keys, action).  
- [ ] Wire palette from registry (not four hardcodes).  
- [ ] Document input ownership in code comments / README.

### Phase 1 — Settings MVP (user-requested)

- [ ] Persist config JSON under `%LOCALAPPDATA%\suzuri\`.  
- [ ] Dialog kit: center card + dim matte over shell.  
- [ ] Settings: font face, size, cursor, theme.  
- [ ] `Ctrl+,` + palette entry.  
- [ ] Live apply + Esc revert + Enter save.

### Phase 2 — Crush-feel polish

- [x] Token theme system (`inkstone` / `charmtone` / `high_contrast`).  
- [x] Optional shell ANSI-16 map (`none` / `soft` / `full`).  
- [x] Confirm dialog on last-tab quit (Enter quits, Esc keeps tab).  
- [x] Status toasts (settings saved, font fallback, session ended, …).  
- [x] Click-outside dismiss; `+` tab clickable.

### Phase 3 — Stack upgrade (when needed)

- [ ] Migrate to `charm.land/*/v2`.  
- [ ] Richer command categories; keybind help.  
- [ ] Profiles (cwd + shell + theme).  
- [ ] First-run splash card (once).

### Non-goals

- AI agent / Crush clone product  
- Pure Bubble Tea replacing Win32 host  
- Dropping ConPTY for embedded mvdan shell  
- Full Ultraviolet shell renderer  
- Perfect pixel-parity with Crush screenshots (different product class)

---

## Open questions (product)

1. **Last-tab Ctrl+W:** keep quit (current preference) or confirm dialog first?  
2. **Acrylic / transparency:** worth Windows DWM complexity in v1? (probably no)  
3. **Theme packs from JSON** vs code-only factories? (Crush-style code first)  
4. **Splits:** after settings/tabs feel solid, or never for inkstone minimalism?

---

## First PR that would make this real

> **`Ctrl+,` opens a rounded Inkstone settings card over a dimmed shell; change Cascadia size live; Esc reverts; Enter saves to `%LOCALAPPDATA%\suzuri\config.json`; `Ctrl+K` lists “Settings…” with the same card chrome as the palette.**

That path is glamorous, Charm-native, and still unmistakably a **real terminal host**.

---

## Research artifacts

| Source | Finding |
|--------|---------|
| Crush `internal/ui/styles` | Charmtone Pantera + quickStyle factory + ANSI16 remap |
| Crush `internal/ui/dialog` + `model` | Overlay stack, typed Actions, command palette, layout modes |
| Crush `internal/config` | `options.tui` persist; themes not in JSON; no font (host terminal) |
| Crush `internal/shell` | Not a terminal emulator; pipes + mvdan |
| Suzuri gap | Charm is skin + 4-item list; hybrid paint solid; no settings/registry/dialogs |
| Charm modules | Crush on v2 `charm.land/*`; suzuri on v1 `github.com/charmbracelet/*` |
| Council vote | Settings + dialog kit + tokens + command registry + input matrix |

Local clone for further digging: `C:\Users\4step\projects\crush`.
