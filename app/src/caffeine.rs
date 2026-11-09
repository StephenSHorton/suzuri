//! Caffeine: keep the machine awake while it's on (Go suzuri's
//! internal/caffeine). Indefinite, or for a while (15 min, 1 h) and then off
//! by itself.
//!
//! The OS hold lives on one dedicated thread. On Windows that matters:
//! SetThreadExecutionState(ES_CONTINUOUS | ...) belongs to the calling
//! thread, so taking and dropping it from whichever thread a command lands
//! on would leak or drop the hold. macOS uses an IOPMAssertion
//! (PreventUserIdleDisplaySleep, as classic Caffeine and Go suzuri do).

use std::sync::Arc;
use std::sync::mpsc::{Sender, channel};
use std::time::{Duration, Instant};

use parking_lot::Mutex;
use serde::Serialize;
use tauri::{AppHandle, Emitter, State};

/// What the UI shows: `label` is "" when off, "∞" indefinite, else "12m".
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct Status {
    pub active: bool,
    /// Seconds left on a timed hold; None when off or indefinite.
    pub remaining_s: Option<u64>,
    pub label: String,
    /// The OS refused the hold (shown, then cleared on the next change).
    pub error: Option<String>,
}

#[derive(Default)]
struct Inner {
    active: bool,
    until: Option<Instant>,
    /// Bumped on every change, so a stale expiry timer does nothing.
    generation: u64,
    error: Option<String>,
}

type OnChange = Box<dyn Fn(Status) + Send + Sync>;

pub struct Caffeine {
    inner: Mutex<Inner>,
    hold: Sender<bool>,
    hold_result: Mutex<std::sync::mpsc::Receiver<Result<(), String>>>,
    on_change: Mutex<Option<OnChange>>,
}

impl Caffeine {
    /// Starts the holder thread with the real OS hold.
    pub fn new() -> Arc<Self> {
        Self::with_hold(platform::Hold::default())
    }

    fn with_hold(mut hold: impl OsHold + Send + 'static) -> Arc<Self> {
        let (tx, rx) = channel::<bool>();
        let (res_tx, res_rx) = channel();
        std::thread::Builder::new()
            .name("caffeine".into())
            .spawn(move || {
                for want in rx {
                    let r = if want { hold.acquire() } else { hold.release() };
                    let _ = res_tx.send(r);
                }
                let _ = hold.release();
            })
            .expect("spawn caffeine thread");
        Arc::new(Self { inner: Mutex::default(), hold: tx, hold_result: Mutex::new(res_rx), on_change: Mutex::new(None) })
    }

    pub fn on_change(&self, f: impl Fn(Status) + Send + Sync + 'static) {
        *self.on_change.lock() = Some(Box::new(f));
    }

    fn os(&self, want: bool) -> Result<(), String> {
        let rx = self.hold_result.lock();
        self.hold.send(want).map_err(|e| e.to_string())?;
        rx.recv_timeout(Duration::from_secs(2)).unwrap_or_else(|_| Err("caffeine thread did not answer".into()))
    }

    pub fn status(&self) -> Status {
        let inner = self.inner.lock();
        status_at(&inner, Instant::now())
    }

    /// On for `minutes`, or indefinitely with None.
    pub fn activate(self: &Arc<Self>, minutes: Option<u32>) -> Status {
        self.activate_for(minutes.filter(|&m| m > 0).map(|m| Duration::from_secs(m as u64 * 60)))
    }

    fn activate_for(self: &Arc<Self>, dur: Option<Duration>) -> Status {
        let mut inner = self.inner.lock();
        inner.generation += 1;
        inner.error = None;
        if !inner.active {
            if let Err(e) = self.os(true) {
                inner.error = Some(e);
                inner.until = None;
                return self.changed(inner);
            }
            inner.active = true;
        }
        inner.until = dur.map(|d| Instant::now() + d);
        if let Some(until) = inner.until {
            let (me, generation) = (Arc::clone(self), inner.generation);
            std::thread::spawn(move || {
                std::thread::sleep(until.saturating_duration_since(Instant::now()));
                let inner = me.inner.lock();
                if inner.generation == generation && inner.active {
                    drop(inner);
                    me.deactivate();
                }
            });
        }
        self.changed(inner)
    }

    pub fn deactivate(&self) -> Status {
        let mut inner = self.inner.lock();
        inner.generation += 1;
        inner.error = None;
        if inner.active {
            let _ = self.os(false);
        }
        inner.active = false;
        inner.until = None;
        self.changed(inner)
    }

    /// Go turns caffeine on, indefinitely, when the window opens
    /// (window_darwin.go, window_windows.go), so long sessions don't sleep.
    #[cfg_attr(not(any(target_os = "macos", windows)), allow(dead_code))]
    pub fn launch(self: &Arc<Self>) -> Status {
        self.activate(None)
    }

    /// Go's caffeine_toggle: off when on (timed or not), else indefinite.
    pub fn toggle(self: &Arc<Self>) -> Status {
        if self.inner.lock().active { self.deactivate() } else { self.activate(None) }
    }

    fn changed(&self, inner: parking_lot::MutexGuard<'_, Inner>) -> Status {
        let s = status_at(&inner, Instant::now());
        drop(inner);
        if let Some(f) = self.on_change.lock().as_ref() {
            f(s.clone());
        }
        s
    }
}

fn status_at(inner: &Inner, now: Instant) -> Status {
    let remaining = inner.until.filter(|_| inner.active).map(|u| u.saturating_duration_since(now));
    Status {
        active: inner.active,
        remaining_s: remaining.map(|d| (d.as_millis() as u64).div_ceil(1000)),
        label: if inner.active { label(remaining) } else { String::new() },
        error: inner.error.clone(),
    }
}

/// Go's StripLabel: "∞" indefinite, else "1h", "1h5", "12m" or "40s"
/// (rounded to the nearest second).
pub fn label(remaining: Option<Duration>) -> String {
    let Some(d) = remaining else { return "∞".into() };
    let sec = (d.as_millis() as u64 + 500) / 1000;
    match sec {
        s if s >= 3600 => match (s % 3600) / 60 {
            0 => format!("{}h", s / 3600),
            m => format!("{}h{}", s / 3600, m),
        },
        s if s >= 60 => format!("{}m", s / 60),
        s => format!("{s}s"),
    }
}

/// The OS side: take and drop the stay-awake hold.
pub trait OsHold {
    fn acquire(&mut self) -> Result<(), String>;
    fn release(&mut self) -> Result<(), String>;
}

#[cfg(target_os = "macos")]
mod platform {
    use std::ffi::{CString, c_char, c_void};

    type CFStringRef = *const c_void;
    const UTF8: u32 = 0x0800_0100;
    const LEVEL_ON: u32 = 255;

    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFStringCreateWithCString(alloc: *const c_void, s: *const c_char, enc: u32) -> CFStringRef;
        fn CFRelease(cf: *const c_void);
    }
    #[link(name = "IOKit", kind = "framework")]
    unsafe extern "C" {
        fn IOPMAssertionCreateWithName(kind: CFStringRef, level: u32, name: CFStringRef, id: *mut u32) -> i32;
        fn IOPMAssertionRelease(id: u32) -> i32;
    }

    fn cfstr(s: &str) -> CFStringRef {
        let c = CString::new(s).expect("no NUL");
        unsafe { CFStringCreateWithCString(std::ptr::null(), c.as_ptr(), UTF8) }
    }

    #[derive(Default)]
    pub struct Hold(Option<u32>);

    impl super::OsHold for Hold {
        fn acquire(&mut self) -> Result<(), String> {
            if self.0.is_some() {
                return Ok(());
            }
            let (kind, name) = (cfstr("PreventUserIdleDisplaySleep"), cfstr("suzuri caffeine"));
            let mut id = 0u32;
            let r = unsafe { IOPMAssertionCreateWithName(kind, LEVEL_ON, name, &mut id) };
            unsafe {
                CFRelease(kind);
                CFRelease(name);
            }
            if r != 0 {
                return Err(format!("IOPMAssertionCreateWithName: 0x{:x}", r as u32));
            }
            self.0 = Some(id);
            Ok(())
        }

        fn release(&mut self) -> Result<(), String> {
            if let Some(id) = self.0.take() {
                unsafe { IOPMAssertionRelease(id) };
            }
            Ok(())
        }
    }
}

#[cfg(windows)]
mod platform {
    const ES_SYSTEM_REQUIRED: u32 = 0x0000_0001;
    const ES_DISPLAY_REQUIRED: u32 = 0x0000_0002;
    const ES_CONTINUOUS: u32 = 0x8000_0000;

    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn SetThreadExecutionState(flags: u32) -> u32;
    }

    /// Always called on the caffeine thread, which owns the state.
    #[derive(Default)]
    pub struct Hold(bool);

    impl super::OsHold for Hold {
        fn acquire(&mut self) -> Result<(), String> {
            if self.0 {
                return Ok(());
            }
            if unsafe { SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED) } == 0 {
                return Err("SetThreadExecutionState failed".into());
            }
            self.0 = true;
            Ok(())
        }

        fn release(&mut self) -> Result<(), String> {
            if std::mem::take(&mut self.0) {
                unsafe { SetThreadExecutionState(ES_CONTINUOUS) };
            }
            Ok(())
        }
    }
}

#[cfg(not(any(target_os = "macos", windows)))]
mod platform {
    #[derive(Default)]
    pub struct Hold;
    impl super::OsHold for Hold {
        fn acquire(&mut self) -> Result<(), String> {
            Err("caffeine is macOS and Windows only".into())
        }
        fn release(&mut self) -> Result<(), String> {
            Ok(())
        }
    }
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
    c.launch();
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
    use std::sync::atomic::{AtomicI32, Ordering};

    /// Counts holds; refuses when `fail` is set.
    #[derive(Clone, Default)]
    struct Fake {
        held: Arc<AtomicI32>,
        fail: Arc<std::sync::atomic::AtomicBool>,
        thread: Arc<Mutex<Option<std::thread::ThreadId>>>,
    }
    impl OsHold for Fake {
        fn acquire(&mut self) -> Result<(), String> {
            *self.thread.lock() = Some(std::thread::current().id());
            if self.fail.load(Ordering::SeqCst) {
                return Err("nope".into());
            }
            self.held.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }
        fn release(&mut self) -> Result<(), String> {
            if self.held.load(Ordering::SeqCst) > 0 {
                self.held.fetch_sub(1, Ordering::SeqCst);
            }
            Ok(())
        }
    }

    #[test]
    fn labels_match_go() {
        assert_eq!(label(None), "∞");
        assert_eq!(label(Some(Duration::from_secs(3600))), "1h");
        assert_eq!(label(Some(Duration::from_secs(3900))), "1h5");
        assert_eq!(label(Some(Duration::from_secs(15 * 60))), "15m");
        assert_eq!(label(Some(Duration::from_secs(119))), "1m");
        assert_eq!(label(Some(Duration::from_millis(40_400))), "40s");
        assert_eq!(label(Some(Duration::ZERO)), "0s");
    }

    #[test]
    fn toggle_holds_once_and_releases() {
        let f = Fake::default();
        let c = Caffeine::with_hold(f.clone());
        assert_eq!(c.status(), Status { active: false, remaining_s: None, label: "".into(), error: None });
        let s = c.toggle();
        assert!(s.active && s.label == "∞");
        // A timed activation on top keeps the one hold and adds a deadline.
        let s = c.activate(Some(15));
        assert_eq!(f.held.load(Ordering::SeqCst), 1);
        assert_eq!(s.label, "15m");
        assert!(s.remaining_s.unwrap() > 14 * 60);
        assert!(!c.toggle().active);
        assert_eq!(f.held.load(Ordering::SeqCst), 0);
        // The hold is always taken on the caffeine thread, never the caller's.
        assert_ne!(*f.thread.lock(), Some(std::thread::current().id()));
    }

    #[test]
    fn launch_is_on_indefinitely_and_a_click_turns_it_off() {
        let f = Fake::default();
        let c = Caffeine::with_hold(f.clone());
        let s = c.launch();
        assert_eq!(s, Status { active: true, remaining_s: None, label: "∞".into(), error: None });
        assert_eq!(f.held.load(Ordering::SeqCst), 1);
        // The ☕ click is caffeine_toggle: off, then back on.
        assert!(!c.toggle().active);
        assert_eq!(f.held.load(Ordering::SeqCst), 0);
        assert_eq!(c.toggle().label, "∞");
        assert_eq!(f.held.load(Ordering::SeqCst), 1);
    }

    #[test]
    fn os_refusal_is_reported_and_stays_off() {
        let f = Fake::default();
        f.fail.store(true, Ordering::SeqCst);
        let c = Caffeine::with_hold(f.clone());
        let s = c.activate(None);
        assert!(!s.active);
        assert_eq!(s.error.as_deref(), Some("nope"));
        assert_eq!(c.deactivate().error, None);
    }

    #[test]
    fn timed_hold_expires_and_tells_the_ui() {
        let f = Fake::default();
        let c = Caffeine::with_hold(f.clone());
        let seen = Arc::new(Mutex::new(Vec::new()));
        let s2 = Arc::clone(&seen);
        c.on_change(move |s| s2.lock().push(s.active));
        c.activate_for(Some(Duration::from_millis(60)));
        assert_eq!(f.held.load(Ordering::SeqCst), 1);
        std::thread::sleep(Duration::from_millis(400));
        assert!(!c.status().active);
        assert_eq!(*seen.lock(), vec![true, false]);
        assert_eq!(f.held.load(Ordering::SeqCst), 0);
    }

    #[test]
    fn a_stale_timer_does_not_turn_off_a_newer_hold() {
        let f = Fake::default();
        let c = Caffeine::with_hold(f.clone());
        c.activate_for(Some(Duration::from_millis(60)));
        c.activate(None); // indefinite now; the 60 ms timer is stale
        std::thread::sleep(Duration::from_millis(300));
        assert!(c.status().active);
        assert_eq!(c.status().label, "∞");
        assert_eq!(f.held.load(Ordering::SeqCst), 1);
    }

    /// The real IOPMAssertion shows in pmset while held and is gone after.
    #[cfg(target_os = "macos")]
    #[test]
    fn macos_assertion_is_visible_to_pmset() {
        let held = || {
            let out = std::process::Command::new("pmset").args(["-g", "assertions"]).output().unwrap();
            // Only this test process's own assertion: Go suzuri may hold one too.
            let me = format!("pid {}(", std::process::id());
            String::from_utf8_lossy(&out.stdout).lines().any(|l| l.contains(&me) && l.contains("suzuri caffeine"))
        };
        let c = Caffeine::new();
        assert!(c.activate(None).active);
        assert!(held(), "assertion missing while on");
        c.deactivate();
        assert!(!held(), "assertion left behind after off");
    }
}
