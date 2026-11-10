//! Clipboard calls off the UI thread, with a time limit.
//!
//! On Windows GetClipboardData on a format the owner renders on demand
//! (Office, browsers, RDP's rdpclip) sends that app WM_RENDERFORMAT and
//! waits for it with no timeout. A hung owner then hangs whoever asked: the
//! main thread for a paste or copy command, or a pane's reader thread (with
//! its engine locked) for an OSC 52 read. So every clipboard call runs here
//! on a thread of its own and the caller waits at most `limit`. A call that
//! never comes back costs one parked thread; while [`MAX_STUCK`] of those
//! are parked, new calls fail at once instead of starting more.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

/// Copy, paste and the input bar's clipboard text.
pub const USER: Duration = Duration::from_secs(3);
/// A program's OSC 52 read, answered under the pane's engine lock: short,
/// and an empty answer when the clipboard doesn't answer in time.
pub const OSC52: Duration = Duration::from_millis(250);
/// A program's OSC 52 write, from the pane's reader thread.
pub const WRITE: Duration = Duration::from_secs(1);

/// Clipboard threads that have not finished yet (stuck or still working).
const MAX_STUCK: usize = 4;
static IN_FLIGHT: AtomicUsize = AtomicUsize::new(0);

/// Run `f` on a clipboard thread and wait up to `limit` for its answer.
pub fn run<T: Send + 'static>(limit: Duration, f: impl FnOnce() -> Result<T, String> + Send + 'static) -> Result<T, String> {
    run_with(&IN_FLIGHT, MAX_STUCK, limit, f)
}

fn run_with<T: Send + 'static>(
    in_flight: &'static AtomicUsize,
    max: usize,
    limit: Duration,
    f: impl FnOnce() -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    if in_flight.fetch_add(1, Ordering::AcqRel) >= max {
        in_flight.fetch_sub(1, Ordering::AcqRel);
        return Err("clipboard busy (another app is not answering)".into());
    }
    let (tx, rx) = crossbeam_channel::bounded(1);
    let spawned = std::thread::Builder::new().name("rsz-clipboard".into()).spawn(move || {
        let out = f();
        in_flight.fetch_sub(1, Ordering::AcqRel);
        let _ = tx.send(out);
    });
    if let Err(e) = spawned {
        in_flight.fetch_sub(1, Ordering::AcqRel);
        return Err(format!("clipboard thread: {e}"));
    }
    match rx.recv_timeout(limit) {
        Ok(out) => out,
        Err(_) => {
            log::warn!("clipboard: no answer within {} ms", limit.as_millis());
            Err("clipboard not answering".into())
        }
    }
}

fn clipboard() -> Result<arboard::Clipboard, String> {
    arboard::Clipboard::new().map_err(|e| e.to_string())
}

/// The clipboard's text.
pub fn text(limit: Duration) -> Result<String, String> {
    run(limit, || clipboard()?.get_text().map_err(|e| e.to_string()))
}

/// Put `text` on the clipboard.
pub fn set_text(limit: Duration, text: String) -> Result<(), String> {
    run(limit, move || clipboard()?.set_text(text).map_err(|e| e.to_string()))
}

/// What a paste takes from the clipboard.
pub enum Paste {
    Image(arboard::ImageData<'static>),
    Text(String),
}

/// The clipboard's image when `image` (a full-screen app takes a copied
/// image as a file path), else or failing that its text.
pub fn paste(limit: Duration, image: bool) -> Result<Paste, String> {
    run(limit, move || {
        let mut c = clipboard()?;
        if image && let Ok(img) = c.get_image() {
            return Ok(Paste::Image(img));
        }
        c.get_text().map(Paste::Text).map_err(|e| e.to_string())
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn answers_in_time() {
        static N: AtomicUsize = AtomicUsize::new(0);
        assert_eq!(run_with(&N, 2, Duration::from_secs(5), || Ok(7)), Ok(7));
        assert_eq!(run_with::<u8>(&N, 2, Duration::from_secs(5), || Err("no".into())), Err("no".into()));
        assert_eq!(N.load(Ordering::Acquire), 0);
    }

    #[test]
    fn a_stuck_call_times_out_and_caps_new_ones() {
        static N: AtomicUsize = AtomicUsize::new(0);
        let (release, wait) = crossbeam_channel::bounded::<()>(0);
        let short = Duration::from_millis(30);
        for _ in 0..2 {
            let w = wait.clone();
            let t = std::time::Instant::now();
            assert!(run_with(&N, 2, short, move || Ok(w.recv().ok())).is_err());
            assert!(t.elapsed() < Duration::from_secs(2));
        }
        // Two parked: the next fails at once without starting a thread.
        let e = run_with(&N, 2, Duration::from_secs(5), || Ok(1)).unwrap_err();
        assert!(e.contains("busy"), "{e}");
        // The owner answers: the parked calls finish and free their slots.
        release.send(()).unwrap();
        release.send(()).unwrap();
        let t = std::time::Instant::now();
        while N.load(Ordering::Acquire) != 0 {
            assert!(t.elapsed() < Duration::from_secs(5));
            std::thread::yield_now();
        }
        assert_eq!(run_with(&N, 2, Duration::from_secs(5), || Ok(3)), Ok(3));
    }
}
