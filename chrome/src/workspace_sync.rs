//! Sidecar host for `suzuri-workspace-sync` (NDJSON `--json` mode).
//!
//! Discovery matches product `internal/workspacesync/resolve.go` plus the
//! monorepo `libs/transfer/target/{release,debug}` walk used by transfer.

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
pub struct SyncUpdate {
    pub phase: String,
    pub ticket: Option<String>,
    pub peers: Option<u64>,
    pub message: Option<String>,
    pub finished: bool,
    pub ok: bool,
}

/// Handle to a running workspace-sync job. UI polls [`SyncJob::try_recv`].
pub struct SyncJob {
    rx: Receiver<SyncUpdate>,
    cancel: Arc<AtomicBool>,
    join: Option<JoinHandle<()>>,
}

impl SyncJob {
    pub fn try_recv(&self) -> Option<SyncUpdate> {
        match self.rx.try_recv() {
            Ok(u) => Some(u),
            Err(TryRecvError::Empty) => None,
            Err(TryRecvError::Disconnected) => None,
        }
    }

    pub fn cancel(mut self) {
        self.cancel.store(true, Ordering::SeqCst);
        if let Some(j) = self.join.take() {
            let _ = j.join();
        }
    }

    pub fn is_finished(&self) -> bool {
        self.join.as_ref().map(|j| j.is_finished()).unwrap_or(true)
    }
}

impl Drop for SyncJob {
    fn drop(&mut self) {
        self.cancel.store(true, Ordering::SeqCst);
        if let Some(j) = self.join.take() {
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
pub enum SyncMode {
    Listen,
    Join,
}

/// Resolve `suzuri-workspace-sync`.
///
/// Order:
/// 1. `SUZURI_WORKSPACE_SYNC_BIN`
/// 2. Next to the running executable
/// 3. Walk up from CWD for `libs/transfer/target/{release,debug}/`
/// 4. `~/projects/suzuri/libs/transfer/target/{release,debug}/` (dev home)
/// 5. `PATH`
pub fn find_workspace_sync_bin() -> Option<PathBuf> {
    if let Ok(p) = std::env::var("SUZURI_WORKSPACE_SYNC_BIN") {
        let pb = PathBuf::from(p.trim());
        if pb.is_file() {
            return Some(pb);
        }
    }

    if let Ok(exe) = std::env::current_exe() {
        let dir = exe
            .canonicalize()
            .ok()
            .and_then(|p| p.parent().map(|d| d.to_path_buf()))
            .or_else(|| exe.parent().map(|d| d.to_path_buf()));
        if let Some(dir) = dir {
            for name in engine_names() {
                let cand = dir.join(name);
                if cand.is_file() {
                    return Some(cand);
                }
            }
        }
    }

    if let Ok(cwd) = std::env::current_dir() {
        if let Some(p) = walk_dev(&cwd) {
            return Some(p);
        }
    }

    if let Some(home) = std::env::var_os("HOME") {
        let home = PathBuf::from(home);
        for profile in ["release", "debug"] {
            for name in engine_names() {
                let cand = home
                    .join("projects/suzuri/libs/transfer/target")
                    .join(profile)
                    .join(name);
                if cand.is_file() {
                    return Some(cand);
                }
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
    if cfg!(windows) {
        &["suzuri-workspace-sync.exe", "suzuri-workspace-sync"]
    } else {
        &["suzuri-workspace-sync"]
    }
}

fn walk_dev(start: &Path) -> Option<PathBuf> {
    let mut dir = start.to_path_buf();
    for _ in 0..8 {
        for profile in ["release", "debug"] {
            for name in engine_names() {
                let cand = dir.join("libs/transfer/target").join(profile).join(name);
                if cand.is_file() {
                    return Some(cand);
                }
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

/// Spawn `suzuri-workspace-sync --enable --json listen|join …`.
pub fn spawn_sync(mode: SyncMode, root: &Path, ticket: &str) -> Result<SyncJob, String> {
    let bin = find_workspace_sync_bin().ok_or_else(|| {
        "suzuri-workspace-sync not found — build libs/transfer -p suzuri-workspace-sync or set SUZURI_WORKSPACE_SYNC_BIN"
            .to_string()
    })?;

    let root = root.to_path_buf();
    if root.as_os_str().is_empty() {
        return Err("workspace root missing".into());
    }
    let _ = std::fs::create_dir_all(&root);

    let mut args: Vec<String> = vec!["--enable".into(), "--json".into()];
    match mode {
        SyncMode::Listen => {
            args.push("listen".into());
            args.push("--root".into());
            args.push(root.to_string_lossy().into_owned());
        }
        SyncMode::Join => {
            let ticket = ticket.trim().to_string();
            if ticket.is_empty() {
                return Err("paste a workspace ticket first".into());
            }
            args.push("join".into());
            args.push("--root".into());
            args.push(root.to_string_lossy().into_owned());
            args.push("--ticket".into());
            args.push(ticket);
        }
    }

    let (tx, rx) = mpsc::channel::<SyncUpdate>();
    let cancel = Arc::new(AtomicBool::new(false));
    let cancel_t = Arc::clone(&cancel);

    let join = thread::Builder::new()
        .name("suzuri-workspace-sync".into())
        .spawn(move || {
            run_sync_worker(bin, args, tx, cancel_t);
        })
        .map_err(|e| format!("spawn worker: {e}"))?;

    Ok(SyncJob {
        rx,
        cancel,
        join: Some(join),
    })
}

fn run_sync_worker(
    bin: PathBuf,
    args: Vec<String>,
    tx: Sender<SyncUpdate>,
    cancel: Arc<AtomicBool>,
) {
    let _ = tx.send(SyncUpdate {
        phase: "preparing".into(),
        ticket: None,
        peers: None,
        message: Some(format!("starting {}", bin.display())),
        finished: false,
        ok: true,
    });

    let mut cmd = Command::new(&bin);
    cmd.args(&args)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());

    let mut child = match cmd.spawn() {
        Ok(c) => c,
        Err(e) => {
            let _ = tx.send(SyncUpdate {
                phase: "error".into(),
                ticket: None,
                peers: None,
                message: Some(format!("exec failed: {e}")),
                finished: true,
                ok: false,
            });
            return;
        }
    };

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
            let _ = tx.send(SyncUpdate {
                phase: "error".into(),
                ticket: None,
                peers: None,
                message: Some("no stdout from engine".into()),
                finished: true,
                ok: false,
            });
            return;
        }
    };

    let cancel_w = Arc::clone(&cancel);
    let child_id = child.id();
    thread::spawn(move || {
        while !cancel_w.load(Ordering::SeqCst) {
            thread::sleep(Duration::from_millis(50));
        }
        kill_pid(child_id);
    });

    let mut saw_terminal = false;
    for line_res in BufReader::new(stdout).lines() {
        if cancel.load(Ordering::SeqCst) {
            break;
        }
        let line = match line_res {
            Ok(l) => l,
            Err(e) => {
                let _ = tx.send(SyncUpdate {
                    phase: "error".into(),
                    ticket: None,
                    peers: None,
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
        let _ = tx.send(SyncUpdate {
            phase: "stopped".into(),
            ticket: None,
            peers: None,
            message: Some("disconnected".into()),
            finished: true,
            ok: true,
        });
        return;
    }

    match status {
        Ok(st) if st.success() || st.code() == Some(130) => {
            if !saw_terminal {
                let _ = tx.send(SyncUpdate {
                    phase: "stopped".into(),
                    ticket: None,
                    peers: None,
                    message: Some("stopped".into()),
                    finished: true,
                    ok: true,
                });
            }
        }
        Ok(st) => {
            if !saw_terminal {
                let _ = tx.send(SyncUpdate {
                    phase: "error".into(),
                    ticket: None,
                    peers: None,
                    message: Some(format!("engine exit {}", st.code().unwrap_or(-1))),
                    finished: true,
                    ok: false,
                });
            }
        }
        Err(e) => {
            let _ = tx.send(SyncUpdate {
                phase: "error".into(),
                ticket: None,
                peers: None,
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
        let _ = send_signal(child.id(), 2);
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
        let _ = pid;
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
    extern "C" {
        fn kill(pid: i32, sig: i32) -> i32;
    }
    kill(pid, sig)
}

/// Best-effort NDJSON object field extraction (no serde).
pub fn parse_ndjson_line(line: &str) -> Option<SyncUpdate> {
    let event = json_string_field(line, "event")?;
    let mut upd = SyncUpdate {
        phase: event.clone(),
        ticket: json_string_field(line, "ticket"),
        peers: json_u64_field(line, "peers"),
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
        "connecting" => {
            upd.phase = "connecting".into();
            if upd.message.is_none() {
                upd.message = Some("connecting…".into());
            }
        }
        "connected" => {
            upd.phase = "connected".into();
            let n = upd.peers.unwrap_or(1);
            upd.message = Some(if n == 1 {
                "p2p · 1 peer".into()
            } else {
                format!("p2p · {n} peers")
            });
        }
        "peer_left" => {
            upd.phase = "connected".into();
            let n = upd.peers.unwrap_or(0);
            if n == 0 {
                upd.phase = "ready".into();
                upd.message = Some("p2p listening · waiting for a peer".into());
            } else {
                upd.message = Some(format!("p2p · {n} peers"));
            }
        }
        "stopped" => {
            upd.phase = "stopped".into();
            upd.finished = true;
            upd.ok = true;
            if upd.message.is_none() {
                upd.message = Some("p2p disconnected".into());
            }
        }
        "error" => {
            upd.phase = "error".into();
            upd.finished = true;
            upd.ok = false;
            if upd.message.is_none() {
                upd.message = Some("workspace sync error".into());
            }
        }
        _ => {}
    }
    Some(upd)
}

fn json_string_field(line: &str, key: &str) -> Option<String> {
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
    fn parse_ready_ticket() {
        let line = r#"{"v":1,"event":"ready","ticket":"{\"id\":\"abc\"}","role":"listen"}"#;
        let u = parse_ndjson_line(line).unwrap();
        assert_eq!(u.phase, "ready");
        assert_eq!(u.ticket.as_deref(), Some(r#"{"id":"abc"}"#));
    }

    #[test]
    fn parse_connected_peers() {
        let line = r#"{"v":1,"event":"connected","peers":2}"#;
        let u = parse_ndjson_line(line).unwrap();
        assert_eq!(u.phase, "connected");
        assert_eq!(u.peers, Some(2));
        assert!(u.message.as_deref().unwrap().contains("2 peers"));
    }

    #[test]
    fn parse_error() {
        let line = r#"{"v":1,"event":"error","code":"generic","message":"connect failed"}"#;
        let u = parse_ndjson_line(line).unwrap();
        assert_eq!(u.phase, "error");
        assert!(!u.ok);
        assert_eq!(u.message.as_deref(), Some("connect failed"));
    }
}
