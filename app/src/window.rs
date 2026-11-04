//! New window and the Windows single instance (Go suzuri's new_window.go
//! and single_instance_windows.go).
//!
//! A new window is a second suzuri process, as in Go: each process has
//! one window, one frame socket and its own panes. On Windows a plain second
//! launch focuses the running window instead of opening another
//! (tauri-plugin-single-instance); a new-window launch sets RSZ_NEW_WINDOW
//! and skips that check, and RSZ_ALLOW_MULTI=1 skips it for side-by-side
//! testing (Go's SUZURI_ALLOW_MULTI).

/// The env var a new-window launch carries.
pub const NEW_WINDOW_ENV: &str = "RSZ_NEW_WINDOW";

/// Whether this launch should go through the single-instance check.
#[cfg_attr(not(windows), allow(dead_code))]
pub fn wants_single_instance(new_window: Option<&str>, allow_multi: Option<&str>) -> bool {
    cfg!(windows) && new_window.is_none_or(|v| v.is_empty()) && allow_multi != Some("1")
}

/// Starts another suzuri process, which opens its own window.
#[tauri::command]
pub fn new_window() -> Result<(), String> {
    let exe = std::env::current_exe().map_err(|e| e.to_string())?;
    let exe = std::fs::canonicalize(&exe).unwrap_or(exe);
    let mut cmd = std::process::Command::new(&exe);
    cmd.env(NEW_WINDOW_ENV, "1");
    // The child is independent, never a pane: don't hand it the panes' env.
    for k in ["RSZ_CMD", "RSZ_PANES", "RSZ_BENCH", "RSZ_BENCH_SECS", "RSZ_BENCH_PROBE"] {
        cmd.env_remove(k);
    }
    if let Some(dir) = exe.parent() {
        cmd.current_dir(dir);
    }
    let mut child = cmd.spawn().map_err(|e| e.to_string())?;
    // Reap it so Unix leaves no zombie; the window lives on its own.
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    Ok(())
}

/// Brings the main window back up for a second launch (Windows).
#[cfg(windows)]
pub fn focus_main<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    use tauri::Manager;
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn single_instance_only_for_plain_windows_launches() {
        assert_eq!(wants_single_instance(None, None), cfg!(windows));
        assert!(!wants_single_instance(Some("1"), None));
        assert!(!wants_single_instance(None, Some("1")));
    }
}
