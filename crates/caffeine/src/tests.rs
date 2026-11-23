use super::*;
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};

/// Counts holds; refuses when `fail` is set.
#[derive(Clone, Default)]
struct Fake {
    held: Arc<AtomicI32>,
    fail: Arc<AtomicBool>,
    thread: Arc<Mutex<Option<std::thread::ThreadId>>>,
}
impl OsHold for Fake {
    fn acquire(&mut self) -> Result<(), String> {
        *lock(&self.thread) = Some(std::thread::current().id());
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
impl Fake {
    fn held(&self) -> i32 {
        self.held.load(Ordering::SeqCst)
    }
}

const POLL: Duration = Duration::from_millis(20);

/// Waits up to 2 s for `f`.
fn eventually(f: impl Fn() -> bool) -> bool {
    let end = Instant::now() + Duration::from_secs(2);
    while Instant::now() < end {
        if f() {
            return true;
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    f()
}

fn temp_switch(name: &str) -> PathBuf {
    let dir = std::env::temp_dir().join(format!("rsz-caffeine-test-{}-{name}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    dir.join("sub").join("switch")
}

#[test]
fn labels() {
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
    let c = Caffeine::custom(f.clone(), None);
    assert!(!c.is_shared());
    assert_eq!(
        c.status(),
        Status {
            active: false,
            remaining_s: None,
            label: "".into(),
            error: None
        }
    );
    let s = c.toggle();
    assert!(s.active && s.label == "∞");
    // A timed activation on top keeps the one hold and adds a deadline.
    let s = c.activate(Some(15));
    assert_eq!(f.held(), 1);
    assert_eq!(s.label, "15m");
    assert!(s.remaining_s.unwrap() > 14 * 60);
    assert!(!c.toggle().active);
    assert_eq!(f.held(), 0);
    // The hold is always taken on the caffeine thread, never the caller's.
    assert_ne!(*lock(&f.thread), Some(std::thread::current().id()));
}

#[test]
fn on_indefinitely_then_toggle_off_and_on() {
    let f = Fake::default();
    let c = Caffeine::custom(f.clone(), None);
    let s = c.activate(None);
    assert_eq!(
        s,
        Status {
            active: true,
            remaining_s: None,
            label: "∞".into(),
            error: None
        }
    );
    assert_eq!(f.held(), 1);
    assert!(!c.toggle().active);
    assert_eq!(f.held(), 0);
    assert_eq!(c.toggle().label, "∞");
    assert_eq!(f.held(), 1);
    assert_eq!(
        c.activate(Some(0)).label,
        "∞",
        "0 minutes means indefinitely"
    );
}

#[test]
fn os_refusal_is_reported_and_stays_off() {
    let f = Fake::default();
    f.fail.store(true, Ordering::SeqCst);
    let c = Caffeine::custom(f.clone(), None);
    let s = c.activate(None);
    assert!(!s.active);
    assert_eq!(s.error.as_deref(), Some("nope"));
    assert_eq!(c.deactivate().error, None);
}

#[test]
fn timed_hold_expires_and_tells_the_program() {
    let f = Fake::default();
    let c = Caffeine::custom(f.clone(), None);
    let seen = Arc::new(Mutex::new(Vec::new()));
    let s2 = Arc::clone(&seen);
    c.on_change(move |s| lock(&s2).push(s.active));
    c.activate_for(Some(Duration::from_millis(60)));
    assert_eq!(f.held(), 1);
    assert!(eventually(|| !c.status().active));
    assert_eq!(*lock(&seen), vec![true, false]);
    assert_eq!(f.held(), 0);
}

#[test]
fn a_stale_timer_does_not_turn_off_a_newer_hold() {
    let f = Fake::default();
    let c = Caffeine::custom(f.clone(), None);
    c.activate_for(Some(Duration::from_millis(60)));
    c.activate(None); // indefinite now; the 60 ms timer is stale
    std::thread::sleep(Duration::from_millis(300));
    assert!(c.status().active);
    assert_eq!(c.status().label, "∞");
    assert_eq!(f.held(), 1);
}

#[test]
fn dropping_it_releases_the_hold() {
    let f = Fake::default();
    let c = Caffeine::custom(f.clone(), None);
    c.activate(Some(60));
    assert_eq!(f.held(), 1);
    drop(c); // the timer thread holds only a weak reference
    assert!(eventually(|| f.held() == 0));
}

#[test]
fn switch_file_round_trips() {
    assert_eq!(parse_switch(&switch_text(false, None)), Some((false, None)));
    assert_eq!(parse_switch(&switch_text(true, None)), Some((true, None)));
    let t = UNIX_EPOCH + Duration::from_millis(1_800_000_000_123);
    assert_eq!(switch_text(true, Some(t)), "on 1800000000123\n");
    assert_eq!(
        parse_switch(&switch_text(true, Some(t))),
        Some((true, Some(t)))
    );
    assert_eq!(parse_switch(""), None);
    assert_eq!(parse_switch("maybe"), None);
    assert_eq!(parse_switch("on soon"), None);
}

/// Two programs on one switch: either turns it on or off for both.
#[test]
fn shared_switch_is_one_switch_for_every_program() {
    let path = temp_switch("one");
    let (fa, fb) = (Fake::default(), Fake::default());
    let a = Caffeine::build(fa.clone(), Some(path.clone()), POLL);
    let b = Caffeine::build(fb.clone(), Some(path.clone()), POLL);
    assert!(a.is_shared());
    assert!(!a.status().active && !b.status().active);

    let b_seen = Arc::new(Mutex::new(Vec::new()));
    let s2 = Arc::clone(&b_seen);
    b.on_change(move |s| lock(&s2).push(s.label));

    a.activate(None);
    assert_eq!(std::fs::read_to_string(&path).unwrap(), "on\n");
    assert!(eventually(|| b.status().active && fb.held() == 1));
    assert_eq!(b.status().label, "∞");

    b.deactivate();
    assert!(eventually(|| !a.status().active && fa.held() == 0));

    b.activate(Some(15));
    assert!(eventually(|| a.status().label == "15m" && fa.held() == 1));

    assert_eq!(
        *lock(&b_seen),
        vec!["∞".to_string(), "".into(), "15m".into()]
    );
    let _ = std::fs::remove_dir_all(path.parent().unwrap().parent().unwrap());
}

/// A program that opens the switch while it is on starts holding; one that
/// opens it after the time ran out does not.
#[test]
fn shared_switch_is_followed_at_start_and_expires_everywhere() {
    let path = temp_switch("start");
    let fa = Fake::default();
    let a = Caffeine::build(fa.clone(), Some(path.clone()), POLL);
    a.activate_for(Some(Duration::from_millis(400)));

    let fb = Fake::default();
    let b = Caffeine::build(fb.clone(), Some(path.clone()), POLL);
    assert!(b.status().active, "on at start");
    assert_eq!(fb.held(), 1);

    assert!(eventually(|| !a.status().active && !b.status().active));
    assert_eq!((fa.held(), fb.held()), (0, 0));

    let fc = Fake::default();
    let c = Caffeine::build(fc.clone(), Some(path.clone()), POLL);
    std::thread::sleep(POLL * 5);
    assert!(!c.status().active, "expired before it started");
    assert_eq!(fc.held(), 0);
    let _ = std::fs::remove_dir_all(path.parent().unwrap().parent().unwrap());
}

/// Quitting one program leaves the others holding.
#[test]
fn shared_switch_outlives_one_program() {
    let path = temp_switch("outlive");
    let (fa, fb) = (Fake::default(), Fake::default());
    let a = Caffeine::build(fa.clone(), Some(path.clone()), POLL);
    let b = Caffeine::build(fb.clone(), Some(path.clone()), POLL);
    a.activate(None);
    assert!(eventually(|| fb.held() == 1));
    drop(a);
    assert!(eventually(|| fa.held() == 0));
    std::thread::sleep(POLL * 5);
    assert!(b.status().active);
    assert_eq!(fb.held(), 1);
    let _ = std::fs::remove_dir_all(path.parent().unwrap().parent().unwrap());
}

/// A switch file nobody can write is an error on the status, and the hold
/// here still follows the call.
#[test]
fn unwritable_shared_switch_is_reported() {
    let dir = temp_switch("unwritable");
    let parent = dir.parent().unwrap().to_path_buf();
    std::fs::create_dir_all(parent.parent().unwrap()).unwrap();
    std::fs::write(&parent, "a file where a folder should be").unwrap();
    let f = Fake::default();
    let c = Caffeine::build(f.clone(), Some(dir.clone()), POLL);
    let s = c.activate(None);
    assert!(s.active);
    assert!(
        s.error
            .as_deref()
            .unwrap_or("")
            .starts_with("shared switch"),
        "{s:?}"
    );
    let _ = std::fs::remove_dir_all(parent.parent().unwrap());
}

#[test]
fn shared_switch_path_is_per_user() {
    if let Some(p) = shared_switch_path() {
        assert!(
            p.ends_with("rsz-caffeine/switch") || p.ends_with("rsz-caffeine\\switch"),
            "{}",
            p.display()
        );
    }
}

/// The real IOPMAssertion shows in pmset while held and is gone after.
#[cfg(target_os = "macos")]
#[test]
fn macos_assertion_is_visible_to_pmset() {
    let held = || {
        let out = std::process::Command::new("pmset")
            .args(["-g", "assertions"])
            .output()
            .unwrap();
        // Only this test process's own assertion: another program may hold one too.
        let me = format!("pid {}(", std::process::id());
        String::from_utf8_lossy(&out.stdout)
            .lines()
            .any(|l| l.contains(&me) && l.contains("rsz-caffeine test"))
    };
    let c = Caffeine::solo("rsz-caffeine test");
    assert!(c.activate(None).active);
    assert!(held(), "assertion missing while on");
    c.deactivate();
    assert!(!held(), "assertion left behind after off");
}

/// The real Windows hold: taken and dropped on the caffeine thread without
/// an error.
#[cfg(windows)]
#[test]
fn windows_execution_state_is_taken_and_dropped() {
    let c = Caffeine::solo("rsz-caffeine test");
    let s = c.activate(None);
    assert!(s.active && s.error.is_none(), "{s:?}");
    let s = c.deactivate();
    assert!(!s.active && s.error.is_none(), "{s:?}");
}
