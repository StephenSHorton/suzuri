//! Chrome prefs persistence — `chrome_prefs.json` next to product `config.json`.
//!
//! Product `config.json` (font, theme, profiles, …) is owned by the Go host
//! (`internal/config`). This module **never** reads or writes that file.
//!
//! Paths match product layout:
//! - macOS: `~/Library/Application Support/suzuri/`
//! - Windows: `%LOCALAPPDATA%\suzuri\`
//! - Linux / other: `~/.config/suzuri/`

use std::fs;
use std::io;
use std::path::{Path, PathBuf};

/// Filename under the product config directory (sibling of `config.json`).
pub const CHROME_PREFS_FILE: &str = "chrome_prefs.json";

/// Default glass face darken (matches product look).
pub const GLASS_DARKEN_DEFAULT: f32 = 0.82;

/// User-tunable chrome UI prefs (rain / lens / glass darken).
#[derive(Clone, Debug, PartialEq)]
pub struct ChromePrefs {
    /// Canvas UI glyph rain under glass.
    pub rain: bool,
    /// Mouse glass lens / magnifier.
    pub lens: bool,
    /// Shared glass face darken 0..1 (panes / chips / modal).
    pub glass_darken: f32,
}

impl Default for ChromePrefs {
    fn default() -> Self {
        Self {
            rain: true,
            lens: true,
            glass_darken: GLASS_DARKEN_DEFAULT,
        }
    }
}

impl ChromePrefs {
    pub fn nudge_darken(&mut self, delta: f32) {
        self.glass_darken = (self.glass_darken + delta).clamp(0.0, 0.95);
    }

    /// Normalize after load (clamp darken, keep bools as-is).
    pub fn normalize(mut self) -> Self {
        self.glass_darken = self.glass_darken.clamp(0.0, 0.95);
        self
    }
}

/// Product config / data directory (same roots as Go `config.Dir()`).
pub fn product_config_dir() -> PathBuf {
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
    format!(
        "{{\n  \"rain\": {},\n  \"lens\": {},\n  \"glass_darken\": {}\n}}\n",
        prefs.rain,
        prefs.lens,
        format_f32(prefs.glass_darken)
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
    Some(ChromePrefs {
        rain,
        lens,
        glass_darken,
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
        };
        save_chrome_prefs(&path, &prefs).unwrap();
        let config_body = fs::read_to_string(&config).unwrap();
        assert!(config_body.contains("keep-me"));
        assert!(!config_body.contains("glass_darken"));
        let _ = fs::remove_dir_all(parent);
    }

    #[test]
    fn json_roundtrip_text() {
        let prefs = ChromePrefs {
            rain: true,
            lens: false,
            glass_darken: 0.82,
        };
        let raw = chrome_prefs_to_json(&prefs);
        assert!(raw.contains("\"rain\": true"));
        assert!(raw.contains("\"lens\": false"));
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
}
