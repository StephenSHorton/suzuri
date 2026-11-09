//! Window position and size across relaunches (Go suzuri's config `window`,
//! internal/config/config.go WindowPlacement and window_darwin.go /
//! window_windows.go's capture, placementOnScreen and persist).
//!
//! The main window starts hidden (tauri.conf.json `visible: false`); setup
//! moves it to the saved frame, clamped onto a connected monitor, and shows
//! it. Moves and resizes are saved after a short pause, and at once on close
//! and exit. Each window is its own process, so the last one to move or close
//! is what the next launch opens with. A new-window launch opens a step down
//! and right of that (cascade) and never maximized or fullscreen.
//!
//! Units: macOS stores points (logical), the only space that is the same on
//! every display there; Windows stores physical pixels, its real desktop
//! space (as Go's Win32 frame did). Size is the inner (content) size.

use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use parking_lot::Mutex;
use serde_json::{Value, json};
use tauri::{LogicalPosition, LogicalSize, Manager, PhysicalPosition, PhysicalSize, Position, Size, WebviewWindow, WindowEvent};

/// Points on macOS, physical pixels elsewhere.
const LOGICAL: bool = cfg!(target_os = "macos");
/// How far a new window steps from the last one.
pub const CASCADE: f64 = 28.0;
/// Quiet time before a move or resize is written.
const DEBOUNCE: Duration = Duration::from_millis(500);
/// How much of a saved frame must still be on a monitor (Go's 80x40 sliver).
const MIN_VISIBLE: (f64, f64) = (80.0, 40.0);

/// The saved frame, Go's WindowPlacement keys plus fullscreen.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Placement {
    pub x: i32,
    pub y: i32,
    pub width: i32,
    pub height: i32,
    pub maximized: bool,
    pub fullscreen: bool,
}

impl Placement {
    /// Go's Valid: a sane size, and not the empty zero frame.
    pub fn valid(&self) -> bool {
        (320..=16000).contains(&self.width) && (200..=16000).contains(&self.height)
    }

    pub fn from_value(v: &Value) -> Option<Self> {
        let n = |k: &str| v.get(k).and_then(Value::as_f64).map(|f| f.round() as i32);
        let b = |k: &str| v.get(k).and_then(Value::as_bool).unwrap_or(false);
        let p = Placement {
            x: n("x")?,
            y: n("y")?,
            width: n("width")?,
            height: n("height")?,
            maximized: b("maximized"),
            fullscreen: b("fullscreen"),
        };
        p.valid().then_some(p)
    }

    pub fn to_value(self) -> Value {
        let mut v = json!({"x": self.x, "y": self.y, "width": self.width, "height": self.height});
        if self.maximized {
            v["maximized"] = true.into();
        }
        if self.fullscreen {
            v["fullscreen"] = true.into();
        }
        v
    }

    fn rect(&self) -> Rect {
        Rect { x: self.x as f64, y: self.y as f64, w: self.width as f64, h: self.height as f64 }
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rect {
    pub x: f64,
    pub y: f64,
    pub w: f64,
    pub h: f64,
}

impl Rect {
    fn overlap(&self, o: &Rect) -> (f64, f64) {
        let w = (self.x + self.w).min(o.x + o.w) - self.x.max(o.x);
        let h = (self.y + self.h).min(o.y + o.h) - self.y.max(o.y);
        (w.max(0.0), h.max(0.0))
    }

    /// Shrink to fit `m` and slide inside it.
    fn clamp_into(mut self, m: &Rect) -> Rect {
        self.w = self.w.min(m.w);
        self.h = self.h.min(m.h);
        self.x = self.x.clamp(m.x, m.x + m.w - self.w);
        self.y = self.y.clamp(m.y, m.y + m.h - self.h);
        self
    }
}

/// Where the window opens. `monitors` are work areas in the saved units and
/// `primary` indexes them. A saved frame goes back on the monitor it overlaps
/// most, clamped inside it; one that is off every monitor (that display is
/// gone) is centered on the primary at its saved size. With nothing saved
/// the default size is centered on the primary. `cascade` steps it down and
/// right, back to the monitor's corner once it would run off. None: no
/// monitors known, leave the window where the OS put it.
pub fn place(saved: Option<Placement>, monitors: &[Rect], primary: usize, default: (f64, f64), cascade: bool) -> Option<Rect> {
    let pm = monitors.get(primary).or(monitors.first())?;
    let centered = |w: f64, h: f64| Rect { x: pm.x + (pm.w - w) / 2.0, y: pm.y + (pm.h - h) / 2.0, w, h }.clamp_into(pm);
    let Some(p) = saved else {
        return Some(centered(default.0, default.1));
    };
    let r = p.rect();
    let best = monitors
        .iter()
        .map(|m| (m, r.overlap(m)))
        .filter(|(_, (w, h))| *w >= MIN_VISIBLE.0.min(r.w) && *h >= MIN_VISIBLE.1.min(r.h))
        .max_by(|a, b| (a.1.0 * a.1.1).total_cmp(&(b.1.0 * b.1.1)));
    let Some((m, _)) = best else {
        return Some(centered(r.w, r.h));
    };
    let mut r = r.clamp_into(m);
    if cascade {
        r.x += CASCADE;
        r.y += CASCADE;
        if r.x + r.w > m.x + m.w || r.y + r.h > m.y + m.h {
            r.x = m.x;
            r.y = m.y;
        }
    }
    Some(r)
}

/// What to save for the window's state now. A maximized or fullscreen
/// window keeps its last normal frame (`normal`), so leaving that state
/// later goes back to a real size, as Go did for fullscreen.
pub fn frame_for(now: Placement, maximized: bool, fullscreen: bool, normal: Option<Placement>) -> Placement {
    if !maximized && !fullscreen {
        return Placement { maximized: false, fullscreen: false, ..now };
    }
    let base = normal.unwrap_or(now);
    Placement { maximized: maximized && !fullscreen, fullscreen, ..base }
}

#[derive(Default)]
struct Tracker {
    /// Last frame seen while neither maximized nor fullscreen.
    normal: Option<Placement>,
    /// Captured but not yet written.
    pending: Option<Placement>,
    /// What this process last wrote (or read at launch), to skip writing
    /// the same frame again while it moves about.
    saved: Option<Placement>,
    /// The window's latest frame, written on close even if unchanged.
    last: Option<Placement>,
    /// A close has been written already (close, destroy and exit all try).
    closed: bool,
    due: Option<Instant>,
}

static TRACKER: Mutex<Tracker> =
    Mutex::new(Tracker { normal: None, pending: None, saved: None, last: None, closed: false, due: None });
static TIMER: AtomicBool = AtomicBool::new(false);

fn units(v: f64, scale: f64) -> f64 {
    if LOGICAL { v / scale } else { v }
}

fn monitor_rect(m: &tauri::Monitor) -> Rect {
    let a = m.work_area();
    let s = m.scale_factor();
    Rect {
        x: units(a.position.x as f64, s),
        y: units(a.position.y as f64, s),
        w: units(a.size.width as f64, s),
        h: units(a.size.height as f64, s),
    }
}

/// The window's current frame in the saved units (minimized: None).
fn capture(w: &WebviewWindow) -> Option<(Placement, bool, bool)> {
    if w.is_minimized().unwrap_or(false) {
        return None;
    }
    let s = w.scale_factor().ok()?;
    let pos = w.outer_position().ok()?;
    let size = w.inner_size().ok()?;
    let p = Placement {
        x: units(pos.x as f64, s).round() as i32,
        y: units(pos.y as f64, s).round() as i32,
        width: units(size.width as f64, s).round() as i32,
        height: units(size.height as f64, s).round() as i32,
        maximized: false,
        fullscreen: false,
    };
    Some((p, w.is_maximized().unwrap_or(false), w.is_fullscreen().unwrap_or(false)))
}

fn set_frame(w: &WebviewWindow, r: Rect) {
    let (size, pos): (Size, Position) = if LOGICAL {
        (LogicalSize::new(r.w, r.h).into(), LogicalPosition::new(r.x, r.y).into())
    } else {
        (
            PhysicalSize::new(r.w.round() as u32, r.h.round() as u32).into(),
            PhysicalPosition::new(r.x.round() as i32, r.y.round() as i32).into(),
        )
    };
    // Windows: move first. Landing on a monitor with another DPI makes tao
    // rescale the size (WM_DPICHANGED), so the size is set after, in place.
    if LOGICAL {
        let _ = w.set_size(size);
        let _ = w.set_position(pos);
    } else {
        let _ = w.set_position(pos);
        let _ = w.set_size(size);
    }
}

/// Put the main window where it was, show it, and start saving its frame.
/// Called once from setup; the window is shown even if nothing was saved.
pub fn install(app: &tauri::AppHandle) {
    let Some(w) = app.get_webview_window("main") else { return };
    let saved = Placement::from_value(&crate::config::load()["window"]);
    let cascade = std::env::var(crate::window::NEW_WINDOW_ENV).is_ok_and(|v| !v.is_empty());
    let monitors = w.available_monitors().unwrap_or_default();
    let primary = w.primary_monitor().ok().flatten();
    let pi = primary
        .and_then(|p| monitors.iter().position(|m| m.position() == p.position() && m.size() == p.size()))
        .unwrap_or(0);
    let rects: Vec<Rect> = monitors.iter().map(monitor_rect).collect();
    let s = w.scale_factor().unwrap_or(1.0);
    let default = w
        .inner_size()
        .map(|z| (units(z.width as f64, s), units(z.height as f64, s)))
        .unwrap_or((1200.0, 800.0));
    let restore_max = !cascade && saved.is_some_and(|p| p.maximized && !p.fullscreen);
    let restore_fs = !cascade && saved.is_some_and(|p| p.fullscreen);
    if let Some(r) = place(saved, &rects, pi, default, cascade) {
        set_frame(&w, r);
        let mut t = TRACKER.lock();
        let n = Placement { x: r.x as i32, y: r.y as i32, width: r.w as i32, height: r.h as i32, ..Default::default() };
        t.normal = Some(n);
        t.last = Some(Placement { maximized: restore_max, fullscreen: restore_fs, ..n }).filter(Placement::valid);
        t.saved = saved;
    }
    if restore_max {
        let _ = w.maximize();
    }
    let _ = w.show();
    if restore_fs {
        let _ = w.set_fullscreen(true);
    }
    let ww = w.clone();
    w.on_window_event(move |ev| match ev {
        WindowEvent::Moved(_) | WindowEvent::Resized(_) => note(&ww, false),
        WindowEvent::CloseRequested { .. } => note(&ww, true),
        WindowEvent::Destroyed => flush_now(true),
        _ => {}
    });
}

/// Capture the frame now (on the event thread) and write it after a pause,
/// or at once.
fn note(w: &WebviewWindow, now: bool) {
    let Some((p, max, fs)) = capture(w) else { return };
    {
        let mut t = TRACKER.lock();
        if !max && !fs && p.valid() {
            t.normal = Some(p);
        }
        let base = t.normal.or(t.saved);
        let f = frame_for(p, max, fs, base);
        if !f.valid() {
            return;
        }
        t.pending = Some(f);
        t.last = Some(f);
        // Still open after all (a close that didn't happen): close again later.
        t.closed &= now;
        t.due = Some(Instant::now() + DEBOUNCE);
    }
    if now {
        flush_now(true);
        return;
    }
    if TIMER.swap(true, Ordering::AcqRel) {
        return;
    }
    std::thread::spawn(|| {
        loop {
            let due = TRACKER.lock().due;
            match due {
                Some(d) if d > Instant::now() => std::thread::sleep(d - Instant::now()),
                _ => break,
            }
        }
        TIMER.store(false, Ordering::Release);
        flush_now(false);
    });
}

/// Write the window's frame on exit, as on close.
pub fn flush() {
    flush_now(true)
}

/// Write the pending frame if it differs from what this process last wrote.
/// `closing` writes the latest frame even if unchanged, once: another window
/// may have written its own since, and the one closed last decides.
fn flush_now(closing: bool) {
    let p = {
        let mut t = TRACKER.lock();
        t.due = None;
        let pending = t.pending.take();
        let p = if closing { pending.or(t.last) } else { pending };
        if !should_write(p, t.saved, closing && !t.closed) {
            return;
        }
        t.closed |= closing;
        let p = p.expect("should_write checked");
        t.saved = Some(p);
        p
    };
    if let Err(e) = crate::config::update(json!({"window": p.to_value()})) {
        eprintln!("window: couldn't save placement: {e}");
    }
}

/// Debounced saves skip a frame this process already wrote; a close never
/// does.
fn should_write(pending: Option<Placement>, saved: Option<Placement>, closing: bool) -> bool {
    pending.is_some() && (closing || pending != saved)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn p(x: i32, y: i32, w: i32, h: i32) -> Placement {
        Placement { x, y, width: w, height: h, ..Default::default() }
    }

    const MAIN: Rect = Rect { x: 0.0, y: 25.0, w: 1728.0, h: 1052.0 };
    const LEFT: Rect = Rect { x: -1920.0, y: 0.0, w: 1920.0, h: 1080.0 };

    #[test]
    fn reads_go_placements_and_rejects_bad_ones() {
        let v = json!({"x": 0, "y": 65, "width": 1728, "height": 1052});
        assert_eq!(Placement::from_value(&v), Some(p(0, 65, 1728, 1052)));
        let v = json!({"x": -1500, "y": 10, "width": 900, "height": 600, "maximized": true});
        assert!(Placement::from_value(&v).unwrap().maximized);
        assert_eq!(Placement::from_value(&json!({"x": 0, "y": 0, "width": 0, "height": 0})), None);
        assert_eq!(Placement::from_value(&json!({"x": 0, "y": 0, "width": 100, "height": 900})), None);
        assert_eq!(Placement::from_value(&json!({"width": 900, "height": 600})), None);
        assert_eq!(Placement::from_value(&Value::Null), None);
    }

    #[test]
    fn round_trips_through_json() {
        let a = Placement { fullscreen: true, ..p(-1800, 40, 1000, 700) };
        assert_eq!(Placement::from_value(&a.to_value()), Some(a));
        assert!(p(1, 2, 400, 300).to_value().get("maximized").is_none());
    }

    #[test]
    fn restores_on_the_saved_monitor() {
        let r = place(Some(p(-1500, 100, 900, 600)), &[MAIN, LEFT], 0, (1200.0, 800.0), false).unwrap();
        assert_eq!(r, Rect { x: -1500.0, y: 100.0, w: 900.0, h: 600.0 });
    }

    #[test]
    fn clamps_a_frame_hanging_off_its_monitor() {
        // Mostly on the main display, sticking out right and above the menu bar.
        let r = place(Some(p(1400, 0, 900, 600)), &[MAIN], 0, (1200.0, 800.0), false).unwrap();
        assert_eq!(r, Rect { x: 1728.0 - 900.0, y: 25.0, w: 900.0, h: 600.0 });
        // Bigger than the display: shrunk to it.
        let r = place(Some(p(10, 30, 3000, 2000)), &[MAIN], 0, (1200.0, 800.0), false).unwrap();
        assert_eq!(r, MAIN);
    }

    #[test]
    fn centers_on_primary_when_its_monitor_is_gone() {
        // Saved on the left display, which is unplugged now.
        let r = place(Some(p(-1500, 100, 900, 600)), &[MAIN], 0, (1200.0, 800.0), false).unwrap();
        assert_eq!(r, Rect { x: (1728.0 - 900.0) / 2.0, y: 25.0 + (1052.0 - 600.0) / 2.0, w: 900.0, h: 600.0 });
        // A sliver under 80x40 doesn't count as still on screen.
        let r = place(Some(p(1700, 100, 900, 600)), &[MAIN], 0, (1200.0, 800.0), false).unwrap();
        assert_eq!(r.x, (1728.0 - 900.0) / 2.0);
    }

    #[test]
    fn nothing_saved_centers_the_default_on_primary() {
        let r = place(None, &[LEFT, MAIN], 1, (1200.0, 800.0), false).unwrap();
        assert_eq!(r, Rect { x: 264.0, y: 25.0 + 126.0, w: 1200.0, h: 800.0 });
        assert_eq!(place(None, &[], 0, (1200.0, 800.0), false), None);
    }

    #[test]
    fn new_windows_cascade_and_wrap() {
        let r = place(Some(p(100, 100, 900, 600)), &[MAIN], 0, (1200.0, 800.0), true).unwrap();
        assert_eq!((r.x, r.y), (128.0, 128.0));
        // At the bottom-right corner the next one goes back to the top-left.
        let r = place(Some(p(828, 477, 900, 600)), &[MAIN], 0, (1200.0, 800.0), true).unwrap();
        assert_eq!((r.x, r.y), (MAIN.x, MAIN.y));
    }

    #[test]
    fn maximized_and_fullscreen_keep_the_normal_frame() {
        let normal = p(100, 100, 900, 600);
        let zoomed = p(0, 25, 1728, 1052);
        assert_eq!(frame_for(zoomed, false, false, Some(normal)), zoomed);
        assert_eq!(frame_for(zoomed, true, false, Some(normal)), Placement { maximized: true, ..normal });
        assert_eq!(frame_for(zoomed, true, true, Some(normal)), Placement { fullscreen: true, ..normal });
        // No normal frame seen yet: the current one stands in.
        assert_eq!(frame_for(zoomed, true, false, None), Placement { maximized: true, ..zoomed });
    }

    #[test]
    fn close_writes_even_an_unchanged_frame() {
        let f = Some(p(100, 100, 900, 600));
        // Another window wrote since; this one closes without moving.
        assert!(should_write(f, f, true));
        assert!(!should_write(f, f, false));
        assert!(should_write(f, Some(p(0, 25, 900, 600)), false));
        assert!(!should_write(None, f, true));
    }
}
