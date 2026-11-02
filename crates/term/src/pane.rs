//! One pane: a PTY session actor plus a reader thread and a frame thread.
//!
//! Thread ownership (handoff §2.1, walnut's E1 notes on #255):
//! - the actor thread alone writes to the PTY, resizes it and closes it, so
//!   WriteFile / ResizePseudoConsole / ClosePseudoConsole never overlap;
//! - the reader thread alone reads the PTY and feeds the VT engine; EOF is
//!   reported to the actor;
//! - the frame thread alone takes snapshots and hands frames to the sink;
//! - the caller (the UI) only sends commands and receives frames.
//!
//! States: Starting -> Running -> Closing -> Closed. A command in the wrong
//! state is a no-op. Resize is latest-wins. Close, shell exit and reader EOF
//! all enter one close sequence and the sink sees exactly one Exit.

use std::io::{Read, Write};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use crossbeam_channel::{Receiver, Sender, unbounded};
use parking_lot::{Condvar, Mutex};
use portable_pty::{ChildKiller, CommandBuilder, MasterPty, PtySize, native_pty_system};

use crate::engine::{Alacritty, Engine, EngineEvent};

/// What a pane sends to its owner.
pub enum Out {
    Frame(Vec<u8>),
    Title(String),
    Bell,
    Clipboard(String),
    /// The pane is closed; sent once. The exit code if the shell reported one.
    Exit(Option<u32>),
}

pub type Sink = Arc<dyn Fn(Out) + Send + Sync>;

#[derive(Clone, Debug)]
pub struct PaneConfig {
    pub cols: u16,
    pub rows: u16,
    /// argv; None runs the user's shell.
    pub command: Option<Vec<String>>,
    pub cwd: Option<String>,
    pub env: Vec<(String, String)>,
    pub scrollback: usize,
}

impl Default for PaneConfig {
    fn default() -> Self {
        Self { cols: 80, rows: 24, command: None, cwd: None, env: Vec::new(), scrollback: 10_000 }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum State {
    Starting,
    Running,
    Closing,
    Closed,
}

enum Cmd {
    Write(Vec<u8>),
    Resize,
    Close,
    ReaderEof,
    ChildExit(Option<u32>),
}

#[derive(Default)]
pub struct Stats {
    pub bytes_in: AtomicU64,
    pub feed_ns: AtomicU64,
    pub frames: AtomicU64,
    pub frame_bytes: AtomicU64,
    pub snapshot_ns: AtomicU64,
    pub writes: AtomicU64,
    pub resizes_applied: AtomicU64,
}

struct Wake {
    dirty: bool,
    force_full: bool,
    input_at: Option<Instant>,
    stop: bool,
    /// Flow control: the last frame sent, when, and the last the UI says it
    /// painted. At most one frame is in flight per pane; damage piles up in
    /// the engine meanwhile, so a slow UI gets fewer, larger frames instead
    /// of a growing queue.
    sent: u32,
    sent_at: Instant,
    acked: u32,
}

struct Shared {
    engine: Mutex<Box<dyn Engine>>,
    wake: Mutex<Wake>,
    cv: Condvar,
    resize_slot: Mutex<Option<(u16, u16)>>,
    focused: AtomicBool,
    state: Mutex<State>,
    stats: Stats,
}

impl Shared {
    fn kick(&self, f: impl FnOnce(&mut Wake)) {
        let mut w = self.wake.lock();
        f(&mut w);
        self.cv.notify_one();
    }
}

pub struct Pane {
    tx: Sender<Cmd>,
    shared: Arc<Shared>,
    threads: Mutex<Vec<JoinHandle<()>>>,
}

/// Frame pacing: a focused pane may send at ~120 Hz, an unfocused one at
/// ~30 Hz. Input skips the wait (input-first).
const FOCUSED_INTERVAL: Duration = Duration::from_millis(8);
const UNFOCUSED_INTERVAL: Duration = Duration::from_millis(33);
const WRITE_CHUNK: usize = 4096;

pub fn default_shell() -> Vec<String> {
    #[cfg(windows)]
    {
        let pwsh = which("pwsh.exe");
        vec![pwsh.unwrap_or_else(|| "powershell.exe".into()), "-NoLogo".into()]
    }
    #[cfg(not(windows))]
    {
        let sh = std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".into());
        vec![sh, "-l".into()]
    }
}

#[cfg(windows)]
fn which(exe: &str) -> Option<String> {
    let path = std::env::var_os("PATH")?;
    std::env::split_paths(&path).map(|p| p.join(exe)).find(|p| p.is_file()).map(|p| p.to_string_lossy().into_owned())
}

impl Pane {
    pub fn spawn(cfg: PaneConfig, sink: Sink) -> std::io::Result<Pane> {
        let io = |e: anyhow_like::E| std::io::Error::other(e.0);
        let size = PtySize { rows: cfg.rows, cols: cfg.cols, pixel_width: 0, pixel_height: 0 };
        let pair = native_pty_system().openpty(size).map_err(|e| io(e.into()))?;
        let argv = cfg.command.clone().unwrap_or_else(default_shell);
        let mut cmd = CommandBuilder::new(&argv[0]);
        cmd.args(&argv[1..]);
        if let Some(cwd) = &cfg.cwd {
            cmd.cwd(cwd);
        } else if let Some(home) = home_dir() {
            cmd.cwd(home);
        }
        cmd.env("TERM", "xterm-256color");
        cmd.env("COLORTERM", "truecolor");
        cmd.env("TERM_PROGRAM", "rust-suzuri");
        for (k, v) in &cfg.env {
            cmd.env(k, v);
        }
        let mut child = pair.slave.spawn_command(cmd).map_err(|e| io(e.into()))?;
        drop(pair.slave);
        let killer = child.clone_killer();
        let reader = pair.master.try_clone_reader().map_err(|e| io(e.into()))?;
        let writer = pair.master.take_writer().map_err(|e| io(e.into()))?;

        let shared = Arc::new(Shared {
            engine: Mutex::new(Box::new(Alacritty::new(cfg.cols, cfg.rows, cfg.scrollback))),
            wake: Mutex::new(Wake { dirty: true, force_full: true, input_at: None, stop: false, sent: 0, sent_at: Instant::now(), acked: 0 }),
            cv: Condvar::new(),
            resize_slot: Mutex::new(None),
            focused: AtomicBool::new(false),
            state: Mutex::new(State::Starting),
            stats: Stats::default(),
        });
        let (tx, rx) = unbounded::<Cmd>();
        let mut threads = Vec::new();
        // Running before the reader starts: a terminal query that arrives at
        // once (ConPTY's cursor-position request on start) must get its reply.
        *shared.state.lock() = State::Running;

        // Reader: feed the engine, answer queries through the actor.
        {
            let shared = shared.clone();
            let tx = tx.clone();
            let sink = sink.clone();
            threads.push(thread("rsz-reader", move || reader_loop(reader, shared, tx, sink)));
        }
        // Waiter: the shell's exit becomes an actor input.
        {
            let tx = tx.clone();
            threads.push(thread("rsz-wait", move || {
                let code = child.wait().ok().map(|s| s.exit_code());
                let _ = tx.send(Cmd::ChildExit(code));
            }));
        }
        // Frame thread.
        {
            let shared = shared.clone();
            let sink = sink.clone();
            let tx = tx.clone();
            threads.push(thread("rsz-frame", move || frame_loop(shared, sink, tx)));
        }
        // Actor.
        {
            let shared = shared.clone();
            threads.push(thread("rsz-actor", move || actor_loop(rx, pair.master, writer, killer, shared, sink, (cfg.cols, cfg.rows))));
        }
        Ok(Pane { tx, shared, threads: Mutex::new(threads) })
    }

    /// Input from the user: snaps the view back to the bottom.
    pub fn write(&self, bytes: Vec<u8>) {
        self.shared.engine.lock().scroll(i32::MIN);
        self.shared.kick(|w| w.input_at = Some(Instant::now()));
        let _ = self.tx.send(Cmd::Write(bytes));
    }

    /// Latest-wins: only the newest size reaches the PTY.
    pub fn resize(&self, cols: u16, rows: u16) {
        *self.shared.resize_slot.lock() = Some((cols.max(2), rows.max(1)));
        let _ = self.tx.send(Cmd::Resize);
    }

    pub fn scroll(&self, delta: i32) {
        self.shared.engine.lock().scroll(delta);
        self.shared.kick(|w| w.dirty = true);
    }

    /// The UI painted frames up to `seq` (flow control).
    pub fn ack(&self, seq: u32) {
        self.shared.kick(|w| w.acked = w.acked.max(seq));
    }

    pub fn set_focused(&self, focused: bool) {
        self.shared.focused.store(focused, Ordering::Relaxed);
    }

    /// Ask for a full frame (e.g. the UI lost its canvas).
    pub fn redraw(&self) {
        self.shared.kick(|w| {
            w.dirty = true;
            w.force_full = true;
        });
    }

    pub fn close(&self) {
        let _ = self.tx.send(Cmd::Close);
    }

    pub fn state(&self) -> State {
        *self.shared.state.lock()
    }

    pub fn stats(&self) -> &Stats {
        &self.shared.stats
    }

    pub fn screen_text(&self) -> String {
        self.shared.engine.lock().screen_text()
    }

    /// Wait for every thread to finish (after Exit was sent).
    pub fn join(&self) {
        for t in self.threads.lock().drain(..) {
            let _ = t.join();
        }
    }
}

fn thread(name: &str, f: impl FnOnce() + Send + 'static) -> JoinHandle<()> {
    std::thread::Builder::new().name(name.into()).spawn(f).expect("spawn thread")
}

fn home_dir() -> Option<String> {
    std::env::var("HOME").ok().or_else(|| std::env::var("USERPROFILE").ok())
}

fn reader_loop(mut reader: Box<dyn Read + Send>, shared: Arc<Shared>, tx: Sender<Cmd>, sink: Sink) {
    let mut buf = vec![0u8; 64 * 1024];
    let mut events = Vec::new();
    loop {
        let n = match reader.read(&mut buf) {
            Ok(0) | Err(_) => break,
            Ok(n) => n,
        };
        if trace() {
            eprintln!("rsz: read {n} {:?}", String::from_utf8_lossy(&buf[..n.min(200)]));
        }
        let t = Instant::now();
        shared.engine.lock().feed(&buf[..n], &mut events);
        shared.stats.feed_ns.fetch_add(t.elapsed().as_nanos() as u64, Ordering::Relaxed);
        shared.stats.bytes_in.fetch_add(n as u64, Ordering::Relaxed);
        dispatch(&mut events, &tx, &sink);
        shared.kick(|w| w.dirty = true);
    }
    let _ = tx.send(Cmd::ReaderEof);
}

fn trace() -> bool {
    static T: std::sync::OnceLock<bool> = std::sync::OnceLock::new();
    *T.get_or_init(|| std::env::var_os("RSZ_TRACE").is_some())
}

fn dispatch(events: &mut Vec<EngineEvent>, tx: &Sender<Cmd>, sink: &Sink) {
    for ev in events.drain(..) {
        match ev {
            EngineEvent::PtyWrite(b) => {
                let _ = tx.send(Cmd::Write(b));
            }
            EngineEvent::Title(t) => sink(Out::Title(t)),
            EngineEvent::Bell => sink(Out::Bell),
            EngineEvent::Clipboard(s) => sink(Out::Clipboard(s)),
        }
    }
}

/// How long a frame may stay unacknowledged before the next one goes anyway
/// (a UI that never acks, or one not listening yet).
const ACK_TIMEOUT: Duration = Duration::from_millis(250);

fn frame_loop(shared: Arc<Shared>, sink: Sink, tx: Sender<Cmd>) {
    let mut last = Instant::now() - Duration::from_secs(1);
    let mut seq = 0u32;
    let mut events = Vec::new();
    loop {
        let (force_full, input) = {
            let mut w = shared.wake.lock();
            loop {
                if w.stop {
                    return;
                }
                let ack_deadline = w.sent_at + ACK_TIMEOUT;
                let waiting_ack = w.acked < w.sent && Instant::now() < ack_deadline;
                if w.dirty && !waiting_ack {
                    break;
                }
                if waiting_ack {
                    shared.cv.wait_until(&mut w, ack_deadline);
                } else {
                    shared.cv.wait(&mut w);
                }
            }
            let input = w.input_at.is_some();
            (w.force_full, input)
        };
        // Pace: let output accumulate unless the user is waiting on an echo.
        if !input {
            let interval = if shared.focused.load(Ordering::Relaxed) { FOCUSED_INTERVAL } else { UNFOCUSED_INTERVAL };
            let since = last.elapsed();
            if since < interval {
                std::thread::sleep(interval - since);
            }
        }
        // DEC 2026: hold the frame while a synchronized update is open, up
        // to its timeout.
        let mut out = Vec::with_capacity(16 * 1024);
        let t = Instant::now();
        let sent = {
            let mut eng = shared.engine.lock();
            if let Some(deadline) = eng.sync_deadline() {
                if Instant::now() < deadline {
                    drop(eng);
                    let mut w = shared.wake.lock();
                    w.dirty = false;
                    let wait = deadline.saturating_duration_since(Instant::now());
                    shared.cv.wait_for(&mut w, wait);
                    w.dirty = true;
                    continue;
                }
                eng.stop_sync(&mut events);
            }
            {
                let mut w = shared.wake.lock();
                w.dirty = false;
                w.force_full = false;
                w.input_at = None;
            }
            seq = seq.wrapping_add(1);
            eng.snapshot(&mut out, seq, force_full)
        };
        // Replies produced when a timed-out sync block is applied (DA, CPR)
        // go to the actor like any other.
        dispatch(&mut events, &tx, &sink);
        if sent {
            shared.stats.snapshot_ns.fetch_add(t.elapsed().as_nanos() as u64, Ordering::Relaxed);
            shared.stats.frames.fetch_add(1, Ordering::Relaxed);
            shared.stats.frame_bytes.fetch_add(out.len() as u64, Ordering::Relaxed);
            {
                let mut w = shared.wake.lock();
                w.sent = seq;
                w.sent_at = Instant::now();
            }
            sink(Out::Frame(out));
        }
        last = Instant::now();
    }
}

fn actor_loop(
    rx: Receiver<Cmd>,
    master: Box<dyn MasterPty + Send>,
    writer: Box<dyn Write + Send>,
    mut killer: Box<dyn ChildKiller + Send + Sync>,
    shared: Arc<Shared>,
    sink: Sink,
    initial: (u16, u16),
) {
    let mut master = Some(master);
    let mut writer = Some(writer);
    let mut applied = initial;
    let mut exit_code: Option<u32> = None;
    let mut reader_done = false;
    let mut child_done = false;

    let set_state = |s: State| *shared.state.lock() = s;

    while let Ok(cmd) = rx.recv() {
        let state = *shared.state.lock();
        match cmd {
            Cmd::Write(bytes) => {
                if state != State::Running {
                    continue;
                }
                let Some(w) = writer.as_mut() else { continue };
                shared.stats.writes.fetch_add(1, Ordering::Relaxed);
                if trace() {
                    eprintln!("rsz: write {:?}", String::from_utf8_lossy(&bytes[..bytes.len().min(80)]));
                }
                // Chunked so a close waits at most one chunk behind a paste.
                let mut failed = false;
                for chunk in bytes.chunks(WRITE_CHUNK) {
                    if w.write_all(chunk).is_err() {
                        failed = true;
                        break;
                    }
                }
                let _ = w.flush();
                if failed {
                    // The shell is gone; the reader's EOF closes the pane.
                    writer = None;
                }
            }
            Cmd::Resize => {
                let Some(size) = shared.resize_slot.lock().take() else { continue };
                if state != State::Running || size == applied {
                    continue;
                }
                if let Some(m) = master.as_ref() {
                    // Engine first, so output produced for the new size lands
                    // on a grid of that size.
                    shared.engine.lock().resize(size.0, size.1);
                    if m.resize(PtySize { rows: size.1, cols: size.0, pixel_width: 0, pixel_height: 0 }).is_ok() {
                        applied = size;
                        shared.stats.resizes_applied.fetch_add(1, Ordering::Relaxed);
                    }
                    shared.kick(|w| w.dirty = true);
                }
            }
            Cmd::Close => {
                if state == State::Running || state == State::Starting {
                    set_state(State::Closing);
                    writer = None;
                    let _ = killer.kill();
                    // Dropping the master closes the pseudo console; the
                    // reader then sees EOF.
                    master = None;
                }
            }
            Cmd::ReaderEof => {
                reader_done = true;
                if state == State::Running {
                    set_state(State::Closing);
                    writer = None;
                    master = None;
                }
            }
            Cmd::ChildExit(code) => {
                child_done = true;
                exit_code = code;
                if state == State::Running {
                    set_state(State::Closing);
                    writer = None;
                    // Drop the master so a reader still blocked on a pipe the
                    // shell's grandchildren keep open still sees EOF.
                    master = None;
                }
            }
        }
        if *shared.state.lock() == State::Closing && reader_done && child_done {
            break;
        }
        // A close with a child that ignores the kill: give up after a while.
        if *shared.state.lock() == State::Closing && reader_done && !child_done {
            match rx.recv_timeout(Duration::from_secs(3)) {
                Ok(Cmd::ChildExit(code)) => {
                    exit_code = code;
                }
                _ => {}
            }
            break;
        }
    }
    drop(master);
    drop(writer);
    set_state(State::Closed);
    shared.kick(|w| w.stop = true);
    // One last frame is not needed: the UI shows the exit.
    sink(Out::Exit(exit_code));
}

/// portable-pty returns anyhow errors; keep anyhow out of our API.
mod anyhow_like {
    pub struct E(pub String);
    impl<T: std::fmt::Display> From<T> for E {
        fn from(e: T) -> Self {
            E(e.to_string())
        }
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::sync::mpsc;

    fn run(cmd: &[&str]) -> (Pane, mpsc::Receiver<String>) {
        let (tx, rx) = mpsc::channel();
        let tx = Mutex::new(tx);
        let sink: Sink = Arc::new(move |o| {
            let msg = match o {
                Out::Frame(_) => "frame".to_string(),
                Out::Exit(c) => format!("exit {c:?}"),
                Out::Title(t) => format!("title {t}"),
                _ => return,
            };
            let _ = tx.lock().send(msg);
        });
        let cfg = PaneConfig { cols: 40, rows: 10, command: Some(cmd.iter().map(|s| s.to_string()).collect()), ..Default::default() };
        (Pane::spawn(cfg, sink).unwrap(), rx)
    }

    fn wait_exit(rx: &mpsc::Receiver<String>) -> Vec<String> {
        let mut seen = Vec::new();
        let deadline = Instant::now() + Duration::from_secs(10);
        while Instant::now() < deadline {
            if let Ok(m) = rx.recv_timeout(Duration::from_millis(100)) {
                let done = m.starts_with("exit");
                seen.push(m);
                if done {
                    break;
                }
            }
        }
        seen
    }

    #[test]
    fn shell_exit_gives_one_exit() {
        let (pane, rx) = run(&["/bin/sh", "-c", "printf 'hi\\033]0;t1\\007'; exit 3"]);
        let seen = wait_exit(&rx);
        assert_eq!(seen.iter().filter(|m| m.starts_with("exit")).count(), 1, "{seen:?}");
        assert!(seen.contains(&"exit Some(3)".to_string()), "{seen:?}");
        assert!(seen.contains(&"title t1".to_string()), "{seen:?}");
        pane.join();
        assert_eq!(pane.state(), State::Closed);
        assert!(pane.screen_text().starts_with("hi"));
        // Exactly one Exit even after close is asked again.
        pane.close();
        std::thread::sleep(Duration::from_millis(200));
        assert!(rx.try_iter().all(|m| !m.starts_with("exit")));
    }

    #[test]
    fn echo_and_close() {
        let (pane, rx) = run(&["/bin/cat"]);
        pane.write(b"ping\r".to_vec());
        let deadline = Instant::now() + Duration::from_secs(5);
        while !pane.screen_text().contains("ping") && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(10));
        }
        assert!(pane.screen_text().contains("ping"));
        pane.close();
        let seen = wait_exit(&rx);
        assert_eq!(seen.iter().filter(|m| m.starts_with("exit")).count(), 1, "{seen:?}");
        pane.join();
    }

    /// A query inside a DEC 2026 block that never closes: the block times
    /// out, the frame thread applies it, and the reply must still reach the
    /// program (it used to block the frame thread forever).
    #[test]
    fn reply_from_timed_out_sync_block() {
        let script = "stty raw -echo; printf '\\033[?2026hq\\033[6n'; IFS= read -r -d R x; printf '\\033[?2026l\\r\\ngot reply\\r\\n'; sleep 5";
        let (pane, rx) = run(&["/bin/bash", "-c", script]);
        let deadline = Instant::now() + Duration::from_secs(4);
        while !pane.screen_text().contains("got reply") && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(20));
        }
        assert!(pane.screen_text().contains("got reply"), "{}", pane.screen_text());
        // And the frame thread is still alive: frames keep coming.
        while rx.try_recv().is_ok() {}
        pane.write(b"x".to_vec());
        assert!(rx.recv_timeout(Duration::from_secs(2)).is_ok());
        pane.close();
        wait_exit(&rx);
        pane.join();
    }

    #[test]
    fn resize_is_latest_wins() {
        let (pane, rx) = run(&["/bin/cat"]);
        for i in 0..50u16 {
            pane.resize(40 + i, 10);
        }
        pane.resize(100, 30);
        let deadline = Instant::now() + Duration::from_secs(5);
        while pane.shared.engine.lock().size() != (100, 30) && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(10));
        }
        assert_eq!(pane.shared.engine.lock().size(), (100, 30));
        assert!(pane.stats().resizes_applied.load(Ordering::Relaxed) < 51);
        pane.close();
        wait_exit(&rx);
        pane.join();
    }
}
