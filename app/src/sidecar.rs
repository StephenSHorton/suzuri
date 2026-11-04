//! Where Go suzuri keeps its files, and where its helper binaries live.
//!
//! suzuri shares Go suzuri's config dir (bridge.json, notes, transfer
//! identity). Its one helper, suzuri-transfer, ships in the bundle (#330).

use std::path::PathBuf;
use std::process::Command;

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

/// The user's home directory (HOME, or USERPROFILE on Windows).
pub fn home_dir() -> Option<PathBuf> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .filter(|v| !v.is_empty())
        .map(PathBuf::from)
}

/// `name` with `.exe` on Windows.
pub fn exe_name(name: &str) -> String {
    if cfg!(windows) { format!("{name}.exe") } else { name.to_string() }
}

/// Directories that may hold Go suzuri's binaries, in lookup order: next to
/// this executable (a bundle that ships them), then an installed Go suzuri.
pub fn install_dirs() -> Vec<PathBuf> {
    let mut dirs = Vec::new();
    if let Ok(exe) = std::env::current_exe() {
        let exe = exe.canonicalize().unwrap_or(exe);
        if let Some(d) = exe.parent() {
            dirs.push(d.to_path_buf());
        }
    }
    #[cfg(target_os = "macos")]
    {
        dirs.push(PathBuf::from("/Applications/suzuri.app/Contents/MacOS"));
        if let Some(h) = home_dir() {
            dirs.push(h.join("Applications/suzuri.app/Contents/MacOS"));
        }
    }
    #[cfg(windows)]
    if let Some(local) = std::env::var_os("LOCALAPPDATA") {
        dirs.push(PathBuf::from(local).join("Programs").join("suzuri"));
    }
    dirs
}

/// Find a helper binary: `env` override, then [`install_dirs`], then PATH.
/// This app's own executable never counts: it is named `suzuri` too, and
/// does not serve Go's helper subcommands.
pub fn find(env: &str, names: &[&str]) -> Option<PathBuf> {
    if let Ok(p) = std::env::var(env) {
        let p = PathBuf::from(p.trim());
        if p.is_file() {
            return Some(p);
        }
    }
    let me = std::env::current_exe().ok().map(|e| e.canonicalize().unwrap_or(e));
    let usable = |c: &PathBuf| c.is_file() && me.as_ref() != Some(&c.canonicalize().unwrap_or_else(|_| c.clone()));
    let path = std::env::var_os("PATH");
    let dirs = install_dirs().into_iter().chain(path.iter().flat_map(std::env::split_paths));
    for dir in dirs {
        for n in names {
            let c = dir.join(exe_name(n));
            if usable(&c) {
                return Some(c);
            }
        }
    }
    None
}

/// Console helpers must not open a window when the parent is a GUI app.
pub fn no_window(cmd: &mut Command) -> &mut Command {
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(0x0800_0000); // CREATE_NO_WINDOW
    }
    cmd
}
