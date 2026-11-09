//! App log (Go internal/applog): suzuri.log in the OS log dir (macOS
//! ~/Library/Logs/suzuri, Windows %LOCALAPPDATA%\suzuri\logs), mirrored to
//! stderr when that is a terminal. SUZURI_LOG_LEVEL (or RSZ_LOG_LEVEL) sets
//! the level: off, error, warn, info, debug (the default, as in Go) or trace.
//! The file rolls over to suzuri.log.1 past 5 MB.
//!
//! A panic writes suzuri-crash.txt next to the log: the message, where it
//! happened, a backtrace, the version and OS, and the last lines logged (the
//! trail). The next launch shows a notice saying where it is, once, and
//! renames it to suzuri-crash.prev.txt. Only a panic on the main thread
//! takes the app down; one on a bridge, pane or async worker thread is
//! reported as an error, not a crash.

use std::collections::VecDeque;
use std::fs::{File, OpenOptions};
use std::io::{IsTerminal, Write};
use std::path::{Path, PathBuf};
use std::sync::OnceLock;

use log::{Level, LevelFilter, Log, Metadata, Record};
use parking_lot::Mutex;

pub const FILE: &str = "suzuri.log";
pub const CRASH: &str = "suzuri-crash.txt";
pub const CRASH_SEEN: &str = "suzuri-crash.prev.txt";
const MAX_BYTES: u64 = 5 << 20;
const TRAIL: usize = 64;
pub const TAIL_DEFAULT: usize = 150;
pub const TAIL_MAX: usize = 5000;

/// Where the log and crash files go. RSZ_CONFIG_DIR (a dev or test
/// instance) keeps its logs in its own logs/ folder.
pub fn dir() -> PathBuf {
    if let Some(d) = std::env::var_os("RSZ_CONFIG_DIR").filter(|d| !d.is_empty()) {
        return PathBuf::from(d).join("logs");
    }
    let env = |k: &str| std::env::var_os(k).filter(|d| !d.is_empty()).map(PathBuf::from);
    if cfg!(windows) {
        if let Some(base) = env("LOCALAPPDATA") {
            return base.join("suzuri").join("logs");
        }
    } else if cfg!(target_os = "macos") {
        if let Some(h) = env("HOME") {
            return h.join("Library/Logs/suzuri");
        }
    } else if let Some(x) = env("XDG_STATE_HOME") {
        return x.join("suzuri");
    } else if let Some(h) = env("HOME") {
        return h.join(".local/state/suzuri");
    }
    std::env::temp_dir().join("suzuri-logs")
}

pub fn path() -> PathBuf {
    dir().join(FILE)
}

/// SUZURI_LOG_LEVEL, then RSZ_LOG_LEVEL; debug when unset or unknown.
pub fn level_from(suzuri: Option<&str>, rsz: Option<&str>) -> LevelFilter {
    [suzuri, rsz]
        .into_iter()
        .flatten()
        .map(str::trim)
        .find(|v| !v.is_empty())
        .and_then(|v| v.parse::<LevelFilter>().ok())
        .unwrap_or(LevelFilter::Debug)
}

/// Other crates (tauri, wry, tao) log a lot; let their warnings through
/// and the rest only at trace.
fn allowed(level: LevelFilter, target: &str, at: Level) -> bool {
    let ours = target == "suzuri" || target.starts_with("suzuri::") || target == "ui" || target.starts_with("rsz");
    if ours || level == LevelFilter::Trace { at <= level } else { at <= level.min(LevelFilter::Warn) }
}

struct Sink {
    file: Option<File>,
    path: PathBuf,
    written: u64,
    trail: VecDeque<String>,
}

impl Sink {
    fn open(path: &Path) -> Self {
        let _ = std::fs::create_dir_all(path.parent().unwrap_or(Path::new(".")));
        rotate_if_big(path, MAX_BYTES);
        let file = OpenOptions::new().create(true).append(true).open(path).ok();
        let written = file.as_ref().and_then(|f| f.metadata().ok()).map_or(0, |m| m.len());
        Self { file, path: path.to_path_buf(), written, trail: VecDeque::new() }
    }

    fn write(&mut self, line: &str) {
        if self.trail.len() == TRAIL {
            self.trail.pop_front();
        }
        self.trail.push_back(line.to_string());
        if let Some(f) = &mut self.file
            && f.write_all(line.as_bytes()).is_ok()
        {
            self.written += line.len() as u64;
        }
        if self.written > MAX_BYTES {
            // Close, roll over, reopen. Another window may hold it open; a
            // failed rename just keeps appending.
            self.file = None;
            let fresh = Self::open(&self.path);
            self.file = fresh.file;
            self.written = fresh.written;
        }
    }
}

/// suzuri.log -> suzuri.log.1 once it passes `max` bytes.
pub fn rotate_if_big(path: &Path, max: u64) -> bool {
    match std::fs::metadata(path) {
        Ok(m) if m.len() > max => std::fs::rename(path, path.with_extension("log.1")).is_ok(),
        _ => false,
    }
}

struct Logger {
    level: LevelFilter,
    stderr: bool,
    sink: Mutex<Sink>,
}

static LOGGER: OnceLock<Logger> = OnceLock::new();

impl Log for Logger {
    fn enabled(&self, m: &Metadata) -> bool {
        allowed(self.level, m.target(), m.level())
    }

    fn log(&self, r: &Record) {
        if !self.enabled(r.metadata()) {
            return;
        }
        let line = format_line(&crate::bridge::rfc3339_now(), r.level(), r.target(), &r.args().to_string());
        if self.stderr {
            eprint!("{line}");
        }
        self.sink.lock().write(&line);
    }

    fn flush(&self) {
        if let Some(f) = &self.sink.lock().file {
            let _ = f.sync_data();
        }
    }
}

pub fn format_line(at: &str, level: Level, target: &str, msg: &str) -> String {
    let target = target.strip_prefix("suzuri::").unwrap_or(target);
    format!("{at} {level:<5} pid={} {target}: {msg}\n", std::process::id())
}

/// Open the log and install the logger and the panic hook. Call once, first
/// thing in main. Without a writable log dir it logs to stderr only.
pub fn init() {
    let level = level_from(std::env::var("SUZURI_LOG_LEVEL").ok().as_deref(), std::env::var("RSZ_LOG_LEVEL").ok().as_deref());
    let logger = Logger { level, stderr: std::io::stderr().is_terminal(), sink: Mutex::new(Sink::open(&path())) };
    let opened = logger.sink.lock().file.is_some();
    if LOGGER.set(logger).is_err() {
        return;
    }
    if log::set_logger(LOGGER.get().unwrap()).is_ok() {
        log::set_max_level(level);
    }
    install_panic_hook();
    if opened {
        log::info!("suzuri {} on {}/{}, log {}", env!("CARGO_PKG_VERSION"), std::env::consts::OS, std::env::consts::ARCH, path().display());
    } else {
        eprintln!("suzuri: can't open {}; logging to stderr only", path().display());
    }
}

fn install_panic_hook() {
    let prev = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        let msg = info
            .payload()
            .downcast_ref::<&str>()
            .map(|s| s.to_string())
            .or_else(|| info.payload().downcast_ref::<String>().cloned())
            .unwrap_or_else(|| "(no message)".into());
        let at = info.location().map(|l| format!("{}:{}:{}", l.file(), l.line(), l.column())).unwrap_or_default();
        let thread = std::thread::current().name().unwrap_or("unnamed").to_string();
        let bt = std::backtrace::Backtrace::force_capture().to_string();
        // try_lock: the panic may have happened while logging.
        let trail = match LOGGER.get().and_then(|l| l.sink.try_lock()) {
            Some(mut s) => {
                s.write(&format_line(&crate::bridge::rfc3339_now(), Level::Error, "panic", &format!("thread '{thread}' panicked at {at}: {msg}")));
                if let Some(f) = &s.file {
                    let _ = f.sync_data();
                }
                s.trail.iter().cloned().collect::<Vec<_>>()
            }
            None => Vec::new(),
        };
        let report = crash_report(&crate::bridge::rfc3339_now(), &thread, &msg, &at, &bt, &trail);
        let _ = write_crash(&dir(), &report);
        prev(info);
    }));
}

pub fn crash_report(at: &str, thread: &str, msg: &str, location: &str, backtrace: &str, trail: &[String]) -> String {
    let mut s = String::new();
    s.push_str(&format!("suzuri {} panicked at {at}\n", env!("CARGO_PKG_VERSION")));
    s.push_str(&format!("os: {} {}\npid: {}\nthread: {thread}\n", std::env::consts::OS, std::env::consts::ARCH, std::process::id()));
    s.push_str(&format!("panic: {msg}\nat: {location}\n\nbacktrace:\n{backtrace}\n"));
    s.push_str(&format!("\nlast {} log lines:\n", trail.len()));
    for l in trail {
        s.push_str(l);
    }
    s
}

/// Write the crash file. A second panic before anyone has seen the first
/// (another thread, or the hook on the way down) appends.
pub fn write_crash(dir: &Path, report: &str) -> std::io::Result<()> {
    static WROTE: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);
    let again = WROTE.swap(true, std::sync::atomic::Ordering::Relaxed);
    let _ = std::fs::create_dir_all(dir);
    let mut f = OpenOptions::new().create(true).write(true).append(again).truncate(!again).open(dir.join(CRASH))?;
    if again {
        f.write_all(b"\n----\n\n")?;
    }
    f.write_all(report.as_bytes())?;
    f.sync_all()
}

/// A crash file left by the last run: moves it to suzuri-crash.prev.txt so
/// it is reported once, and returns where it now is.
pub fn take_crash(dir: &Path) -> Option<PathBuf> {
    let from = dir.join(CRASH);
    if !from.is_file() {
        return None;
    }
    let to = dir.join(CRASH_SEEN);
    let _ = std::fs::remove_file(&to);
    Some(if std::fs::rename(&from, &to).is_ok() { to } else { from })
}

/// The threads that panicked, from a crash file's "thread:" lines.
pub fn panic_threads(report: &str) -> Vec<String> {
    let mut v: Vec<String> = Vec::new();
    for t in report.lines().filter_map(|l| l.strip_prefix("thread: ")) {
        if !v.iter().any(|x| x == t) {
            v.push(t.to_string());
        }
    }
    v
}

/// The notice for the last run's panic report; stays up until clicked
/// away. "Crashed" only when the main thread panicked: other threads
/// unwind and the app keeps running.
pub fn crash_notice(file: &Path) -> rsz_term::protocols::Notice {
    let threads = panic_threads(&std::fs::read_to_string(file).unwrap_or_default());
    let fatal = threads.is_empty() || threads.iter().any(|t| t == "main");
    let (title, body) = if fatal {
        ("suzuri crashed last time".to_string(), format!("Crash report: {}", file.display()))
    } else {
        ("suzuri hit an error last time".to_string(), format!("Panic in thread {}. Report: {}", threads.join(", "), file.display()))
    };
    rsz_term::protocols::Notice {
        id: "suzuri-crash".into(),
        title,
        body,
        sound: "silent".into(),
        icon: "warn".into(),
        urgency: 1,
        occasion: "always".into(),
        expire_ms: Some(0),
        report: false,
        focus: true,
        close_event: false,
    }
}

/// The last `n` lines of the log (default 150, at most 5000), without
/// trailing "\r". Flushes this process's writes first.
pub fn tail(n: Option<usize>) -> (PathBuf, std::io::Result<Vec<String>>) {
    log::logger().flush();
    let p = path();
    let lines = tail_file(&p, n.unwrap_or(TAIL_DEFAULT));
    (p, lines)
}

pub fn tail_file(path: &Path, n: usize) -> std::io::Result<Vec<String>> {
    let n = n.clamp(1, TAIL_MAX);
    let text = std::fs::read(path)?;
    let text = String::from_utf8_lossy(&text);
    let mut lines: Vec<&str> = text.split('\n').collect();
    if lines.last() == Some(&"") {
        lines.pop();
    }
    let from = lines.len().saturating_sub(n);
    Ok(lines[from..].iter().map(|l| l.trim_end_matches('\r').to_string()).collect())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("rsz-applog-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn level_env_order_and_default() {
        assert_eq!(level_from(None, None), LevelFilter::Debug);
        assert_eq!(level_from(Some("warn"), Some("trace")), LevelFilter::Warn);
        assert_eq!(level_from(Some(" "), Some("ERROR")), LevelFilter::Error);
        assert_eq!(level_from(Some("off"), None), LevelFilter::Off);
        assert_eq!(level_from(Some("loud"), None), LevelFilter::Debug);
    }

    #[test]
    fn other_crates_only_warn_unless_trace() {
        assert!(allowed(LevelFilter::Debug, "suzuri::bridge", Level::Debug));
        assert!(allowed(LevelFilter::Debug, "ui", Level::Info));
        assert!(!allowed(LevelFilter::Debug, "tao::platform", Level::Info));
        assert!(allowed(LevelFilter::Debug, "tao::platform", Level::Warn));
        assert!(allowed(LevelFilter::Trace, "wry", Level::Debug));
        assert!(!allowed(LevelFilter::Error, "suzuri", Level::Warn));
    }

    #[test]
    fn line_shape() {
        let l = format_line("2026-11-09T10:00:00Z", Level::Info, "suzuri::update", "up to date");
        assert!(l.starts_with("2026-11-09T10:00:00Z INFO  pid="), "{l}");
        assert!(l.ends_with(" update: up to date\n"), "{l}");
    }

    #[test]
    fn tail_takes_last_lines() {
        let d = tmp("tail");
        let p = d.join(FILE);
        std::fs::write(&p, "a\r\nb\nc\n").unwrap();
        assert_eq!(tail_file(&p, 2).unwrap(), vec!["b", "c"]);
        assert_eq!(tail_file(&p, 0).unwrap(), vec!["c"]);
        assert_eq!(tail_file(&p, 99).unwrap(), vec!["a", "b", "c"]);
        std::fs::write(&p, "").unwrap();
        assert!(tail_file(&p, 5).unwrap().is_empty());
        assert!(tail_file(&d.join("nope.log"), 5).is_err());
    }

    #[test]
    fn rotates_past_the_limit() {
        let d = tmp("rotate");
        let p = d.join(FILE);
        std::fs::write(&p, "0123456789").unwrap();
        assert!(!rotate_if_big(&p, 10));
        assert!(rotate_if_big(&p, 9));
        assert!(!p.exists());
        assert_eq!(std::fs::read_to_string(d.join("suzuri.log.1")).unwrap(), "0123456789");
    }

    #[test]
    fn sink_keeps_a_trail() {
        let d = tmp("sink");
        let mut s = Sink::open(&d.join(FILE));
        for i in 0..TRAIL + 3 {
            s.write(&format!("line {i}\n"));
        }
        assert_eq!(s.trail.len(), TRAIL);
        assert_eq!(s.trail.front().unwrap(), "line 3\n");
        assert_eq!(tail_file(&d.join(FILE), 1).unwrap(), vec![format!("line {}", TRAIL + 2)]);
    }

    #[test]
    fn crash_file_reported_once() {
        let d = tmp("crash");
        assert!(take_crash(&d).is_none());
        let r = crash_report("T", "main", "boom", "src/x.rs:1:2", "bt", &["last\n".into()]);
        assert!(r.contains("panic: boom\nat: src/x.rs:1:2") && r.contains("thread: main") && r.ends_with("last\n"), "{r}");
        write_crash(&d, &r).unwrap();
        let seen = take_crash(&d).unwrap();
        assert_eq!(seen, d.join(CRASH_SEEN));
        assert!(std::fs::read_to_string(&seen).unwrap().contains("boom"));
        assert!(take_crash(&d).is_none());
        let n = crash_notice(&seen);
        assert!(n.body.contains(CRASH_SEEN) && n.expire_ms == Some(0));
        assert_eq!(n.title, "suzuri crashed last time");
    }

    #[test]
    fn worker_panic_is_an_error_not_a_crash() {
        let d = tmp("worker-panic");
        let a = crash_report("T", "bridge-req", "boom", "src/x.rs:1:2", "bt", &[]);
        let b = crash_report("T", "tokio-runtime-worker", "bang", "src/y.rs:1:2", "bt", &[]);
        write_crash(&d, &a).unwrap();
        write_crash(&d, &b).unwrap();
        let seen = take_crash(&d).unwrap();
        assert_eq!(panic_threads(&std::fs::read_to_string(&seen).unwrap()), vec!["bridge-req", "tokio-runtime-worker"]);
        let n = crash_notice(&seen);
        assert_eq!(n.title, "suzuri hit an error last time");
        assert!(n.body.contains("bridge-req, tokio-runtime-worker") && n.body.contains(CRASH_SEEN), "{}", n.body);
        // A main-thread panic in the same file still counts as a crash.
        write_crash(&d, &crash_report("T", "main", "x", "", "", &[])).unwrap();
        assert_eq!(crash_notice(&take_crash(&d).unwrap()).title, "suzuri crashed last time");
    }
}
