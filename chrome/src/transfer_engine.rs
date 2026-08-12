//! Sidecar process host for `suzuri-transfer` / `hato` (NDJSON `--json` mode).
//!
//! Protocol: `libs/transfer/docs/machine-mode.md`
//! Discovery order matches product `internal/transfer/resolve.go` plus a monorepo
//! dev path under `libs/transfer/target/release/`.

use std::io::{BufRead, BufReader};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, Receiver, Sender, TryRecvError};
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::Duration;

/// One machine-mode event (or engine lifecycle message) for the UI thread.
#[derive(Clone, Debug)]
pub struct EngineUpdate {
    pub phase: String,
    pub ticket: Option<String>,
    pub done: Option<u64>,
    pub total: Option<u64>,
    pub message: Option<String>,
    pub finished: bool,
    pub ok: bool,
}

/// Handle to a running engine job. UI polls [`EngineJob::try_recv`] each frame.
pub struct EngineJob {
    rx: Receiver<EngineUpdate>,
    cancel: Arc<AtomicBool>,
    join: Option<JoinHandle<()>>,
}

impl EngineJob {
    pub fn try_recv(&self) -> Option<EngineUpdate> {
        match self.rx.try_recv() {
            Ok(u) => Some(u),
            Err(TryRecvError::Empty) => None,
            Err(TryRecvError::Disconnected) => None,
        }
    }

    /// Request stop (SIGINT-equivalent kill) and wait briefly for the worker.
    pub fn cancel(mut self) {
        self.cancel.store(true, Ordering::SeqCst);
        if let Some(j) = self.join.take() {
            let _ = j.join();
        }
    }

    pub fn is_finished(&self) -> bool {
        self.join
            .as_ref()
            .map(|j| j.is_finished())
            .unwrap_or(true)
    }
}

impl Drop for EngineJob {
    fn drop(&mut self) {
        self.cancel.store(true, Ordering::SeqCst);
        if let Some(j) = self.join.take() {
            // Don't block the UI thread forever if the engine hangs.
            let done = Arc::new(AtomicBool::new(false));
            let flag = Arc::clone(&done);
            thread::spawn(move || {
                let _ = j.join();
                flag.store(true, Ordering::SeqCst);
            });
            for _ in 0..50 {
                if done.load(Ordering::SeqCst) {
                    break;
                }
                thread::sleep(Duration::from_millis(10));
            }
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum EngineMode {
    Send,
    Receive,
}

/// Resolve `suzuri-transfer` (or legacy `hato`).
///
/// Order:
/// 1. `SUZURI_TRANSFER_BIN`
/// 2. Next to the running executable
/// 3. Walk up from CWD for `libs/transfer/target/release/suzuri-transfer`
/// 4. `~/projects/suzuri/libs/transfer/target/release/suzuri-transfer` (dev home)
/// 5. `PATH` (`suzuri-transfer`, then `hato`)
pub fn find_transfer_bin() -> Option<PathBuf> {
    if let Ok(p) = std::env::var("SUZURI_TRANSFER_BIN") {
        let pb = PathBuf::from(p.trim());
        if pb.is_file() {
            return Some(pb);
        }
    }

    if let Ok(exe) = std::env::current_exe() {
        if let Ok(exe) = exe.canonicalize() {
            if let Some(dir) = exe.parent() {
                for name in engine_names() {
                    let cand = dir.join(name);
                    if cand.is_file() {
                        return Some(cand);
                    }
                }
            }
        } else if let Some(dir) = exe.parent() {
            for name in engine_names() {
                let cand = dir.join(name);
                if cand.is_file() {
                    return Some(cand);
                }
            }
        }
    }

    if let Ok(cwd) = std::env::current_dir() {
        if let Some(p) = walk_dev_release(&cwd) {
            return Some(p);
        }
    }

    if let Some(home) = std::env::var_os("HOME") {
        let home = PathBuf::from(home);
        for name in engine_names() {
            let cand = home
                .join("projects/suzuri/libs/transfer/target/release")
                .join(name);
            if cand.is_file() {
                return Some(cand);
            }
        }
    }

    for name in engine_names() {
        if let Some(p) = which(name) {
            return Some(p);
        }
    }
    None
}

fn engine_names() -> &'static [&'static str] {
    &["suzuri-transfer", "hato"]
}

fn walk_dev_release(start: &Path) -> Option<PathBuf> {
    let mut dir = start.to_path_buf();
    for _ in 0..8 {
        for name in engine_names() {
            let cand = dir.join("libs/transfer/target/release").join(name);
            if cand.is_file() {
                return Some(cand);
            }
            let cand_debug = dir.join("libs/transfer/target/debug").join(name);
            if cand_debug.is_file() {
                return Some(cand_debug);
            }
        }
        if !dir.pop() {
            break;
        }
    }
    None
}

fn which(name: &str) -> Option<PathBuf> {
    let path = std::env::var_os("PATH")?;
    for dir in std::env::split_paths(&path) {
        let p = dir.join(name);
        if p.is_file() {
            return Some(p);
        }
    }
    None
}

/// Default engine identity/config dir (`…/suzuri/transfer/`).
pub fn transfer_config_dir() -> PathBuf {
    if let Ok(p) = std::env::var("HATO_CONFIG_DIR") {
        let pb = PathBuf::from(p);
        let _ = std::fs::create_dir_all(&pb);
        return pb;
    }
    let base = suzuri_config_dir();
    let dir = base.join("transfer");
    let _ = std::fs::create_dir_all(&dir);
    dir
}

fn suzuri_config_dir() -> PathBuf {
    #[cfg(target_os = "macos")]
    {
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join("Library/Application Support/suzuri");
        }
    }
    #[cfg(target_os = "windows")]
    {
        if let Some(local) = std::env::var_os("LOCALAPPDATA") {
            return PathBuf::from(local).join("suzuri");
        }
        if let Some(home) = std::env::var_os("USERPROFILE") {
            return PathBuf::from(home).join("AppData/Local/suzuri");
        }
    }
    #[cfg(not(any(target_os = "macos", target_os = "windows")))]
    {
        if let Some(xdg) = std::env::var_os("XDG_CONFIG_HOME") {
            return PathBuf::from(xdg).join("suzuri");
        }
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join(".config/suzuri");
        }
    }
    PathBuf::from("suzuri-config")
}

/// Default receive directory: `~/Downloads` when present, else home.
pub fn default_receive_dir() -> PathBuf {
    if let Some(home) = std::env::var_os("HOME") {
        let dl = PathBuf::from(&home).join("Downloads");
        if dl.is_dir() {
            return dl;
        }
        return PathBuf::from(home);
    }
    PathBuf::from(".")
}

/// Spawn `suzuri-transfer --json send|receive …` on a background thread.
pub fn spawn_engine(mode: EngineMode, value: &str) -> Result<EngineJob, String> {
    let bin = find_transfer_bin().ok_or_else(|| {
        "suzuri-transfer not found — build libs/transfer or set SUZURI_TRANSFER_BIN".to_string()
    })?;

    let value = value.trim().to_string();
    if value.is_empty() {
        return Err("Enter a path or ticket first".into());
    }

    let mut args: Vec<String> = vec!["--json".into()];
    match mode {
        EngineMode::Send => {
            let path = PathBuf::from(&value);
            if !path.exists() {
                return Err(format!("no such file or folder: {value}"));
            }
            args.push("send".into());
            args.push(value);
        }
        EngineMode::Receive => {
            let dir = default_receive_dir();
            let _ = std::fs::create_dir_all(&dir);
            args.push("receive".into());
            args.push(value);
            args.push(dir.to_string_lossy().into_owned());
        }
    }

    let cfg = transfer_config_dir();
    let (tx, rx) = mpsc::channel::<EngineUpdate>();
    let cancel = Arc::new(AtomicBool::new(false));
    let cancel_t = Arc::clone(&cancel);

    let join = thread::Builder::new()
        .name("suzuri-transfer".into())
        .spawn(move || {
            run_engine_worker(bin, args, cfg, tx, cancel_t);
        })
        .map_err(|e| format!("spawn worker: {e}"))?;

    Ok(EngineJob {
        rx,
        cancel,
        join: Some(join),
    })
}

fn run_engine_worker(
    bin: PathBuf,
    args: Vec<String>,
    cfg: PathBuf,
    tx: Sender<EngineUpdate>,
    cancel: Arc<AtomicBool>,
) {
    let _ = tx.send(EngineUpdate {
        phase: "preparing".into(),
        ticket: None,
        done: None,
        total: None,
        message: Some(format!("starting {}", bin.display())),
        finished: false,
        ok: true,
    });

    let mut cmd = Command::new(&bin);
    cmd.args(&args)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .env("HATO_CONFIG_DIR", &cfg)
        .env("HATO_OUTPUT", "json");

    let mut child = match cmd.spawn() {
        Ok(c) => c,
        Err(e) => {
            let _ = tx.send(EngineUpdate {
                phase: "error".into(),
                ticket: None,
                done: None,
                total: None,
                message: Some(format!("exec failed: {e}")),
                finished: true,
                ok: false,
            });
            return;
        }
    };

    // Drain stderr so the pipe never blocks the engine.
    if let Some(stderr) = child.stderr.take() {
        thread::spawn(move || {
            let reader = BufReader::new(stderr);
            for _line in reader.lines() {}
        });
    }

    let stdout = match child.stdout.take() {
        Some(s) => s,
        None => {
            kill_child(&mut child);
            let _ = tx.send(EngineUpdate {
                phase: "error".into(),
                ticket: None,
                done: None,
                total: None,
                message: Some("no stdout from engine".into()),
                finished: true,
                ok: false,
            });
            return;
        }
    };

    let reader = BufReader::new(stdout);
    let lines = reader.lines();

    // Watchdog: when cancel flips, kill child so a blocking stdout read unblocks.
    let cancel_w = Arc::clone(&cancel);
    let child_id = child.id();
    thread::spawn(move || {
        while !cancel_w.load(Ordering::SeqCst) {
            thread::sleep(Duration::from_millis(50));
        }
        kill_pid(child_id);
    });

    let mut saw_terminal = false;
    for line_res in lines {
        if cancel.load(Ordering::SeqCst) {
            break;
        }
        let line = match line_res {
            Ok(l) => l,
            Err(e) => {
                let _ = tx.send(EngineUpdate {
                    phase: "error".into(),
                    ticket: None,
                    done: None,
                    total: None,
                    message: Some(format!("read stdout: {e}")),
                    finished: true,
                    ok: false,
                });
                kill_child(&mut child);
                let _ = child.wait();
                return;
            }
        };
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        if let Some(upd) = parse_ndjson_line(line) {
            if upd.finished {
                saw_terminal = true;
            }
            let _ = tx.send(upd);
        }
    }

    let status = child.wait();
    if cancel.load(Ordering::SeqCst) {
        let _ = tx.send(EngineUpdate {
            phase: "stopped".into(),
            ticket: None,
            done: None,
            total: None,
            message: Some("cancelled".into()),
            finished: true,
            ok: true,
        });
        return;
    }

    // If NDJSON already emitted done/error/stopped, only add a fallback when needed.
    match status {
        Ok(st) if st.success() || st.code() == Some(130) => {
            if !saw_terminal {
                let phase = if st.code() == Some(130) {
                    "stopped"
                } else {
                    "done"
                };
                let _ = tx.send(EngineUpdate {
                    phase: phase.into(),
                    ticket: None,
                    done: None,
                    total: None,
                    message: None,
                    finished: true,
                    ok: true,
                });
            } else {
                // Ensure UI drops the job handle.
                let _ = tx.send(EngineUpdate {
                    phase: String::new(),
                    ticket: None,
                    done: None,
                    total: None,
                    message: None,
                    finished: true,
                    ok: true,
                });
            }
        }
        Ok(st) => {
            if !saw_terminal {
                let _ = tx.send(EngineUpdate {
                    phase: "error".into(),
                    ticket: None,
                    done: None,
                    total: None,
                    message: Some(format!("engine exit {}", st.code().unwrap_or(-1))),
                    finished: true,
                    ok: false,
                });
            } else {
                let _ = tx.send(EngineUpdate {
                    phase: String::new(),
                    ticket: None,
                    done: None,
                    total: None,
                    message: None,
                    finished: true,
                    ok: false,
                });
            }
        }
        Err(e) => {
            let _ = tx.send(EngineUpdate {
                phase: "error".into(),
                ticket: None,
                done: None,
                total: None,
                message: Some(format!("wait failed: {e}")),
                finished: true,
                ok: false,
            });
        }
    }
}

fn kill_child(child: &mut Child) {
    #[cfg(unix)]
    {
        // SIGINT first (engine emits `stopped` on send), then SIGKILL.
        let _ = send_signal(child.id(), 2); // SIGINT
        thread::sleep(Duration::from_millis(200));
        let _ = child.kill();
    }
    #[cfg(not(unix))]
    {
        let _ = child.kill();
    }
}

fn kill_pid(pid: u32) {
    #[cfg(unix)]
    {
        let _ = send_signal(pid, 2);
        thread::sleep(Duration::from_millis(150));
        let _ = send_signal(pid, 9);
    }
    #[cfg(not(unix))]
    {
        let _ = pid; // cancel relies on Drop / explicit kill_child on worker
    }
}

#[cfg(unix)]
fn send_signal(pid: u32, sig: i32) -> std::io::Result<()> {
    let rc = unsafe { libc_kill(pid as i32, sig) };
    if rc == 0 {
        Ok(())
    } else {
        Err(std::io::Error::last_os_error())
    }
}

#[cfg(unix)]
unsafe fn libc_kill(pid: i32, sig: i32) -> i32 {
    // Avoid libc crate dep — use raw syscall via libc from std is not available;
    // use `kill` from nix-less extern.
    extern "C" {
        fn kill(pid: i32, sig: i32) -> i32;
    }
    kill(pid, sig)
}

/// Best-effort NDJSON object field extraction (no serde).
pub fn parse_ndjson_line(line: &str) -> Option<EngineUpdate> {
    let event = json_string_field(line, "event")?;
    let mut upd = EngineUpdate {
        phase: event.clone(),
        ticket: json_string_field(line, "ticket"),
        done: json_u64_field(line, "done"),
        total: json_u64_field(line, "total"),
        message: json_string_field(line, "message"),
        finished: false,
        ok: true,
    };

    match event.as_str() {
        "ready" => {
            upd.phase = "ready".into();
            if upd.message.is_none() {
                upd.message = Some("share this ticket · keep suzuri open".into());
            }
        }
        "receiving" => {
            upd.phase = "receiving".into();
            if upd.message.is_none() {
                if let Some(dir) = json_string_field(line, "out_dir")
                    .or_else(|| json_string_field(line, "dir"))
                {
                    upd.message = Some(dir);
                }
            }
        }
        "progress" => {
            upd.phase = "progress".into();
        }
        "resumed" => {
            upd.phase = "progress".into();
            upd.message = Some("resumed".into());
            if let Some(n) = json_u64_field(line, "already_had") {
                upd.message = Some(format!("resumed ({n} bytes already had)"));
            }
        }
        "done" => {
            upd.phase = "done".into();
            upd.finished = true;
            upd.ok = true;
            if let Some(n) = json_u64_field(line, "total_bytes") {
                upd.total = Some(n);
                upd.done = Some(n);
            }
            if let Some(dir) = json_string_field(line, "out_dir") {
                upd.message = Some(format!("received → {dir}"));
            } else {
                upd.message = Some("receive finished".into());
            }
        }
        "stopped" => {
            upd.phase = "stopped".into();
            upd.finished = true;
            upd.ok = true;
            if upd.message.is_none() {
                upd.message = Some("stopped serving".into());
            }
        }
        "error" => {
            upd.phase = "error".into();
            upd.finished = true;
            upd.ok = false;
            if upd.message.is_none() {
                if let Some(code) = json_string_field(line, "code") {
                    upd.message = Some(code);
                } else {
                    upd.message = Some("transfer error".into());
                }
            }
        }
        _ => {
            // Unknown events: still surface phase for UI.
        }
    }
    Some(upd)
}

fn json_string_field(line: &str, key: &str) -> Option<String> {
    // Match "key" then optional space, colon, optional space, "
    let key_pat = format!("\"{key}\"");
    let mut rest = line;
    loop {
        let idx = rest.find(&key_pat)?;
        let after_key = &rest[idx + key_pat.len()..];
        let after_key = after_key.trim_start();
        if !after_key.starts_with(':') {
            rest = &rest[idx + 1..];
            continue;
        }
        let after_colon = after_key[1..].trim_start();
        if !after_colon.starts_with('"') {
            return None;
        }
        let s = &after_colon[1..];
        let mut out = String::new();
        let mut chars = s.chars();
        while let Some(c) = chars.next() {
            if c == '\\' {
                match chars.next() {
                    Some('n') => out.push('\n'),
                    Some('r') => out.push('\r'),
                    Some('t') => out.push('\t'),
                    Some('"') => out.push('"'),
                    Some('\\') => out.push('\\'),
                    Some('u') => {
                        // Skip basic \uXXXX
                        let hex: String = chars.by_ref().take(4).collect();
                        if let Ok(v) = u32::from_str_radix(&hex, 16) {
                            if let Some(ch) = char::from_u32(v) {
                                out.push(ch);
                            }
                        }
                    }
                    Some(other) => out.push(other),
                    None => break,
                }
            } else if c == '"' {
                return Some(out);
            } else {
                out.push(c);
            }
        }
        return None;
    }
}

fn json_u64_field(line: &str, key: &str) -> Option<u64> {
    let key_pat = format!("\"{key}\"");
    let mut rest = line;
    loop {
        let idx = rest.find(&key_pat)?;
        let after_key = &rest[idx + key_pat.len()..];
        let after_key = after_key.trim_start();
        if !after_key.starts_with(':') {
            rest = &rest[idx + 1..];
            continue;
        }
        let after_colon = after_key[1..].trim_start();
        let num: String = after_colon
            .chars()
            .take_while(|c| c.is_ascii_digit())
            .collect();
        if num.is_empty() {
            return None;
        }
        return num.parse().ok();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ready() {
        let line = r#"{"v":1,"event":"ready","ticket":"blobabc","relays":1,"ips":2,"path":"/tmp/f"}"#;
        let u = parse_ndjson_line(line).unwrap();
        assert_eq!(u.phase, "ready");
        assert_eq!(u.ticket.as_deref(), Some("blobabc"));
    }

    #[test]
    fn parse_progress() {
        let line = r#"{"v":1,"event":"progress","done":10,"total":100}"#;
        let u = parse_ndjson_line(line).unwrap();
        assert_eq!(u.phase, "progress");
        assert_eq!(u.done, Some(10));
        assert_eq!(u.total, Some(100));
    }

    #[test]
    fn parse_error() {
        let line = r#"{"v":1,"event":"error","code":"usage","message":"missing path"}"#;
        let u = parse_ndjson_line(line).unwrap();
        assert_eq!(u.phase, "error");
        assert!(!u.ok);
        assert_eq!(u.message.as_deref(), Some("missing path"));
    }
}
