# rsz-settings

Suzuri's terminal settings for any program: the font, text size, cursor,
look, shell profiles and the rest, with their names, defaults and limits,
reading and saving the settings file, and a watch that says when another
program changed it. Only dependency: `serde_json`.

```toml
[dependencies]
rsz-settings = { git = "https://github.com/StephenSHorton/suzuri", tag = "settings-v0.1.0" }
```

## One file, shared

`Store::shared()` is Suzuri's own `config.json` (Go suzuri's keys):
`%LOCALAPPDATA%\suzuri\config.json` on Windows,
`~/Library/Application Support/suzuri/config.json` on macOS,
`~/.config/suzuri/config.json` elsewhere (`RSZ_CONFIG_DIR` overrides the
folder). Every program that uses it reads and writes that one file, so a
font picked in Suzuri is the font in Hearth's terminal, and the other way
round.

```rust
use rsz_settings::Store;
use serde_json::json;

let store = Store::shared();
let s = store.load();                       // normalized: defaults filled, limits applied
let px = s["font_size_px"].as_i64();        // 10..=36, default 14
store.update(json!({"font_size_px": 16}))?; // merge, normalize, save; returns the whole settings

// Told when another program (or a hand edit) changes the file; not for
// this program's own saves. Stops when `_w` is dropped (`.forever()` keeps it).
let _w = store.watch(|s| apply(&s));
```

The settings are a `serde_json::Value` so keys a program does not know are
kept: a save writes only the keys it changed, each normalized, and leaves
every other key and value in the file as it was. So a program pinned to an
older tag never resets a setting a newer Suzuri added (a new theme, say).
Saves take an OS lock on `config.json.lock` next to the file (the same lock
Suzuri has always taken), so programs saving at once never lose a key, and
write atomically (temp file, then rename).

## The keys

| Key | Values | Default |
|---|---|---|
| `font_face` | any font name; `mono_font_faces()` is the list to offer | `FONT_FACE_DEFAULT` (GohuFont, bundled in Suzuri) |
| `font_size_px` | `FONT_SIZE_MIN`..=`FONT_SIZE_MAX` (10..=36) | 14 |
| `cursor` | `CURSORS`: block, underline, bar (`cursor_code` gives 1/2/3) | block |
| `ui_theme` | `UI_THEMES` (Astryx themes), labels `UI_THEME_LABELS` | neutral |
| `ui_mode` | `UI_MODES`: dark, light, system | dark |
| `ui_text_size` | `TEXT_SIZES` (the window chrome's text) | xl |
| `notice_position` | `NOTICE_POSITIONS` (screen corners) | bottom-left |
| `animate_unfocused` | bool | true |
| `restore_sessions` | bool | true |
| `profiles` | `[{name, shell?, cwd?}]`, unique names, never empty | `default_profiles()` |
| `active_profile` | one of the profile names | the first |

`profile_launch(&s, None)` gives the active profile's argv (None: the
user's shell) and working folder; `meta()` gives every choice with its
label and the limits, as one JSON object.

## Each program draws its own settings screen

The crate holds the settings, not a screen. Suzuri's screen is React with
Astryx inside a Tauri window, wired to its own commands; another program
may draw with something else, and should place the terminal settings among
its own. So each program draws its own, and `meta()` makes them offer the
same choices with the same labels and limits.

## Try it

```
cargo run -p rsz-settings --example settings -- watch
cargo run -p rsz-settings --example settings -- set font_size_px 18
```

Run `watch` in one terminal and `set` in another, or change a setting in
Suzuri. Tests: `cargo test -p rsz-settings`.
