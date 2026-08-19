//! Spawn a second OS window as a new process (product `internal/ui/new_window.go`).
//!
//! Not multi-window in one event loop — `exec` self, detach, soft-fail.

use std::path::{Path, PathBuf};
use std::process::Command;

use crate::config_store::ENV_CONFIG_DIR;

/// Resolve the path of this binary for re-exec.
///
/// Uses `current_exe`, then `canonicalize` when possible (symlink-aware).
pub fn resolve_self_exe() -> Result<PathBuf, String> {
    let self_exe = std::env::current_exe().map_err(|e| format!("executable: {e}"))?;
    Ok(canonicalize_exe(&self_exe))
}

/// Prefer a fully resolved path; fall back to the raw path if canonicalize fails.
pub fn canonicalize_exe(path: &Path) -> PathBuf {
    std::fs::canonicalize(path).unwrap_or_else(|_| path.to_path_buf())
}

/// Start another suzuri-chrome process so the user gets a second OS window.
///
/// Failures are returned to the caller (toast / eprintln) — never panic.
/// Child is reaped on a background thread so Unix does not leave a zombie.
pub fn spawn_new_window() -> Result<(), String> {
    let exe = resolve_self_exe()?;
    let mut cmd = Command::new(&exe);
    let dir = crate::session::initial_cwd();
    if !dir.is_empty() {
        cmd.current_dir(dir);
    }
    // Pass through host config dir when set so the child shares notes/prefs.
    if let Ok(cfg) = std::env::var(ENV_CONFIG_DIR) {
        let t = cfg.trim();
        if !t.is_empty() {
            cmd.env(ENV_CONFIG_DIR, t);
        }
    }
    let mut child = cmd
        .spawn()
        .map_err(|e| format!("spawn new window ({exe}): {e}", exe = exe.display()))?;
    let pid = child.id();
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    eprintln!(
        "suzuri-chrome: opened new window pid={pid} path={}",
        exe.display()
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn canonicalize_exe_keeps_existing_path() {
        // Cargo test binary always exists; canonicalize should succeed and stay absolute.
        let exe = std::env::current_exe().expect("current_exe");
        let resolved = canonicalize_exe(&exe);
        assert!(resolved.is_absolute());
        assert!(resolved.exists());
    }

    #[test]
    fn canonicalize_exe_fallback_on_missing() {
        let missing = PathBuf::from("/no/such/suzuri-chrome-bin-for-test");
        let resolved = canonicalize_exe(&missing);
        assert_eq!(resolved, missing);
    }

    #[test]
    fn resolve_self_exe_ok() {
        let p = resolve_self_exe().expect("resolve");
        assert!(p.exists());
    }
}
