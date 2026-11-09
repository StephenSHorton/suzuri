//! Pane activity (ui/activity.js): how long since each pane's PTY last
//! printed, for the output guess, and the "command finished" notice.

use std::collections::HashMap;
use std::sync::Arc;

use rsz_term::protocols::Notice;
use tauri::State;

use crate::{notices, Panes};

/// Milliseconds since each live pane last printed, by pane id.
#[tauri::command]
pub fn pane_activity(panes: State<'_, Arc<Panes>>) -> HashMap<u32, u64> {
    quiet_ms(&panes)
}

fn quiet_ms(panes: &Panes) -> HashMap<u32, u64> {
    panes.map.lock().iter().map(|(&id, p)| (id, p.last_output_at().elapsed().as_millis() as u64)).collect()
}

/// A long command finished in a pane you weren't looking at (Go's
/// flushCmdFinishes). The UI decides when; a click focuses the pane.
#[tauri::command]
pub fn activity_notice(app: tauri::AppHandle, notes: State<'_, notices::Notices>, pane: u32, title: String, body: String, failed: bool) {
    crate::notice_effects(&app, notes.post(Some(pane), finished(title, body, failed)));
}

fn finished(mut title: String, body: String, failed: bool) -> Notice {
    if title.chars().count() > 120 {
        title = title.chars().take(120).collect();
    }
    Notice {
        id: String::new(),
        title,
        body,
        sound: if failed { "error" } else { "info" }.into(),
        icon: if failed { "error" } else { "" }.into(),
        urgency: 1,
        occasion: "always".into(),
        expire_ms: None,
        report: false,
        focus: true,
        close_event: false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn failed_commands_sound_the_error() {
        let n = finished("cargo test".into(), "failed (exit 101)".into(), true);
        assert_eq!((n.sound.as_str(), n.icon.as_str(), n.focus), ("error", "error", true));
        let n = finished("x".repeat(200), "succeeded".into(), false);
        assert_eq!((n.title.len(), n.sound.as_str()), (120, "info"));
    }

    #[test]
    fn no_panes_no_activity() {
        assert!(quiet_ms(&Panes::default()).is_empty());
    }
}
