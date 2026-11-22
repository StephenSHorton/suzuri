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

use crossbeam_channel::{Receiver, RecvTimeoutError, Sender, unbounded};
use parking_lot::{Condvar, Mutex};
use portable_pty::{ChildKiller, CommandBuilder, MasterPty, PtySize, native_pty_system};

use crate::engine::{Engine, EngineEvent, Link, SelectKind};
use crate::ghostty::Ghostty;
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
    /// Text typed while a command ran that it never read, taken back from
    /// the tty at the prompt: it goes in the bar, before the prompt's status.
    Typeahead(String),
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
    /// then the bar holds keys only while `pending`; never (a custom command,
    /// an unknown shell) the bar stays off and keys go to the PTY.
    pub integrated: bool,
    /// The working directory at the last prompt.
    pub cwd: String,
    /// A program other than the shell owns the keyboard: between a command's
    /// start mark and the next prompt, or while one holds the terminal.
    pub program: bool,
    /// Prompts seen so far. It counts up at every prompt, so the UI can tell
    /// "the prompt is back" even when nothing else changed.
    pub prompts: u64,
    /// suzuri's hooks were started and the first prompt has not come yet:
    /// the bar holds what is typed (until it does, or the wait is given up)
    /// and Enter types the line into the pane as is.
    pub pending: bool,
}

/// The shell state as the reader and the probe see it; `status()` is what
/// the UI is told.
#[derive(Default)]
struct ShellState {
    integrated: bool,
    /// See ShellStatus::pending. Never set again once cleared.
    pending: bool,
    /// Started with suzuri's hooks. Until the first prompt (integrated) a
    /// bar line is typed into the pane as is: no block, no queue.
    hooked: bool,
    cwd: String,
    /// Latched by the start mark, cleared by the next prompt.
    started: bool,
    /// The foreground probe's last answer.
    child: bool,
    /// A shell with its own marks (133/633) is running a command. Shown to
    /// the UI, never busy(): its C may never get a D.
    foreign: bool,
    prompts: u64,
    sent: Option<ShellStatus>,
    /// A bar line was sent at this prompt count: the command owns the pane
    /// until a later prompt.
    awaiting: Option<u64>,
    /// Lines typed with Enter before the first prompt that the shell has not
    /// read yet. They wait in the tty, the shell reads one per prompt, and
    /// they are meant to run: when the prompt hook takes text back, this
    /// many whole lines at its start are typed into the pane again, and only
    /// the rest goes to the bar.
    run_ahead: usize,
}

impl ShellState {
    fn status(&self) -> ShellStatus {
        ShellStatus { integrated: self.integrated, cwd: self.cwd.clone(), program: self.started || self.child || self.foreign, prompts: self.prompts, pending: self.pending }
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
    /// `TERM_PROGRAM` for the shell; None is this app's own name. An app
    /// that embeds rsz-term sets its own, so scripts can tell where they run.
    pub term_program: Option<String>,
    /// Keep the person's prompt (an embedder with no input bar). The shell
    /// still reports its marks; a submitted line is written as typed, with
    /// no command block and no echo filtering.
    pub keep_prompt: bool,
}

impl Default for PaneConfig {
    fn default() -> Self {
        Self { cols: 80, rows: 24, command: None, cwd: None, env: Vec::new(), scrollback: 10_000, plain_shell: false, cursor: 1, term_program: None, keep_prompt: false }
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
    /// Held while a shell status is made and sent, so the UI gets them in
    /// the order they happened.
    status_order: Mutex<()>,
    /// Keys typed right after a bar line, held back from PSReadLine.
    hold: Mutex<Hold>,
}

/// PSReadLine reads every key waiting in the console input buffer at once
/// (a paste comes in one go) and keeps those after the Enter for its next
/// line, so keys typed right after a bar line never reach the command it
/// starts (cmd, Read-Host): PowerShell runs them itself at the next prompt.
/// After a bar line, the pane holds the keys typed next until PSReadLine has
/// taken the line (its echo is done), a prompt comes, or HOLD_MAX passes.
#[derive(Default)]
struct Hold {
    until: Option<Instant>,
    keys: Vec<u8>,
}

/// The longest keys wait for PowerShell to take a bar line.
const HOLD_MAX: Duration = Duration::from_millis(500);

/// Let the held keys go to the PTY, ahead of any typed after them.
fn release_hold(shared: &Shared, tx: &Sender<Cmd>) {
    let mut h = shared.hold.lock();
    if h.until.take().is_some() && !h.keys.is_empty() {
        let _ = tx.send(Cmd::Write(std::mem::take(&mut h.keys)));
    }
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
    keep_prompt: bool,
    /// PowerShell: PSReadLine reads ahead of a bar line's Enter (see Hold).
    reads_ahead: bool,
    sink: Sink,
}

/// A dropped pane closes its shell. It never joins: the last handle can be
/// dropped on one of the pane's own threads, and a join there would wait on
/// itself. Callers that need the threads gone still call close and join.
impl Drop for Pane {
    fn drop(&mut self) {
        self.close();
    }
}

/// What a bar submit did.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Submit {
    /// A blank line: Enter went to the shell.
    Blank,
    Sent,
    /// Held until the shell is idle; the queue's length.
    Queued(usize),
    /// [`Pane::try_submit`] only: the shell was busy, so nothing was written.
    Busy,
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

/// Env vars a running Claude Code session sets for its own children.
/// When Suzuri is launched from inside a session (a rebuild that relaunches
/// the app, say) it inherits them, and every pane would pass them on: a
/// `claude` started there then thinks it is a sub-session and, among other
/// things, stops saving its transcript. Panes are top-level shells, so drop
/// them. Settings-style vars (CLAUDE_CODE_PLUGIN_DIRS and the like) stay.
const CLAUDE_SESSION_ENV: &[&str] = &[
    "CLAUDECODE",
    "CLAUDE_PID",
    "CLAUDE_EFFORT",
    "CLAUDE_CODE_CHILD_SESSION",
    "CLAUDE_CODE_SESSION_ID",
    "CLAUDE_CODE_SESSION_ATTENDED",
    "CLAUDE_CODE_ENTRYPOINT",
    "CLAUDE_CODE_EXECPATH",
    "CLAUDE_CODE_MESSAGING_SOCKET",
    "CLAUDE_CODE_MESSAGING_TOKEN",
];

fn scrub_claude_session_env(cmd: &mut CommandBuilder) {
    for k in CLAUDE_SESSION_ENV {
        cmd.env_remove(k);
    }
}

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
        let reads_ahead = {
            let base = std::path::Path::new(&shell_prog).file_name().map(|b| b.to_string_lossy().to_lowercase()).unwrap_or_default();
            matches!(base.strip_suffix(".exe").unwrap_or(&base), "pwsh" | "powershell")
        };
        // The input bar's shell hooks, for the user's own shell only.
        let mut boot_env = Vec::new();
        let mut temp_dir = None;
        let mut hooked = false;
        if cfg.command.is_none() && !cfg.plain_shell {
            if let Some(b) = shell::bootstrap_with(&argv, cfg.keep_prompt) {
                argv = b.argv;
                boot_env = b.env;
                temp_dir = b.temp_dir;
                hooked = true;
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
        cmd.env("TERM_PROGRAM", cfg.term_program.as_deref().unwrap_or("suzuri"));
        scrub_claude_session_env(&mut cmd);
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

        let start_cwd: Option<std::path::PathBuf> = cfg.cwd.clone().map(Into::into).or_else(|| home_dir().map(Into::into));
        // A hooked shell's first prompt is coming: the bar holds keys for it
        // (see ShellStatus::pending), in the folder the shell starts in.
        let shell_state = ShellState {
            pending: hooked,
            hooked,
            cwd: if hooked { start_cwd.as_deref().map(|p| p.to_string_lossy().into_owned()).unwrap_or_default() } else { String::new() },
            ..Default::default()
        };
        let shared = Arc::new(Shared {
            engine: Mutex::new({
                // libghostty-vt (#290).
                let mut e: Box<dyn Engine> = Box::new(Ghostty::new(cfg.cols, cfg.rows, cfg.scrollback));
                if cfg.cursor != 1 {
                    e.set_default_cursor(cfg.cursor);
                }
                e.set_grow_at_bottom(cfg!(windows));
                e
            }),
            wake: Mutex::new(Wake { dirty: true, force_full: true, input_at: None, stop: false, sent: 0, sent_at: Instant::now(), acked: 0 }),
            cv: Condvar::new(),
            resize_slot: Mutex::new(None),
            focused: AtomicBool::new(false),
            state: Mutex::new(State::Starting),
            stats: Stats::default(),
            shell: Mutex::new(shell_state),
            images: Mutex::new(Images::default()),
            cell_px: AtomicU32::new(9 << 16 | 18),
            cwd: start_cwd,
            echo: Mutex::new(EchoFilter::default()),
            last_io: Mutex::new(Instant::now()),
            status_order: Mutex::new(()),
            hold: Mutex::new(Hold::default()),
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
            let sink = sink.clone();
            threads.push(thread("rsz-actor", move || actor_loop(rx, pair.master, writer, killer, shared, sink, (cfg.cols, cfg.rows))));
        }
        Ok(Pane { tx, shared, threads: Mutex::new(threads), pid, reported_cwd: Mutex::new(None), shell_prog, queue: Mutex::default(), keep_prompt: cfg.keep_prompt, reads_ahead, sink })
    }

    /// Enter in the input bar (#296, Go suzuri's submitBarLine): the bare-cd
    /// alias, then either a command block plus the line, or a place in the
    /// queue while the shell is busy.
    /// A hooked shell before its first prompt gets the line as typed at a
    /// terminal, blank or not: the tty holds it for the shell (it runs at
    /// the prompt) or for a start-up question that reads a line. It does
    /// not end the wait for the prompt.
    pub fn submit(&self, line: &str) -> Submit {
        let (display, payload) = block::expand(line, &self.shell_prog);
        let blank = display.trim().is_empty() && payload.trim().is_empty();
        let early = {
            let sh = self.shared.shell.lock();
            sh.hooked && !sh.integrated
        };
        if early {
            self.shared.shell.lock().run_ahead += 1 + payload.matches('\n').count();
            self.shared.engine.lock().scroll(i32::MIN);
            self.shared.kick(|w| w.input_at = Some(Instant::now()));
            let _ = self.tx.send(Cmd::Write(typed(&payload)));
            return if blank { Submit::Blank } else { Submit::Sent };
        }
        if blank {
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
        self.send(&display, &payload);
        Submit::Sent
    }

    /// [`Pane::submit`] that never queues: when the shell is busy it writes
    /// nothing and says [`Submit::Busy`], so a caller with its own queue
    /// (Irori's runs) keeps rsz-term's empty.
    pub fn try_submit(&self, line: &str) -> Submit {
        {
            let sh = self.shared.shell.lock();
            if sh.pending {
                return Submit::Busy;
            }
            if sh.hooked && !sh.integrated {
                drop(sh);
                return self.submit(line);
            }
        }
        let (display, payload) = block::expand(line, &self.shell_prog);
        if display.trim().is_empty() && payload.trim().is_empty() {
            self.write(b"\r".to_vec());
            return Submit::Blank;
        }
        if !self.queue.lock().is_empty() || self.busy() {
            return Submit::Busy;
        }
        self.send(&display, &payload);
        Submit::Sent
    }

    /// Whether a command owns the pane now (see `busy`): a submit would wait.
    pub fn is_busy(&self) -> bool {
        self.busy()
    }

    /// When the PTY last produced output.
    pub fn last_output_at(&self) -> Instant {
        *self.shared.last_io.lock()
    }

    /// A command block plus the line, or with `keep_prompt` the line alone
    /// as if typed at the person's prompt (the shell's echo stays).
    fn send(&self, display: &str, payload: &str) {
        if !self.keep_prompt {
            return self.send_block(display, payload);
        }
        {
            let mut sh = self.shared.shell.lock();
            sh.awaiting = Some(sh.prompts);
        }
        let mut line = payload.replace("\r\n", "\n").replace('\n', "\r").into_bytes();
        line.push(b'\r');
        self.write(line);
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
        self.send(&display, &payload);
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
    /// output that hasn't been quiet for QUEUE_IDLE. A shell without the
    /// bootstrap never reports a prompt, so a sent line can't hold it busy.
    /// A hooked shell is busy until its first prompt.
    fn busy(&self) -> bool {
        let sh = self.shared.shell.lock();
        if sh.pending || sh.integrated && sh.awaiting.is_some_and(|n| sh.prompts <= n) || sh.started || sh.child {
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
            let mut lines = block::block_lines(display, &cwd, &home, cols, spacer, palette::active().pal.accent);
            // The command's last line goes on the cursor's row: the shell's
            // echo would have been there, and its newline still follows.
            let on_line = lines.pop().unwrap_or_default();
            eng.insert_block(&lines, &on_line, keep);
            if trace() {
                eprintln!("rsz: block cy={cy} above={} keep={keep} spacer={spacer} rows={} cursor after={:?}", lines.len(), eng.size().1, eng.cursor());
            }
        }
        self.shared.echo.lock().arm(payload);
        let mut line = payload.replace("\r\n", "\n").replace('\n', "\r").into_bytes();
        line.push(b'\r');
        self.typing(&line);
        let mut h = self.shared.hold.lock();
        if !h.keys.is_empty() {
            let _ = self.tx.send(Cmd::Write(std::mem::take(&mut h.keys)));
        }
        let _ = self.tx.send(Cmd::Write(line));
        h.until = self.reads_ahead.then(|| Instant::now() + HOLD_MAX);
    }

    /// Input from the user: snaps the view back to the bottom. Before the
    /// first prompt it ends the wait (the keys go to the PTY from now on).
    pub fn write(&self, bytes: Vec<u8>) {
        self.typing(&bytes);
        let mut h = self.shared.hold.lock();
        if h.until.is_some() {
            h.keys.extend_from_slice(&bytes);
            return;
        }
        let _ = self.tx.send(Cmd::Write(bytes));
    }

    /// What typing does besides reaching the PTY.
    fn typing(&self, bytes: &[u8]) {
        let pending = {
            let mut sh = self.shared.shell.lock();
            if sh.hooked && !sh.integrated {
                sh.run_ahead += bytes.iter().filter(|&&b| b == b'\r').count();
            }
            sh.pending
        };
        if pending {
            give_up(&self.shared, &self.sink);
        }
        self.shared.engine.lock().scroll(i32::MIN);
        self.shared.kick(|w| w.input_at = Some(Instant::now()));
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

    /// The shell's process id.
    pub fn pid(&self) -> Option<u32> {
        self.pid
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
    // A hooked shell: tell the UI at once, ahead of any mark.
    if shared.shell.lock().pending {
        tell(&shared, &sink, |_| {});
    }
    loop {
        let n = match reader.read(&mut buf) {
            Ok(0) | Err(_) => break,
            Ok(n) => n,
        };
        if trace() {
            eprintln!("rsz: read {n} {:?}", String::from_utf8_lossy(&buf[..n.min(2000)]));
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
        // Before the marks: a submit racing the first prompt sees fresh
        // output and queues.
        *shared.last_io.lock() = Instant::now();
        if !marks.is_empty() {
            shell_marks(&mut marks, &shared, &sink, &tx);
        }
        // The shell's echo of a bar line goes (#296): the block shows it.
        {
            let mut echo = shared.echo.lock();
            if echo.active() {
                let raw = std::mem::take(&mut clean);
                echo.feed(&raw, &mut clean);
            }
            // The echo is done: PSReadLine has taken the line.
            if !echo.armed() {
                release_hold(&shared, &tx);
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
        // A full-screen program before the first prompt has the keys.
        if shared.shell.lock().pending && shared.engine.lock().alt_screen() {
            give_up(&shared, &sink);
        }
        shared.stats.feed_ns.fetch_add(t.elapsed().as_nanos() as u64, Ordering::Relaxed);
        shared.stats.bytes_in.fetch_add(n as u64, Ordering::Relaxed);
        dispatch(&mut events, &tx, &sink);
        shared.kick(|w| w.dirty = true);
    }
    let _ = tx.send(Cmd::ReaderEof);
}

/// Apply the shell's marks to the bar state and tell the UI what changed.
fn shell_marks(marks: &mut Vec<ShellEvent>, shared: &Shared, sink: &Sink, tx: &Sender<Cmd>) {
    let mut done = Vec::new();
    let mut typed = Vec::new();
    let _order = shared.status_order.lock();
    let status = {
        let mut sh = shared.shell.lock();
        for m in marks.drain(..) {
            match m {
                ShellEvent::Start => sh.started = true,
                ShellEvent::Cwd(cwd) => {
                    // The shell reads one waiting start-up line at this prompt.
                    sh.run_ahead = sh.run_ahead.saturating_sub(1);
                    // The echo filter stays armed: ConPTY passes these marks on
                    // before it paints the echo itself. Its byte budget, or the
                    // next submit, ends it.
                    sh.integrated = true;
                    sh.pending = false;
                    sh.started = false;
                    sh.foreign = false;
                    sh.prompts += 1;
                    sh.cwd = cwd;
                    release_hold(shared, tx);
                }
                ShellEvent::Foreign(running) => sh.foreign = running,
                ShellEvent::Typeahead(raw) => {
                    let (again, rest) = split_lines(&raw, sh.run_ahead);
                    sh.run_ahead = again.iter().filter(|&&b| b == b'\r').count();
                    if !again.is_empty() {
                        let _ = tx.send(Cmd::Write(again));
                    }
                    let text = shell::clean_typeahead(rest);
                    if !text.is_empty() {
                        typed.push(text);
                    }
                }
                ShellEvent::Done { exit, command } => {
                    sh.started = false;
                    sh.foreign = false;
                    done.push((exit, command));
                }
            }
        }
        sh.changed()
    };
    for (exit, command) in done {
        sink(Out::CommandDone { exit, command });
    }
    for text in typed {
        sink(Out::Typeahead(text));
    }
    if let Some(st) = status {
        sink(Out::Shell(st));
    }
}

/// The first `n` whole lines of text the prompt hook took back (the tty
/// turned their Enter into LF), as typed again (Enter as CR), and the rest.
fn split_lines(raw: &[u8], n: usize) -> (Vec<u8>, &[u8]) {
    let mut again = Vec::new();
    let mut i = 0;
    for _ in 0..n {
        let Some(k) = raw[i..].iter().position(|&b| b == b'\n' || b == b'\r') else { break };
        again.extend_from_slice(&raw[i..i + k]);
        again.push(b'\r');
        i += k + 1;
    }
    (again, &raw[i..])
}

/// Poll the foreground program while the pane runs. Only for an integrated
/// shell: without prompt marks the bar is off anyway. Before the first
/// prompt it watches for a start-up that reads keys itself.
fn fg_loop(probe: Probe, shared: Arc<Shared>, sink: Sink) {
    let mut reading = 0;
    loop {
        std::thread::sleep(FG_POLL);
        if *shared.state.lock() != State::Running {
            return;
        }
        let (pending, integrated) = {
            let sh = shared.shell.lock();
            (sh.pending, sh.integrated)
        };
        if pending {
            // Something reads keys itself (a question in the rc): two looks
            // in a row, since zle does too for a moment after the mark.
            reading = if probe.reads_keys() { reading + 1 } else { 0 };
            if reading >= 2 {
                give_up(&shared, &sink);
            }
            continue;
        }
        if !integrated {
            continue;
        }
        let child = probe.program_running();
        tell(&shared, &sink, |sh| sh.child = child);
    }
}

/// Change the shell state and send the status if it changed, in order with
/// every other status.
fn tell(shared: &Shared, sink: &Sink, f: impl FnOnce(&mut ShellState)) {
    let _order = shared.status_order.lock();
    let status = {
        let mut sh = shared.shell.lock();
        f(&mut sh);
        sh.changed()
    };
    if let Some(st) = status {
        sink(Out::Shell(st));
    }
}

/// A line as typed at a terminal: newlines as CR, then Enter.
fn typed(payload: &str) -> Vec<u8> {
    let mut b = payload.replace("\r\n", "\n").replace('\n', "\r").into_bytes();
    b.push(b'\r');
    b
}

/// The first prompt is not coming (yet): the rc execs another program,
/// reads keys itself, goes full screen, or someone typed into the PTY. The
/// bar lets go (the UI types what it holds into the pane).
fn give_up(shared: &Shared, sink: &Sink) {
    tell(shared, sink, |sh| {
        if sh.pending {
            sh.pending = false;
            // Only a prompt's cwd is the shell's; splits ask the OS again.
            sh.cwd.clear();
        }
    });
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

    // Kitty places an image at the cursor, then moves the cursor to the
    // right of it on its last row, unless C=1. The move only travels (LF and
    // a column move write no cell), so the mark cell stays as it is.
    fn place(&mut self, kid: u32, place: KittyPlace) {
        self.settle();
        let alt = self.eng.alt_screen();
        let cell = self.eng.cursor();
        {
            let mut im = self.shared.images.lock();
            let mark = (!alt).then(|| {
                let tag = im.tag();
                self.eng.mark_cursor(tag);
                tag
            });
            im.kitty_place(kid, place, mark, cell);
        }
        if !place.keep_cursor {
            let (cols, _) = self.eng.size();
            let col = (cell.0 as u32 + place.cols).min(cols.saturating_sub(1) as u32);
            // LF scrolls at the bottom row, taking the image with the text.
            let mut mv = b"\n".repeat(place.rows.saturating_sub(1).min(u16::MAX as u32) as usize);
            mv.extend_from_slice(format!("\x1b[{}G", col + 1).as_bytes());
            self.eng.feed(&mv, self.events);
        }
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

    loop {
        let until = shared.hold.lock().until;
        let cmd = match until {
            None => match rx.recv() {
                Ok(c) => c,
                Err(_) => break,
            },
            Some(t) => match rx.recv_timeout(t.saturating_duration_since(Instant::now())) {
                Ok(c) => c,
                Err(RecvTimeoutError::Timeout) => {
                    // PowerShell never showed taking the line: the keys go.
                    let mut h = shared.hold.lock();
                    if !h.until.is_some_and(|u| u <= Instant::now()) {
                        continue;
                    }
                    h.until = None;
                    let keys = std::mem::take(&mut h.keys);
                    if keys.is_empty() {
                        continue;
                    }
                    Cmd::Write(keys)
                }
                Err(RecvTimeoutError::Disconnected) => break,
            },
        };
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
                    {
                        let mut eng = shared.engine.lock();
                        let before = (eng.size(), eng.cursor());
                        eng.resize(size.0, size.1);
                        if trace() {
                            eprintln!("rsz: resize {:?} -> {:?}, cursor {:?} -> {:?}", before.0, size, before.1, eng.cursor());
                        }
                    }
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

    #[test]
    fn panes_drop_inherited_claude_session_env() {
        let mut cmd = CommandBuilder::new("sh");
        for k in CLAUDE_SESSION_ENV {
            cmd.env(k, "1");
        }
        cmd.env("CLAUDE_CODE_PLUGIN_DIRS", "/x");
        scrub_claude_session_env(&mut cmd);
        for k in CLAUDE_SESSION_ENV {
            assert_eq!(cmd.get_env(k), None, "{k} should be dropped");
        }
        assert_eq!(cmd.get_env("CLAUDE_CODE_PLUGIN_DIRS"), Some(std::ffi::OsStr::new("/x")));
    }

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

    /// bash with the bootstrap by hand (spawn hooks only $SHELL, zsh here):
    /// text typed while `sleep` runs is taken back from the tty at the next
    /// prompt and sent for the bar; the Enter in it runs nothing, and the
    /// shell's own line is left empty (the next command runs clean).
    #[test]
    fn bash_hands_typeahead_to_the_bar() {
        let shell = "/bin/bash";
        if !std::path::Path::new(shell).exists() {
            eprintln!("skipped: no {shell}");
            return;
        }
        enum Seen {
            Shell(ShellStatus),
            Typed(String),
        }
        let (tx, rx) = mpsc::channel();
        let tx = Mutex::new(tx);
        let sink: Sink = Arc::new(move |o| {
            let seen = match o {
                Out::Shell(st) => Seen::Shell(st),
                Out::Typeahead(t) => Seen::Typed(t),
                _ => return,
            };
            let _ = tx.lock().send(seen);
        });
        let home = std::env::temp_dir().join(format!("rsz-test-ahead-bash-{}", std::process::id()));
        std::fs::create_dir_all(&home).unwrap();
        let boot = shell::bootstrap(&[shell.to_string()]).unwrap();
        let mut env = boot.env.clone();
        env.push(("HOME".into(), home.to_string_lossy().into_owned()));
        let cfg = PaneConfig { cols: 60, rows: 12, command: Some(boot.argv.clone()), cwd: Some("/tmp".into()), env, ..Default::default() };
        let pane = Pane::spawn(cfg, sink).unwrap();
        let typed = std::cell::RefCell::new(Vec::new());
        let prompts = std::cell::Cell::new(0);
        let wait = |want: &dyn Fn(&ShellStatus) -> bool, what: &str| {
            let deadline = Instant::now() + Duration::from_secs(10);
            while Instant::now() < deadline {
                match rx.recv_timeout(Duration::from_millis(100)) {
                    Ok(Seen::Shell(st)) if want(&st) => return prompts.set(st.prompts),
                    Ok(Seen::Typed(t)) => typed.borrow_mut().push(t),
                    _ => {}
                }
            }
            panic!("{shell}: never saw {what}");
        };
        // A prompt: the next one, not the status a command's done mark gives.
        let prompt = |what: &str| {
            let n = prompts.get();
            wait(&|s| s.integrated && !s.program && s.prompts > n, what)
        };
        prompt("the first prompt");
        pane.write(b"sleep 1\r".to_vec());
        wait(&|s| s.program, "the command start");
        pane.write("echo $((6*7))\rnext \x1b[Aline 日本".as_bytes().to_vec());
        prompt("the prompt after sleep");
        assert_eq!(*typed.borrow(), vec!["echo $((6*7))\nnext line 日本".to_string()]);
        pane.write(b"echo A$((1+2))B\r".to_vec());
        prompt("the prompt after echo");
        let screen = pane.screen_text();
        eprintln!("bash screen:\n{screen}");
        let lines = lines_of(&screen);
        assert!(lines.iter().any(|l| l.trim() == "A3B"), "the next command did not run clean: {lines:?}");
        assert!(!lines.iter().any(|l| l.trim() == "42"), "the typed-ahead line ran: {lines:?}");
        assert!(!screen.contains("7878") && !screen.contains("typeahead"), "marks leaked: {screen}");
        assert_eq!(typed.borrow().len(), 1, "{:?}", typed.borrow());
        pane.close();
        pane.join();
        let _ = std::fs::remove_dir_all(home);
        if let Some(d) = boot.temp_dir {
            let _ = std::fs::remove_dir_all(d);
        }
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

    /// Run a shell script that prints a Kitty image then `X`, and give the
    /// screen's lines and what the pane placed.
    fn kitty_then_x(at: &str, keys: &str) -> (Vec<String>, Vec<String>) {
        let script = format!(
            "printf '{at}\\033_Ga=T,i=1,f=100,t=d,{keys};{}\\033\\\\X'; sleep 1",
            png_b64(30, 20)
        );
        let (pane, rx) = run(&["/bin/sh", "-c", &script]);
        let seen = wait_exit(&rx);
        let lines = pane.screen_text().lines().map(str::to_string).collect();
        pane.join();
        (lines, seen)
    }

    // One test, three panes in a row: opening many ptys at once makes
    // openpty fail on macOS, so this adds one pane at a time, not three.
    #[test]
    fn kitty_image_moves_the_cursor_past_it() {
        // 3 x 2 cells at row 3, col 5 (1-based): the cursor lands on the
        // image's last row, just right of it, so X is at row 4, col 8 (0-based
        // row 3, col 7) and the image is still shown, anchored at row 2 col 4.
        let (lines, seen) = kitty_then_x("\\033[3;5H", "c=3,r=2");
        assert_eq!(lines[3].find('X'), Some(7), "{lines:?}");
        assert!(!lines[2].contains('X'), "{lines:?}");
        assert!(seen.iter().any(|m| m == "placed [(1, 2, 4, 3, 2)]"), "{seen:?}");

        // C=1: X is written on the image's top-left cell, as before.
        let (lines, _) = kitty_then_x("\\033[3;5H", "c=3,r=2,C=1");
        assert_eq!(lines[2].find('X'), Some(4), "{lines:?}");

        // The pane is 10 rows. An image 4 rows tall from row 9 runs off the
        // bottom: the screen scrolls two rows, the cursor ends on the last
        // row, and the image moves up with its text.
        let (lines, seen) = kitty_then_x("\\033[9;1H", "c=2,r=4");
        assert_eq!(lines[9].find('X'), Some(2), "{lines:?}");
        assert!(seen.iter().any(|m| m == "placed [(1, 6, 0, 2, 4)]"), "{seen:?}");
    }

    /// S-2: dropping the last handle closes the shell (and doesn't hang).
    #[test]
    fn dropped_pane_closes_its_shell() {
        let (pane, rx) = run(&["/bin/cat"]);
        drop(pane);
        let seen = wait_exit(&rx);
        assert_eq!(seen.iter().filter(|m| m.starts_with("exit")).count(), 1, "{seen:?}");
    }

    fn wait_until(what: &str, f: impl Fn() -> bool) {
        let deadline = Instant::now() + Duration::from_secs(10);
        while !f() {
            assert!(Instant::now() < deadline, "never: {what}");
            std::thread::sleep(Duration::from_millis(20));
        }
    }

    /// S-10: a shell without the bootstrap never reports a prompt, so a
    /// submitted line must not keep it busy for good; try_submit refuses
    /// while busy instead of queuing.
    #[test]
    fn plain_shell_goes_idle_and_try_submit_never_queues() {
        let (pane, _rx) = run(&["/bin/cat"]);
        assert!(pane.is_busy(), "busy until the first QUEUE_IDLE passes");
        assert_eq!(pane.try_submit("early"), Submit::Busy);
        wait_until("idle at start", || !pane.is_busy());
        let before = pane.last_output_at();
        assert_eq!(pane.try_submit("hello"), Submit::Sent);
        // The command block shows "hello" at once; cat's echo is the output.
        wait_until("cat's echo", || pane.last_output_at() > before);
        assert_eq!(pane.try_submit("again"), Submit::Busy, "output was just now");
        assert_eq!(pane.queue_len(), 0, "try_submit never queues");
        wait_until("idle after the line", || !pane.is_busy());
        assert!(!pane.screen_text().contains("early"));
        pane.close();
        pane.join();
    }

    /// A foreign 133;C with no D (a remote fish's `exit`, a dropped ssh)
    /// shows as a program to the UI but never holds the pane busy, and its
    /// next prompt (133;A) clears it.
    #[test]
    fn foreign_start_without_done_never_holds_busy() {
        let shell = Arc::new(Mutex::new(ShellStatus::default()));
        let sh = shell.clone();
        let sink: Sink = Arc::new(move |o| {
            if let Out::Shell(st) = o {
                *sh.lock() = st;
            }
        });
        let cfg = PaneConfig {
            command: Some(vec!["/bin/sh".into(), "-c".into(), "printf '\\033]133;C\\007'; sleep 2; printf '\\033]133;A\\007'; sleep 5".into()]),
            ..Default::default()
        };
        let pane = Pane::spawn(cfg, sink).unwrap();
        wait_until("program", || shell.lock().program);
        assert!(!shell.lock().integrated);
        wait_until("idle with the C still open", || !pane.is_busy());
        assert!(shell.lock().program, "the UI still sees the command");
        assert_eq!(pane.try_submit("x"), Submit::Sent);
        wait_until("the prompt clears it", || !shell.lock().program);
        pane.close();
        pane.join();
    }

    /// S-3: an embedder names itself in TERM_PROGRAM.
    #[test]
    fn term_program_reaches_the_shell() {
        let sink: Sink = Arc::new(|_| {});
        let cmd = |tp: Option<&str>| PaneConfig {
            command: Some(vec!["/bin/sh".into(), "-c".into(), "printf '[%s]' \"$TERM_PROGRAM\"; sleep 5".into()]),
            term_program: tp.map(Into::into),
            ..Default::default()
        };
        let p = Pane::spawn(cmd(Some("irori")), sink.clone()).unwrap();
        wait_until("irori", || p.screen_text().contains("[irori]"));
        let d = Pane::spawn(cmd(None), sink).unwrap();
        wait_until("the default", || d.screen_text().contains("[suzuri]"));
    }

    /// S-9: keep_prompt leaves the user's prompt on screen, still reports
    /// the marks, and a submitted line is typed at that prompt as is.
    #[test]
    fn keep_prompt_shows_the_users_prompt() {
        if !std::env::var("SHELL").unwrap_or_default().ends_with("zsh") {
            eprintln!("skipped: $SHELL is not zsh");
            return;
        }
        let home = std::env::temp_dir().join(format!("rsz-test-keep-{}", std::process::id()));
        std::fs::create_dir_all(&home).unwrap();
        std::fs::write(home.join(".zshrc"), "PROMPT='KEEPME> '\n").unwrap();
        let shell = Arc::new(Mutex::new(ShellStatus::default()));
        let open = |keep: bool| {
            let sh = shell.clone();
            let sink: Sink = Arc::new(move |o| {
                if let Out::Shell(st) = o {
                    *sh.lock() = st;
                }
            });
            let cfg = PaneConfig {
                cols: 60,
                rows: 10,
                cwd: Some("/tmp".into()),
                env: vec![("RSZ_USER_ZDOTDIR".into(), home.to_string_lossy().into_owned())],
                keep_prompt: keep,
                ..Default::default()
            };
            Pane::spawn(cfg, sink).unwrap()
        };
        let kept = open(true);
        wait_until("the kept prompt", || kept.screen_text().contains("KEEPME>"));
        wait_until("integrated", || shell.lock().integrated);
        wait_until("idle", || !kept.is_busy());
        assert_eq!(kept.submit("echo kept-$((40+2))"), Submit::Sent);
        wait_until("the result", || kept.screen_text().contains("kept-42"));
        let screen = kept.screen_text();
        assert!(screen.contains("KEEPME> echo kept-$((40+2))"), "typed at the prompt, no block: {screen}");
        kept.close();
        kept.join();
        let quiet = open(false);
        wait_until("integrated", || shell.lock().integrated);
        std::thread::sleep(Duration::from_millis(300));
        assert!(!quiet.screen_text().contains("KEEPME>"), "the bar's prompt stays blank: {}", quiet.screen_text());
        quiet.close();
        quiet.join();
        let _ = std::fs::remove_dir_all(home);
    }

    /// A pane running the user's zsh with suzuri's hooks and `rc` as the
    /// user's .zshrc; every status it sends. None when $SHELL is not zsh.
    struct Hooked {
        pane: Pane,
        seen: Arc<Mutex<Vec<ShellStatus>>>,
        /// Text the prompt hook took back for the bar (Out::Typeahead).
        typed: Arc<Mutex<Vec<String>>>,
        home: std::path::PathBuf,
    }

    impl Hooked {
        fn open(name: &str, rc: &str, keep_prompt: bool) -> Option<Hooked> {
            if !std::env::var("SHELL").unwrap_or_default().ends_with("zsh") {
                eprintln!("skipped: $SHELL is not zsh");
                return None;
            }
            let home = std::env::temp_dir().join(format!("rsz-test-{name}-{}", std::process::id()));
            std::fs::create_dir_all(&home).unwrap();
            std::fs::write(home.join(".zshrc"), rc).unwrap();
            let seen = Arc::new(Mutex::new(Vec::new()));
            let typed = Arc::new(Mutex::new(Vec::new()));
            let (s, t) = (seen.clone(), typed.clone());
            let sink: Sink = Arc::new(move |o| match o {
                Out::Shell(st) => s.lock().push(st),
                Out::Typeahead(text) => t.lock().push(text),
                _ => {}
            });
            let cfg = PaneConfig {
                cols: 80,
                rows: 20,
                cwd: Some("/tmp".into()),
                env: vec![("RSZ_USER_ZDOTDIR".into(), home.to_string_lossy().into_owned())],
                keep_prompt,
                ..Default::default()
            };
            Some(Hooked { pane: Pane::spawn(cfg, sink).unwrap(), seen, typed, home })
        }

        fn last(&self) -> ShellStatus {
            self.seen.lock().last().cloned().unwrap_or_default()
        }

        fn wait(&self, what: &str, within: Duration, f: impl Fn(&ShellStatus) -> bool) {
            let deadline = Instant::now() + within;
            while !self.seen.lock().iter().any(&f) {
                assert!(Instant::now() < deadline, "never within {within:?}: {what}; saw {:?}", self.seen.lock());
                std::thread::sleep(Duration::from_millis(10));
            }
        }

        /// Wait for the prompt after the one counted now.
        fn next_prompt(&self, what: &str) {
            let n = self.last().prompts;
            self.wait(what, Duration::from_secs(10), |s| s.integrated && !s.program && s.prompts > n);
        }

        fn has_line(&self, want: &str) -> bool {
            lines_of(&self.pane.screen_text()).iter().any(|l| l == want)
        }
    }

    impl Drop for Hooked {
        fn drop(&mut self) {
            self.pane.close();
            self.pane.join();
            let _ = std::fs::remove_dir_all(&self.home);
        }
    }

    fn lines_of(screen: &str) -> Vec<String> {
        screen.lines().map(|l| l.trim_end().to_string()).collect()
    }

    /// The bar knows from the start that a prompt is coming, with the
    /// folder the shell starts in, and the first prompt ends the wait.
    #[test]
    fn hooked_shell_is_pending_until_its_first_prompt() {
        let Some(h) = Hooked::open("pending", "sleep 2\n", false) else { return };
        h.wait("pending at start", Duration::from_millis(500), |s| s.pending && !s.integrated && s.cwd.ends_with("tmp"));
        h.wait("the first prompt", Duration::from_secs(10), |s| s.integrated);
        let seen = h.seen.lock().clone();
        let first = seen.iter().position(|s| s.integrated).unwrap();
        assert!(seen[..first].iter().all(|s| s.pending && !s.integrated), "{seen:?}");
        assert!(seen[first..].iter().all(|s| s.integrated && !s.pending), "{seen:?}");
    }

    /// Enter in the bar before the first prompt types the line into the
    /// pane as is (no block, no queue) and the wait goes on; the shell runs
    /// it at its first prompt, once, and the bar works as usual after.
    #[test]
    fn enter_before_the_first_prompt_runs_at_it() {
        let Some(h) = Hooked::open("early", "sleep 2\n", false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        std::thread::sleep(Duration::from_millis(300));
        assert_eq!(h.pane.submit("echo early-$((40+2))"), Submit::Sent);
        assert_eq!(h.pane.queue_len(), 0);
        assert!(h.last().pending, "the wait goes on");
        h.wait("the prompt", Duration::from_secs(10), |s| s.integrated && !s.pending);
        wait_until("the typed line ran", || h.has_line("early-42"));
        std::thread::sleep(Duration::from_millis(300));
        assert_eq!(lines_of(&h.pane.screen_text()).iter().filter(|l| *l == "early-42").count(), 1, "{}", h.pane.screen_text());
        wait_until("idle", || !h.pane.is_busy());
        let r = h.pane.submit("echo after-$((2+3))");
        assert!(matches!(r, Submit::Sent | Submit::Queued(_)), "{r:?}");
        wait_until("the bar line runs", || {
            let _ = h.pane.flush_queue();
            h.has_line("after-5")
        });
        assert!(h.pane.screen_text().contains('❯'), "a block again at the prompt: {}", h.pane.screen_text());
    }

    /// A start-up question that ends in a newline and reads a line (the
    /// common "Press Enter" / "Start tmux? (y/n)" shape): a bare Enter and
    /// a typed answer both reach it at once.
    #[test]
    fn a_start_up_question_gets_its_enter_at_once() {
        let rc = "print 'Press Enter to continue'; read; print continued; sleep 0.3\n";
        let Some(h) = Hooked::open("enter", rc, false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        std::thread::sleep(Duration::from_millis(300));
        assert_eq!(h.pane.submit(""), Submit::Blank);
        let t = Instant::now();
        wait_until("continued", || h.has_line("continued"));
        assert!(t.elapsed() < Duration::from_millis(1500), "{:?}", t.elapsed());
        h.wait("the prompt", Duration::from_secs(5), |s| s.integrated);

        let rc = "print 'Start? (y/n)'; read ans; print \"answer=$ans\"; sleep 0.3\n";
        let Some(h) = Hooked::open("answer", rc, false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        std::thread::sleep(Duration::from_millis(300));
        assert_eq!(h.pane.submit("y"), Submit::Sent);
        let t = Instant::now();
        wait_until("the answer", || h.has_line("answer=y"));
        assert!(t.elapsed() < Duration::from_millis(1500), "{:?}", t.elapsed());
        h.wait("the prompt", Duration::from_secs(5), |s| s.integrated);
    }

    /// A question without the newline (`read 'ans?continue? '`) too.
    #[test]
    fn a_line_question_in_the_rc_gets_its_answer_at_once() {
        let rc = "read 'ans?continue? '; print \"answer=$ans\"; sleep 0.3\n";
        let Some(h) = Hooked::open("ask", rc, false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        std::thread::sleep(Duration::from_millis(300));
        assert_eq!(h.pane.submit("y"), Submit::Sent);
        wait_until("the answer", || h.pane.screen_text().contains("answer=y"));
        h.wait("the prompt", Duration::from_secs(5), |s| s.integrated);
    }

    /// try_submit (an embedder with its own queue) waits for the prompt.
    #[test]
    fn try_submit_is_busy_before_the_first_prompt() {
        let Some(h) = Hooked::open("try", "sleep 2\n", true) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        std::thread::sleep(Duration::from_millis(1000));
        assert_eq!(h.pane.try_submit("x"), Submit::Busy);
        assert_eq!(h.pane.try_submit(""), Submit::Busy);
        assert_eq!(h.pane.queue_len(), 0);
    }

    /// No prompt comes (the rc execs another program): every Enter is typed
    /// into the pane as is, in order, never drawn as a block.
    #[test]
    fn no_first_prompt_types_lines_in_order() {
        let rc = "exec /bin/sh -c 'while IFS= read -r l; do echo \"got:$l\"; done'\n";
        let Some(h) = Hooked::open("noprompt", rc, false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        for l in ["one", "two", "three"] {
            assert_eq!(h.pane.submit(l), Submit::Sent);
        }
        assert_eq!(h.pane.queue_len(), 0);
        wait_until("the lines", || h.has_line("got:three"));
        std::thread::sleep(Duration::from_millis(300));
        let screen = h.pane.screen_text();
        let lines = lines_of(&screen);
        let at = |want: &str| {
            assert_eq!(lines.iter().filter(|l| *l == want).count(), 1, "{want} once: {screen}");
            lines.iter().position(|l| l == want).unwrap()
        };
        assert!(at("got:one") < at("got:two") && at("got:two") < at("got:three"), "{screen}");
        assert!(!screen.contains('❯'), "no command blocks: {screen}");
        assert!(h.last().pending, "the bar stays");
    }

    /// A start-up file that reads single keys gets them at once: the wait
    /// ends when the shell reads keys itself.
    #[test]
    fn a_question_in_the_rc_releases_the_hold() {
        let rc = "read -k 1 '?go? '; print \"\\nanswer=$REPLY\"; sleep 1\n";
        let Some(h) = Hooked::open("question", rc, false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        h.wait("the wait given up", Duration::from_millis(1500), |s| !s.pending && !s.integrated);
        h.pane.write(b"y".to_vec());
        wait_until("the answer", || h.pane.screen_text().contains("answer=y"));
        h.wait("the late prompt", Duration::from_secs(10), |s| s.integrated);
    }

    /// After the wait is given up, an Enter that raced it is typed as is:
    /// never queued, never drawn as a block on a screen with no prompt.
    #[test]
    fn enter_after_giving_up_is_typed_as_is() {
        let rc = "exec /bin/sh -c 'while IFS= read -r l; do echo \"got:$l\"; done'\n";
        let Some(h) = Hooked::open("raced", rc, false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        h.pane.write(b"zero\r".to_vec());
        h.wait("the wait given up", Duration::from_millis(500), |s| !s.pending && !s.integrated);
        assert_eq!(h.pane.submit("one"), Submit::Sent);
        h.pane.write(b"two\r".to_vec());
        assert_eq!(h.pane.queue_len(), 0);
        wait_until("the lines", || h.has_line("got:two"));
        let screen = h.pane.screen_text();
        let lines = lines_of(&screen);
        let at = |want: &str| lines.iter().position(|l| l == want).unwrap_or(usize::MAX);
        assert!(at("got:zero") < at("got:one") && at("got:one") < at("got:two"), "{screen}");
        assert!(!screen.contains('❯'), "no command blocks: {screen}");
    }

    /// Focus reports (ESC[I / ESC[O) are answers, not typing: they go
    /// through reply and the wait goes on (ConPTY turns 1004 on at start).
    #[test]
    fn a_focus_report_does_not_end_the_wait() {
        let Some(h) = Hooked::open("focus", "printf '\\033[?1004h'; sleep 1.5\n", false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        std::thread::sleep(Duration::from_millis(300));
        h.pane.reply(b"\x1b[I".to_vec());
        std::thread::sleep(Duration::from_millis(300));
        let last = h.last();
        assert!(last.pending, "{last:?}");
        h.wait("the prompt", Duration::from_secs(5), |s| s.integrated);
    }

    /// A full-screen program before the first prompt has the keys.
    #[test]
    fn alt_screen_before_the_first_prompt_gives_up() {
        let Some(h) = Hooked::open("alt", "printf '\\033[?1049h'; sleep 30\n", false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        h.wait("the wait given up", Duration::from_millis(1500), |s| !s.pending && !s.integrated);
    }

    /// ^C (or anything else written to the PTY) ends the wait at once, and
    /// a line typed from the bar before it still goes first.
    #[test]
    fn input_written_before_the_first_prompt_ends_the_wait() {
        let Some(h) = Hooked::open("ctrlc", "sleep 3\n", false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        h.pane.write(b"\x03".to_vec());
        h.wait("the wait ended", Duration::from_millis(200), |s| !s.pending);
        drop(h);

        let Some(h) = Hooked::open("order", "sleep 2\n", false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        assert_eq!(h.pane.submit("echo q-$((1+1))"), Submit::Sent);
        assert!(h.last().pending, "a bar line does not end the wait");
        h.pane.write(b"echo second-$((2+2))\r".to_vec());
        h.wait("the wait ended", Duration::from_millis(200), |s| !s.pending);
        wait_until("both ran", || h.has_line("second-4"));
        let lines = lines_of(&h.pane.screen_text());
        let q = lines.iter().position(|l| l == "q-2");
        let s = lines.iter().position(|l| l == "second-4");
        assert!(q.is_some() && q < s, "{lines:?}");
    }

    /// The pane a person opens (the hooked $SHELL, zsh): text typed while
    /// `sleep` runs and never read is taken back at the next prompt and sent
    /// for the bar. The Enter in it runs nothing, an arrow key in it is
    /// dropped, and the shell's own line is left empty.
    #[test]
    fn zsh_hands_typeahead_to_the_bar() {
        let Some(h) = Hooked::open("ahead", "", false) else { return };
        h.next_prompt("the first prompt");
        assert!(h.typed.lock().is_empty(), "{:?}", h.typed.lock());
        wait_until("idle", || !h.pane.is_busy());
        assert_eq!(h.pane.submit("sleep 1"), Submit::Sent);
        h.wait("the command start", Duration::from_secs(5), |s| s.program);
        h.pane.write("echo $((6*7))\rnext \x1b[Aline 日本".as_bytes().to_vec());
        h.next_prompt("the prompt after sleep");
        assert_eq!(*h.typed.lock(), vec!["echo $((6*7))\nnext line 日本".to_string()]);
        wait_until("idle", || !h.pane.is_busy());
        assert_eq!(h.pane.submit("echo A$((1+2))B"), Submit::Sent);
        h.next_prompt("the prompt after echo");
        let screen = h.pane.screen_text();
        eprintln!("zsh screen:\n{screen}");
        assert!(h.has_line("A3B"), "the next command did not run clean: {screen}");
        assert!(!h.has_line("42"), "the typed-ahead line ran: {screen}");
        // The tty echoed the keys while sleep ran, as any terminal does; the
        // shell's line editor must not draw them a second time at the prompt.
        assert_eq!(screen.matches("next ").count(), 1, "the typed text shows twice: {screen}");
        assert!(!screen.contains("7878") && !screen.contains("typeahead"), "marks leaked: {screen}");
        assert_eq!(h.typed.lock().len(), 1, "{:?}", h.typed.lock());
        // A program that reads the keyboard still gets every key: nothing is
        // left to take back.
        h.pane.write(b"read x; echo got-$x\r".to_vec());
        h.wait("read runs", Duration::from_secs(5), |s| s.program);
        h.pane.write(b"abc\r".to_vec());
        h.next_prompt("the prompt after read");
        wait_until("read got the keys", || h.has_line("got-abc"));
        assert_eq!(h.typed.lock().len(), 1, "nothing more taken back: {:?}", h.typed.lock());
    }

    /// Lines typed with Enter before the first prompt still all run (the
    /// hook's take-back types them again), and text typed with no Enter
    /// while the first of them runs goes to the bar, not the shell's line.
    #[test]
    fn startup_lines_run_and_typeahead_after_them_goes_to_the_bar() {
        let Some(h) = Hooked::open("ahead-startup", "sleep 1\n", false) else { return };
        h.wait("pending", Duration::from_millis(500), |s| s.pending);
        assert_eq!(h.pane.submit("sleep 1; echo one-$((0+1))"), Submit::Sent);
        assert_eq!(h.pane.submit("echo two-$((1+1))"), Submit::Sent);
        h.wait("the first prompt", Duration::from_secs(10), |s| s.integrated);
        h.wait("the first line runs", Duration::from_secs(5), |s| s.program);
        h.pane.write(b"later words".to_vec());
        wait_until("both lines ran", || h.has_line("two-2"));
        h.wait("a prompt after both", Duration::from_secs(5), |s| s.integrated && !s.program && s.prompts >= 3);
        // The tty echoes keys typed during a command, as in any terminal:
        // "later words" sits on the screen before one-1's output.
        assert!(h.pane.screen_text().contains("one-1"), "{}", h.pane.screen_text());
        assert_eq!(*h.typed.lock(), vec!["later words".to_string()], "{}", h.pane.screen_text());
        wait_until("idle", || !h.pane.is_busy());
        assert_eq!(h.pane.submit("echo A$((1+2))B"), Submit::Sent);
        wait_until("the next line ran clean", || h.has_line("A3B"));
    }

    /// Panes that run a command, and shells without the hooks, never wait.
    #[test]
    fn command_and_plain_shells_are_never_pending() {
        let seen = Arc::new(Mutex::new(Vec::<ShellStatus>::new()));
        let zdot = std::env::temp_dir().join(format!("rsz-test-plain-{}", std::process::id()));
        std::fs::create_dir_all(&zdot).unwrap();
        for cfg in [
            PaneConfig { command: Some(vec!["/bin/cat".into()]), ..Default::default() },
            PaneConfig { plain_shell: true, env: vec![("ZDOTDIR".into(), zdot.to_string_lossy().into_owned())], ..Default::default() },
            PaneConfig { command: Some(vec!["/bin/zsh".into(), "-c".into(), "sleep 5".into()]), ..Default::default() },
        ] {
            let s = seen.clone();
            let sink: Sink = Arc::new(move |o| {
                if let Out::Shell(st) = o {
                    s.lock().push(st);
                }
            });
            let pane = Pane::spawn(cfg, sink).unwrap();
            wait_until("idle", || !pane.is_busy());
            pane.close();
            pane.join();
        }
        assert!(seen.lock().iter().all(|s| !s.pending), "{:?}", seen.lock());
        let _ = std::fs::remove_dir_all(zdot);
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

#[cfg(all(test, windows))]
mod windows_tests {
    use super::*;

    fn lines_of(screen: &str) -> Vec<String> {
        screen.lines().map(|l| l.trim_end().to_string()).collect()
    }

    fn wait_until(what: &str, f: impl Fn() -> bool) {
        let deadline = Instant::now() + Duration::from_secs(20);
        while !f() {
            assert!(Instant::now() < deadline, "never: {what}");
            std::thread::sleep(Duration::from_millis(10));
        }
    }

    /// PowerShell under ConPTY (pwsh, from $RSZ_TEST_PWSH or PATH, and
    /// Windows PowerShell 5.1): keys typed while a command runs wait in the
    /// console input buffer; the prompt hook reads them out before
    /// PSReadLine does and sends them for the bar. The Enter in them runs
    /// nothing, an arrow is dropped, PSReadLine's line is left empty, and a
    /// program that reads keys itself (cmd, Read-Host) still gets them all.
    #[test]
    fn powershell_hands_typeahead_to_the_bar() {
        let given = std::env::var("RSZ_TEST_PWSH").ok().filter(|s| !s.is_empty());
        if let Some(p) = &given {
            assert!(std::path::Path::new(p).is_file(), "RSZ_TEST_PWSH={p} is not a file");
        }
        let mut shells: Vec<String> = given.or_else(|| which("pwsh.exe")).into_iter().collect();
        if shells.is_empty() {
            eprintln!("skipped pwsh 7: none on PATH and RSZ_TEST_PWSH unset");
        }
        shells.push("powershell.exe".into());
        for exe in shells {
            powershell_typeahead(&exe);
        }
    }

    fn powershell_typeahead(exe: &str) {
        let seen = Arc::new(Mutex::new(Vec::<ShellStatus>::new()));
        let typed = Arc::new(Mutex::new(Vec::<String>::new()));
        let (s, t) = (seen.clone(), typed.clone());
        let sink: Sink = Arc::new(move |o| match o {
            Out::Shell(st) => s.lock().push(st),
            Out::Typeahead(text) => t.lock().push(text),
            _ => {}
        });
        let boot = shell::bootstrap(&[exe.to_string(), "-NoLogo".into()]).unwrap();
        let cfg = PaneConfig { cols: 100, rows: 30, command: Some(boot.argv), ..Default::default() };
        let pane = Pane::spawn(cfg, sink).unwrap();
        let last = || seen.lock().last().cloned().unwrap_or_default();
        let wait = |what: &str, f: &dyn Fn(&ShellStatus) -> bool| {
            let deadline = Instant::now() + Duration::from_secs(30);
            while !seen.lock().iter().any(f) {
                assert!(Instant::now() < deadline, "{exe}: never saw {what}; saw {:?}\n{}", seen.lock(), pane.screen_text());
                std::thread::sleep(Duration::from_millis(10));
            }
        };
        let next_prompt = |what: &str| {
            let n = last().prompts;
            wait(what, &|s| s.integrated && !s.program && s.prompts > n);
        };
        let has_line = |want: &str| lines_of(&pane.screen_text()).iter().any(|l| l == want);
        wait("the first prompt", &|s| s.integrated);
        wait_until("idle", || !pane.is_busy());
        assert_eq!(pane.submit("Start-Sleep 3"), Submit::Sent);
        // PowerShell has no start mark: give the command a moment to run.
        std::thread::sleep(Duration::from_millis(800));
        pane.write("echo (6*7)\rnext \x1b[Aline 日本".as_bytes().to_vec());
        next_prompt("the prompt after Start-Sleep");
        assert_eq!(*typed.lock(), vec!["echo (6*7)\nnext line 日本".to_string()], "{exe}");
        eprintln!("{exe}: the bar got {:?}", typed.lock());
        wait_until("idle", || !pane.is_busy());
        assert_eq!(pane.submit("echo A$(1+2)B"), Submit::Sent);
        next_prompt("the prompt after echo");
        wait_until("echo's output", || has_line("A3B"));
        let screen = pane.screen_text();
        eprintln!("{exe} screen:\n{screen}");
        assert!(!has_line("42"), "{exe}: the typed-ahead line ran: {screen}");
        assert!(!screen.contains("7878") && !screen.contains("typeahead"), "{exe}: marks leaked: {screen}");
        // Programs that read the keyboard get every key: cmd (a REPL in its
        // own process) and Read-Host (PowerShell's own), even keys typed at
        // once after the bar's Enter, which PSReadLine would read ahead and
        // keep for itself (the pane holds them until it has taken the line).
        wait_until("idle", || !pane.is_busy());
        assert_eq!(pane.submit("cmd.exe /d"), Submit::Sent);
        pane.write(b"echo got-cmd\rexit\r".to_vec());
        next_prompt("the prompt after cmd");
        wait_until("cmd got the keys", || has_line("got-cmd"));
        let screen = pane.screen_text();
        eprintln!("{exe} cmd:\n{screen}");
        // cmd's own prompt read both lines (not PowerShell, later).
        assert!(lines_of(&screen).iter().any(|l| l.ends_with(">echo got-cmd")), "{exe}: cmd never read the line: {screen}");
        assert!(lines_of(&screen).iter().any(|l| l.ends_with(">exit")), "{exe}: cmd never read exit: {screen}");
        wait_until("idle", || !pane.is_busy());
        assert_eq!(pane.submit("Read-Host | % { \"got-$_\" }"), Submit::Sent);
        pane.write(b"abc\r".to_vec());
        next_prompt("the prompt after Read-Host");
        wait_until("Read-Host got the keys", || has_line("got-abc"));
        assert_eq!(typed.lock().len(), 1, "{exe}: nothing more taken back: {:?}", typed.lock());
        eprintln!("{exe}: cmd and Read-Host got every key");
        pane.close();
        pane.join();
    }

    /// The pane a person opens (the default PowerShell, hooked by spawn):
    /// lines typed with Enter before the first prompt all run, the second
    /// one too (taken back after the first ran and typed again), and none
    /// reaches the bar.
    #[test]
    fn powershell_startup_lines_all_run() {
        let seen = Arc::new(Mutex::new(Vec::<ShellStatus>::new()));
        let typed = Arc::new(Mutex::new(Vec::<String>::new()));
        let (s, t) = (seen.clone(), typed.clone());
        let sink: Sink = Arc::new(move |o| match o {
            Out::Shell(st) => s.lock().push(st),
            Out::Typeahead(text) => t.lock().push(text),
            _ => {}
        });
        let pane = Pane::spawn(PaneConfig { cols: 100, rows: 30, ..Default::default() }, sink).unwrap();
        wait_until("pending", || seen.lock().iter().any(|s| s.pending));
        assert_eq!(pane.submit("Start-Sleep 1"), Submit::Sent);
        assert_eq!(pane.submit("echo B$(2+3)C"), Submit::Sent);
        let has_line = |want: &str| lines_of(&pane.screen_text()).iter().any(|l| l == want);
        wait_until("the second line ran", || has_line("B5C"));
        eprintln!("{} screen:
{}", default_shell()[0], pane.screen_text());
        assert!(typed.lock().is_empty(), "{:?}", typed.lock());
        pane.close();
        pane.join();
    }
}
