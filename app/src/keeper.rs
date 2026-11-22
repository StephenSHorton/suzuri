//! Shells that outlive the app (card hearth#913). On macOS and Linux each
//! pane's shell runs in its own host process, `suzuri --keeper-host <dir>
//! <id>`, started in its own session: the host owns the whole `Pane` (PTY,
//! screen, scrollback) and serves it on `<dir>/<id>.sock`. The app is a
//! viewer: [`Shell::Kept`] forwards every pane call over the socket and
//! turns the host's events back into `Out`s for the pane's sink. Quitting,
//! updating or crashing the app leaves the hosts; the next start lists them
//! and attaches. Closing a pane sends `close`, which ends the shell, and a
//! host exits when its shell does.
//!
//! The wire format and the client are crates/keeper (rsz-keeper), with no
//! rsz-term in it, so other programs (Hearth) can attach too. Both halves
//! of the messages are here, the host's ([`host`]) and the viewer's
//! ([`Kept`]); keep them in step and keep reading what older hosts send:
//! a host keeps the program it was started with until its shell ends.
//!
//! RSZ_KEEPER=0 keeps panes in the app, as before (and Windows, until its
//! half is built: the console belongs to the process that made it). A
//! bench or UI-test launch (RSZ_BENCH, RSZ_PANES, RSZ_UI_TEST, RSZ_CMD) keeps
//! them in the app too, so it never leaves shells behind.
#![cfg_attr(not(unix), allow(dead_code, unused_imports))]

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use base64::Engine as _;
use parking_lot::Mutex;
use rsz_term::images::{ImageData, Placement};
use rsz_term::palette::{Palette, Rgb};
use rsz_term::protocols::Notice;
use rsz_term::pane::ShellStatus;
use rsz_term::{Link, MouseEvent, MouseKind, Out, Pane, SelectKind, Sink, Submit};
use serde_json::{Value, json};

const B64: base64::engine::GeneralPurpose = base64::engine::general_purpose::STANDARD;

/// Kept shells are on: a Unix build, not turned off with RSZ_KEEPER=0.
pub fn enabled() -> bool {
    let set = |k: &str| std::env::var(k).is_ok_and(|v| !v.is_empty());
    cfg!(unix) && std::env::var("RSZ_KEEPER").as_deref() != Ok("0") && !["RSZ_BENCH", "RSZ_PANES", "RSZ_UI_TEST", "RSZ_CMD"].iter().any(|k| set(k))
}

pub fn dir() -> PathBuf {
    rsz_keeper::dir::default_dir()
}

// ---- what both halves send

fn rgb(c: Rgb) -> Value {
    json!([c.0, c.1, c.2])
}

fn rgb_of(v: &Value) -> Option<Rgb> {
    let a = v.as_array()?;
    Some((a.first()?.as_u64()? as u8, a.get(1)?.as_u64()? as u8, a.get(2)?.as_u64()? as u8))
}

pub fn palette_json(p: &Palette) -> Value {
    json!({ "t": "palette", "dark": p.dark, "bg": rgb(p.bg), "fg": rgb(p.fg), "panel": rgb(p.panel), "accent": rgb(p.accent),
            "ansi16": p.ansi16.iter().map(|c| rgb(*c)).collect::<Vec<_>>() })
}

fn palette_of(v: &Value) -> Option<Palette> {
    let a = v["ansi16"].as_array()?;
    let mut ansi16 = [(0, 0, 0); 16];
    for (i, c) in a.iter().take(16).enumerate() {
        ansi16[i] = rgb_of(c)?;
    }
    Some(Palette { dark: v["dark"].as_bool()?, bg: rgb_of(&v["bg"])?, fg: rgb_of(&v["fg"])?, panel: rgb_of(&v["panel"])?, accent: rgb_of(&v["accent"])?, ansi16 })
}

fn shell_json(st: &ShellStatus) -> Value {
    json!({ "t": "shell", "integrated": st.integrated, "cwd": st.cwd, "program": st.program, "prompts": st.prompts, "pending": st.pending })
}

fn image_json(d: &ImageData) -> Value {
    json!({ "t": "img", "id": d.id, "name": d.name, "path": d.path, "format": d.format, "w": d.width, "h": d.height, "z": d.compressed, "data": B64.encode(&d.data) })
}

fn placements_json(list: &[Placement]) -> Value {
    let list: Vec<Value> = list
        .iter()
        .map(|p| json!([p.image, p.row, p.col, p.cols, p.rows, p.z, p.inline, p.crop.0, p.crop.1, p.crop.2, p.crop.3]))
        .collect();
    json!({ "t": "imgpos", "list": list })
}

fn notice_json(n: &Notice) -> Value {
    json!({ "t": "notice", "id": n.id, "title": n.title, "body": n.body, "sound": n.sound, "icon": n.icon, "urgency": n.urgency,
            "occasion": n.occasion, "expire_ms": n.expire_ms, "report": n.report, "focus": n.focus, "close_event": n.close_event })
}

/// A host event back into the pane's Out (the viewer's half).
fn out_of(v: &Value) -> Option<Out> {
    let s = |k: &str| v[k].as_str().unwrap_or("").to_string();
    let u = |k: &str| v[k].as_u64().unwrap_or(0);
    let b = |k: &str| v[k].as_bool().unwrap_or(false);
    Some(match v["t"].as_str()? {
        "title" => Out::Title(s("title")),
        "bell" => Out::Bell,
        "clip" => Out::Clipboard(s("text")),
        "shell" => Out::Shell(ShellStatus { integrated: b("integrated"), cwd: s("cwd"), program: b("program"), prompts: u("prompts"), pending: b("pending") }),
        "done" => Out::CommandDone { exit: v["exit"].as_i64().map(|x| x as i32), command: s("command") },
        "typeahead" => Out::Typeahead(s("text")),
        "exit" => Out::Exit(v["code"].as_u64().map(|x| x as u32)),
        "notice" => Out::Notice(Notice {
            id: s("id"),
            title: s("title"),
            body: s("body"),
            sound: s("sound"),
            icon: s("icon"),
            urgency: u("urgency") as u8,
            occasion: s("occasion"),
            expire_ms: v["expire_ms"].as_u64().map(|x| x as u32),
            report: b("report"),
            focus: b("focus"),
            close_event: b("close_event"),
        }),
        "notice_close" => Out::NoticeClose(s("id")),
        "notice_alive" => Out::NoticeAlive(s("id")),
        "progress" => Out::Progress { state: u("state") as u8, percent: u("percent") as u8 },
        "img" => Out::ImageData(ImageData {
            id: u("id") as u32,
            name: s("name"),
            path: v["path"].as_str().map(str::to_string),
            format: u("format") as u32,
            width: u("w") as u32,
            height: u("h") as u32,
            compressed: b("z"),
            data: B64.decode(v["data"].as_str().unwrap_or("")).ok()?,
        }),
        "imgdrop" => Out::ImageDrop(v["ids"].as_array()?.iter().filter_map(|x| x.as_u64().map(|x| x as u32)).collect()),
        "imgpos" => Out::Placements(
            v["list"]
                .as_array()?
                .iter()
                .filter_map(|p| {
                    let a = p.as_array()?;
                    let n = |i: usize| a.get(i).and_then(|x| x.as_i64()).unwrap_or(0);
                    Some(Placement {
                        image: n(0) as u32,
                        row: n(1) as i32,
                        col: n(2) as u16,
                        cols: n(3) as u32,
                        rows: n(4) as u32,
                        z: n(5) as i32,
                        inline: a.get(6).and_then(|x| x.as_bool()).unwrap_or(false),
                        crop: (n(7) as u32, n(8) as u32, n(9) as u32, n(10) as u32),
                    })
                })
                .collect(),
        ),
        _ => return None,
    })
}

fn mouse_json(ev: &MouseEvent) -> Value {
    let (kind, held) = match ev.kind {
        MouseKind::Press => ("press", None),
        MouseKind::Release => ("release", None),
        MouseKind::Motion(b) => ("motion", b),
        MouseKind::WheelUp => ("wheel_up", None),
        MouseKind::WheelDown => ("wheel_down", None),
    };
    json!({ "t": "mouse", "kind": kind, "held": held, "button": ev.button, "col": ev.col, "row": ev.row, "shift": ev.shift, "alt": ev.alt, "ctrl": ev.ctrl })
}

fn mouse_of(v: &Value) -> Option<MouseEvent> {
    let kind = match v["kind"].as_str()? {
        "press" => MouseKind::Press,
        "release" => MouseKind::Release,
        "motion" => MouseKind::Motion(v["held"].as_u64().map(|b| b as u8)),
        "wheel_up" => MouseKind::WheelUp,
        "wheel_down" => MouseKind::WheelDown,
        _ => return None,
    };
    let b = |k: &str| v[k].as_bool().unwrap_or(false);
    Some(MouseEvent { kind, button: v["button"].as_u64()? as u8, col: v["col"].as_u64()? as u16, row: v["row"].as_u64()? as u16, shift: b("shift"), alt: b("alt"), ctrl: b("ctrl") })
}

fn select_kind(s: &str) -> SelectKind {
    match s {
        "word" => SelectKind::Word,
        "line" => SelectKind::Line,
        _ => SelectKind::Cell,
    }
}

fn select_kind_name(k: SelectKind) -> &'static str {
    match k {
        SelectKind::Cell => "cell",
        SelectKind::Word => "word",
        SelectKind::Line => "line",
    }
}

fn submit_json(s: Submit) -> Value {
    match s {
        Submit::Blank => json!({ "kind": "blank" }),
        Submit::Sent => json!({ "kind": "sent" }),
        Submit::Queued(n) => json!({ "kind": "queued", "n": n }),
        Submit::Busy => json!({ "kind": "busy" }),
    }
}

fn submit_of(v: &Value) -> Submit {
    match v["kind"].as_str() {
        Some("blank") => Submit::Blank,
        Some("queued") => Submit::Queued(v["n"].as_u64().unwrap_or(0) as usize),
        Some("busy") => Submit::Busy,
        _ => Submit::Sent,
    }
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
    #[cfg_attr(not(unix), allow(dead_code))]
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
        both!(self, p => p.close(), k => k.send(json!({ "t": "close" })))
    }
    pub fn cwd(&self) -> Option<String> {
        both!(self, p => p.cwd(), k => k.call(json!({ "t": "cwd" })).ok().and_then(|v| v.as_str().map(str::to_string)))
    }
    pub fn stats(&self) -> StatsSnap {
        match self {
            Shell::Local(p) => {
                let s = p.stats();
                let l = |a: &std::sync::atomic::AtomicU64| a.load(Ordering::Relaxed);
                StatsSnap { bytes_in: l(&s.bytes_in), feed_ns: l(&s.feed_ns), frames: l(&s.frames), frame_bytes: l(&s.frame_bytes), snapshot_ns: l(&s.snapshot_ns), writes: l(&s.writes) }
            }
            Shell::Kept(k) => {
                let v = k.call(json!({ "t": "stats" })).unwrap_or(Value::Null);
                let n = |f: &str| v[f].as_u64().unwrap_or(0);
                StatsSnap { bytes_in: n("bytes_in"), feed_ns: n("feed_ns"), frames: n("frames"), frame_bytes: n("frame_bytes"), snapshot_ns: n("snapshot_ns"), writes: n("writes") }
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
                (v["name"].as_str().map(str::to_string), v["pid"].as_u64().map(|n| n as u32), cwd)
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
    #[cfg(unix)]
    conn: rsz_keeper::Conn,
    #[cfg(not(unix))]
    conn: NoConn,
    last_out: Arc<Mutex<Instant>>,
    /// Set when this app lets go on purpose: no Exit for the pane then.
    detached: Arc<AtomicBool>,
    pub hello: Value,
}

#[cfg(not(unix))]
pub struct NoConn;
#[cfg(not(unix))]
impl NoConn {
    fn input(&self, _: &[u8]) -> std::io::Result<()> {
        Ok(())
    }
}

impl Kept {
    fn send(&self, v: Value) {
        #[cfg(unix)]
        {
            let _ = self.conn.send(&v);
        }
        #[cfg(not(unix))]
        let _ = v;
    }

    fn call(&self, v: Value) -> std::io::Result<Value> {
        #[cfg(unix)]
        {
            self.conn.call(v)
        }
        #[cfg(not(unix))]
        {
            let _ = v;
            Err(std::io::Error::other("no keeper"))
        }
    }

    /// (screen, the last `lines` of scrollback).
    fn text(&self, lines: usize) -> (String, Vec<String>) {
        let v = self.call(json!({ "t": "text", "lines": lines })).unwrap_or(Value::Null);
        let tail = v["tail"].as_array().map(|a| a.iter().filter_map(|l| l.as_str().map(str::to_string)).collect()).unwrap_or_default();
        (v["screen"].as_str().unwrap_or("").to_string(), tail)
    }

    /// Attach to host `id` as the app's viewer; its events reach `sink`.
    #[cfg(unix)]
    pub fn attach(dir: &Path, id: u32, sink: Sink) -> std::io::Result<Kept> {
        let last_out = Arc::new(Mutex::new(Instant::now()));
        let detached = Arc::new(AtomicBool::new(false));
        let exited = Arc::new(AtomicBool::new(false));
        let (lo, de) = (last_out.clone(), detached.clone());
        let conn = rsz_keeper::Conn::connect(&rsz_keeper::dir::sock_path(dir, id), "viewer", "suzuri", move |e| match e {
            rsz_keeper::Event::Frame(f) => {
                *lo.lock() = Instant::now();
                sink(Out::Frame(f));
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
        Ok(Kept { conn, last_out, detached, hello })
    }
}

impl Drop for Kept {
    /// The app lets go of the shell; it keeps running in its host.
    fn drop(&mut self) {
        self.detached.store(true, Ordering::Relaxed);
        #[cfg(unix)]
        self.conn.detach();
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

/// Start a host for pane `id` and wait until it serves its socket.
#[cfg(unix)]
pub fn spawn(dir: &Path, s: Spawn) -> std::io::Result<()> {
    use std::io::{BufRead, Write};
    use std::os::unix::process::CommandExt;
    use std::process::{Command, Stdio};
    rsz_keeper::dir::ensure(dir)?;
    let exe = std::env::current_exe()?;
    let log = std::fs::OpenOptions::new().create(true).append(true).open(dir.join(format!("{}.log", s.id)))?;
    let c = s.cfg;
    let cfg = json!({
        "cols": c.cols, "rows": c.rows, "command": c.command, "cwd": c.cwd, "env": c.env, "scrollback": c.scrollback,
        "plain_shell": c.plain_shell, "cursor": c.cursor, "term_program": c.term_program, "keep_prompt": c.keep_prompt,
        "preamble": c.preamble,
        "palette": palette_json(&s.palette), "version": s.version,
    });
    let mut cmd = Command::new(exe);
    cmd.arg("--keeper-host").arg(dir).arg(s.id.to_string()).stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(log);
    // Its own session: no hangup when the app's terminal or process group goes.
    // SAFETY: setsid is async-signal-safe.
    unsafe {
        cmd.pre_exec(|| {
            libc::setsid();
            Ok(())
        });
    }
    let mut child = cmd.spawn()?;
    child.stdin.take().expect("stdin").write_all(cfg.to_string().as_bytes())?;
    let out = child.stdout.take().expect("stdout");
    let (tx, rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let mut line = String::new();
        let _ = std::io::BufReader::new(out).read_line(&mut line);
        let _ = tx.send(line);
    });
    // Reaped when it ends, so no zombie while the app lives; after the app
    // is gone it is init's.
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    match rx.recv_timeout(Duration::from_secs(10)) {
        Ok(l) if l.starts_with("ready") => Ok(()),
        Ok(l) => Err(std::io::Error::other(format!("keeper host: {}", l.trim()))),
        Err(_) => Err(std::io::Error::other("keeper host: did not start")),
    }
}

#[cfg(not(unix))]
pub fn spawn(_: &Path, _: Spawn) -> std::io::Result<()> {
    Err(std::io::Error::other("kept shells are macOS and Linux only"))
}

#[cfg(not(unix))]
impl Kept {
    pub fn attach(_: &Path, _: u32, _: Sink) -> std::io::Result<Kept> {
        Err(std::io::Error::other("kept shells are macOS and Linux only"))
    }
}

/// The hosts in `dir` that answer, by id, with their hello. Sockets
/// nobody answers on are left by a host that was killed: removed.
#[cfg(unix)]
pub fn live(dir: &Path) -> Vec<(u32, Value)> {
    let mut out = Vec::new();
    for id in rsz_keeper::dir::list(dir) {
        let path = rsz_keeper::dir::sock_path(dir, id);
        match rsz_keeper::Conn::connect(&path, "control", "suzuri", |_| {}) {
            Ok(c) => out.push((id, c.hello.clone())),
            Err(e) if matches!(e.kind(), std::io::ErrorKind::ConnectionRefused | std::io::ErrorKind::NotFound) => {
                let _ = std::fs::remove_file(&path);
                let _ = std::fs::remove_file(dir.join(format!("{id}.log")));
            }
            Err(e) => log::warn!("keeper: shell {id}: {e}"),
        }
    }
    out
}

#[cfg(not(unix))]
pub fn live(_: &Path) -> Vec<(u32, Value)> {
    Vec::new()
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
        let Some(pid) = name.strip_prefix("layout-").and_then(|n| n.strip_suffix(".json")).and_then(|n| n.parse::<u32>().ok()) else { continue };
        if pid == std::process::id() || alive(pid) {
            continue;
        }
        let when = e.metadata().and_then(|m| m.modified()).unwrap_or(std::time::UNIX_EPOCH);
        if let Some(v) = std::fs::read(e.path()).ok().and_then(|b| serde_json::from_slice(&b).ok()) {
            found.push((when, v));
        }
        let _ = std::fs::remove_file(e.path());
    }
    found.sort_by(|a, b| b.0.cmp(&a.0));
    found.into_iter().map(|(_, v)| v).collect()
}

fn alive(pid: u32) -> bool {
    #[cfg(unix)]
    {
        // SAFETY: signal 0 only checks that the process exists.
        unsafe { libc::kill(pid as i32, 0) == 0 || std::io::Error::last_os_error().raw_os_error() == Some(libc::EPERM) }
    }
    #[cfg(not(unix))]
    {
        let _ = pid;
        false
    }
}

// ---- the host

#[cfg(unix)]
pub mod host {
    use super::*;
    use std::collections::VecDeque;
    use std::io::{Read, Write};
    use std::os::unix::net::{UnixListener, UnixStream};
    use std::sync::OnceLock;
    use std::sync::mpsc::{SyncSender, TrySendError, sync_channel};

    use rsz_keeper::{K_FRAME, K_INPUT, K_JSON, PROTO, wire};
    use rsz_term::PaneConfig;

    pub fn wants(args: &[String]) -> bool {
        args.get(1).is_some_and(|a| a == "--keeper-host")
    }

    /// How many messages a viewer may fall behind before it is let go (it
    /// can attach again; the shell is not touched).
    const BEHIND_MAX: usize = 1024;

    struct Client {
        n: u64,
        tx: SyncSender<Arc<Vec<u8>>>,
        viewer: bool,
        stream: UnixStream,
    }

    /// What a viewer that attaches now must be told first: events the pane
    /// sends only when they change.
    #[derive(Default)]
    struct Cache {
        title: Option<String>,
        shell: Option<Value>,
        progress: Option<Value>,
        images: HashMap<u32, Value>,
        placements: Option<Value>,
        exited: Option<Option<u32>>,
    }

    #[derive(Default)]
    struct Hub {
        next: u64,
        clients: Vec<Client>,
        cache: Cache,
    }

    struct Host {
        hub: Mutex<Hub>,
        pane: OnceLock<Pane>,
        hello: Mutex<Value>,
    }

    impl Host {
        fn broadcast(hub: &mut Hub, msg: Vec<u8>, viewers_only: bool) {
            let msg = Arc::new(msg);
            hub.clients.retain(|c| {
                if viewers_only && !c.viewer {
                    return true;
                }
                match c.tx.try_send(msg.clone()) {
                    Ok(()) => true,
                    Err(TrySendError::Full(_)) | Err(TrySendError::Disconnected(_)) => {
                        let _ = c.stream.shutdown(std::net::Shutdown::Both);
                        false
                    }
                }
            });
        }

        fn out(self: &Arc<Self>, o: Out) {
            let mut hub = self.hub.lock();
            let ev = match o {
                Out::Frame(f) => return Self::broadcast(&mut hub, wire::encode(K_FRAME, &f), true),
                Out::Title(t) => {
                    hub.cache.title = Some(t.clone());
                    json!({ "t": "title", "title": t })
                }
                Out::Bell => json!({ "t": "bell" }),
                Out::Clipboard(s) => json!({ "t": "clip", "text": s }),
                Out::Shell(st) => {
                    let v = shell_json(&st);
                    hub.cache.shell = Some(v.clone());
                    v
                }
                Out::CommandDone { exit, command } => json!({ "t": "done", "exit": exit, "command": command }),
                Out::Typeahead(t) => json!({ "t": "typeahead", "text": t }),
                Out::Exit(code) => {
                    hub.cache.exited = Some(code);
                    let v = json!({ "t": "exit", "code": code });
                    Self::broadcast(&mut hub, wire::encode_json(&v), true);
                    drop(hub);
                    self.finish();
                    return;
                }
                Out::Notice(n) => notice_json(&n),
                Out::NoticeClose(id) => json!({ "t": "notice_close", "id": id }),
                Out::NoticeAlive(id) => json!({ "t": "notice_alive", "id": id }),
                Out::Progress { state, percent } => {
                    let v = json!({ "t": "progress", "state": state, "percent": percent });
                    hub.cache.progress = Some(v.clone());
                    v
                }
                Out::ImageData(d) => {
                    let v = image_json(&d);
                    hub.cache.images.insert(d.id, v.clone());
                    v
                }
                Out::ImageDrop(ids) => {
                    for id in &ids {
                        hub.cache.images.remove(id);
                    }
                    json!({ "t": "imgdrop", "ids": ids })
                }
                Out::Placements(list) => {
                    let v = placements_json(&list);
                    hub.cache.placements = Some(v.clone());
                    v
                }
            };
            Self::broadcast(&mut hub, wire::encode_json(&ev), true);
        }

        /// The shell ended: let the viewers read the exit, then go.
        fn finish(self: &Arc<Self>) {
            let path = PathBuf::from(self.hello.lock()["sock"].as_str().unwrap_or("").to_string());
            std::thread::spawn(move || {
                let _ = std::fs::remove_file(&path);
                let _ = std::fs::remove_file(path.with_extension("log"));
                std::thread::sleep(Duration::from_millis(300));
                std::process::exit(0);
            });
        }

        fn hello(&self) -> Value {
            // Asked before the hub lock: the pane's threads take it to send.
            let cwd = self.pane.get().and_then(|p| p.cwd());
            let mut h = self.hello.lock().clone();
            h["cwd"] = cwd.into();
            let hub = self.hub.lock();
            h["viewers"] = hub.clients.iter().filter(|c| c.viewer).count().into();
            h["title"] = hub.cache.title.clone().into();
            h["exited"] = hub.cache.exited.map(|c| json!({ "code": c })).unwrap_or(Value::Null);
            h
        }

        fn serve(self: Arc<Self>, mut stream: UnixStream) {
            if !rsz_keeper::dir::peer_is_me(&stream) {
                return;
            }
            let _ = stream.set_read_timeout(Some(Duration::from_secs(5)));
            let Ok(hi) = wire::read(&mut stream).and_then(|(k, b)| wire::json(k, &b)) else { return };
            if hi["t"] != "hello" {
                return;
            }
            let _ = stream.set_read_timeout(None);
            let viewer = hi["role"] == "viewer";
            let Ok(mut out) = stream.try_clone() else { return };
            let (tx, rx) = sync_channel::<Arc<Vec<u8>>>(BEHIND_MAX);
            std::thread::spawn(move || {
                while let Ok(m) = rx.recv() {
                    if out.write_all(&m).is_err() {
                        break;
                    }
                }
                let _ = out.shutdown(std::net::Shutdown::Both);
            });
            // The hello and, for a viewer, the cached state go first, under
            // the hub lock: no event can slip in between.
            let hello = self.hello();
            let me;
            {
                let mut hub = self.hub.lock();
                let mut first: VecDeque<Value> = VecDeque::from([hello]);
                if viewer {
                    let c = &hub.cache;
                    if let Some(t) = &c.title {
                        first.push_back(json!({ "t": "title", "title": t }));
                    }
                    first.extend(c.shell.iter().cloned());
                    first.extend(c.progress.iter().cloned());
                    first.extend(c.images.values().cloned());
                    first.extend(c.placements.iter().cloned());
                    if let Some(code) = c.exited {
                        first.push_back(json!({ "t": "exit", "code": code }));
                    }
                }
                for v in first {
                    let _ = tx.try_send(Arc::new(wire::encode_json(&v)));
                }
                let Ok(s2) = stream.try_clone() else { return };
                hub.next += 1;
                me = hub.next;
                hub.clients.push(Client { n: me, tx: tx.clone(), viewer, stream: s2 });
            }
            if viewer {
                if let Some(p) = self.pane.get() {
                    p.redraw();
                }
            }
            loop {
                let Ok((kind, body)) = wire::read(&mut stream) else { break };
                let Some(p) = self.pane.get() else { continue };
                match kind {
                    K_INPUT => p.write(body),
                    K_JSON => {
                        let Ok(v) = serde_json::from_slice::<Value>(&body) else { continue };
                        if v["t"] == "resize" {
                            let mut h = self.hello.lock();
                            h["cols"] = v["cols"].clone();
                            h["rows"] = v["rows"].clone();
                        }
                        if let Some(ans) = handle(p, &v) {
                            if let Some(q) = v["q"].as_u64() {
                                let _ = tx.try_send(Arc::new(wire::encode_json(&json!({ "t": "r", "q": q, "v": ans }))));
                            }
                        }
                    }
                    _ => {}
                }
            }
            let mut hub = self.hub.lock();
            hub.clients.retain(|c| c.n != me);
        }
    }

    /// One client message: Some(answer) for a request.
    fn handle(p: &Pane, v: &Value) -> Option<Value> {
        let u = |k: &str| v[k].as_u64().unwrap_or(0);
        let b = |k: &str| v[k].as_bool().unwrap_or(false);
        let s = |k: &str| v[k].as_str().unwrap_or("");
        let none = Some(Value::Null);
        match v["t"].as_str()? {
            "resize" => p.resize(u("cols") as u16, u("rows") as u16),
            "scroll" => p.scroll(v["delta"].as_i64().unwrap_or(0) as i32),
            "ack" => p.ack(u("seq") as u32),
            "focus" => p.set_focused(b("on")),
            "redraw" => p.redraw(),
            "cell_px" => p.set_cell_px(u("w") as u16, u("h") as u16),
            "cursor" => p.set_default_cursor(u("shape") as u8),
            "palette" => {
                if let Some(pal) = palette_of(v) {
                    if rsz_term::palette::set_active(pal) {
                        p.redraw();
                    }
                }
            }
            "close" => p.close(),
            "reply" => p.reply(B64.decode(s("data")).unwrap_or_default()),
            "paste" => p.paste(s("text")),
            "select" => match s("op") {
                "start" => p.select_start(u("col") as u16, u("row") as u16, b("right"), select_kind(s("kind"))),
                "extend" => p.select_extend(u("col") as u16, u("row") as u16, b("right")),
                _ => p.select_clear(),
            },
            "submit" => return Some(submit_json(p.submit(s("line")))),
            "try_submit" => return Some(submit_json(p.try_submit(s("line")))),
            "flush_queue" => return Some(p.flush_queue().into()),
            "clear_queue" => return Some(p.clear_queue().into()),
            "queue_len" => return Some(p.queue_len().into()),
            "is_busy" => return Some(p.is_busy().into()),
            "cwd" => return Some(p.cwd().into()),
            "program" => return Some(json!({ "name": p.program_name(), "pid": p.program_pid(), "cwd": p.live_cwd() })),
            "state" => return Some(format!("{:?}", p.state()).into()),
            "alt_screen" => return Some(p.alt_screen().into()),
            "selection" => return Some(p.selection_text().into()),
            "text" => {
                let lines = u("lines") as usize;
                let tail = if lines > 0 { p.history_tail(lines) } else { Vec::new() };
                return Some(json!({ "screen": p.screen_text(), "tail": tail }));
            }
            "link" => {
                return Some(match p.link_at(u("col") as u16, u("row") as u16) {
                    Some(l) => json!({ "url": l.url, "row": l.row, "x0": l.x0, "x1": l.x1, "osc8": l.osc8 }),
                    None => Value::Null,
                });
            }
            "mouse" => return Some(mouse_of(v).is_some_and(|ev| p.mouse(ev)).into()),
            "stats" => {
                let st = p.stats();
                let l = |a: &std::sync::atomic::AtomicU64| a.load(Ordering::Relaxed);
                return Some(json!({ "bytes_in": l(&st.bytes_in), "feed_ns": l(&st.feed_ns), "frames": l(&st.frames), "frame_bytes": l(&st.frame_bytes), "snapshot_ns": l(&st.snapshot_ns), "writes": l(&st.writes) }));
            }
            _ => {}
        }
        if v["q"].is_u64() { none } else { None }
    }

    /// `suzuri --keeper-host <dir> <id>`, its config on stdin. Prints
    /// "ready <pid>" once it serves its socket (or "error ..."), then lives
    /// until its shell ends.
    pub fn run(args: &[String]) -> i32 {
        // SAFETY: setting signal dispositions at the start of main.
        unsafe {
            libc::signal(libc::SIGHUP, libc::SIG_IGN);
            libc::signal(libc::SIGPIPE, libc::SIG_IGN);
        }
        let fail = |m: String| {
            println!("error {m}");
            eprintln!("keeper host: {m}");
            1
        };
        let (Some(dir), Some(id)) = (args.first().map(PathBuf::from), args.get(1).and_then(|s| s.parse::<u32>().ok())) else {
            return fail("usage: suzuri --keeper-host <dir> <id>".into());
        };
        let mut input = String::new();
        if let Err(e) = std::io::stdin().read_to_string(&mut input) {
            return fail(format!("stdin: {e}"));
        }
        let c: Value = serde_json::from_str(&input).unwrap_or(Value::Null);
        let strs = |v: &Value| v.as_array().map(|a| a.iter().filter_map(|x| x.as_str().map(str::to_string)).collect::<Vec<_>>());
        let cfg = PaneConfig {
            cols: c["cols"].as_u64().unwrap_or(80) as u16,
            rows: c["rows"].as_u64().unwrap_or(24) as u16,
            command: strs(&c["command"]),
            cwd: c["cwd"].as_str().map(str::to_string),
            env: c["env"]
                .as_array()
                .map(|a| a.iter().filter_map(|kv| Some((kv.get(0)?.as_str()?.to_string(), kv.get(1)?.as_str()?.to_string()))).collect())
                .unwrap_or_default(),
            scrollback: c["scrollback"].as_u64().unwrap_or(10_000) as usize,
            plain_shell: c["plain_shell"].as_bool().unwrap_or(false),
            cursor: c["cursor"].as_u64().unwrap_or(1) as u8,
            term_program: c["term_program"].as_str().map(str::to_string),
            keep_prompt: c["keep_prompt"].as_bool().unwrap_or(false),
            preamble: c["preamble"].as_str().map(str::to_string),
        };
        if let Some(pal) = palette_of(&c["palette"]) {
            rsz_term::palette::set_active(pal);
        }
        if let Err(e) = rsz_keeper::dir::ensure(&dir) {
            return fail(e.to_string());
        }
        let sock = rsz_keeper::dir::sock_path(&dir, id);
        if std::os::unix::net::UnixStream::connect(&sock).is_ok() {
            return fail(format!("shell {id} is kept already"));
        }
        let _ = std::fs::remove_file(&sock);
        let listener = match UnixListener::bind(&sock) {
            Ok(l) => l,
            Err(e) => return fail(format!("bind {}: {e}", sock.display())),
        };
        {
            use std::os::unix::fs::PermissionsExt;
            let _ = std::fs::set_permissions(&sock, std::fs::Permissions::from_mode(0o600));
        }
        let started = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0);
        let host = Arc::new(Host {
            hub: Mutex::default(),
            pane: OnceLock::new(),
            hello: Mutex::new(json!({
                "t": "hello", "proto": PROTO, "min": 1, "frame": rsz_term::frame::FORMAT_VERSION,
                "version": c["version"].as_str().unwrap_or(env!("CARGO_PKG_VERSION")),
                "exe": std::env::current_exe().ok().map(|p| p.display().to_string()),
                "id": id, "pid": std::process::id(), "cols": cfg.cols, "rows": cfg.rows,
                "command": cfg.command, "started": started, "sock": sock.display().to_string(),
            })),
        });
        let h2 = host.clone();
        let sink: Sink = Arc::new(move |o| h2.out(o));
        let pane = match Pane::spawn(cfg, sink) {
            Ok(p) => p,
            Err(e) => {
                let _ = std::fs::remove_file(&sock);
                return fail(format!("spawn: {e}"));
            }
        };
        host.hello.lock()["shell_pid"] = pane.pid().into();
        let _ = host.pane.set(pane);
        println!("ready {}", std::process::id());
        let _ = std::io::stdout().flush();
        // Nothing more on stdout or from stdin: the app may be gone.
        if let Ok(null) = std::fs::OpenOptions::new().read(true).write(true).open("/dev/null") {
            use std::os::fd::AsRawFd;
            // SAFETY: dup2 onto the standard descriptors.
            unsafe {
                libc::dup2(null.as_raw_fd(), 0);
                libc::dup2(null.as_raw_fd(), 1);
            }
        }
        for stream in listener.incoming() {
            let Ok(stream) = stream else { continue };
            let h = host.clone();
            std::thread::spawn(move || h.serve(stream));
        }
        0
    }
}
