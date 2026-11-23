//! Caffeine: keep the computer awake while it's on. Indefinitely, or for a
//! while and then off by itself.
//!
//! A program picks how to use it:
//!
//! - **On its own** ([`Caffeine::solo`]): the switch lives in this process.
//!   When the program quits, the hold goes with it.
//! - **One switch shared by several programs** ([`Caffeine::shared`]): the
//!   switch is a small file that every program using the same path follows.
//!   Turning it on or off in one turns it on or off in all of them, and while
//!   any of them runs the computer stays awake. Where that file lives is the
//!   program's choice; [`shared_switch_path`] is a neutral default.
//!
//! The OS hold lives on one dedicated thread. On Windows that matters:
//! `SetThreadExecutionState(ES_CONTINUOUS | ...)` belongs to the calling
//! thread, so taking and dropping it from whichever thread a call lands on
//! would leak or drop the hold. macOS uses an IOPMAssertion
//! (`PreventUserIdleDisplaySleep`, as classic Caffeine does).
//!
//! No dependencies beyond std; the `serde` feature adds `Serialize` to
//! [`Status`].

use std::path::{Path, PathBuf};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::sync::{Arc, Mutex, MutexGuard, Weak};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

/// What a UI shows: `label` is "" when off, "∞" indefinite, else "12m".
#[derive(Clone, Debug, PartialEq)]
#[cfg_attr(feature = "serde", derive(serde::Serialize))]
pub struct Status {
    pub active: bool,
    /// Seconds left on a timed hold; None when off or indefinite.
    pub remaining_s: Option<u64>,
    pub label: String,
    /// The OS refused the hold, or the shared switch could not be written
    /// (cleared on the next change).
    pub error: Option<String>,
}

/// The OS side: take and drop the stay-awake hold. Always called on the
/// caffeine thread. [`SystemHold`] is the real one; a program can pass its
/// own to [`Caffeine::custom`] (a test fake, or extra work around the hold).
pub trait OsHold {
    fn acquire(&mut self) -> Result<(), String>;
    fn release(&mut self) -> Result<(), String>;
}

/// How often a shared switch looks at its file for changes by others.
pub const SHARED_POLL: Duration = Duration::from_millis(500);

#[derive(Default)]
struct Inner {
    active: bool,
    until: Option<Instant>,
    /// Bumped on every change, so a stale expiry timer does nothing.
    generation: u64,
    error: Option<String>,
}

type OnChange = Box<dyn Fn(Status) + Send + Sync>;

struct Shared {
    path: PathBuf,
    /// The file's text as last written or read here, so the watcher only
    /// acts on changes made by others.
    seen: Mutex<Option<String>>,
}

pub struct Caffeine {
    inner: Mutex<Inner>,
    hold: Sender<bool>,
    hold_result: Mutex<Receiver<Result<(), String>>>,
    on_change: Mutex<Option<OnChange>>,
    shared: Option<Shared>,
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

impl Caffeine {
    /// This program's own switch, with the real OS hold. `name` is what the
    /// OS shows for the hold (macOS: `pmset -g assertions`).
    pub fn solo(name: &str) -> Arc<Self> {
        Self::custom(SystemHold::new(name), None)
    }

    /// One switch shared through the file at `path` (made if missing) by
    /// every program that opens the same path. If the file says on, this one
    /// starts holding straight away.
    pub fn shared(name: &str, path: impl Into<PathBuf>) -> Arc<Self> {
        Self::custom(SystemHold::new(name), Some(path.into()))
    }

    /// Any hold, solo (`shared: None`) or shared through a file.
    pub fn custom(hold: impl OsHold + Send + 'static, shared: Option<PathBuf>) -> Arc<Self> {
        Self::build(hold, shared, SHARED_POLL)
    }

    fn build(mut hold: impl OsHold + Send + 'static, shared: Option<PathBuf>, poll: Duration) -> Arc<Self> {
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
        let me = Arc::new(Self {
            inner: Mutex::default(),
            hold: tx,
            hold_result: Mutex::new(res_rx),
            on_change: Mutex::new(None),
            shared: shared.map(|path| Shared { path, seen: Mutex::new(None) }),
        });
        if me.shared.is_some() {
            me.follow_file();
            let weak = Arc::downgrade(&me);
            std::thread::Builder::new()
                .name("caffeine-shared".into())
                .spawn(move || watch(weak, poll))
                .expect("spawn caffeine watcher");
        }
        me
    }

    /// Called with the new status on every change, including a timer running
    /// out and (shared) a change made by another program. One callback; a
    /// second call replaces the first.
    pub fn on_change(&self, f: impl Fn(Status) + Send + Sync + 'static) {
        *lock(&self.on_change) = Some(Box::new(f));
    }

    /// True for [`Caffeine::shared`].
    pub fn is_shared(&self) -> bool {
        self.shared.is_some()
    }

    pub fn status(&self) -> Status {
        status_at(&lock(&self.inner), Instant::now())
    }

    /// On for `minutes`, or indefinitely with None (or 0).
    pub fn activate(self: &Arc<Self>, minutes: Option<u32>) -> Status {
        self.activate_for(minutes.filter(|&m| m > 0).map(|m| Duration::from_secs(m as u64 * 60)))
    }

    /// On for `dur`, or indefinitely with None. On top of a hold it keeps the
    /// one hold and replaces the deadline.
    pub fn activate_for(self: &Arc<Self>, dur: Option<Duration>) -> Status {
        self.apply(true, dur.map(|d| SystemTime::now() + d), true)
    }

    pub fn deactivate(self: &Arc<Self>) -> Status {
        self.apply(false, None, true)
    }

    /// Off when on (timed or not), else on indefinitely.
    pub fn toggle(self: &Arc<Self>) -> Status {
        if lock(&self.inner).active { self.deactivate() } else { self.activate(None) }
    }

    fn os(&self, want: bool) -> Result<(), String> {
        let rx = lock(&self.hold_result);
        self.hold.send(want).map_err(|e| e.to_string())?;
        rx.recv_timeout(Duration::from_secs(2)).unwrap_or_else(|_| Err("caffeine thread did not answer".into()))
    }

    /// Makes the switch `on` (until `until`, or indefinitely) here, and with
    /// `publish` writes it to the shared file too.
    fn apply(self: &Arc<Self>, on: bool, until: Option<SystemTime>, publish: bool) -> Status {
        let mut inner = lock(&self.inner);
        inner.generation += 1;
        inner.error = None;
        if let (true, Some(sh)) = (publish, &self.shared) {
            // `seen` is held across the write, so the watcher never takes
            // this program's own write for someone else's.
            let mut seen = lock(&sh.seen);
            let text = switch_text(on, until);
            match write_switch(&sh.path, &text) {
                Ok(()) => *seen = Some(text),
                Err(e) => inner.error = Some(format!("shared switch {}: {e}", sh.path.display())),
            }
        }
        if !on {
            if inner.active {
                let _ = self.os(false);
            }
            inner.active = false;
            inner.until = None;
            return self.changed(inner);
        }
        if !inner.active {
            if let Err(e) = self.os(true) {
                inner.error = Some(e);
                inner.until = None;
                return self.changed(inner);
            }
            inner.active = true;
        }
        inner.until = until.map(instant_at);
        if let Some(at) = inner.until {
            let (me, generation) = (Arc::downgrade(self), inner.generation);
            std::thread::spawn(move || {
                std::thread::sleep(at.saturating_duration_since(Instant::now()));
                let Some(me) = me.upgrade() else { return };
                let inner = lock(&me.inner);
                if inner.generation == generation && inner.active {
                    drop(inner);
                    // The file already says when it ends: nothing to publish.
                    me.apply(false, None, false);
                }
            });
        }
        self.changed(inner)
    }

    fn changed(&self, inner: MutexGuard<'_, Inner>) -> Status {
        let s = status_at(&inner, Instant::now());
        drop(inner);
        if let Some(f) = lock(&self.on_change).as_ref() {
            f(s.clone());
        }
        s
    }

    /// Reads the shared file and, if someone else changed it, follows it.
    fn follow_file(self: &Arc<Self>) {
        let Some(sh) = &self.shared else { return };
        let text = {
            // Read under `seen` (see apply); let go before applying.
            let mut seen = lock(&sh.seen);
            let Ok(text) = std::fs::read_to_string(&sh.path) else { return };
            if seen.as_deref() == Some(text.as_str()) {
                return;
            }
            *seen = Some(text.clone());
            text
        };
        let Some((on, until)) = parse_switch(&text) else { return };
        let expired = until.is_some_and(|u| u <= SystemTime::now());
        let on = on && !expired;
        // Off and already off: nothing changes, so no change event.
        if !on && !lock(&self.inner).active {
            return;
        }
        self.apply(on, until, false);
    }
}

fn watch(weak: Weak<Caffeine>, poll: Duration) {
    loop {
        std::thread::sleep(poll);
        let Some(me) = weak.upgrade() else { return };
        me.follow_file();
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

/// "∞" indefinite, else "1h", "1h5", "12m" or "40s" (rounded to the nearest
/// second).
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

fn instant_at(t: SystemTime) -> Instant {
    Instant::now() + t.duration_since(SystemTime::now()).unwrap_or(Duration::ZERO)
}

// ---- The shared switch file: one line, `off`, `on`, or `on <unix ms>` (on
// until then). Written whole to a temporary file and renamed over the old
// one, so a reader never sees half a line.

fn switch_text(on: bool, until: Option<SystemTime>) -> String {
    match (on, until) {
        (false, _) => "off\n".into(),
        (true, None) => "on\n".into(),
        (true, Some(t)) => format!("on {}\n", t.duration_since(UNIX_EPOCH).unwrap_or_default().as_millis()),
    }
}

fn parse_switch(text: &str) -> Option<(bool, Option<SystemTime>)> {
    let mut words = text.split_whitespace();
    match (words.next()?, words.next()) {
        ("off", _) => Some((false, None)),
        ("on", None) => Some((true, None)),
        ("on", Some(ms)) => Some((true, Some(UNIX_EPOCH + Duration::from_millis(ms.parse().ok()?)))),
        _ => None,
    }
}

fn write_switch(path: &Path, text: &str) -> std::io::Result<()> {
    if let Some(dir) = path.parent().filter(|d| !d.as_os_str().is_empty()) {
        std::fs::create_dir_all(dir)?;
    }
    let mut tmp = path.as_os_str().to_owned();
    tmp.push(format!(".{}.tmp", std::process::id()));
    std::fs::write(&tmp, text)?;
    // Windows: rename replaces the target, but a reader holding it open can
    // make that fail for a moment; try a few times.
    let mut tries = 0;
    loop {
        match std::fs::rename(&tmp, path) {
            Ok(()) => return Ok(()),
            Err(_) if tries < 5 => {
                tries += 1;
                std::thread::sleep(Duration::from_millis(20));
            }
            Err(e) => {
                let _ = std::fs::remove_file(&tmp);
                return Err(e);
            }
        }
    }
}

/// A neutral place for the shared switch, the same for every program that
/// uses it: `~/Library/Application Support/rsz-caffeine/switch` on macOS,
/// `%LOCALAPPDATA%\rsz-caffeine\switch` on Windows, else
/// `$XDG_STATE_HOME` or `~/.local/state`. None when the home folder is
/// unknown.
pub fn shared_switch_path() -> Option<PathBuf> {
    let env = |k: &str| std::env::var_os(k).filter(|v| !v.is_empty()).map(PathBuf::from);
    let base = if cfg!(target_os = "macos") {
        env("HOME")?.join("Library/Application Support")
    } else if cfg!(windows) {
        env("LOCALAPPDATA")?
    } else {
        env("XDG_STATE_HOME").or_else(|| env("HOME").map(|h| h.join(".local/state")))?
    };
    Some(base.join("rsz-caffeine").join("switch"))
}

/// The real stay-awake hold for this OS. Other systems have none: acquire
/// fails with a plain message.
pub struct SystemHold {
    #[cfg_attr(not(target_os = "macos"), allow(dead_code))]
    name: String,
    held: platform::Held,
}

impl SystemHold {
    /// `name`: what the OS shows for the hold (macOS `pmset -g assertions`).
    pub fn new(name: &str) -> Self {
        Self { name: name.to_owned(), held: Default::default() }
    }
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
        let c = CString::new(s.replace('\0', "")).expect("no NUL");
        unsafe { CFStringCreateWithCString(std::ptr::null(), c.as_ptr(), UTF8) }
    }

    pub type Held = Option<u32>;

    impl super::OsHold for super::SystemHold {
        fn acquire(&mut self) -> Result<(), String> {
            if self.held.is_some() {
                return Ok(());
            }
            let (kind, name) = (cfstr("PreventUserIdleDisplaySleep"), cfstr(&self.name));
            let mut id = 0u32;
            let r = unsafe { IOPMAssertionCreateWithName(kind, LEVEL_ON, name, &mut id) };
            unsafe {
                CFRelease(kind);
                CFRelease(name);
            }
            if r != 0 {
                return Err(format!("IOPMAssertionCreateWithName: 0x{:x}", r as u32));
            }
            self.held = Some(id);
            Ok(())
        }

        fn release(&mut self) -> Result<(), String> {
            if let Some(id) = self.held.take() {
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

    pub type Held = bool;

    /// Always called on the caffeine thread, which owns the state.
    impl super::OsHold for super::SystemHold {
        fn acquire(&mut self) -> Result<(), String> {
            if self.held {
                return Ok(());
            }
            if unsafe { SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED) } == 0 {
                return Err("SetThreadExecutionState failed".into());
            }
            self.held = true;
            Ok(())
        }

        fn release(&mut self) -> Result<(), String> {
            if std::mem::take(&mut self.held) {
                unsafe { SetThreadExecutionState(ES_CONTINUOUS) };
            }
            Ok(())
        }
    }
}

#[cfg(not(any(target_os = "macos", windows)))]
mod platform {
    pub type Held = ();

    impl super::OsHold for super::SystemHold {
        fn acquire(&mut self) -> Result<(), String> {
            Err("caffeine is macOS and Windows only".into())
        }
        fn release(&mut self) -> Result<(), String> {
            Ok(())
        }
    }
}

#[cfg(test)]
mod tests;
