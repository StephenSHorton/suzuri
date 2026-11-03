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
use std::sync::atomic::{AtomicBool, AtomicU32, AtomicU64, Ordering};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use crossbeam_channel::{Receiver, Sender, unbounded};
use parking_lot::{Condvar, Mutex};
use portable_pty::{ChildKiller, CommandBuilder, MasterPty, PtySize, native_pty_system};

use crate::engine::{Alacritty, Engine, EngineEvent, Link, SelectKind};
use crate::fg::Probe;
use crate::input::MouseEvent;
use crate::shell::{self, OscFilter, ShellEvent};
use crate::block;
use crate::palette;
use crate::echo::EchoFilter;
use crate::images::{self, Changes, ImageData, Images, MARK_REACH, Placement};
use crate::protocols::{Kitty, KittyPlace, Notice, ProtoEvent, ProtoSink, Protocols};

/// What a pane sends to its owner.
pub enum Out {
    Frame(Vec<u8>),
    Title(String),
    Bell,
    Clipboard(String),
    /// The shell's state for the input bar changed (#285).
    Shell(ShellStatus),
    /// A command run from the shell finished (shell integration only).
    CommandDone { exit: Option<i32>, command: String },
    /// The pane is closed; sent once. The exit code if the shell reported one.
    Exit(Option<u32>),
    // Card #288: notices and images.
    Notice(Notice),
    /// OSC 99 p=close.
    NoticeClose(String),
    /// OSC 99 p=alive: answer with Pane::reply(protocols::alive_reply(..)).
    NoticeAlive(String),
    /// ConEmu OSC 9;4.
    Progress { state: u8, percent: u8 },
    /// An image's bytes, sent once before any placement shows it.
    ImageData(ImageData),
    /// Images the UI can forget.
    ImageDrop(Vec<u32>),
    /// Every image on screen now (sent when it changes, after the frame).
    Placements(Vec<Placement>),
}

/// What the input bar needs to know about the shell.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ShellStatus {
    /// The shell runs with suzuri's bootstrap and has reported a prompt. Until
    /// then (or never, for a custom command or an unknown shell) the bar stays
    /// off and keys go to the PTY.
    pub integrated: bool,
    /// The working directory at the last prompt.
    pub cwd: String,
    /// A program other than the shell owns the keyboard: between a command's
    /// start mark and the next prompt, or while one holds the terminal.
    pub program: bool,
    /// Prompts seen so far. It counts up at every prompt, so the UI can tell
    /// "the prompt is back" even when nothing else changed.
    pub prompts: u64,
}

/// The shell state as the reader and the probe see it; `status()` is what
/// the UI is told.
#[derive(Default)]
struct ShellState {
    integrated: bool,
    cwd: String,
    /// Latched by the start mark, cleared by the next prompt.
    started: bool,
    /// The foreground probe's last answer.
    child: bool,
    prompts: u64,
    sent: Option<ShellStatus>,
    /// A bar line was sent at this prompt count: the command owns the pane
    /// until a later prompt.
    awaiting: Option<u64>,
}

impl ShellState {
    fn status(&self) -> ShellStatus {
        ShellStatus { integrated: self.integrated, cwd: self.cwd.clone(), program: self.started || self.child, prompts: self.prompts }
    }

    /// The status to send, if it changed since the last one sent.
    fn changed(&mut self) -> Option<ShellStatus> {
        let st = self.status();
        if self.sent.as_ref() == Some(&st) {
            return None;
        }
        self.sent = Some(st.clone());
        Some(st)
    }
}

/// How often the foreground probe looks (Go suzuri: 200 ms).
const FG_POLL: Duration = Duration::from_millis(200);

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
    /// Start the user's shell without suzuri's bootstrap (no input bar).
    pub plain_shell: bool,
    /// Cursor shape until a program sets one: 1 block, 2 underline, 3 beam.
    pub cursor: u8,
}

impl Default for PaneConfig {
    fn default() -> Self {
        Self { cols: 80, rows: 24, command: None, cwd: None, env: Vec::new(), scrollback: 10_000, plain_shell: false, cursor: 1 }
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
    shell: Mutex<ShellState>,
    images: Mutex<Images>,
    /// The UI's cell size in pixels, w << 16 | h (Go's default 9 x 18).
    cell_px: AtomicU32,
    cwd: Option<std::path::PathBuf>,
    /// Hides the shell's echo of a bar line (#296).
    echo: Mutex<EchoFilter>,
    /// When the PTY last produced output.
    last_io: Mutex<Instant>,
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
    /// The shell's process id, for its cwd.
    pid: Option<u32>,
    /// The cwd the shell last reported (OSC 7 / 7878), if any.
    reported_cwd: Mutex<Option<String>>,
    /// The shell's program (before suzuri's bootstrap), for the cd alias.
    shell_prog: String,
    /// Bar lines waiting for the shell to be idle: (display, payload).
    queue: Mutex<std::collections::VecDeque<(String, String)>>,
}

/// What a bar submit did.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Submit {
    /// A blank line: Enter went to the shell.
    Blank,
    Sent,
    /// Held until the shell is idle; the queue's length.
    Queued(usize),
}

/// How long the shell must be quiet before a queued line runs, and the
/// most lines a pane holds (Go suzuri's cmdQueueIdle, cmdQueueMax).
pub const QUEUE_IDLE: Duration = Duration::from_millis(900);
const QUEUE_MAX: usize = 32;

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
        let mut argv = cfg.command.clone().unwrap_or_else(default_shell);
        let shell_prog = argv.first().cloned().unwrap_or_default();
        // The input bar's shell hooks, for the user's own shell only.
        let mut boot_env = Vec::new();
        let mut temp_dir = None;
        if cfg.command.is_none() && !cfg.plain_shell {
            if let Some(b) = shell::bootstrap(&argv) {
                argv = b.argv;
                boot_env = b.env;
                temp_dir = b.temp_dir;
            }
        }
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
        for (k, v) in boot_env.iter().chain(&cfg.env) {
            cmd.env(k, v);
        }
        let mut child = pair.slave.spawn_command(cmd).map_err(|e| io(e.into()))?;
        let probe = Probe {
            shell_pid: child.process_id().unwrap_or(0),
            #[cfg(unix)]
            master_fd: pair.master.as_raw_fd().unwrap_or(-1),
        };
        drop(pair.slave);
        let pid = child.process_id();
        let killer = child.clone_killer();
        let reader = pair.master.try_clone_reader().map_err(|e| io(e.into()))?;
        let writer = pair.master.take_writer().map_err(|e| io(e.into()))?;

        let shared = Arc::new(Shared {
            engine: Mutex::new({
                let mut e: Box<dyn Engine> = Box::new(Alacritty::new(cfg.cols, cfg.rows, cfg.scrollback));
                if cfg.cursor != 1 {
                    e.set_default_cursor(cfg.cursor);
                }
                e
            }),
            wake: Mutex::new(Wake { dirty: true, force_full: true, input_at: None, stop: false, sent: 0, sent_at: Instant::now(), acked: 0 }),
            cv: Condvar::new(),
            resize_slot: Mutex::new(None),
            focused: AtomicBool::new(false),
            state: Mutex::new(State::Starting),
            stats: Stats::default(),
            shell: Mutex::new(ShellState::default()),
            images: Mutex::new(Images::default()),
            cell_px: AtomicU32::new(9 << 16 | 18),
            cwd: cfg.cwd.clone().map(Into::into).or_else(|| home_dir().map(Into::into)),
            echo: Mutex::new(EchoFilter::default()),
            last_io: Mutex::new(Instant::now()),
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
                if let Some(dir) = temp_dir {
                    let _ = std::fs::remove_dir_all(dir);
                }
                let _ = tx.send(Cmd::ChildExit(code));
            }));
        }
        // Foreground probe: is a program other than the shell running?
        {
            let shared = shared.clone();
            let sink = sink.clone();
            threads.push(thread("rsz-fg", move || fg_loop(probe, shared, sink)));
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
        Ok(Pane { tx, shared, threads: Mutex::new(threads), pid, reported_cwd: Mutex::new(None), shell_prog, queue: Mutex::default() })
    }

    /// Enter in the input bar (#296, Go suzuri's submitBarLine): the bare-cd
    /// alias, then either a command block plus the line, or a place in the
    /// queue while the shell is busy.
    pub fn submit(&self, line: &str) -> Submit {
        let (display, payload) = block::expand(line, &self.shell_prog);
        if display.trim().is_empty() && payload.trim().is_empty() {
            self.write(b"\r".to_vec());
            return Submit::Blank;
        }
        let mut q = self.queue.lock();
        if !q.is_empty() || self.busy() {
            q.push_back((display, payload));
            while q.len() > QUEUE_MAX {
                q.pop_front();
            }
            return Submit::Queued(q.len());
        }
        drop(q);
        self.send_block(&display, &payload);
        Submit::Sent
    }

    /// Run the next queued line if the shell is idle. Returns how many are
    /// left after it, or None when nothing ran.
    pub fn flush_queue(&self) -> Option<usize> {
        let mut q = self.queue.lock();
        if q.is_empty() || self.busy() {
            return None;
        }
        let (display, payload) = q.pop_front()?;
        let left = q.len();
        drop(q);
        self.send_block(&display, &payload);
        Some(left)
    }

    /// Drop the queued lines (Ctrl+C while a command runs). Returns how many.
    pub fn clear_queue(&self) -> usize {
        std::mem::take(&mut *self.queue.lock()).len()
    }

    pub fn queue_len(&self) -> usize {
        self.queue.lock().len()
    }

    /// A command owns the pane: one sent from the bar and not yet back at a
    /// prompt, one the shell marked as started, a foreground child, or
    /// output that hasn't been quiet for QUEUE_IDLE.
    fn busy(&self) -> bool {
        let sh = self.shared.shell.lock();
        if sh.awaiting.is_some_and(|n| sh.prompts <= n) || sh.started || sh.child {
            return true;
        }
        drop(sh);
        self.shared.last_io.lock().elapsed() < QUEUE_IDLE
    }

    fn send_block(&self, display: &str, payload: &str) {
        let cwd = {
            let mut sh = self.shared.shell.lock();
            sh.awaiting = Some(sh.prompts);
            sh.cwd.clone()
        };
        {
            let mut eng = self.shared.engine.lock();
            eng.scroll(i32::MIN);
            let (cols, _) = eng.size();
            let (_, cy) = eng.cursor();
            let text = eng.screen_text();
            let row = |y: u16| text.lines().nth(y as usize).unwrap_or("").trim().is_empty();
            // A line of output the cursor sits after (no newline) stays; a
            // blank line above is the spacer already.
            let keep = !row(cy);
            let spacer = !(cy == 0 || if keep { false } else { row(cy - 1) });
            let home = home_dir().unwrap_or_default();
            let mut lines = block::block_lines(display, &cwd, &home, cols, spacer, palette::active().theme.chrome.primary);
            // The command's last line goes on the cursor's row: the shell's
            // echo would have been there, and its newline still follows.
            let on_line = lines.pop().unwrap_or_default();
            eng.insert_block(&lines, &on_line, keep);
        }
        self.shared.echo.lock().arm(payload);
        let mut line = payload.replace("\r\n", "\n").replace('\n', "\r").into_bytes();
        line.push(b'\r');
        self.write(line);
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

    /// Bytes for the program that are not typing (answers to its queries):
    /// the view stays where it is.
    pub fn reply(&self, bytes: Vec<u8>) {
        let _ = self.tx.send(Cmd::Write(bytes));
    }

    /// The UI's cell size in pixels, for sizing inline images.
    pub fn set_cell_px(&self, w: u16, h: u16) {
        self.shared.cell_px.store((w.max(1) as u32) << 16 | h.max(1) as u32, Ordering::Relaxed);
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

    /// The Settings cursor shape (1 block, 2 underline, 3 beam) for programs
    /// that don't set their own.
    pub fn set_default_cursor(&self, shape: u8) {
        self.shared.engine.lock().set_default_cursor(shape);
        self.redraw();
    }

    pub fn close(&self) {
        let _ = self.tx.send(Cmd::Close);
    }

    /// Records the cwd the shell reported (OSC 7 or Go suzuri's OSC 7878).
    pub fn set_reported_cwd(&self, cwd: String) {
        *self.reported_cwd.lock() = Some(cwd);
    }

    /// Where a new pane split from this one should start: the shell's last
    /// report, else the shell process's cwd from the OS (macOS, Linux).
    pub fn cwd(&self) -> Option<String> {
        // The input bar's hooks report the cwd at every prompt (OSC 7878).
        let hooked = self.shared.shell.lock().cwd.clone();
        if !hooked.is_empty() {
            return Some(hooked);
        }
        if let Some(c) = self.reported_cwd.lock().clone() {
            return Some(c);
        }
        self.pid.and_then(crate::cwd::process_cwd)
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

    /// The last `lines` scrollback lines above the screen, oldest first.
    pub fn history_tail(&self, lines: usize) -> Vec<String> {
        self.shared.engine.lock().history_tail(lines)
    }

    /// Start a selection at a visible cell (see Engine::select_start).
    pub fn select_start(&self, col: u16, row: u16, right: bool, kind: SelectKind) {
        self.shared.engine.lock().select_start(col, row, right, kind);
        self.shared.kick(|w| w.dirty = true);
    }

    pub fn select_extend(&self, col: u16, row: u16, right: bool) {
        self.shared.engine.lock().select_extend(col, row, right);
        self.shared.kick(|w| w.dirty = true);
    }

    pub fn select_clear(&self) {
        self.shared.engine.lock().select_clear();
        self.shared.kick(|w| w.dirty = true);
    }

    pub fn selection_text(&self) -> Option<String> {
        self.shared.engine.lock().selection_text()
    }

    pub fn link_at(&self, col: u16, row: u16) -> Option<Link> {
        self.shared.engine.lock().link_at(col, row)
    }

    /// Report a mouse event to the app if its modes ask for it. Returns
    /// whether it was reported (the UI handles it otherwise).
    pub fn mouse(&self, ev: MouseEvent) -> bool {
        let bytes = self.shared.engine.lock().mouse_report(ev);
        match bytes {
            Some(b) => {
                let _ = self.tx.send(Cmd::Write(b));
                true
            }
            None => false,
        }
    }

    pub fn alt_screen(&self) -> bool {
        self.shared.engine.lock().alt_screen()
    }

    /// Paste text: framed for bracketed paste when the app asked for it.
    pub fn paste(&self, text: &str) {
        let bytes = self.shared.engine.lock().paste_bytes(text);
        self.write(bytes);
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
    let mut osc = OscFilter::default();
    let mut clean = Vec::with_capacity(buf.len());
    let mut marks = Vec::new();
    let mut protocols = Protocols::new();
    loop {
        let n = match reader.read(&mut buf) {
            Ok(0) | Err(_) => break,
            Ok(n) => n,
        };
        if trace() {
            eprintln!("rsz: read {n} {:?}", String::from_utf8_lossy(&buf[..n.min(200)]));
            // #288: do image and notice sequences reach the reader (ConPTY
            // may drop or rewrite them)?
            for (name, pat) in [("osc1337", &b"\x1b]1337;"[..]), ("osc7879", b"\x1b]7879;"), ("osc9", b"\x1b]9;"), ("osc777", b"\x1b]777;"), ("osc99", b"\x1b]99;"), ("apcG", b"\x1b_G")] {
                let hits = buf[..n].windows(pat.len()).filter(|w| *w == pat).count();
                if hits > 0 {
                    eprintln!("rsz: read has {hits} x {name}");
                }
            }
        }
        let t = Instant::now();
        clean.clear();
        osc.feed(&buf[..n], &mut clean, &mut marks);
        if !marks.is_empty() {
            shell_marks(&mut marks, &shared, &sink);
        }
        *shared.last_io.lock() = Instant::now();
        // The shell's echo of a bar line goes (#296): the block shows it.
        {
            let mut echo = shared.echo.lock();
            if echo.active() {
                let raw = std::mem::take(&mut clean);
                echo.feed(&raw, &mut clean);
            }
        }
        // Then #288's protocols (notices, images) on what is left, ahead of
        // the engine.
        let outs = {
            let mut eng = shared.engine.lock();
            let mut f = Feeder { eng: &mut **eng, events: &mut events, shared: &shared, outs: Vec::new() };
            protocols.feed(&clean, &mut f);
            f.outs
        };
        for o in outs {
            sink(o);
        }
        shared.stats.feed_ns.fetch_add(t.elapsed().as_nanos() as u64, Ordering::Relaxed);
        shared.stats.bytes_in.fetch_add(n as u64, Ordering::Relaxed);
        dispatch(&mut events, &tx, &sink);
        shared.kick(|w| w.dirty = true);
    }
    let _ = tx.send(Cmd::ReaderEof);
}

/// Apply the shell's marks to the bar state and tell the UI what changed.
fn shell_marks(marks: &mut Vec<ShellEvent>, shared: &Shared, sink: &Sink) {
    let mut done = Vec::new();
    let status = {
        let mut sh = shared.shell.lock();
        for m in marks.drain(..) {
            match m {
                ShellEvent::Start => sh.started = true,
                ShellEvent::Cwd(cwd) => {
                    // The echo filter stays armed: ConPTY passes these marks on
                    // before it paints the echo itself. Its byte budget, or the
                    // next submit, ends it.
                    sh.integrated = true;
                    sh.started = false;
                    sh.prompts += 1;
                    sh.cwd = cwd;
                }
                ShellEvent::Done { exit, command } => {
                    sh.started = false;
                    done.push((exit, command));
                }
            }
        }
        sh.changed()
    };
    for (exit, command) in done {
        sink(Out::CommandDone { exit, command });
    }
    if let Some(st) = status {
        sink(Out::Shell(st));
    }
}

/// Poll the foreground program while the pane runs. Only for an integrated
/// shell: without prompt marks the bar is off anyway.
fn fg_loop(probe: Probe, shared: Arc<Shared>, sink: Sink) {
    loop {
        std::thread::sleep(FG_POLL);
        if *shared.state.lock() != State::Running {
            return;
        }
        if !shared.shell.lock().integrated {
            continue;
        }
        let child = probe.program_running();
        let status = {
            let mut sh = shared.shell.lock();
            sh.child = child;
            sh.changed()
        };
        if let Some(st) = status {
            sink(Out::Shell(st));
        }
    }
}

fn trace() -> bool {
    static T: std::sync::OnceLock<bool> = std::sync::OnceLock::new();
    *T.get_or_init(|| std::env::var_os("RSZ_TRACE").is_some())
}

/// Takes the protocol filter's output on the reader thread, engine locked:
/// text goes to the engine, and images are placed against its cursor at
/// their point in the stream.
struct Feeder<'a> {
    eng: &'a mut dyn Engine,
    events: &'a mut Vec<EngineEvent>,
    shared: &'a Shared,
    outs: Vec<Out>,
}

impl ProtoSink for Feeder<'_> {
    fn text(&mut self, bytes: &[u8]) {
        self.eng.feed(bytes, self.events);
    }

    fn event(&mut self, ev: ProtoEvent) {
        match ev {
            ProtoEvent::Notice(n) => self.outs.push(Out::Notice(n)),
            ProtoEvent::CloseNotice(id) => self.outs.push(Out::NoticeClose(id)),
            ProtoEvent::AliveQuery(id) => self.outs.push(Out::NoticeAlive(id)),
            ProtoEvent::Reply(b) => self.events.push(EngineEvent::PtyWrite(b)),
            ProtoEvent::Progress { state, percent } => self.outs.push(Out::Progress { state, percent }),
            ProtoEvent::InlineImage { name, data } => self.inline(name, None, data),
            ProtoEvent::ImagePath(r) => {
                if self.eng.alt_screen() {
                    return;
                }
                let home = home_dir().map(std::path::PathBuf::from);
                // The cwd the shell hooks report (#285), else where it started.
                let hooked = self.shared.shell.lock().cwd.clone();
                let cwd = if hooked.is_empty() { self.shared.cwd.clone() } else { Some(hooked.into()) };
                let Some(path) = images::resolve_path(cwd.as_deref(), home.as_deref(), &r) else { return };
                let Some(data) = images::read_image(&path) else { return };
                let p = path.to_string_lossy().into_owned();
                self.inline(p.clone(), Some(p), data);
            }
            ProtoEvent::Kitty(k) => self.kitty(k),
        }
    }
}

impl Feeder<'_> {
    /// Images go where the text has got to: apply a synchronized update
    /// that is still being held first.
    fn settle(&mut self) {
        if self.eng.sync_deadline().is_some() {
            self.eng.stop_sync(self.events);
        }
    }

    fn cell(&self) -> (u32, u32) {
        let v = self.shared.cell_px.load(Ordering::Relaxed);
        (v >> 16, v & 0xffff)
    }

    // Go tab.ingestImages: main screen only, under the output so far, as a
    // block of rows and a caption row.
    fn inline(&mut self, name: String, path: Option<String>, data: Vec<u8>) {
        if self.eng.alt_screen() {
            return;
        }
        let Some(px) = images::pixel_size(&data) else { return };
        self.settle();
        if self.eng.cursor().0 > 0 {
            self.eng.feed(b"\r\n", self.events);
        }
        let (cols, rows) = images::inline_cells(px, self.cell(), self.eng.size().0 as u32);
        // Go's caption row comes first: "img <name>".
        let label: String = name.chars().filter(|c| !c.is_control()).take(200).collect();
        self.eng.feed(format!("img {label}\r\n").as_bytes(), self.events);
        let mut ch = Changes::default();
        let id = {
            let mut im = self.shared.images.lock();
            let tag = im.tag();
            self.eng.mark_cursor(tag);
            im.add_inline(path.as_deref().unwrap_or(&name), tag, cols, rows, &mut ch)
        };
        self.eng.feed(&b"\r\n".repeat(rows as usize), self.events);
        self.send(ch);
        self.outs.push(Out::ImageData(ImageData {
            id,
            name,
            path,
            format: 100,
            width: px.0,
            height: px.1,
            compressed: false,
            data,
        }));
        self.shared.kick(|w| w.dirty = true);
    }

    fn kitty(&mut self, k: Kitty) {
        let mut ch = Changes::default();
        match k {
            Kitty::Image { id: kid, format, width, height, compressed, data, place } => {
                let id = self.shared.images.lock().kitty_image(kid, width, height, &mut ch);
                self.send(ch);
                self.outs.push(Out::ImageData(ImageData {
                    id,
                    name: format!("kitty {kid}"),
                    path: None,
                    format,
                    width,
                    height,
                    compressed,
                    data,
                }));
                if let Some(p) = place {
                    self.place(kid, p);
                }
            }
            Kitty::Place { id, place } => self.place(id, place),
            Kitty::Delete { id } => {
                self.shared.images.lock().kitty_delete(id, &mut ch);
                self.send(ch);
            }
        }
        self.shared.kick(|w| w.dirty = true);
    }

    // Go places Kitty images at the cursor and leaves the cursor alone.
    fn place(&mut self, kid: u32, place: KittyPlace) {
        self.settle();
        let alt = self.eng.alt_screen();
        let cell = self.eng.cursor();
        let mut im = self.shared.images.lock();
        let mark = (!alt).then(|| {
            let tag = im.tag();
            self.eng.mark_cursor(tag);
            tag
        });
        im.kitty_place(kid, place, mark, cell);
    }

    fn send(&mut self, ch: Changes) {
        if !ch.dropped.is_empty() {
            self.outs.push(Out::ImageDrop(ch.dropped));
        }
    }
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

/// For this long after a key, the pane's frames skip both the pacing and the
/// one-in-flight ack limit, so an echo never waits on the previous frame's
/// paint round trip (it cost a frame, +7 ms p50, on Windows).
const INPUT_WINDOW: Duration = Duration::from_millis(100);

fn frame_loop(shared: Arc<Shared>, sink: Sink, tx: Sender<Cmd>) {
    let mut last = Instant::now() - Duration::from_secs(1);
    let mut seq = 0u32;
    let mut events = Vec::new();
    loop {
        let placements;
        let (force_full, input) = {
            let mut w = shared.wake.lock();
            loop {
                if w.stop {
                    return;
                }
                let ack_deadline = w.sent_at + ACK_TIMEOUT;
                let urgent = w.input_at.is_some_and(|t| t.elapsed() < INPUT_WINDOW);
                let waiting_ack = !urgent && w.acked < w.sent && Instant::now() < ack_deadline;
                if w.dirty && !waiting_ack {
                    break;
                }
                if waiting_ack {
                    shared.cv.wait_until(&mut w, ack_deadline);
                } else {
                    shared.cv.wait(&mut w);
                }
            }
            let input = w.input_at.is_some_and(|t| t.elapsed() < INPUT_WINDOW);
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
            }
            seq = seq.wrapping_add(1);
            let sent = eng.snapshot(&mut out, seq, force_full);
            let mut im = shared.images.lock();
            let marks = if im.is_empty() { Vec::new() } else { eng.marks(MARK_REACH) };
            placements = im.placements(&marks, eng.alt_screen());
            sent
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
        if let Some(p) = placements {
            sink(Out::Placements(p));
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
                Out::Notice(n) => format!("notice {}|{}", n.title, n.body),
                Out::ImageData(d) => format!("image {} {} {}x{}", d.id, d.name, d.width, d.height),
                Out::Placements(p) => format!("placed {:?}", p.iter().map(|p| (p.image, p.row, p.col, p.cols, p.rows)).collect::<Vec<_>>()),
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

    /// The input bar's shell hooks, end to end in a real zsh: the prompt
    /// marks make the pane integrated with a cwd, a running command is a
    /// program until the next prompt, and the marks never reach the screen.
    #[test]
    fn zsh_hooks_report_prompt_and_program() {
        if !std::env::var("SHELL").unwrap_or_default().ends_with("zsh") {
            eprintln!("skipped: $SHELL is not zsh");
            return;
        }
        let (tx, rx) = mpsc::channel();
        let tx = Mutex::new(tx);
        let sink: Sink = Arc::new(move |o| {
            if let Out::Shell(st) = o {
                let _ = tx.lock().send(st);
            }
        });
        // An empty ZDOTDIR for the user's files keeps their config out of the test.
        let home = std::env::temp_dir().join(format!("rsz-test-zdot-{}", std::process::id()));
        std::fs::create_dir_all(&home).unwrap();
        let cfg = PaneConfig {
            cols: 60,
            rows: 10,
            cwd: Some("/tmp".into()),
            env: vec![("RSZ_USER_ZDOTDIR".into(), home.to_string_lossy().into_owned())],
            ..Default::default()
        };
        let pane = Pane::spawn(cfg, sink).unwrap();
        let wait = |want: &dyn Fn(&ShellStatus) -> bool, what: &str| {
            let deadline = Instant::now() + Duration::from_secs(10);
            while Instant::now() < deadline {
                if let Ok(st) = rx.recv_timeout(Duration::from_millis(100)) {
                    if want(&st) {
                        return st;
                    }
                }
            }
            panic!("never saw {what}");
        };
        let st = wait(&|s| s.integrated && !s.program, "the first prompt");
        assert!(st.cwd.ends_with("tmp"), "cwd {:?}", st.cwd);
        pane.write(b"sleep 1\r".to_vec());
        wait(&|s| s.program, "the command start");
        wait(&|s| s.integrated && !s.program, "the prompt after sleep");
        let screen = pane.screen_text();
        assert!(!screen.contains("7878") && !screen.contains("7879"), "marks leaked: {screen}");
        pane.close();
        pane.join();
        let _ = std::fs::remove_dir_all(home);
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

    /// A PNG header is enough: the pane only reads the size.
    fn png_b64(w: u32, h: u32) -> String {
        use base64::Engine as _;
        let mut b = b"\x89PNG\r\n\x1a\n\0\0\0\rIHDR".to_vec();
        b.extend_from_slice(&w.to_be_bytes());
        b.extend_from_slice(&h.to_be_bytes());
        b.extend_from_slice(&[8, 6, 0, 0, 0, 0, 0, 0, 0]);
        base64::engine::general_purpose::STANDARD.encode(b)
    }

    #[test]
    fn notice_and_inline_image() {
        // 90 x 36 px at the default 9 x 18 cell: a caption row, then 10
        // columns by 2 rows, under "hi".
        let script = format!(
            "printf 'hi\\033]777;notify;Done;tests pass\\007\\033]1337;File=name=Y2F0LnBuZw==;inline=1:{}\\007after\\n'; sleep 1",
            png_b64(90, 36)
        );
        let (pane, rx) = run(&["/bin/sh", "-c", &script]);
        let seen = wait_exit(&rx);
        assert!(seen.contains(&"notice Done|tests pass".to_string()), "{seen:?}");
        assert!(seen.iter().any(|m| m.starts_with("image 2 cat.png 90x36")), "{seen:?}");
        // "hi", the caption, then the image's two rows from row 2.
        assert!(seen.iter().any(|m| m == "placed [(2, 2, 0, 10, 2)]"), "{seen:?}");
        let text = pane.screen_text();
        let lines: Vec<&str> = text.lines().collect();
        assert_eq!(&lines[..5], &["hi", "img cat.png", "", "", "after"], "{text}");
        pane.join();
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

    #[cfg(target_os = "macos")]
    #[test]
    fn cwd_follows_the_shell() {
        let dir = std::fs::canonicalize(std::env::temp_dir()).unwrap();
        let (pane, rx) = run(&["/bin/sh"]);
        pane.write(format!("cd '{}'; echo here\r", dir.display()).into_bytes());
        let deadline = Instant::now() + Duration::from_secs(5);
        while pane.cwd().map(std::path::PathBuf::from).as_ref() != Some(&dir) && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(20));
        }
        assert_eq!(pane.cwd().map(std::path::PathBuf::from), Some(dir));
        pane.set_reported_cwd("/reported".into());
        assert_eq!(pane.cwd().as_deref(), Some("/reported"));
        pane.close();
        wait_exit(&rx);
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
