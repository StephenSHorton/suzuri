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

/// User-tunable chrome UI prefs (rain / lens / glass darken / accent / splash).
#[derive(Clone, Debug, PartialEq)]
pub struct ChromePrefs {
    /// Canvas UI glyph rain under glass.
    pub rain: bool,
    /// Mouse glass lens / magnifier.
    pub lens: bool,
    /// Shared glass face darken 0..1 (panes / chips / modal).
    pub glass_darken: f32,
    /// Legacy named theme id (migrated → accent on load if no accent saved).
    pub theme: String,
    /// User accent RGB (primary / jade). Drives [`Self::theme_colors`].
    pub accent: [f32; 3],
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
            accent: theme::DEFAULT_ACCENT,
            splash_seen: false,
        }
    }
}

impl ChromePrefs {
    pub fn nudge_darken(&mut self, delta: f32) {
        self.glass_darken = (self.glass_darken + delta).clamp(0.0, 0.95);
    }

    /// Rotate accent hue by `delta_deg` degrees.
    pub fn nudge_accent_hue(&mut self, delta_deg: f32) {
        self.accent = theme::rotate_hue(self.accent, delta_deg);
    }

    pub fn set_accent(&mut self, rgb: [f32; 3]) {
        self.accent = [
            rgb[0].clamp(0.0, 1.0),
            rgb[1].clamp(0.0, 1.0),
            rgb[2].clamp(0.0, 1.0),
        ];
    }

    /// Active paint palette from the user accent (not legacy theme names).
    pub fn theme_colors(&self) -> theme::ThemeColors {
        theme::from_accent(self.accent)
    }

    /// Reset toggles / accent / darken to factory defaults (keeps `splash_seen`).
    pub fn reset_to_defaults(&mut self) {
        let splash = self.splash_seen;
        *self = Self::default();
        self.splash_seen = splash;
    }

    /// Normalize after load (clamp darken/accent; migrate legacy `theme` → accent).
    pub fn normalize(mut self) -> Self {
        self.glass_darken = self.glass_darken.clamp(0.0, 0.95);
        self.theme = theme::normalize_id(&self.theme).to_string();
        for c in &mut self.accent {
            *c = c.clamp(0.0, 1.0);
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
    let accent = theme::to_hex(prefs.accent);
    format!(
        "{{\n  \"rain\": {},\n  \"lens\": {},\n  \"glass_darken\": {},\n  \"theme\": \"{}\",\n  \"accent\": \"{}\",\n  \"splash_seen\": {}\n}}\n",
        prefs.rain,
        prefs.lens,
        format_f32(prefs.glass_darken),
        theme,
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
    // Prefer explicit accent; else migrate legacy theme id → that palette's jade.
    let accent = extract_string(trimmed, "accent")
        .and_then(|s| theme::parse_hex(&s))
        .unwrap_or_else(|| {
            // No accent field: use named theme's jade as the accent seed.
            theme::colors(&theme).jade
        });
    Some(ChromePrefs {
        rain,
        lens,
        glass_darken,
        theme,
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
            accent: theme::NORD.jade,
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
        assert!((loaded.accent[1] - theme::NORD.jade[1]).abs() < 0.02);
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
            accent: theme::DEFAULT_ACCENT,
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
            accent: theme::DRACULA.jade,
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
            accent: theme::CHARM.jade,
            splash_seen: true,
        };
        let raw = chrome_prefs_to_json(&prefs);
        assert!(raw.contains("\"rain\": true"));
        assert!(raw.contains("\"lens\": false"));
        assert!(raw.contains("\"theme\": \"charm\""));
        assert!(raw.contains("\"accent\":"));
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
    fn accent_drives_palette_and_hue_nudge() {
        let mut p = ChromePrefs::default();
        assert_eq!(p.theme_colors().jade, theme::DEFAULT_ACCENT);
        p.set_accent([1.0, 0.0, 0.0]);
        assert!((p.theme_colors().jade[0] - 1.0).abs() < 1e-4);
        p.nudge_accent_hue(60.0);
        // Hue rotate should change green channel away from pure red.
        assert!(p.accent[1] > 0.1);
    }

    #[test]
    fn legacy_theme_migrates_to_accent() {
        let p = parse_chrome_prefs_json(r#"{ "theme": "dracula" }"#).unwrap();
        assert_eq!(p.theme, "dracula");
        let jade = theme::DRACULA.jade;
        assert!((p.accent[0] - jade[0]).abs() < 0.02);
    }

    #[test]
    fn reset_keeps_splash_seen() {
        let mut p = ChromePrefs {
            rain: false,
            lens: false,
            glass_darken: 0.1,
            theme: "nord".into(),
            accent: [1.0, 0.0, 0.0],
            splash_seen: true,
        };
        p.reset_to_defaults();
        assert!(p.rain && p.lens);
        assert!(p.splash_seen);
        assert_eq!(p.accent, theme::DEFAULT_ACCENT);
    }

    #[test]
    fn normalize_unknown_theme() {
        let p = ChromePrefs {
            rain: true,
            lens: true,
            glass_darken: 1.5,
            theme: "nope".into(),
            accent: theme::DEFAULT_ACCENT,
            splash_seen: false,
        }
        .normalize();
        assert!((p.glass_darken - 0.95).abs() < 1e-5);
        assert_eq!(p.theme, "inkstone");
    }
}
