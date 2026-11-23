//! Caffeine: keep the machine awake while it's on (Go suzuri's
//! internal/caffeine). The hold, timers and label live in the `rsz-caffeine`
//! crate (crates/caffeine); this is Suzuri's Tauri side: its switch, the
//! `caffeine` event and the four commands.
//!
//! The switch is the shared one (`rsz_caffeine::shared_switch_path()`), the
//! same file Hearth and any other program using the library follow: turning
//! it on or off in one turns it on or off in all of them within half a
//! second, and the ☕ follows (the `caffeine` event fires on others' changes
//! too).
//!
//! At launch (macOS and Windows): if the switch file does not exist yet, no
//! program has ever set it, so Suzuri turns caffeine on indefinitely as it
//! always has (Go's window_darwin.go / window_windows.go). If the file exists,
//! Suzuri follows it: on stays on (and Suzuri starts holding), off stays off,
//! a timer keeps its end time. So turning caffeine off in Suzuri or Hearth
//! is remembered across restarts instead of being undone by the next launch.

use std::path::Path;
use std::sync::Arc;

use tauri::{AppHandle, Emitter, State};

pub use rsz_caffeine::{Caffeine, Status};

/// The name shows in `pmset -g assertions`.
const NAME: &str = "suzuri caffeine";

/// Suzuri's switch: the shared one on macOS and Windows. Solo where there is
/// no home folder to keep the file in, or no OS hold (other systems would
/// only report errors following a shared "on").
pub fn new() -> Arc<Caffeine> {
    match switch_path() {
        Some(path) => Caffeine::shared(NAME, path),
        None => Caffeine::solo(NAME),
    }
}

fn switch_path() -> Option<std::path::PathBuf> {
    if cfg!(any(target_os = "macos", windows)) { rsz_caffeine::shared_switch_path() } else { None }
}

/// What launch does (see the module notes): on indefinitely when the switch
/// has never been set (solo, or no shared file yet), else follow it.
#[cfg_attr(not(any(target_os = "macos", windows)), allow(dead_code))]
fn launch(c: &Arc<Caffeine>, switch: Option<&Path>) -> Status {
    match switch {
        Some(p) if c.is_shared() && p.exists() => c.status(),
        _ => c.activate(None),
    }
}

/// Wires the change events to the UI (`caffeine` event with a Status, also
/// for changes made by other programs on the shared switch) and does the
/// launch step. Other platforms have no hold, so they start off rather than
/// toast an error.
pub fn install(app: &AppHandle, c: &Arc<Caffeine>) {
    let app = app.clone();
    c.on_change(move |s| {
        let _ = app.emit("caffeine", s);
    });
    #[cfg(any(target_os = "macos", windows))]
    launch(c, switch_path().as_deref());
}

/// minutes: Some(n) for n minutes, None (or 0) for indefinitely.
#[tauri::command]
pub fn caffeine_on(c: State<'_, Arc<Caffeine>>, minutes: Option<u32>) -> Status {
    c.activate(minutes)
}

#[tauri::command]
pub fn caffeine_off(c: State<'_, Arc<Caffeine>>) -> Status {
    c.deactivate()
}

#[tauri::command]
pub fn caffeine_toggle(c: State<'_, Arc<Caffeine>>) -> Status {
    c.toggle()
}

#[tauri::command]
pub fn caffeine_status(c: State<'_, Arc<Caffeine>>) -> Status {
    c.status()
}

#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(any(target_os = "macos", windows))]
    use std::time::{Duration, Instant};

    #[cfg(any(target_os = "macos", windows))]
    fn temp_switch(tag: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("suzuri-caffeine-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        dir.join("switch")
    }

    #[cfg(any(target_os = "macos", windows))]
    fn wait_for(c: &Arc<Caffeine>, active: bool) -> Duration {
        let start = Instant::now();
        while c.status().active != active {
            assert!(start.elapsed() < Duration::from_secs(3), "switch did not follow within 3s");
            std::thread::sleep(Duration::from_millis(20));
        }
        start.elapsed()
    }

    #[cfg(any(target_os = "macos", windows))]
    /// The ☕ click is caffeine_toggle: launch turns it on indefinitely, a
    /// click turns it off and another back on.
    #[test]
    fn launch_is_on_indefinitely_and_a_click_turns_it_off() {
        let c = Caffeine::solo(NAME);
        let s = launch(&c, None);
        assert_eq!(s, Status { active: true, remaining_s: None, label: "∞".into(), error: None });
        assert!(!c.toggle().active);
        assert_eq!(c.toggle().label, "∞");
        c.deactivate();
    }

    #[cfg(any(target_os = "macos", windows))]
    /// First launch ever (no switch file): on, and the file now says so.
    /// Later launches follow the file: off stays off, on stays on.
    #[test]
    fn launch_turns_on_only_when_the_shared_switch_was_never_set() {
        let path = temp_switch("launch");
        let first = Caffeine::shared(NAME, &path);
        assert!(launch(&first, Some(&path)).active);
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "on\n");
        first.deactivate();
        drop(first);

        let off = Caffeine::shared(NAME, &path);
        assert!(!launch(&off, Some(&path)).active, "an off switch stays off at launch");
        off.activate(None);
        drop(off);

        let on = Caffeine::shared(NAME, &path);
        assert!(launch(&on, Some(&path)).active, "an on switch stays on at launch");
        on.deactivate();
        let _ = std::fs::remove_dir_all(path.parent().unwrap());
    }

    #[cfg(any(target_os = "macos", windows))]
    /// Two Suzuri windows (or Suzuri and Hearth) on one switch: a change in
    /// either shows in the other, and in its `caffeine` event, within a
    /// second.
    #[test]
    fn a_change_in_another_program_shows_within_a_second() {
        let path = temp_switch("follow");
        let a = Caffeine::shared(NAME, &path);
        let b = Caffeine::shared("hearth caffeine", &path);
        let (tx, rx) = std::sync::mpsc::channel();
        a.on_change(move |s| {
            let _ = tx.send(s.active);
        });
        launch(&a, Some(&path));
        assert_eq!(rx.recv_timeout(Duration::from_secs(1)), Ok(true));
        assert!(wait_for(&b, true) < Duration::from_secs(1));

        b.deactivate();
        assert!(wait_for(&a, false) < Duration::from_secs(1));
        assert_eq!(rx.recv_timeout(Duration::from_secs(1)), Ok(false), "a's ☕ got the change event");

        b.activate(Some(15));
        assert!(wait_for(&a, true) < Duration::from_secs(1));
        // b's timer, end time and all: a little under 15 minutes left.
        assert!(matches!(a.status().remaining_s, Some(880..=900)), "{:?}", a.status());
        b.deactivate();
        wait_for(&a, false);
        let _ = std::fs::remove_dir_all(path.parent().unwrap());
    }
}
