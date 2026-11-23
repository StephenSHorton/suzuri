//! Caffeine: keep the machine awake while it's on (Go suzuri's
//! internal/caffeine). The hold, timers and label live in the `rsz-caffeine`
//! crate (crates/caffeine); this is Suzuri's Tauri side: its own switch
//! (solo, as before), the `caffeine` event and the four commands.

use std::sync::Arc;

use tauri::{AppHandle, Emitter, State};

pub use rsz_caffeine::{Caffeine, Status};

/// Suzuri's switch. The name shows in `pmset -g assertions`.
pub fn new() -> Arc<Caffeine> {
    Caffeine::solo("suzuri caffeine")
}

/// Go turns caffeine on, indefinitely, when the window opens
/// (window_darwin.go, window_windows.go), so long sessions don't sleep.
#[cfg_attr(not(any(target_os = "macos", windows)), allow(dead_code))]
fn launch(c: &Arc<Caffeine>) -> Status {
    c.activate(None)
}

/// Wires the change events to the UI (`caffeine` event with a Status) and
/// turns caffeine on, as Go does at launch. Other platforms have no hold, so
/// they start off rather than toast an error.
pub fn install(app: &AppHandle, c: &Arc<Caffeine>) {
    let app = app.clone();
    c.on_change(move |s| {
        let _ = app.emit("caffeine", s);
    });
    #[cfg(any(target_os = "macos", windows))]
    launch(c);
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
    /// The ☕ click is caffeine_toggle: launch turns it on indefinitely, a
    /// click turns it off and another back on.
    #[test]
    fn launch_is_on_indefinitely_and_a_click_turns_it_off() {
        let c = new();
        let s = launch(&c);
        assert_eq!(s, Status { active: true, remaining_s: None, label: "∞".into(), error: None });
        assert!(!c.toggle().active);
        assert_eq!(c.toggle().label, "∞");
        c.deactivate();
    }
}
