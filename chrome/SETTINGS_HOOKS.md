# Settings prefs — host hooks

Chrome UI prefs (`rain`, `lens`, `glass_darken`, `theme`) persist in
**`chrome_prefs.json`**, a sibling of product **`config.json`**. They never
share a schema or write path with the Go product store.

## Paths

| OS | Directory | Prefs file | Product config (do not touch) |
|----|-----------|------------|-------------------------------|
| **env** | `$SUZURI_CONFIG_DIR` when set (host spawn) | `chrome_prefs.json` | `config.json` |
| macOS | `~/Library/Application Support/suzuri/` | `chrome_prefs.json` | `config.json` |
| Windows | `%LOCALAPPDATA%\suzuri\` | `chrome_prefs.json` | `config.json` |
| Linux | `~/.config/suzuri/` | `chrome_prefs.json` | `config.json` |

Helpers: `config_store::product_config_dir()`, `config_store::chrome_prefs_path()`.

**`SUZURI_CONFIG_DIR`** — Go host (`internal/chromehost`) sets this to
`config.Dir()` when spawning chrome so notes / prefs share the product data
root. `product_config_dir()` **prefers this env** over OS defaults.

## JSON shape

```json
{
  "rain": true,
  "lens": true,
  "glass_darken": 0.82,
  "theme": "inkstone"
}
```

Missing file or keys → defaults (`rain`/`lens` true, `glass_darken` `0.82`,
`theme` `"inkstone"`). Unknown theme ids normalize to `inkstone`. Aliases:
`tokyo_night` → `tokyo-night`, `charmtone` → `charm`.

## Themes

Named paint palettes live in `src/theme.rs` (ids cycle in settings):

| id | Label | Notes |
|----|-------|-------|
| `inkstone` | Inkstone | Default; matches `cells::theme` VT consts |
| `nord` | Nord | Arctic blue-greys |
| `dracula` | Dracula | Purple / pink accents |
| `tokyo-night` | Tokyo Night | Night-city blues |
| `charm` | Charm | Warm violet (product charmtone) |

Each palette exposes **`bg` / `fg` / `jade` / `muted` / `err`** as `[f32; 3]`
RGB in 0..=1.

### How the renderer should use theme colors

Cell VT pens still default to **inkstone** consts (`cells::theme::{BG,FG,JADE,DIM,ERR}`)
so ANSI decode stays stable. Chrome paints should track the **prefs theme**:

```text
let pal = settings.prefs.theme_colors();
// or: theme::colors(&settings.prefs.theme)

// Clear / terminal hole background
pal.bg

// Chrome labels, status glyphs
pal.fg / pal.muted

// Selection wash, accent chips, rain body, focus rings
pal.jade

// Error / destructive
pal.err
```

Recommended call sites (binary, not lib):

| Surface | Prefer |
|---------|--------|
| Selection highlight under cells | `pal.jade` (+ alpha) instead of hard-coded `theme::JADE` |
| Glyph rain body color | `pal.jade` (head = brighter mix of jade) |
| Glass modal title / toggle accents | `pal.jade` / `pal.fg` |
| Window / panel clear behind cells | `pal.bg` |

Do **not** write product `config.json` theme from chrome — that field stays
with the Go host. Chrome’s `theme` key is chrome-local paints only.

Settings hotkeys while modal is open:

| Key | Action |
|-----|--------|
| `1` | Toggle rain |
| `2` | Toggle lens |
| `3` / `t` | Cycle theme forward |
| `T` | Cycle theme backward |
| `[` / `-` | Glass darken −5% |
| `]` / `=` / `+` | Glass darken +5% |

## `SettingsState` lifecycle

| Hook | Behavior |
|------|----------|
| `SettingsState::new()` | Loads from product `chrome_prefs.json` (env-aware path). |
| `SettingsState::with_path(p)` | Same, injectable path (tests). |
| `handle_hotkey` | Mutates prefs **and** saves immediately when a toggle matches. |
| `close()` | Closes modal; **saves if dirty**. |
| `toggle()` | Close path flushes; open path does not write. |
| `tick(dt)` | Springs + **auto-save** if `prefs` was mutated via the public field (e.g. palette). |
| `mark_dirty()` | Host signal after external `prefs` mutation (optional if `tick` runs). |
| `save_if_dirty()` / `save_prefs()` | Explicit flush. |
| `theme_colors()` | Active `ThemeColors` for `prefs.theme`. |

Public API kept stable: `prefs`, `open` / `close` / `toggle`, `tick`, modal rects (`base_modal_rect`, `animated_modal_rect`), `handle_hotkey`, `visible`, `present`, `overlay`, `display_lines`.

## Host integration checklist

1. **Construct once** — `SettingsState::new()` at app start (already does load).
2. **Modal hotkeys** — while settings open, route keys to `handle_hotkey` (saves itself).
3. **Palette / commands** that flip `settings.prefs.rain` / `.lens` / `.theme` directly:
   - either call `settings.mark_dirty()` then `settings.save_if_dirty()`, **or**
   - rely on per-frame `settings.tick(dt)` (detects `prefs != last_saved` and saves).
4. **On modal close** — use `settings.close()` (not only `settings.open = false`) so dirty prefs flush.
5. **Never** write product `config.json` from chrome; font/theme/profiles stay in the Go host store.
6. **Renderer** — each frame (or on prefs change), sample `settings.prefs.theme_colors()` for chrome paints (see above).

## Modules

| File | Role |
|------|------|
| `src/config_store.rs` | Paths (`SUZURI_CONFIG_DIR`), JSON parse/serialize, atomic load/save |
| `src/theme.rs` | Named palettes, cycle, normalize ids |
| `src/settings.rs` | Modal state, dirty flag, load-on-new, save-on-change/close |
| `SETTINGS_HOOKS.md` | This document |

## Tests

- `config_store` — temp-dir roundtrip; theme field; env override; does not clobber sibling `config.json`.
- `theme` — ids, aliases, cycle, palette distinctness.
- `settings` — hotkey persist, theme cycle, close flush, tick-detected mutation, full field roundtrip.
