//! Where Go suzuri keeps its files.
//!
//! suzuri shares Go suzuri's config dir (bridge.json, notes).

use std::path::PathBuf;

/// Go suzuri's `config.Dir()`: `%LOCALAPPDATA%\suzuri` when LOCALAPPDATA is
/// set (Windows), else the OS user config dir + `suzuri`
/// (macOS: `~/Library/Application Support/suzuri`).
pub fn config_dir() -> PathBuf {
    if let Some(local) = std::env::var_os("LOCALAPPDATA").filter(|v| !v.is_empty()) {
        return PathBuf::from(local).join("suzuri");
    }
    #[cfg(target_os = "macos")]
    if let Some(home) = std::env::var_os("HOME") {
        return PathBuf::from(home).join("Library/Application Support/suzuri");
    }
    #[cfg(not(any(target_os = "macos", target_os = "windows")))]
    {
        if let Some(xdg) = std::env::var_os("XDG_CONFIG_HOME").filter(|v| !v.is_empty()) {
            return PathBuf::from(xdg).join("suzuri");
        }
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join(".config/suzuri");
        }
    }
    std::env::temp_dir().join("suzuri")
}
