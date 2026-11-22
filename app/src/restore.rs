//! Terminals back after a reboot or a logout (card hearth#916). Kept shells
//! (keeper.rs) outlive the app but not the computer; this saves what is
//! needed to start over: the tabs and splits the UI last saved, each pane's
//! folder, its last [`KEEP_LINES`] lines of text, and the Claude Code
//! conversation in front of it, if any. The next start that finds no kept
//! shell to put back opens a fresh shell in each folder, the old text above
//! it (PaneConfig::preamble), and offers `claude --resume` without running it.
//!
//! On by default (Stephen's choice on the card). The files are plain text,
//! private to the user: `<config>/restore/<app pid>/`, 0700 folders with
//! 0600 files (`session.json`, `<pane id>.txt`); each window is its own app
//! process and keeps its own. A start takes the sessions of processes that
//! are gone (or ran before the last boot) and removes them. Closing the last
//! pane on purpose (the quit dialog, or `exit` in it) clears this process's.
//! Settings › Session › "Bring back after a reboot" (config key
//! `restore_sessions`, card hearth#951) turns it off: that clears the whole
//! restore folder at once, and every window reads the setting before each
//! write, so none writes again (each clears its own folder if one is left)
//! until it is turned back on. RSZ_RESTORE=0 turns it off for one run.
//!
//! Written every [`TICK`] while the app runs, a pane's text only when it
//! printed something since, and once more as the app exits.

use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::{Arc, OnceLock};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use parking_lot::Mutex;
use serde_json::{Value, json};

use crate::Panes;

/// Lines of text kept per pane.
pub const KEEP_LINES: usize = 2000;
const TICK: Duration = Duration::from_secs(3);
/// After a shell is killed (not ended by itself), how long the saved
/// session is left as it was: a logout or shutdown kills every shell before
/// the app, and the panes going one by one must not shrink or clear it.
const HOLD: Duration = Duration::from_secs(10);

/// Not turned off for this run (RSZ_RESTORE=0, benches and tests).
fn allowed() -> bool {
    let set = |k: &str| std::env::var(k).is_ok_and(|v| !v.is_empty());
    std::env::var("RSZ_RESTORE").as_deref() != Ok("0") && !["RSZ_BENCH", "RSZ_PANES", "RSZ_UI_TEST", "RSZ_CMD"].iter().any(|k| set(k))
}

/// The Settings switch (`restore_sessions`), read from the file each time so
/// a change in any window reaches every one.
fn setting_on() -> bool {
    is_on(&crate::config::load())
}

fn is_on(cfg: &Value) -> bool {
    cfg["restore_sessions"].as_bool() != Some(false)
}

pub fn enabled() -> bool {
    allowed() && setting_on()
}

/// The Settings switch was turned off: remove every saved session (every
/// window's), now. This process writes nothing more while it is off
/// ([`Restore::save`] reads the setting under the same lock).
pub fn turned_off() {
    let _g = get().map(|r| r.write.lock());
    if let Some(r) = get() {
        r.written.lock().clear();
    }
    clear(&dir());
}


/// `<config>/restore`: beside config.json (RSZ_CONFIG_DIR wins).
pub fn dir() -> PathBuf {
    crate::config::path().parent().map(Path::to_path_buf).unwrap_or_else(std::env::temp_dir).join("restore")
}

pub struct Restore {
    dir: PathBuf,
    panes: Arc<Panes>,
    /// The UI's last layout (keeper's layout_save shape).
    layout: Mutex<Option<Value>>,
    /// Per pane: the output time its text file was written for.
    written: Mutex<HashMap<u32, Instant>>,
    /// The session was cleared on purpose: write nothing more.
    off: std::sync::atomic::AtomicBool,
    /// The app is exiting (a quit, an update's restart): the last save is
    /// written, and a shell ending in the shutdown clears nothing.
    closing: std::sync::atomic::AtomicBool,
    /// When a shell was last killed (see [`HOLD`]).
    lost_at: Mutex<Option<Instant>>,
    write: Mutex<()>,
}

static RESTORE: OnceLock<Arc<Restore>> = OnceLock::new();

pub fn get() -> Option<&'static Arc<Restore>> {
    RESTORE.get()
}

/// Start saving (once, unless turned off for this run). The thread runs
/// while the Settings switch is off too, so turning it on saves again.
pub fn start(panes: Arc<Panes>) {
    if !allowed() {
        return;
    }
    let r = Arc::new(Restore { dir: dir().join(std::process::id().to_string()), panes, layout: Mutex::new(None), written: Mutex::default(), off: false.into(), closing: false.into(), lost_at: Mutex::new(None), write: Mutex::new(()) });
    if RESTORE.set(Arc::clone(&r)).is_err() {
        return;
    }
    std::thread::Builder::new()
        .name("rsz-restore".into())
        .spawn(move || loop {
            std::thread::sleep(TICK);
            r.save();
        })
        .ok();
}

impl Restore {
    /// The UI's tabs and splits changed.
    pub fn set_layout(&self, layout: Value) {
        *self.layout.lock() = Some(layout);
    }

    /// A shell's host was killed: hold the saved session for [`HOLD`].
    pub fn shell_lost(&self) {
        *self.lost_at.lock() = Some(Instant::now());
    }

    fn holding(&self) -> bool {
        self.lost_at.lock().is_some_and(|t| t.elapsed() < HOLD)
    }

    /// The person closed the last pane: nothing to bring back.
    pub fn forget(&self) {
        if self.closing.load(std::sync::atomic::Ordering::Relaxed) || self.holding() {
            return;
        }
        self.off.store(true, std::sync::atomic::Ordering::Relaxed);
        let _g = self.write.lock();
        clear(&self.dir);
    }

    /// The app is exiting: write the session one last time.
    pub fn last_save(&self) {
        self.closing.store(true, std::sync::atomic::Ordering::Relaxed);
        self.save();
    }

    /// Write the session as it is now, if the Settings switch is on.
    pub fn save(&self) {
        self.save_if(&setting_on)
    }

    fn save_if(&self, on: &dyn Fn() -> bool) {
        if self.off.load(std::sync::atomic::Ordering::Relaxed) || self.holding() {
            return;
        }
        let Some(layout) = self.layout.lock().clone() else { return };
        let _g = self.write.lock();
        if !on() {
            // Off in Settings (maybe in another window): nothing kept.
            if self.dir.exists() {
                clear(&self.dir);
            }
            self.written.lock().clear();
            return;
        }
        if let Err(e) = self.save_locked(layout) {
            log::warn!("restore: not saved: {e}");
        }
    }

    fn save_locked(&self, mut layout: Value) -> std::io::Result<()> {
        ensure(&self.dir)?;
        let mut ids = Vec::new();
        for t in layout["tabs"].as_array_mut().into_iter().flatten() {
            leaves(&mut t["tree"], &mut |leaf| {
                let Some(id) = leaf["id"].as_u64().map(|n| n as u32) else { return };
                let Some(shell) = self.panes.get(id) else { return };
                ids.push(id);
                let (name, pid, cwd) = shell.program();
                if let Some(cwd) = cwd {
                    leaf["cwd"] = cwd.into();
                }
                if let Some(c) = pid.and_then(claude_session) {
                    leaf["claude"] = c;
                } else if let Some(n) = name {
                    leaf["program"] = n.into();
                }
                let at = shell.last_output_at();
                if self.written.lock().get(&id) == Some(&at) {
                    return;
                }
                let mut lines = shell.history_tail(KEEP_LINES);
                if !shell.alt_screen() {
                    lines.extend(shell.screen_text().lines().map(str::to_string));
                }
                let text = tail_text(lines, KEEP_LINES);
                match write_private(&self.dir.join(format!("{id}.txt")), text.as_bytes()) {
                    Ok(()) => {
                        self.written.lock().insert(id, at);
                    }
                    Err(e) => log::warn!("restore: pane {id}: {e}"),
                }
            });
        }
        layout["v"] = 1.into();
        layout["boot"] = boot_stamp().into();
        layout["saved_at"] = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_millis() as u64).unwrap_or(0).into();
        write_private(&self.dir.join("session.json"), layout.to_string().as_bytes())?;
        // Text of panes that are gone.
        let keep: HashSet<u32> = ids.into_iter().collect();
        self.written.lock().retain(|id, _| keep.contains(id));
        for (id, path) in texts(&self.dir) {
            if !keep.contains(&id) {
                let _ = std::fs::remove_file(path);
            }
        }
        Ok(())
    }
}

/// The session the last run left, each leaf with its `text`; None when
/// there is none (or restore is off).
pub fn take() -> Option<Value> {
    take_if(enabled(), &dir())
}

/// Off: nothing is brought back (the start opens one fresh pane), whatever
/// is in `root`.
fn take_if(on: bool, root: &Path) -> Option<Value> {
    if !on {
        return None;
    }
    take_from(root, std::process::id(), boot_stamp(), alive)
}

/// The sessions in `root` left by processes that are gone (not `me`, not
/// alive, or saved before this boot: a pid can be reused after a reboot),
/// newest first, as one: their tabs in turn, the newest one's tab in front.
/// Their folders are removed: this process saves its own from now on.
fn take_from(root: &Path, me: u32, boot: Option<u64>, alive: fn(u32) -> bool) -> Option<Value> {
    let mut found: Vec<(u64, Value)> = Vec::new();
    for e in std::fs::read_dir(root).into_iter().flatten().flatten() {
        let Some(pid) = e.file_name().to_str().and_then(|n| n.parse::<u32>().ok()) else { continue };
        if pid == me {
            continue;
        }
        let path = e.path();
        let Some(v) = load(&path) else {
            let _ = std::fs::remove_dir_all(&path);
            continue;
        };
        let same_boot = matches!((boot, v["boot"].as_u64()), (Some(a), Some(b)) if a == b) || boot.is_none();
        if same_boot && alive(pid) {
            continue;
        }
        found.push((v["saved_at"].as_u64().unwrap_or(0), v));
        let _ = std::fs::remove_dir_all(&path);
    }
    found.sort_by(|a, b| b.0.cmp(&a.0));
    let (saved_at, first) = found.first()?.clone();
    let active = first["active"].as_u64().unwrap_or(0);
    let tabs: Vec<Value> = found.into_iter().flat_map(|(_, v)| v["tabs"].as_array().cloned().unwrap_or_default()).collect();
    Some(json!({ "v": 1, "active": active, "saved_at": saved_at, "tabs": tabs }))
}

/// Is process `pid` running?
fn alive(pid: u32) -> bool {
    #[cfg(unix)]
    {
        // SAFETY: signal 0 only checks that the process exists.
        unsafe { libc::kill(pid as i32, 0) == 0 || std::io::Error::last_os_error().raw_os_error() == Some(libc::EPERM) }
    }
    #[cfg(windows)]
    {
        use windows_sys::Win32::Foundation::{CloseHandle, STILL_ACTIVE};
        use windows_sys::Win32::System::Threading::{GetExitCodeProcess, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION};
        // SAFETY: a plain query; the handle is closed.
        unsafe {
            let h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
            if h.is_null() {
                return false;
            }
            let mut code = 0u32;
            let ok = GetExitCodeProcess(h, &mut code) != 0;
            CloseHandle(h);
            ok && code == STILL_ACTIVE as u32
        }
    }
}

/// When the computer last started (seconds), where the OS says: macOS and
/// Linux. A session saved under another boot is never a live window's.
fn boot_stamp() -> Option<u64> {
    #[cfg(target_os = "macos")]
    {
        let mut tv: libc::timeval = unsafe { std::mem::zeroed() };
        let mut len = std::mem::size_of::<libc::timeval>();
        // SAFETY: sysctlbyname fills at most len bytes of tv.
        let ok = unsafe { libc::sysctlbyname(c"kern.boottime".as_ptr(), (&mut tv as *mut libc::timeval).cast(), &mut len, std::ptr::null_mut(), 0) } == 0;
        ok.then_some(tv.tv_sec as u64)
    }
    #[cfg(target_os = "linux")]
    {
        let stat = std::fs::read_to_string("/proc/stat").ok()?;
        stat.lines().find_map(|l| l.strip_prefix("btime ")).and_then(|v| v.trim().parse().ok())
    }
    #[cfg(not(any(target_os = "macos", target_os = "linux")))]
    {
        None
    }
}

pub fn load(dir: &Path) -> Option<Value> {
    let mut v: Value = serde_json::from_slice(&std::fs::read(dir.join("session.json")).ok()?).ok()?;
    let mut any = false;
    for t in v["tabs"].as_array_mut().into_iter().flatten() {
        leaves(&mut t["tree"], &mut |leaf| {
            any = true;
            if let Some(id) = leaf["id"].as_u64() {
                if let Ok(text) = std::fs::read_to_string(dir.join(format!("{id}.txt"))) {
                    leaf["text"] = text.into();
                }
            }
        });
    }
    any.then_some(v)
}

/// Each `{id}` leaf of a saved tree (`{dir, a, b}` splits).
fn leaves(n: &mut Value, f: &mut dyn FnMut(&mut Value)) {
    if n.get("id").is_some() {
        return f(n);
    }
    if n.get("a").is_some() {
        leaves(&mut n["a"], f);
        leaves(&mut n["b"], f);
    }
}

/// The last `max` lines, trailing blank ones dropped.
fn tail_text(mut lines: Vec<String>, max: usize) -> String {
    while lines.last().is_some_and(|l| l.trim().is_empty()) {
        lines.pop();
    }
    let from = lines.len().saturating_sub(max);
    lines[from..].iter().map(|l| l.trim_end()).collect::<Vec<_>>().join("\n")
}

/// Claude Code's own record of process `pid`: `~/.claude/sessions/<pid>.json`
/// (CLAUDE_CONFIG_DIR wins), with the conversation's id and folder.
fn claude_session(pid: u32) -> Option<Value> {
    let base = std::env::var_os("CLAUDE_CONFIG_DIR")
        .filter(|d| !d.is_empty())
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").or_else(|| std::env::var_os("USERPROFILE")).map(|h| PathBuf::from(h).join(".claude")))?;
    claude_session_in(&base.join("sessions"), pid)
}

fn claude_session_in(dir: &Path, pid: u32) -> Option<Value> {
    let v: Value = serde_json::from_slice(&std::fs::read(dir.join(format!("{pid}.json"))).ok()?).ok()?;
    let id = v["sessionId"].as_str().filter(|s| is_session_id(s))?;
    Some(json!({ "session": id, "cwd": v["cwd"].as_str() }))
}

/// A conversation id is a UUID: it goes into a command line.
fn is_session_id(s: &str) -> bool {
    !s.is_empty() && s.len() <= 64 && s.chars().all(|c| c.is_ascii_hexdigit() || c == '-')
}

fn texts(dir: &Path) -> Vec<(u32, PathBuf)> {
    std::fs::read_dir(dir)
        .into_iter()
        .flatten()
        .flatten()
        .filter_map(|e| {
            let name = e.file_name().to_string_lossy().into_owned();
            Some((name.strip_suffix(".txt")?.parse().ok()?, e.path()))
        })
        .collect()
}

fn clear(dir: &Path) {
    let _ = std::fs::remove_dir_all(dir);
}

/// `dir` and the restore folder above it, private to this user.
fn ensure(dir: &Path) -> std::io::Result<()> {
    std::fs::create_dir_all(dir)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        for d in [Some(dir), dir.parent()].into_iter().flatten() {
            std::fs::set_permissions(d, std::fs::Permissions::from_mode(0o700))?;
        }
    }
    Ok(())
}

/// Write through a temp file, readable by this user only.
fn write_private(path: &Path, bytes: &[u8]) -> std::io::Result<()> {
    use std::io::Write;
    let tmp = path.with_extension("tmp");
    let mut o = std::fs::OpenOptions::new();
    o.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        o.mode(0o600);
    }
    let mut f = o.open(&tmp)?;
    f.write_all(bytes)?;
    drop(f);
    std::fs::rename(tmp, path)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn scratch(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("rsz-restore-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        d
    }

    #[test]
    fn tail_keeps_the_last_lines_without_trailing_blanks() {
        let lines = (1..=10).map(|i| format!("l{i}  ")).chain(["".into(), "  ".into()]).collect();
        assert_eq!(tail_text(lines, 3), "l8\nl9\nl10");
        assert_eq!(tail_text(vec![], 3), "");
    }

    #[test]
    fn load_puts_each_panes_text_on_its_leaf() {
        let d = scratch("load").join("9");
        ensure(&d).unwrap();
        let s = json!({ "v": 1, "active": 0, "tabs": [{ "name": "", "tree": { "dir": "h", "a": { "id": 3, "cwd": "/tmp" }, "b": { "id": 4 } } }] });
        write_private(&d.join("session.json"), s.to_string().as_bytes()).unwrap();
        write_private(&d.join("3.txt"), b"hello\nthere").unwrap();
        let v = load(&d).unwrap();
        assert_eq!(v["tabs"][0]["tree"]["a"]["text"], "hello\nthere");
        assert_eq!(v["tabs"][0]["tree"]["a"]["cwd"], "/tmp");
        assert!(v["tabs"][0]["tree"]["b"].get("text").is_none());
        clear(&d);
        assert!(load(&d).is_none(), "cleared");
        assert!(!d.exists());
        let _ = std::fs::remove_dir_all(d.parent().unwrap());
    }

    fn session(root: &Path, pid: u32, saved_at: u64, boot: u64, cwd: &str) {
        let d = root.join(pid.to_string());
        ensure(&d).unwrap();
        let s = json!({ "v": 1, "active": 0, "saved_at": saved_at, "boot": boot, "tabs": [{ "name": cwd, "tree": { "id": 1, "cwd": cwd } }] });
        write_private(&d.join("session.json"), s.to_string().as_bytes()).unwrap();
        write_private(&d.join("1.txt"), cwd.as_bytes()).unwrap();
    }

    #[test]
    fn a_start_takes_only_the_sessions_of_processes_that_are_gone() {
        let root = scratch("take");
        session(&root, 100, 10, 7, "/me");
        session(&root, 200, 20, 7, "/live-window");
        session(&root, 300, 30, 7, "/quit");
        session(&root, 400, 40, 6, "/before-reboot");
        // 200 and 400 are running pids; 400's session is from the last boot.
        let alive = |pid: u32| pid == 200 || pid == 400;
        let v = take_from(&root, 100, Some(7), alive).unwrap();
        let names: Vec<_> = v["tabs"].as_array().unwrap().iter().map(|t| t["name"].as_str().unwrap().to_string()).collect();
        assert_eq!(names, ["/before-reboot", "/quit"], "newest first; not mine, not a live window's");
        assert_eq!(v["tabs"][1]["tree"]["text"], "/quit");
        assert_eq!(v["saved_at"], 40);
        assert!(root.join("100").exists() && root.join("200").exists(), "mine and the live window's stay");
        assert!(!root.join("300").exists() && !root.join("400").exists(), "taken ones are removed");
        assert!(take_from(&root, 100, Some(7), alive).is_none(), "taken once");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn the_switch_off_clears_every_session_and_writes_nothing_until_on() {
        let root = scratch("switch");
        let cfg = scratch("switch-cfg").join("config.json");
        let on = || is_on(&crate::config::load_from(&cfg, &cfg));
        let set = |v: bool| crate::config::update_at(&cfg, &cfg, json!({ "restore_sessions": v })).unwrap();
        assert!(on(), "on by default");
        let d = root.join("7");
        let r = Restore { dir: d.clone(), panes: Arc::default(), layout: Mutex::new(Some(json!({ "active": 0, "tabs": [{ "name": "t", "tree": { "id": 1 } }] }))), written: Mutex::default(), off: false.into(), closing: false.into(), lost_at: Mutex::new(None), write: Mutex::new(()) };
        r.save_if(&on);
        session(&root, 8, 1, 1, "/another-window");
        assert!(d.join("session.json").exists() && root.join("8").exists(), "on: saved");
        // Turned off in Settings: config_set saves the key, then turned_off
        // clears the whole restore folder.
        set(false);
        clear(&root);
        assert!(!root.exists(), "off: everything in restore/ is removed");
        for _ in 0..3 {
            r.save_if(&on);
        }
        assert!(!root.exists(), "off: nothing more is written");
        // Off set in another window, with this one's folder still there: its
        // next tick removes it.
        ensure(&d).unwrap();
        r.save_if(&on);
        assert!(!d.exists(), "off: a window clears its own folder on its next tick");
        set(true);
        r.save_if(&on);
        assert!(d.join("session.json").exists(), "on again: saved on the next tick (every 3 s)");
        let _ = std::fs::remove_dir_all(&root);
        let _ = std::fs::remove_dir_all(cfg.parent().unwrap());
    }

    #[test]
    fn the_switch_off_brings_nothing_back() {
        let root = scratch("take-off");
        // A session left before a reboot, by a process that is gone.
        session(&root, u32::MAX - 1, 10, 1, "/before-reboot");
        assert!(take_if(false, &root).is_none(), "off: one fresh pane");
        assert!(take_if(true, &root).is_some(), "on: it comes back");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn an_empty_session_is_none() {
        let d = scratch("empty").join("9");
        ensure(&d).unwrap();
        write_private(&d.join("session.json"), br#"{"v":1,"tabs":[]}"#).unwrap();
        assert!(load(&d).is_none());
        let _ = std::fs::remove_dir_all(d.parent().unwrap());
    }

    #[cfg(unix)]
    #[test]
    fn files_are_private() {
        use std::os::unix::fs::PermissionsExt;
        let root = scratch("perm");
        let d = root.join("42");
        ensure(&d).unwrap();
        write_private(&d.join("1.txt"), b"secret").unwrap();
        let mode = |p: &Path| std::fs::metadata(p).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode(&root), 0o700);
        assert_eq!(mode(&d), 0o700);
        assert_eq!(mode(&d.join("1.txt")), 0o600);
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn claude_session_is_read_by_pid_and_checked() {
        let d = scratch("claude");
        std::fs::create_dir_all(&d).unwrap();
        std::fs::write(d.join("42.json"), r#"{"pid":42,"sessionId":"093417ed-34d5-48b1-8e1a-f7698077b897","cwd":"/w"}"#).unwrap();
        std::fs::write(d.join("43.json"), r#"{"pid":43,"sessionId":"x; rm -rf ~","cwd":"/w"}"#).unwrap();
        let c = claude_session_in(&d, 42).unwrap();
        assert_eq!(c["session"], "093417ed-34d5-48b1-8e1a-f7698077b897");
        assert_eq!(c["cwd"], "/w");
        assert!(claude_session_in(&d, 43).is_none(), "not an id: never offered");
        assert!(claude_session_in(&d, 44).is_none());
        let _ = std::fs::remove_dir_all(&d);
    }
}
