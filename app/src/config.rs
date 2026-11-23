//! Settings: Suzuri's side of the `rsz-settings` crate (crates/settings),
//! which holds the keys, defaults, limits and the file itself (Go suzuri's
//! config.json keys, in Suzuri's config folder). Hearth and any other
//! program using the crate share that one file.
//!
//! Suzuri follows changes others make (another window's process, Hearth, a
//! hand edit): the running panes take the new cursor, the notice cards
//! their corner, and the UI gets the settings on its `rsz-config` event (the
//! window's `rsz:config`, as after its own save).

use std::sync::{Arc, Mutex, OnceLock};

use serde_json::{Value, json};
use tauri::{AppHandle, Emitter, Manager};

pub use rsz_settings::*;

/// The shared file, one store for this process.
pub fn store() -> &'static Store {
    static STORE: OnceLock<Store> = OnceLock::new();
    STORE.get_or_init(Store::shared)
}

/// The settings, normalized, importing Go suzuri's on first run.
pub fn load() -> Value {
    store().load()
}

/// Merge `patch` into the file and save it; returns the whole settings.
pub fn update(patch: Value) -> std::io::Result<Value> {
    let v = store().update(patch)?;
    is_news(&LAST, &v);
    Ok(v)
}

/// The settings as this process last applied them, window frame aside:
/// set by its own saves and by each outside change it follows.
static LAST: Mutex<Option<Value>> = Mutex::new(None);

/// The settings without the window frame: each window process saves its
/// own frame (window_state.rs), which is no news to the others.
fn without_window(v: &Value) -> Value {
    let mut v = v.clone();
    if let Some(m) = v.as_object_mut() {
        m.remove("window");
    }
    v
}

/// Whether `v` differs from what was last applied (window frame aside),
/// and from now on is what was.
fn is_news(last: &Mutex<Option<Value>>, v: &Value) -> bool {
    let now = without_window(v);
    let mut last = last.lock().unwrap_or_else(|e| e.into_inner());
    if last.as_ref() == Some(&now) {
        return false;
    }
    *last = Some(now);
    true
}

/// Follow changes made outside this process, for as long as it runs.
pub fn follow(app: &AppHandle) {
    is_news(&LAST, &load());
    let app = app.clone();
    store()
        .watch(move |v| {
            if !is_news(&LAST, &v) {
                return;
            }
            // On the main thread, as a save from Settings (config_set) is.
            let a = app.clone();
            let _ = app.run_on_main_thread(move || {
                let shape = cursor_code(&v);
                for p in a.state::<Arc<crate::Panes>>().map.lock().values() {
                    p.set_default_cursor(shape);
                }
                // An empty patch: the corner moves, without the sample card
                // the window that made the change shows.
                if let Some(n) = a.try_state::<crate::notices::Notices>() {
                    n.apply_config(&v, &json!({}));
                }
                let _ = a.emit("rsz-config", v);
            });
        })
        .forever();
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn news_is_any_change_but_the_window_frame_even_back_to_an_old_value() {
        let last = Mutex::new(None);
        assert!(is_news(&last, &json!({"font_size_px": 14})));
        assert!(
            !is_news(&last, &json!({"font_size_px": 14, "window": {"x": 5}})),
            "only the frame moved"
        );
        // This process saves 18 (update), then another program puts 14 back.
        assert!(is_news(&last, &json!({"font_size_px": 18})));
        assert!(
            is_news(&last, &json!({"font_size_px": 14})),
            "back to the old value is still news"
        );
        assert!(!is_news(&last, &json!({"font_size_px": 14})));
    }
}
