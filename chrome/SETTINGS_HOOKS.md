# Settings prefs — host hooks

Chrome UI prefs (`rain`, `lens`, `glass_darken`) persist in **`chrome_prefs.json`**, a sibling of product **`config.json`**. They never share a schema or write path.

## Paths

| OS | Directory | Prefs file | Product config (do not touch) |
|----|-----------|------------|-------------------------------|
| macOS | `~/Library/Application Support/suzuri/` | `chrome_prefs.json` | `config.json` |
| Windows | `%LOCALAPPDATA%\suzuri\` | `chrome_prefs.json` | `config.json` |
| Linux | `~/.config/suzuri/` | `chrome_prefs.json` | `config.json` |

Helpers: `config_store::product_config_dir()`, `config_store::chrome_prefs_path()`.

## JSON shape

```json
{
  "rain": true,
  "lens": true,
  "glass_darken": 0.82
}
```

Missing file or keys → defaults (`rain`/`lens` true, `glass_darken` `0.82`).

## `SettingsState` lifecycle

| Hook | Behavior |
|------|----------|
| `SettingsState::new()` | Loads from product `chrome_prefs.json`. |
| `SettingsState::with_path(p)` | Same, injectable path (tests). |
| `handle_hotkey` | Mutates prefs **and** saves immediately when a toggle matches. |
| `close()` | Closes modal; **saves if dirty**. |
| `toggle()` | Close path flushes; open path does not write. |
| `tick(dt)` | Springs + **auto-save** if `prefs` was mutated via the public field (e.g. palette). |
| `mark_dirty()` | Host signal after external `prefs` mutation (optional if `tick` runs). |
| `save_if_dirty()` / `save_prefs()` | Explicit flush. |

Public API kept stable: `prefs`, `open` / `close` / `toggle`, `tick`, modal rects (`base_modal_rect`, `animated_modal_rect`), `handle_hotkey`, `visible`, `present`, `overlay`, `display_lines`.

## Host integration checklist

1. **Construct once** — `SettingsState::new()` at app start (already does load).
2. **Modal hotkeys** — while settings open, route keys to `handle_hotkey` (saves itself).
3. **Palette / commands** that flip `settings.prefs.rain` / `.lens` directly:
   - either call `settings.mark_dirty()` then `settings.save_if_dirty()`, **or**
   - rely on per-frame `settings.tick(dt)` (detects `prefs != last_saved` and saves).
4. **On modal close** — use `settings.close()` (not only `settings.open = false`) so dirty prefs flush.
5. **Never** write product `config.json` from chrome; font/theme/profiles stay in the Go host store.

## Modules

| File | Role |
|------|------|
| `src/config_store.rs` | Paths, JSON parse/serialize, atomic load/save |
| `src/settings.rs` | Modal state, dirty flag, load-on-new, save-on-change/close |
| `SETTINGS_HOOKS.md` | This document |

## Tests

- `config_store` — temp-dir roundtrip; does not clobber sibling `config.json`.
- `settings` — hotkey persist, close flush, tick-detected mutation, full field roundtrip.
