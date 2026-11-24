//! Shells that outlive the app (cards hearth#913, hearth#923 for Windows).
//! Each pane's shell runs in its own host process, `suzuri --keeper-host
//! <dir> <id>`, started in its own session (Windows: detached, out of the
//! app's job and process tree): the host owns the whole `Pane` (PTY or
//! ConPTY, screen, scrollback) and serves it on `<dir>/<id>.sock`. The app is a
//! viewer: [`Shell::Kept`] forwards every pane call over the socket and
//! turns the host's events back into `Out`s for the pane's sink. Quitting,
//! updating or crashing the app leaves the hosts; the next start lists them
//! and attaches. Closing a pane sends `close`, which ends the shell, and a
//! host exits when its shell does.
//!
//! The wire format and the client are crates/keeper (rsz-keeper), with no
//! rsz-term in it, so other programs (Hearth) can attach too. The host
//! itself and the JSON both halves send are crates/keeper-host
//! (rsz-keeper-host), which also builds the host as its own small program
//! for apps that ship it without Suzuri (hearth#190). The viewer's half is
//! here ([`Kept`]); keep the two in step and keep reading what older hosts
//! send: a host keeps the program it was started with until its shell ends.
//!
//! On Windows a running exe can't be replaced, and the update's installer
//! ends whatever runs `suzuri.exe` from the install folder; so hosts run
//! from a copy, `<dir>/host/<version>-<size>-<time>/suzuri-keeper.exe`
//! (with conpty.dll and OpenConsole.exe beside it), made once per build.
//! A copy no host runs from any more is removed at the next start.
//!
//! RSZ_KEEPER=0 keeps panes in the app, as before. A
//! bench or UI-test launch (RSZ_BENCH, RSZ_PANES, RSZ_UI_TEST, RSZ_CMD) keeps
//! them in the app too, so it never leaves shells behind, unless RSZ_KEEPER=1
//! asks for kept shells anyway (scripts/bench-mac.sh's kept run, which closes
//! its hosts itself).

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use base64::Engine as _;
use parking_lot::Mutex;
use rsz_keeper_host::codec::*;
use rsz_keeper_host::detached;
use rsz_term::palette::Palette;
use rsz_term::{Link, MouseEvent, Out, Pane, SelectKind, Sink, Submit};
use serde_json::{Value, json};

/// The host, run by `suzuri --keeper-host` (main.rs).
pub use rsz_keeper_host::host;

/// Kept shells are on: not turned off with RSZ_KEEPER=0, and
/// not a bench or UI-test launch unless RSZ_KEEPER=1 asks for them.
pub fn enabled() -> bool {
    let set = |k: &str| std::env::var(k).is_ok_and(|v| !v.is_empty());
    cfg!(any(unix, windows))
        && wanted(
            std::env::var("RSZ_KEEPER").ok().as_deref(),
            ["RSZ_BENCH", "RSZ_PANES", "RSZ_UI_TEST", "RSZ_CMD"]
                .iter()
                .any(|k| set(k)),
        )
}

/// RSZ_KEEPER's say (`0` off, `1` on), else on unless `dev` (a bench or
/// UI-test launch).
fn wanted(keeper: Option<&str>, dev: bool) -> bool {
    match keeper {
        Some("0") => false,
        Some("1") => true,
        _ => !dev,
    }
}

#[test]
fn rsz_keeper_1_keeps_a_bench_launch_kept() {
    assert!(wanted(None, false));
    assert!(!wanted(None, true));
    assert!(!wanted(Some("0"), false));
    assert!(wanted(Some("1"), true));
}

pub fn dir() -> PathBuf {
    rsz_keeper::dir::default_dir()
}

// ---- the pane handle the app holds

/// Counters for the stats command, from either kind of shell.
#[derive(Default)]
pub struct StatsSnap {
    pub bytes_in: u64,
    pub feed_ns: u64,
    pub frames: u64,
    pub frame_bytes: u64,
    pub snapshot_ns: u64,
    pub writes: u64,
}

/// A pane's shell: in this process, or kept by a host.
pub enum Shell {
    Local(Pane),
    Kept(Kept),
}

macro_rules! both {
    ($self:ident, $p:ident => $local:expr, $k:ident => $kept:expr) => {
        match $self {
            Shell::Local($p) => $local,
            Shell::Kept($k) => $kept,
        }
    };
}

impl Shell {
    pub fn submit(&self, line: &str) -> Submit {
        both!(self, p => p.submit(line), k => k.call(json!({ "t": "submit", "line": line })).map(|v| submit_of(&v)).unwrap_or(Submit::Busy))
    }
    pub fn last_output_at(&self) -> Instant {
        both!(self, p => p.last_output_at(), k => *k.last_out.lock())
    }
    pub fn flush_queue(&self) -> Option<usize> {
        both!(self, p => p.flush_queue(), k => k.call(json!({ "t": "flush_queue" })).ok().and_then(|v| v.as_u64()).map(|n| n as usize))
    }
    pub fn clear_queue(&self) -> usize {
        both!(self, p => p.clear_queue(), k => k.call(json!({ "t": "clear_queue" })).ok().and_then(|v| v.as_u64()).unwrap_or(0) as usize)
    }
    #[allow(dead_code)]
    pub fn queue_len(&self) -> usize {
        both!(self, p => p.queue_len(), k => k.call(json!({ "t": "queue_len" })).ok().and_then(|v| v.as_u64()).unwrap_or(0) as usize)
    }
    pub fn write(&self, bytes: Vec<u8>) {
        both!(self, p => p.write(bytes), k => { let _ = k.conn.input(&bytes); })
    }
    pub fn reply(&self, bytes: Vec<u8>) {
        both!(self, p => p.reply(bytes), k => k.send(json!({ "t": "reply", "data": B64.encode(&bytes) })))
    }
    pub fn resize(&self, cols: u16, rows: u16) {
        both!(self, p => p.resize(cols, rows), k => k.send(json!({ "t": "resize", "cols": cols, "rows": rows })))
    }
    pub fn scroll(&self, delta: i32) {
        both!(self, p => p.scroll(delta), k => k.send(json!({ "t": "scroll", "delta": delta })))
    }
    pub fn ack(&self, seq: u32) {
        both!(self, p => p.ack(seq), k => k.send(json!({ "t": "ack", "seq": seq })))
    }
    pub fn set_cell_px(&self, w: u16, h: u16) {
        both!(self, p => p.set_cell_px(w, h), k => k.send(json!({ "t": "cell_px", "w": w, "h": h })))
    }
    pub fn set_focused(&self, focused: bool) {
        both!(self, p => p.set_focused(focused), k => k.send(json!({ "t": "focus", "on": focused })))
    }
    pub fn redraw(&self) {
        both!(self, p => p.redraw(), k => k.send(json!({ "t": "redraw" })))
    }
    /// After the palette changed: a kept shell's host has its own.
    pub fn palette_changed(&self, pal: &Palette) {
        both!(self, p => p.redraw(), k => k.send(palette_json(pal)))
    }
    pub fn set_default_cursor(&self, shape: u8) {
        both!(self, p => p.set_default_cursor(shape), k => k.send(json!({ "t": "cursor", "shape": shape })))
    }
    /// Close the pane: this ends its shell, kept or not.
    pub fn close(&self) {
        both!(self, p => p.close(), k => if k.borrowed { k.let_go() } else { k.send(json!({ "t": "close" })) })
    }
    pub fn cwd(&self) -> Option<String> {
        both!(self, p => p.cwd(), k => k.call(json!({ "t": "cwd" })).ok().and_then(|v| v.as_str().map(str::to_string)))
    }
    pub fn stats(&self) -> StatsSnap {
        match self {
            Shell::Local(p) => {
                let s = p.stats();
                let l = |a: &std::sync::atomic::AtomicU64| a.load(Ordering::Relaxed);
                StatsSnap {
                    bytes_in: l(&s.bytes_in),
                    feed_ns: l(&s.feed_ns),
                    frames: l(&s.frames),
                    frame_bytes: l(&s.frame_bytes),
                    snapshot_ns: l(&s.snapshot_ns),
                    writes: l(&s.writes),
                }
            }
            Shell::Kept(k) => {
                let v = k.call(json!({ "t": "stats" })).unwrap_or(Value::Null);
                let n = |f: &str| v[f].as_u64().unwrap_or(0);
                StatsSnap {
                    bytes_in: n("bytes_in"),
                    feed_ns: n("feed_ns"),
                    frames: n("frames"),
                    frame_bytes: n("frame_bytes"),
                    snapshot_ns: n("snapshot_ns"),
                    writes: n("writes"),
                }
            }
        }
    }
    /// The program in front of the shell now (its name and process id),
    /// and the shell's folder as the OS sees it. A host older than
    /// hearth#916 does not answer: none, and the last prompt's folder.
    pub fn program(&self) -> (Option<String>, Option<u32>, Option<String>) {
        match self {
            Shell::Local(p) => (p.program_name(), p.program_pid(), p.live_cwd()),
            Shell::Kept(k) => {
                let v = k.call(json!({ "t": "program" })).unwrap_or(Value::Null);
                let cwd = v["cwd"].as_str().map(str::to_string).or_else(|| self.cwd());
                (
                    v["name"].as_str().map(str::to_string),
                    v["pid"].as_u64().map(|n| n as u32),
                    cwd,
                )
            }
        }
    }
    pub fn screen_text(&self) -> String {
        both!(self, p => p.screen_text(), k => k.text(0).0)
    }
    pub fn history_tail(&self, lines: usize) -> Vec<String> {
        both!(self, p => p.history_tail(lines), k => k.text(lines).1)
    }
    pub fn select_start(&self, col: u16, row: u16, right: bool, kind: SelectKind) {
        both!(self, p => p.select_start(col, row, right, kind), k => k.send(json!({ "t": "select", "op": "start", "col": col, "row": row, "right": right, "kind": select_kind_name(kind) })))
    }
    pub fn select_extend(&self, col: u16, row: u16, right: bool) {
        both!(self, p => p.select_extend(col, row, right), k => k.send(json!({ "t": "select", "op": "extend", "col": col, "row": row, "right": right })))
    }
    pub fn select_clear(&self) {
        both!(self, p => p.select_clear(), k => k.send(json!({ "t": "select", "op": "clear" })))
    }
    pub fn selection_text(&self) -> Option<String> {
        both!(self, p => p.selection_text(), k => k.call(json!({ "t": "selection" })).ok().and_then(|v| v.as_str().map(str::to_string)))
    }
    pub fn link_at(&self, col: u16, row: u16) -> Option<Link> {
        both!(self, p => p.link_at(col, row), k => {
            let v = k.call(json!({ "t": "link", "col": col, "row": row })).ok()?;
            Some(Link { url: v["url"].as_str()?.to_string(), row: v["row"].as_u64()? as u16, x0: v["x0"].as_u64()? as u16, x1: v["x1"].as_u64()? as u16, osc8: v["osc8"].as_bool().unwrap_or(false) })
        })
    }
    pub fn mouse(&self, ev: MouseEvent) -> bool {
        both!(self, p => p.mouse(ev), k => k.call(mouse_json(&ev)).ok().and_then(|v| v.as_bool()).unwrap_or(false))
    }
    pub fn alt_screen(&self) -> bool {
        both!(self, p => p.alt_screen(), k => k.call(json!({ "t": "alt_screen" })).ok().and_then(|v| v.as_bool()).unwrap_or(false))
    }
    pub fn paste(&self, text: &str) {
        both!(self, p => p.paste(text), k => k.send(json!({ "t": "paste", "text": text })))
    }
}

/// A shell kept by a host, as the app sees it.
pub struct Kept {
    conn: rsz_keeper::Conn,
    last_out: Arc<Mutex<Instant>>,
    /// Set when this app lets go on purpose: no Exit for the pane then.
    detached: Arc<AtomicBool>,
    /// Another program's shell (Hearth's open_kept): closing its pane lets
    /// go of it, it never sends `close`.
    pub borrowed: bool,
    pub hello: Value,
}

impl Kept {
    fn send(&self, v: Value) {
        let _ = self.conn.send(&v);
    }

    fn call(&self, v: Value) -> std::io::Result<Value> {
        self.conn.call(v)
    }

    /// Leave the shell running in its host and stop viewing it.
    fn let_go(&self) {
        self.detached.store(true, Ordering::Relaxed);
        self.conn.detach();
    }

    /// (screen, the last `lines` of scrollback).
    fn text(&self, lines: usize) -> (String, Vec<String>) {
        let v = self
            .call(json!({ "t": "text", "lines": lines }))
            .unwrap_or(Value::Null);
        let tail = v["tail"]
            .as_array()
            .map(|a| {
                a.iter()
                    .filter_map(|l| l.as_str().map(str::to_string))
                    .collect()
            })
            .unwrap_or_default();
        (v["screen"].as_str().unwrap_or("").to_string(), tail)
    }

    /// Attach to host `id` as the app's viewer; its events reach `sink`.
    pub fn attach(dir: &Path, id: u32, sink: Sink) -> std::io::Result<Kept> {
        Kept::attach_sock(&rsz_keeper::dir::sock_path(dir, id), sink)
    }

    /// Attach as a viewer to the host serving `sock` (one of ours, or
    /// another program's: Hearth's open_kept, checked by [`foreign_sock`]).
    pub fn attach_sock(sock: &Path, sink: Sink) -> std::io::Result<Kept> {
        let last_out = Arc::new(Mutex::new(Instant::now()));
        let detached = Arc::new(AtomicBool::new(false));
        let exited = Arc::new(AtomicBool::new(false));
        let (lo, de) = (last_out.clone(), detached.clone());
        let sock = sock.to_path_buf();
        let ask_sock = sock.clone();
        let conn = rsz_keeper::Conn::connect(&sock, "viewer", "suzuri", move |e| match e {
            rsz_keeper::Event::Frame(f) => {
                *lo.lock() = Instant::now();
                sink(Out::Frame(f));
            }
            // A program in the shell reads the clipboard: this window answers.
            rsz_keeper::Event::Msg(v) if v["t"] == "clip_ask" => {
                let (n, path) = (v["n"].clone(), ask_sock.clone());
                std::thread::spawn(move || answer_clipboard(&path, n));
            }
            rsz_keeper::Event::Msg(v) => {
                if let Some(o) = out_of(&v) {
                    if matches!(o, Out::Exit(_)) {
                        exited.store(true, Ordering::Relaxed);
                    }
                    sink(o);
                }
            }
            // The host went away without a word (killed): the pane ends.
            rsz_keeper::Event::Closed => {
                if !de.load(Ordering::Relaxed) && !exited.swap(true, Ordering::Relaxed) {
                    sink(Out::Exit(None));
                }
            }
        })?;
        let hello = conn.hello.clone();
        Ok(Kept {
            conn,
            last_out,
            detached,
            borrowed: false,
            hello,
        })
    }
}

/// Answer a host's clipboard ask `n` with the clipboard's text (null when
/// it has none or doesn't answer in time), on a connection of its own: the
/// viewer's may be waiting on a call the host can't serve until it has this.
fn answer_clipboard(sock: &Path, n: Value) {
    let text = crate::clip::text(crate::clip::OSC52).ok();
    match rsz_keeper::Conn::connect(sock, "control", "suzuri", |_| {}) {
        Ok(c) => {
            let _ = c.send(&json!({ "t": "clip_answer", "n": n, "text": text }));
        }
        Err(e) => log::warn!("keeper: clipboard answer: {e}"),
    }
}

impl Drop for Kept {
    /// The app lets go of the shell; it keeps running in its host.
    fn drop(&mut self) {
        self.let_go();
    }
}

// ---- starting a host, and finding the ones already running

/// What a new host is told on its stdin.
pub struct Spawn<'a> {
    pub id: u32,
    pub cfg: &'a rsz_term::PaneConfig,
    pub palette: Palette,
    pub version: &'a str,
}

/// Start a host for pane `id` and wait until it serves its socket. A host
/// that ends before it says so (seen once on Windows, its first start from
/// a new copy) is started once more before the pane gives up on it.
pub fn spawn(dir: &Path, s: Spawn) -> std::io::Result<()> {
    match spawn_once(dir, &s) {
        Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => {
            log::warn!("keeper: shell {}: {e}; starting it once more", s.id);
            spawn_once(dir, &s)
        }
        r => r,
    }
}

fn spawn_once(dir: &Path, s: &Spawn) -> std::io::Result<()> {
    use std::io::{BufRead, Write};
    use std::process::{Command, Stdio};
    let t0 = Instant::now();
    rsz_keeper::dir::ensure(dir)?;
    let exe = host_exe(dir)?;
    let log_path = dir.join(format!("{}.log", s.id));
    let log = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)?;
    let c = s.cfg;
    let cfg = json!({
        "cols": c.cols, "rows": c.rows, "command": c.command, "cwd": c.cwd, "env": c.env, "scrollback": c.scrollback,
        "plain_shell": c.plain_shell, "cursor": c.cursor, "term_program": c.term_program, "keep_prompt": c.keep_prompt,
        "preamble": c.preamble,
        "palette": palette_json(&s.palette), "version": s.version,
    });
    let mut cmd = Command::new(exe);
    // Its folder, not the app's: a host must not hold the install folder.
    cmd.arg("--keeper-host")
        .arg(dir)
        .arg(s.id.to_string())
        .current_dir(dir)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(log);
    let mut child = detached(&mut cmd)?;
    child
        .stdin
        .take()
        .expect("stdin")
        .write_all(cfg.to_string().as_bytes())?;
    let out = child.stdout.take().expect("stdout");
    let (tx, rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let mut line = String::new();
        let _ = std::io::BufReader::new(out).read_line(&mut line);
        let _ = tx.send(line);
    });
    // Reaped when it ends, so no zombie while the app lives; after the app
    // is gone it is init's. (Windows: this is the short-lived first one.)
    let (st_tx, st_rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let _ = st_tx.send(
            child
                .wait()
                .map(|st| st.to_string())
                .unwrap_or_else(|e| e.to_string()),
        );
    });
    match rx.recv_timeout(Duration::from_secs(10)) {
        Ok(l) if l.starts_with("ready") => {
            log::info!(
                "keeper: shell {} host ready in {} ms",
                s.id,
                t0.elapsed().as_millis()
            );
            Ok(())
        }
        Ok(l) if l.trim().is_empty() => {
            // Ended without a word: say how, and what it logged.
            let st = st_rx
                .recv_timeout(Duration::from_secs(1))
                .unwrap_or_else(|_| "still running".into());
            let tail = std::fs::read_to_string(&log_path).unwrap_or_default();
            let tail: String = tail
                .chars()
                .rev()
                .take(400)
                .collect::<Vec<_>>()
                .into_iter()
                .rev()
                .collect();
            Err(std::io::Error::new(
                std::io::ErrorKind::UnexpectedEof,
                format!(
                    "keeper host ended before it was ready ({st}); its log: {}",
                    tail.trim()
                ),
            ))
        }
        Ok(l) => Err(std::io::Error::other(format!("keeper host: {}", l.trim()))),
        Err(_) => Err(std::io::Error::other("keeper host: did not start")),
    }
}

/// Whether `path` is a socket another program (Hearth) keeps a shell on
/// that this window may show, checked as this user's only (macOS, Linux
/// and Windows): see [`rsz_keeper::dir::foreign_sock`].
pub use rsz_keeper::dir::foreign_sock;

/// The host's program file name on Windows: not suzuri.exe, which the
/// update's installer looks for.
#[cfg(windows)]
const HOST_EXE: &str = "suzuri-keeper.exe";

/// This build's own program, which a host runs. On Windows a copy of it in
/// `<dir>/host/<build>/`, made here once per build: the update can then
/// replace suzuri.exe while hosts run.
#[cfg(unix)]
fn host_exe(_: &Path) -> std::io::Result<PathBuf> {
    std::env::current_exe()
}

#[cfg(windows)]
fn host_exe(dir: &Path) -> std::io::Result<PathBuf> {
    static COPYING: Mutex<()> = Mutex::new(());
    let _one = COPYING.lock();
    let exe = std::env::current_exe()?;
    let to = dir.join("host").join(build_name(&exe)?);
    let bin = to.join(HOST_EXE);
    if bin.is_file() {
        return Ok(bin);
    }
    let tmp = to.with_extension(format!("tmp{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&tmp);
    std::fs::create_dir_all(&tmp)?;
    let copy = || -> std::io::Result<()> {
        std::fs::copy(&exe, tmp.join(HOST_EXE))?;
        // The bundled ConPTY: a host loads conpty.dll from its own folder.
        for f in ["conpty.dll", "OpenConsole.exe"] {
            let src = exe.with_file_name(f);
            if src.is_file() {
                std::fs::copy(&src, tmp.join(f))?;
            }
        }
        std::fs::rename(&tmp, &to)
    };
    if let Err(e) = copy() {
        let _ = std::fs::remove_dir_all(&tmp);
        // Another window made it first.
        if !bin.is_file() {
            return Err(e);
        }
    }
    log::info!("keeper: host program {}", bin.display());
    Ok(bin)
}

/// `<version>-<size>-<modified>` of this program: a new build, even of the
/// same version, gets its own copy.
#[cfg(windows)]
fn build_name(exe: &Path) -> std::io::Result<String> {
    let md = std::fs::metadata(exe)?;
    let t = md
        .modified()?
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    Ok(format!(
        "{}-{:x}-{:x}",
        env!("CARGO_PKG_VERSION"),
        md.len(),
        t
    ))
}

/// At start: make this build's host copy (so the first pane doesn't wait
/// for it) and remove the copies no host runs from any more (Windows only).
pub fn prepare(dir: &Path) {
    #[cfg(windows)]
    {
        let dir = dir.to_path_buf();
        std::thread::spawn(move || {
            if let Err(e) = host_exe(&dir) {
                log::warn!("keeper: host program not copied: {e}");
            }
            let Ok(mine) = std::env::current_exe().and_then(|exe| build_name(&exe)) else {
                return;
            };
            for e in std::fs::read_dir(dir.join("host"))
                .into_iter()
                .flatten()
                .flatten()
            {
                let name = e.file_name().to_string_lossy().into_owned();
                if name == mine || name.contains(".tmp") {
                    continue;
                }
                // A running host's program can't be removed: then the copy stays.
                let bin = e.path().join(HOST_EXE);
                if std::fs::remove_file(&bin).is_ok() || !bin.exists() {
                    let _ = std::fs::remove_dir_all(e.path());
                    log::info!("keeper: removed old host program {name}");
                }
            }
        });
    }
    #[cfg(not(windows))]
    let _ = dir;
}

/// The hosts in `dir` that answer, by id, with their hello. Sockets
/// nobody answers on are left by a host that was killed: removed.
pub fn live(dir: &Path) -> Vec<(u32, Value)> {
    let mut out = Vec::new();
    for id in rsz_keeper::dir::list(dir) {
        let path = rsz_keeper::dir::sock_path(dir, id);
        match rsz_keeper::Conn::connect(&path, "control", "suzuri", |_| {}) {
            Ok(c) => out.push((id, c.hello.clone())),
            Err(e)
                if matches!(
                    e.kind(),
                    std::io::ErrorKind::ConnectionRefused | std::io::ErrorKind::NotFound
                ) =>
            {
                let _ = std::fs::remove_file(&path);
                let _ = std::fs::remove_file(dir.join(format!("{id}.log")));
            }
            Err(e) => log::warn!("keeper: shell {id}: {e}"),
        }
    }
    out
}

// ---- the saved layout (tabs and splits), per app process

/// The UI's layout, saved on every change: `layout-<pid>.json`.
pub fn save_layout(dir: &Path, layout: &Value) -> std::io::Result<()> {
    rsz_keeper::dir::ensure(dir)?;
    let path = dir.join(format!("layout-{}.json", std::process::id()));
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, layout.to_string())?;
    std::fs::rename(tmp, path)
}

/// The layouts left by app processes that are gone, newest first; their
/// files are removed (this process saves its own from now on).
pub fn take_layouts(dir: &Path) -> Vec<Value> {
    let mut found: Vec<(std::time::SystemTime, Value)> = Vec::new();
    for e in std::fs::read_dir(dir).into_iter().flatten().flatten() {
        let name = e.file_name().to_string_lossy().into_owned();
        let Some(pid) = name
            .strip_prefix("layout-")
            .and_then(|n| n.strip_suffix(".json"))
            .and_then(|n| n.parse::<u32>().ok())
        else {
            continue;
        };
        if pid == std::process::id() || alive(pid) {
            continue;
        }
        let when = e
            .metadata()
            .and_then(|m| m.modified())
            .unwrap_or(std::time::UNIX_EPOCH);
        if let Some(v) = std::fs::read(e.path())
            .ok()
            .and_then(|b| serde_json::from_slice(&b).ok())
        {
            found.push((when, v));
        }
        let _ = std::fs::remove_file(e.path());
    }
    found.sort_by_key(|f| std::cmp::Reverse(f.0));
    found.into_iter().map(|(_, v)| v).collect()
}

fn alive(pid: u32) -> bool {
    #[cfg(unix)]
    {
        // SAFETY: signal 0 only checks that the process exists.
        unsafe {
            libc::kill(pid as i32, 0) == 0
                || std::io::Error::last_os_error().raw_os_error() == Some(libc::EPERM)
        }
    }
    #[cfg(windows)]
    {
        use windows_sys::Win32::Foundation::{CloseHandle, STILL_ACTIVE};
        use windows_sys::Win32::System::Threading::{
            GetExitCodeProcess, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION,
        };
        // SAFETY: OpenProcess has no preconditions; a null handle is checked.
        let h = unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid) };
        if h.is_null() {
            return false;
        }
        let mut code = 0u32;
        // SAFETY: h came from OpenProcess and is closed once.
        let ok = unsafe { GetExitCodeProcess(h, &mut code) } != 0;
        unsafe { CloseHandle(h) };
        ok && code == STILL_ACTIVE as u32
    }
}
