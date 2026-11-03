//! Settings: one JSON file, the same keys as Go suzuri's config.json
//! (internal/config/config.go), so a user's settings carry over.
//!
//! The file lives in rust-suzuri's own folder under the OS config dir
//! (Windows %LOCALAPPDATA%\rust-suzuri, macOS ~/Library/Application
//! Support/rust-suzuri, Linux ~/.config/rust-suzuri). On first run it is
//! copied from Go suzuri's config.json when that exists. Keys this app does
//! not use yet (intro, ambient, glass, window...) are kept as they are, so
//! nothing is lost on save. RSZ_CONFIG_DIR overrides the folder (tests,
//! screenshots).

use std::path::{Path, PathBuf};

use serde_json::{Map, Value, json};

pub const FONT_SIZE_DEFAULT: i64 = 14;
pub const FONT_SIZE_MIN: i64 = 10;
pub const FONT_SIZE_MAX: i64 = 36;
pub const FONT_FACE_DEFAULT: &str = "GohuFont uni14 Nerd Font Mono";

/// Go suzuri's theme ids (config.ThemeIDs). Drawing them is #284's part.
pub const THEMES: &[&str] = &[
    "inkstone",
    "charmtone",
    "high_contrast",
    "nord",
    "dracula",
    "tokyo_night",
    "catppuccin",
    "gruvbox",
    "one_dark",
    "solarized",
    "rose_pine",
    "kanagawa",
    "monokai",
    "forest",
    "ocean",
    "amber",
];
pub const CURSORS: &[&str] = &["block", "underline", "bar"];
pub const ANSI_MAPS: &[&str] = &["none", "soft", "full"];

/// The folder settings live in, named `name` under the OS config dir.
fn dir_named(name: &str) -> PathBuf {
    if let Some(d) = std::env::var_os("RSZ_CONFIG_DIR").filter(|d| !d.is_empty()) {
        let d = PathBuf::from(d);
        return if name == "suzuri" {
            d.join("go-suzuri")
        } else {
            d
        };
    }
    // Same order as Go suzuri's config.Dir.
    if let Some(base) = std::env::var_os("LOCALAPPDATA").filter(|d| !d.is_empty()) {
        return PathBuf::from(base).join(name);
    }
    let home = std::env::var_os("HOME").map(PathBuf::from);
    if cfg!(target_os = "macos") {
        if let Some(h) = home {
            return h.join("Library/Application Support").join(name);
        }
    } else if let Some(x) = std::env::var_os("XDG_CONFIG_HOME").filter(|d| !d.is_empty()) {
        return PathBuf::from(x).join(name);
    } else if let Some(h) = home {
        return h.join(".config").join(name);
    }
    std::env::temp_dir().join(name)
}

pub fn path() -> PathBuf {
    dir_named("rust-suzuri").join("config.json")
}

/// Go suzuri's config.json, imported on first run.
pub fn go_path() -> PathBuf {
    dir_named("suzuri").join("config.json")
}

pub fn default_profiles() -> Value {
    if cfg!(windows) {
        json!([
            {"name": "Default"},
            {"name": "PowerShell", "shell": "powershell.exe -NoLogo -NoProfile"},
            {"name": "Cmd", "shell": "cmd.exe"},
        ])
    } else {
        json!([
            {"name": "Default"},
            {"name": "Zsh", "shell": "/bin/zsh"},
            {"name": "Bash", "shell": "/bin/bash"},
        ])
    }
}

/// Fill defaults and clamp known keys, as Go's config.Normalize does.
/// Unknown keys pass through untouched.
pub fn normalize(v: Value) -> Value {
    let mut m = match v {
        Value::Object(m) => m,
        _ => Map::new(),
    };
    let s = |m: &Map<String, Value>, k: &str| {
        m.get(k)
            .and_then(Value::as_str)
            .map(|s| s.trim().to_lowercase())
    };

    let face = m
        .get("font_face")
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|f| !f.is_empty());
    let face = face.unwrap_or(FONT_FACE_DEFAULT).to_string();
    m.insert("font_face".into(), face.into());

    let size = m
        .get("font_size_px")
        .and_then(Value::as_i64)
        .unwrap_or(FONT_SIZE_DEFAULT);
    m.insert(
        "font_size_px".into(),
        size.clamp(FONT_SIZE_MIN, FONT_SIZE_MAX).into(),
    );

    let cursor = s(&m, "cursor")
        .filter(|c| CURSORS.contains(&c.as_str()))
        .unwrap_or_else(|| "block".into());
    m.insert("cursor".into(), cursor.into());

    // Go falls back to high_contrast for an unknown theme; so do we.
    let theme = s(&m, "theme")
        .filter(|t| THEMES.contains(&t.as_str()))
        .unwrap_or_else(|| "high_contrast".into());
    m.insert("theme".into(), theme.into());

    let ansi = match s(&m, "shell_ansi_map").as_deref() {
        Some(a) if ANSI_MAPS.contains(&a) => a.to_string(),
        _ => "soft".into(),
    };
    m.insert("shell_ansi_map".into(), ansi.into());

    // Profiles: unique, non-empty names (case-insensitive), never empty.
    let mut seen = std::collections::HashSet::new();
    let mut profiles: Vec<Value> = m
        .get("profiles")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|p| {
            let mut p = p.as_object()?.clone();
            let name = p.get("name").and_then(Value::as_str)?.trim().to_string();
            if name.is_empty() || !seen.insert(name.to_lowercase()) {
                return None;
            }
            p.insert("name".into(), name.into());
            Some(Value::Object(p))
        })
        .collect();
    if profiles.is_empty() {
        profiles = default_profiles().as_array().cloned().unwrap_or_default();
    }
    let names: Vec<String> = profiles
        .iter()
        .filter_map(|p| p["name"].as_str().map(str::to_string))
        .collect();
    let active = m
        .get("active_profile")
        .and_then(Value::as_str)
        .unwrap_or("");
    let active = names
        .iter()
        .find(|n| n.eq_ignore_ascii_case(active))
        .unwrap_or(&names[0])
        .clone();
    m.insert("profiles".into(), Value::Array(profiles));
    m.insert("active_profile".into(), active.into());
    Value::Object(m)
}

/// Read the settings, importing Go suzuri's on first run. A missing or
/// broken file gives the defaults (a broken one is left on disk as is).
pub fn load() -> Value {
    load_from(&path(), &go_path())
}

pub fn load_from(path: &Path, go: &Path) -> Value {
    let read = |p: &Path| {
        std::fs::read(p)
            .ok()
            .and_then(|b| serde_json::from_slice::<Value>(&b).ok())
            .filter(Value::is_object)
    };
    if let Some(v) = read(path) {
        return normalize(v);
    }
    if !path.exists()
        && let Some(v) = read(go)
    {
        let v = normalize(v);
        let _ = save_to(path, &v);
        return v;
    }
    normalize(Value::Object(Map::new()))
}

/// Merge `patch`'s keys into the file, normalize and save. Returns what was
/// saved.
pub fn update(patch: Value) -> std::io::Result<Value> {
    update_at(&path(), &go_path(), patch)
}

pub fn update_at(path: &Path, go: &Path, patch: Value) -> std::io::Result<Value> {
    let mut cur = match load_from(path, go) {
        Value::Object(m) => m,
        _ => Map::new(),
    };
    if let Value::Object(p) = patch {
        for (k, v) in p {
            cur.insert(k, v);
        }
    }
    let v = normalize(Value::Object(cur));
    save_to(path, &v)?;
    Ok(v)
}

/// Atomic write: temp file, then rename over the old one.
fn save_to(path: &Path, v: &Value) -> std::io::Result<()> {
    if let Some(d) = path.parent() {
        std::fs::create_dir_all(d)?;
    }
    let mut text = serde_json::to_string_pretty(v).map_err(std::io::Error::other)?;
    text.push('\n');
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, text)?;
    if std::fs::rename(&tmp, path).is_err() {
        let _ = std::fs::remove_file(path);
        std::fs::rename(&tmp, path)?;
    }
    Ok(())
}

/// A profile's launch: argv (None = the user's shell) and cwd.
pub fn profile_launch(cfg: &Value, name: Option<&str>) -> (Option<Vec<String>>, Option<String>) {
    let want = name
        .or_else(|| cfg["active_profile"].as_str())
        .unwrap_or("");
    let p = cfg["profiles"].as_array().and_then(|ps| {
        ps.iter().find(|p| {
            p["name"]
                .as_str()
                .is_some_and(|n| n.eq_ignore_ascii_case(want))
        })
    });
    let Some(p) = p else { return (None, None) };
    let shell = p["shell"]
        .as_str()
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(crate::split_args);
    let cwd = p["cwd"]
        .as_str()
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(expand_home);
    (shell, cwd)
}

fn expand_home(p: &str) -> String {
    if let Some(rest) = p.strip_prefix('~') {
        let home = std::env::var("HOME")
            .or_else(|_| std::env::var("USERPROFILE"))
            .unwrap_or_default();
        return format!("{home}{rest}");
    }
    p.to_string()
}

/// Settings cursor name to the engine's shape code.
pub fn cursor_code(cfg: &Value) -> u8 {
    match cfg["cursor"].as_str() {
        Some("underline") => 2,
        Some("bar") => 3,
        _ => 1,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("rsz-config-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn defaults_when_nothing_exists() {
        let d = tmp("defaults");
        let v = load_from(&d.join("rs/config.json"), &d.join("go/config.json"));
        assert_eq!(v["font_size_px"], 14);
        assert_eq!(v["cursor"], "block");
        assert_eq!(v["theme"], "high_contrast");
        assert_eq!(v["active_profile"], "Default");
        assert!(v["profiles"].as_array().unwrap().len() >= 3);
    }

    #[test]
    fn imports_go_suzuri_once_and_keeps_unknown_keys() {
        let d = tmp("import");
        let go = d.join("go/config.json");
        std::fs::create_dir_all(go.parent().unwrap()).unwrap();
        std::fs::write(
            &go,
            r#"{"font_size_px": 16, "cursor": "bar", "theme": "Nord", "intro": "matrix",
                "glass_blur": 48, "profiles": [{"name": "Default"}, {"name": "Work", "cwd": "/tmp"}],
                "active_profile": "work", "window": {"x": 0, "y": 65, "width": 1728, "height": 1052}}"#,
        )
        .unwrap();
        let rs = d.join("rs/config.json");
        let v = load_from(&rs, &go);
        assert_eq!(v["font_size_px"], 16);
        assert_eq!(v["cursor"], "bar");
        assert_eq!(v["theme"], "nord");
        assert_eq!(v["active_profile"], "Work");
        assert_eq!(v["intro"], "matrix");
        assert_eq!(v["window"]["width"], 1728);
        assert!(rs.exists(), "the import is saved as our own file");
        // Later changes to Go's file don't leak in: ours exists now.
        std::fs::write(&go, r#"{"font_size_px": 20}"#).unwrap();
        assert_eq!(load_from(&rs, &go)["font_size_px"], 16);
    }

    #[test]
    fn update_merges_clamps_and_persists() {
        let d = tmp("update");
        let (rs, go) = (d.join("rs/config.json"), d.join("go/config.json"));
        let v = update_at(
            &rs,
            &go,
            json!({"font_size_px": 99, "theme": "nope", "extra": 1}),
        )
        .unwrap();
        assert_eq!(v["font_size_px"], 36);
        assert_eq!(v["theme"], "high_contrast");
        assert_eq!(v["extra"], 1);
        let v = update_at(&rs, &go, json!({"cursor": "underline"})).unwrap();
        assert_eq!(v["extra"], 1, "earlier keys kept");
        assert_eq!(load_from(&rs, &go)["cursor"], "underline");
    }

    #[test]
    fn profiles_are_unique_and_launch() {
        let v = normalize(json!({
            "profiles": [{"name": " Dev ", "shell": "\"/bin/my shell\" -l", "cwd": "~/src"}, {"name": "dev"}, {"name": ""}],
            "active_profile": "missing"
        }));
        let ps = v["profiles"].as_array().unwrap();
        assert_eq!(ps.len(), 1);
        assert_eq!(v["active_profile"], "Dev");
        let (argv, cwd) = profile_launch(&v, None);
        assert_eq!(argv.unwrap(), vec!["/bin/my shell", "-l"]);
        assert!(cwd.unwrap().ends_with("/src"));
        assert_eq!(profile_launch(&v, Some("nothing")), (None, None));
    }

    #[test]
    fn broken_file_gives_defaults_and_is_not_replaced_by_import() {
        let d = tmp("broken");
        let (rs, go) = (d.join("rs/config.json"), d.join("go/config.json"));
        std::fs::create_dir_all(rs.parent().unwrap()).unwrap();
        std::fs::write(&rs, "{ not json").unwrap();
        std::fs::create_dir_all(go.parent().unwrap()).unwrap();
        std::fs::write(&go, r#"{"font_size_px": 20}"#).unwrap();
        assert_eq!(load_from(&rs, &go)["font_size_px"], 14);
        assert_eq!(std::fs::read_to_string(&rs).unwrap(), "{ not json");
    }
}
