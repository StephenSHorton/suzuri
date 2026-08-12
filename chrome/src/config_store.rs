//! Chrome prefs persistence — `chrome_prefs.json` next to product `config.json`.
//!
//! Product `config.json` (font, theme, profiles, …) is owned by the Go host
//! (`internal/config`). This module **never** reads or writes that file.
//!
//! Paths match product layout (override with `SUZURI_CONFIG_DIR` — set by the
//! Go host spawn so notes/prefs share the product dir):
//! - env `SUZURI_CONFIG_DIR` when set (wins)
//! - macOS: `~/Library/Application Support/suzuri/`
//! - Windows: `%LOCALAPPDATA%\suzuri\`
//! - Linux / other: `~/.config/suzuri/`

use std::fs;
use std::io;
use std::path::{Path, PathBuf};

use crate::theme;

/// Filename under the product config directory (sibling of `config.json`).
pub const CHROME_PREFS_FILE: &str = "chrome_prefs.json";

/// Env var the Go host sets to the product `config.Dir()` path.
pub const ENV_CONFIG_DIR: &str = "SUZURI_CONFIG_DIR";

/// Default glass face darken (matches product look).
pub const GLASS_DARKEN_DEFAULT: f32 = 0.82;

/// User-tunable chrome UI prefs (rain / lens / glass darken / colors / splash).
#[derive(Clone, Debug, PartialEq)]
pub struct ChromePrefs {
    /// Canvas UI glyph rain under glass.
    pub rain: bool,
    /// Mouse glass lens / magnifier.
    pub lens: bool,
    /// Shared glass face darken 0..1 (panes / chips / modal).
    pub glass_darken: f32,
    /// Legacy named theme id (migrated → primary on load if no primary saved).
    pub theme: String,
    /// User **primary** RGB (product `colPrimary` / jade). Main brand color.
    pub primary: [f32; 3],
    /// Optional **accent** override (product `colSecondary`).
    /// `None` → derive from [`Self::primary`] via [`theme::derive_accent`].
    pub accent: Option<[f32; 3]>,
    /// First-run welcome splash has been dismissed (product `first_run_done` analog).
    pub splash_seen: bool,
}

impl Default for ChromePrefs {
    fn default() -> Self {
        Self {
            rain: true,
            lens: true,
            glass_darken: GLASS_DARKEN_DEFAULT,
            theme: theme::DEFAULT_THEME_ID.to_string(),
            primary: theme::DEFAULT_PRIMARY,
            accent: None,
            splash_seen: false,
        }
    }
}

impl ChromePrefs {
    pub fn nudge_darken(&mut self, delta: f32) {
        self.glass_darken = (self.glass_darken + delta).clamp(0.0, 0.95);
    }

    /// Effective accent (custom or derived).
    pub fn effective_accent(&self) -> [f32; 3] {
        self.accent
            .unwrap_or_else(|| theme::derive_accent(self.primary))
    }

    /// Whether accent is user-picked (`false` = auto from primary).
    #[inline]
    pub fn accent_is_custom(&self) -> bool {
        self.accent.is_some()
    }

    pub fn set_primary(&mut self, rgb: [f32; 3]) {
        self.primary = [
            rgb[0].clamp(0.0, 1.0),
            rgb[1].clamp(0.0, 1.0),
            rgb[2].clamp(0.0, 1.0),
        ];
    }

    /// Rotate primary hue; keeps custom accent override if set.
    pub fn nudge_primary_hue(&mut self, delta_deg: f32) {
        self.primary = theme::rotate_hue(self.primary, delta_deg);
    }

    /// Set a custom accent (stops auto-derive until cleared).
    pub fn set_accent(&mut self, rgb: [f32; 3]) {
        self.accent = Some([
            rgb[0].clamp(0.0, 1.0),
            rgb[1].clamp(0.0, 1.0),
            rgb[2].clamp(0.0, 1.0),
        ]);
    }

    /// Clear custom accent → derive from primary again.
    pub fn clear_accent(&mut self) {
        self.accent = None;
    }

    /// Rotate accent hue; materializes a custom accent from the derived value first.
    pub fn nudge_accent_hue(&mut self, delta_deg: f32) {
        let base = self.effective_accent();
        self.accent = Some(theme::rotate_hue(base, delta_deg));
    }

    /// Active paint palette from primary + optional accent override.
    pub fn theme_colors(&self) -> theme::ThemeColors {
        theme::from_primary(self.primary, self.accent)
    }

    /// Reset toggles / colors / darken to factory defaults (keeps `splash_seen`).
    pub fn reset_to_defaults(&mut self) {
        let splash = self.splash_seen;
        *self = Self::default();
        self.splash_seen = splash;
    }

    /// Normalize after load (clamp darken/colors; migrate legacy theme).
    pub fn normalize(mut self) -> Self {
        self.glass_darken = self.glass_darken.clamp(0.0, 0.95);
        self.theme = theme::normalize_id(&self.theme).to_string();
        for c in &mut self.primary {
            *c = c.clamp(0.0, 1.0);
        }
        if let Some(a) = self.accent.as_mut() {
            for c in a.iter_mut() {
                *c = c.clamp(0.0, 1.0);
            }
        }
        self
    }
}

/// Product config / data directory (same roots as Go `config.Dir()`).
///
/// Prefer **`SUZURI_CONFIG_DIR`** when set so a host-spawned chrome process
/// shares notes / prefs with the product binary.
pub fn product_config_dir() -> PathBuf {
    if let Ok(dir) = std::env::var(ENV_CONFIG_DIR) {
        let trimmed = dir.trim();
        if !trimmed.is_empty() {
            return PathBuf::from(trimmed);
        }
    }
    #[cfg(target_os = "macos")]
    {
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join("Library/Application Support/suzuri");
        }
    }
    #[cfg(target_os = "windows")]
    {
        if let Some(base) = std::env::var_os("LOCALAPPDATA") {
            return PathBuf::from(base).join("suzuri");
        }
    }
    if let Some(home) = std::env::var_os("HOME") {
        return PathBuf::from(home).join(".config/suzuri");
    }
    PathBuf::from("suzuri")
}

/// Default on-disk path for chrome prefs (`…/suzuri/chrome_prefs.json`).
pub fn chrome_prefs_path() -> PathBuf {
    product_config_dir().join(CHROME_PREFS_FILE)
}

/// Load prefs from `path`. Missing or invalid → [`ChromePrefs::default`].
pub fn load_chrome_prefs(path: &Path) -> ChromePrefs {
    let Ok(raw) = fs::read_to_string(path) else {
        return ChromePrefs::default();
    };
    parse_chrome_prefs_json(&raw).unwrap_or_default().normalize()
}

/// Write prefs atomically (temp + rename). Creates parent dirs as needed.
/// Does **not** touch product `config.json`.
pub fn save_chrome_prefs(path: &Path, prefs: &ChromePrefs) -> io::Result<()> {
    if let Some(parent) = path.parent() {
        if !parent.as_os_str().is_empty() {
            fs::create_dir_all(parent)?;
        }
    }
    let body = chrome_prefs_to_json(prefs);
    let tmp = path.with_extension("json.tmp");
    fs::write(&tmp, body.as_bytes())?;
    // On Windows, rename over existing may fail — replace explicitly.
    match fs::rename(&tmp, path) {
        Ok(()) => Ok(()),
        Err(_) => {
            let _ = fs::remove_file(path);
            match fs::rename(&tmp, path) {
                Ok(()) => Ok(()),
                Err(e) => {
                    let _ = fs::remove_file(&tmp);
                    Err(e)
                }
            }
        }
    }
}

/// Serialize prefs to stable JSON (pretty, trailing newline).
pub fn chrome_prefs_to_json(prefs: &ChromePrefs) -> String {
    let theme = theme::normalize_id(&prefs.theme);
    let primary = theme::to_hex(prefs.primary);
    let accent = match prefs.accent {
        Some(rgb) => format!("\"{}\"", theme::to_hex(rgb)),
        None => "\"auto\"".into(),
    };
    format!(
        "{{\n  \"rain\": {},\n  \"lens\": {},\n  \"glass_darken\": {},\n  \"theme\": \"{}\",\n  \"primary\": \"{}\",\n  \"accent\": {},\n  \"splash_seen\": {}\n}}\n",
        prefs.rain,
        prefs.lens,
        format_f32(prefs.glass_darken),
        theme,
        primary,
        accent,
        prefs.splash_seen,
    )
}

/// Parse a chrome_prefs JSON object. Unknown / missing keys fall back to defaults.
pub fn parse_chrome_prefs_json(raw: &str) -> Option<ChromePrefs> {
    // Reject empty / non-object payloads so callers can fall back to default.
    let trimmed = raw.trim();
    if trimmed.is_empty() || !trimmed.contains('{') {
        return None;
    }
    let d = ChromePrefs::default();
    let rain = extract_bool(trimmed, "rain").unwrap_or(d.rain);
    let lens = extract_bool(trimmed, "lens").unwrap_or(d.lens);
    let glass_darken = extract_f32(trimmed, "glass_darken").unwrap_or(d.glass_darken);
    let theme = extract_string(trimmed, "theme")
        .map(|s| theme::normalize_id(&s).to_string())
        .unwrap_or(d.theme.clone());
    let splash_seen = extract_bool(trimmed, "splash_seen").unwrap_or(d.splash_seen);

    // Primary: explicit `primary` hex, else legacy `accent` hex (was primary),
    // else named theme jade, else default.
    let has_primary_key = extract_string(trimmed, "primary").is_some();
    let primary = extract_string(trimmed, "primary")
        .and_then(|s| theme::parse_hex(&s))
        .or_else(|| {
            // Legacy: only `accent` hex meant the brand/primary color.
            if !has_primary_key {
                extract_string(trimmed, "accent").and_then(|s| {
                    if s.eq_ignore_ascii_case("auto") {
                        None
                    } else {
                        theme::parse_hex(&s)
                    }
                })
            } else {
                None
            }
        })
        .unwrap_or_else(|| theme::colors(&theme).jade);

    // Accent override: only when `primary` is present (new format).
    // `"auto"` / missing → None (derive). Hex → custom.
    // Legacy files (no primary key) always auto-derive accent.
    let accent = if has_primary_key {
        match extract_string(trimmed, "accent") {
            None => None,
            Some(s) if s.eq_ignore_ascii_case("auto") || s.is_empty() => None,
            Some(s) => theme::parse_hex(&s),
        }
    } else {
        None
    };

    Some(ChromePrefs {
        rain,
        lens,
        glass_darken,
        theme,
        primary,
        accent,
        splash_seen,
    })
}

fn format_f32(v: f32) -> String {
    // Compact but stable — avoid scientific notation for normal 0..1 values.
    let s = format!("{v:.4}");
    let s = s.trim_end_matches('0').trim_end_matches('.').to_string();
    if s.is_empty() || s == "-" {
        "0".into()
    } else {
        s
    }
}

fn extract_bool(s: &str, key: &str) -> Option<bool> {
    let pat = format!("\"{key}\"");
    let i = s.find(&pat)?;
    let after = &s[i + pat.len()..];
    let colon = after.find(':')?;
    let rest = after[colon + 1..].trim_start();
    if rest.starts_with("true") {
        Some(true)
    } else if rest.starts_with("false") {
        Some(false)
    } else {
        None
    }
}

fn extract_f32(s: &str, key: &str) -> Option<f32> {
    let pat = format!("\"{key}\"");
    let i = s.find(&pat)?;
    let after = &s[i + pat.len()..];
    let colon = after.find(':')?;
    let rest = after[colon + 1..].trim_start();
    let mut end = 0;
    for (idx, c) in rest.char_indices() {
        if c.is_ascii_digit() || c == '.' || c == '-' || c == '+' {
            end = idx + c.len_utf8();
        } else {
            break;
        }
    }
    if end == 0 {
        return None;
    }
    rest[..end].parse().ok()
}

fn extract_string(s: &str, key: &str) -> Option<String> {
    let pat = format!("\"{key}\"");
    let i = s.find(&pat)?;
    let after = &s[i + pat.len()..];
    let colon = after.find(':')?;
    let rest = after[colon + 1..].trim_start();
    if !rest.starts_with('"') {
        return None;
    }
    let body = &rest[1..];
    let mut out = String::new();
    let mut chars = body.chars();
    while let Some(c) = chars.next() {
        if c == '\\' {
            if let Some(n) = chars.next() {
                out.push(n);
            }
            continue;
        }
        if c == '"' {
            return Some(out);
        }
        out.push(c);
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_prefs_path(tag: &str) -> PathBuf {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or(0);
        let dir = std::env::temp_dir().join(format!("suzuri-chrome-prefs-{tag}-{nanos}"));
        let _ = fs::create_dir_all(&dir);
        dir.join(CHROME_PREFS_FILE)
    }

    #[test]
    fn roundtrip_temp_dir() {
        let path = temp_prefs_path("roundtrip");
        let prefs = ChromePrefs {
            rain: false,
            lens: true,
            glass_darken: 0.55,
            theme: "nord".into(),
            primary: theme::NORD.jade,
            accent: Some(theme::NORD.secondary),
            splash_seen: true,
        };
        save_chrome_prefs(&path, &prefs).expect("save");
        assert!(path.is_file(), "expected file at {}", path.display());
        // Sibling product config must not be created.
        if let Some(parent) = path.parent() {
            assert!(!parent.join("config.json").exists());
        }
        let loaded = load_chrome_prefs(&path);
        assert_eq!(loaded.rain, false);
        assert_eq!(loaded.lens, true);
        assert!((loaded.glass_darken - 0.55).abs() < 1e-4);
        assert_eq!(loaded.theme, "nord");
        assert!((loaded.primary[1] - theme::NORD.jade[1]).abs() < 0.02);
        assert!(loaded.accent_is_custom());
        assert!(loaded.splash_seen);
        let _ = fs::remove_file(&path);
        if let Some(parent) = path.parent() {
            let _ = fs::remove_dir_all(parent);
        }
    }

    #[test]
    fn missing_file_returns_defaults() {
        let path = temp_prefs_path("missing");
        let _ = fs::remove_file(&path);
        let loaded = load_chrome_prefs(&path);
        assert_eq!(loaded, ChromePrefs::default());
        assert_eq!(loaded.theme, theme::DEFAULT_THEME_ID);
        if let Some(parent) = path.parent() {
            let _ = fs::remove_dir_all(parent);
        }
    }

    #[test]
    fn parse_partial_json_fills_defaults() {
        let p = parse_chrome_prefs_json(r#"{ "rain": false }"#).expect("parse");
        assert!(!p.rain);
        assert!(p.lens);
        assert!((p.glass_darken - GLASS_DARKEN_DEFAULT).abs() < 1e-4);
        assert_eq!(p.theme, theme::DEFAULT_THEME_ID);
        assert!(!p.splash_seen);
    }

    #[test]
    fn splash_seen_roundtrip() {
        let path = temp_prefs_path("splash");
        let prefs = ChromePrefs {
            rain: true,
            lens: true,
            glass_darken: GLASS_DARKEN_DEFAULT,
            theme: theme::DEFAULT_THEME_ID.into(),
            primary: theme::DEFAULT_PRIMARY,
            accent: None,
            splash_seen: true,
        };
        save_chrome_prefs(&path, &prefs).expect("save");
        let loaded = load_chrome_prefs(&path);
        assert!(loaded.splash_seen);
        let raw = fs::read_to_string(&path).unwrap();
        assert!(raw.contains("\"splash_seen\": true"));
        // Missing key → default false
        let p = parse_chrome_prefs_json(r#"{ "rain": true }"#).unwrap();
        assert!(!p.splash_seen);
        let _ = fs::remove_dir_all(path.parent().unwrap());
    }

    #[test]
    fn parse_theme_aliases() {
        let p = parse_chrome_prefs_json(r#"{ "theme": "tokyo_night" }"#).unwrap();
        assert_eq!(p.theme, "tokyo-night");
        let p = parse_chrome_prefs_json(r#"{ "theme": "charmtone" }"#).unwrap();
        assert_eq!(p.theme, "charm");
    }

    #[test]
    fn save_does_not_clobber_config_json() {
        let path = temp_prefs_path("noclobber");
        let parent = path.parent().unwrap().to_path_buf();
        let config = parent.join("config.json");
        fs::write(&config, b"{\"theme\":\"keep-me\"}\n").unwrap();
        let prefs = ChromePrefs {
            rain: false,
            lens: false,
            glass_darken: 0.4,
            theme: "dracula".into(),
            primary: theme::DRACULA.jade,
            accent: None,
            splash_seen: false,
        };
        save_chrome_prefs(&path, &prefs).unwrap();
        let config_body = fs::read_to_string(&config).unwrap();
        assert!(config_body.contains("keep-me"));
        assert!(!config_body.contains("glass_darken"));
        let prefs_body = fs::read_to_string(&path).unwrap();
        assert!(prefs_body.contains("\"theme\": \"dracula\""));
        let _ = fs::remove_dir_all(parent);
    }

    #[test]
    fn json_roundtrip_text() {
        let prefs = ChromePrefs {
            rain: true,
            lens: false,
            glass_darken: 0.82,
            theme: "charm".into(),
            primary: theme::CHARM.jade,
            accent: None,
            splash_seen: true,
        };
        let raw = chrome_prefs_to_json(&prefs);
        assert!(raw.contains("\"rain\": true"));
        assert!(raw.contains("\"lens\": false"));
        assert!(raw.contains("\"theme\": \"charm\""));
        assert!(raw.contains("\"primary\":"));
        assert!(raw.contains("\"accent\": \"auto\""));
        assert!(raw.contains("\"splash_seen\": true"));
        let back = parse_chrome_prefs_json(&raw).unwrap();
        assert_eq!(back, prefs);
    }

    #[test]
    fn chrome_prefs_path_is_sibling_of_config() {
        let p = chrome_prefs_path();
        assert_eq!(
            p.file_name().and_then(|s| s.to_str()),
            Some(CHROME_PREFS_FILE)
        );
        assert_eq!(p.parent().map(|x| x.to_path_buf()), Some(product_config_dir()));
    }

    #[test]
    fn product_config_dir_honors_suzuri_config_dir_env() {
        // Process-global env — restore previous value so other tests stay clean.
        let prev = std::env::var_os(ENV_CONFIG_DIR);
        let custom = std::env::temp_dir().join(format!(
            "suzuri-config-dir-test-{}",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        ));
        std::env::set_var(ENV_CONFIG_DIR, &custom);
        let got = product_config_dir();
        assert_eq!(got, custom);
        assert_eq!(chrome_prefs_path(), custom.join(CHROME_PREFS_FILE));
        match prev {
            Some(v) => std::env::set_var(ENV_CONFIG_DIR, v),
            None => std::env::remove_var(ENV_CONFIG_DIR),
        }
    }

    #[test]
    fn primary_drives_palette_and_accent_derives() {
        let mut p = ChromePrefs::default();
        assert_eq!(p.theme_colors().jade, theme::DEFAULT_PRIMARY);
        assert!(!p.accent_is_custom());
        let derived = theme::derive_accent(theme::DEFAULT_PRIMARY);
        assert_eq!(p.effective_accent(), derived);
        assert_eq!(p.theme_colors().secondary, derived);

        p.set_primary([1.0, 0.0, 0.0]);
        assert!((p.theme_colors().jade[0] - 1.0).abs() < 1e-4);
        // Auto accent still tracks primary.
        assert!(!p.accent_is_custom());
        assert_eq!(p.effective_accent(), theme::derive_accent([1.0, 0.0, 0.0]));

        p.set_accent([0.0, 1.0, 0.0]);
        assert!(p.accent_is_custom());
        assert!((p.effective_accent()[1] - 1.0).abs() < 1e-4);
        p.nudge_accent_hue(60.0);
        assert!(p.accent_is_custom());
        p.clear_accent();
        assert!(!p.accent_is_custom());
    }

    #[test]
    fn legacy_theme_migrates_to_primary() {
        let p = parse_chrome_prefs_json(r#"{ "theme": "dracula" }"#).unwrap();
        assert_eq!(p.theme, "dracula");
        let jade = theme::DRACULA.jade;
        assert!((p.primary[0] - jade[0]).abs() < 0.02);
        assert!(!p.accent_is_custom());
    }

    #[test]
    fn legacy_accent_field_becomes_primary() {
        // Pre-split format stored brand color under "accent".
        // Use r## so #hex does not end the raw string early.
        let p = parse_chrome_prefs_json(r##"{ "accent": "#ff0000" }"##).unwrap();
        assert!((p.primary[0] - 1.0).abs() < 0.02);
        assert!(!p.accent_is_custom());
    }

    #[test]
    fn new_format_primary_and_custom_accent() {
        let p = parse_chrome_prefs_json(
            r##"{ "primary": "#00e676", "accent": "#ff79c6" }"##,
        )
        .unwrap();
        assert!((p.primary[1] - theme::DEFAULT_PRIMARY[1]).abs() < 0.02);
        assert!(p.accent_is_custom());
        let a = p.effective_accent();
        assert!(a[0] > 0.9 && a[2] > 0.7);
    }

    #[test]
    fn reset_keeps_splash_seen() {
        let mut p = ChromePrefs {
            rain: false,
            lens: false,
            glass_darken: 0.1,
            theme: "nord".into(),
            primary: [1.0, 0.0, 0.0],
            accent: Some([0.0, 1.0, 0.0]),
            splash_seen: true,
        };
        p.reset_to_defaults();
        assert!(p.rain && p.lens);
        assert!(p.splash_seen);
        assert_eq!(p.primary, theme::DEFAULT_PRIMARY);
        assert!(!p.accent_is_custom());
    }

    #[test]
    fn normalize_unknown_theme() {
        let p = ChromePrefs {
            rain: true,
            lens: true,
            glass_darken: 1.5,
            theme: "nope".into(),
            primary: theme::DEFAULT_PRIMARY,
            accent: None,
            splash_seen: false,
        }
        .normalize();
        assert!((p.glass_darken - 0.95).abs() < 1e-5);
        assert_eq!(p.theme, "inkstone");
    }
}
