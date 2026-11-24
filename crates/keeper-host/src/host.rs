//! The host: one shell, its whole `Pane` (PTY or ConPTY, screen,
//! scrollback), served on `<dir>/<id>.sock` to any number of viewers.
//! Started as `rsz-keeper-host --keeper-host <dir> <id>` (or the app's
//! `suzuri --keeper-host <dir> <id>`), its config as JSON on stdin.

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::Ordering;
use std::time::Duration;

use base64::Engine as _;
use parking_lot::Mutex;
use rsz_term::{Out, Pane, Sink};
use serde_json::{Value, json};

use crate::codec::*;
use std::collections::VecDeque;
use std::io::{Read, Write};

use rsz_keeper::sock::{UnixListener, UnixStream};
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
    /// Clipboard reads waiting for a viewer's answer, by ask number.
    clip: Mutex<HashMap<u64, SyncSender<Option<String>>>>,
    clip_next: std::sync::atomic::AtomicU64,
}

/// How long a program's clipboard read waits for the viewer's answer:
/// the app's own read takes up to clip::OSC52, plus the round trip.
const CLIP_WAIT: Duration = Duration::from_millis(600);

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
            Out::CommandDone { exit, command } => {
                json!({ "t": "done", "exit": exit, "command": command })
            }
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

    /// A program in the shell reads the clipboard (OSC 52 "?"). The host
    /// has no clipboard of its own: an attached viewer (a Suzuri window)
    /// is asked, `{"t":"clip_ask","n"}`, and answers on a connection of
    /// its own, `{"t":"clip_answer","n","text"}`, so a call of its
    /// waiting behind the engine lock can't hold the answer up. With no
    /// viewer, or none answering in time, the program gets an empty
    /// answer. Runs under the pane's engine lock, as the app's reader
    /// does: ghostty takes the answer only before this returns.
    fn read_clipboard(&self) -> Option<String> {
        let (tx, rx) = sync_channel(1);
        let n = self.clip_next.fetch_add(1, Ordering::Relaxed) + 1;
        {
            let mut hub = self.hub.lock();
            if !hub.clients.iter().any(|c| c.viewer) {
                return None;
            }
            self.clip.lock().insert(n, tx);
            Self::broadcast(
                &mut hub,
                wire::encode_json(&json!({ "t": "clip_ask", "n": n })),
                true,
            );
        }
        let text = rx.recv_timeout(CLIP_WAIT).ok().flatten();
        self.clip.lock().remove(&n);
        text
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
        h["exited"] = hub
            .cache
            .exited
            .map(|c| json!({ "code": c }))
            .unwrap_or(Value::Null);
        h
    }

    fn serve(self: Arc<Self>, mut stream: UnixStream) {
        if !rsz_keeper::dir::peer_is_me(&stream) {
            return;
        }
        let _ = stream.set_read_timeout(Some(Duration::from_secs(5)));
        let Ok(hi) = wire::read(&mut stream).and_then(|(k, b)| wire::json(k, &b)) else {
            return;
        };
        if hi["t"] != "hello" {
            return;
        }
        let _ = stream.set_read_timeout(None);
        let viewer = hi["role"] == "viewer";
        let Ok(mut out) = stream.try_clone() else {
            return;
        };
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
            hub.clients.push(Client {
                n: me,
                tx: tx.clone(),
                viewer,
                stream: s2,
            });
        }
        if viewer && let Some(p) = self.pane.get() {
            p.redraw();
        }
        while let Ok((kind, body)) = wire::read(&mut stream) {
            let Some(p) = self.pane.get() else { continue };
            match kind {
                K_INPUT => p.write(body),
                K_JSON => {
                    let Ok(v) = serde_json::from_slice::<Value>(&body) else {
                        continue;
                    };
                    if v["t"] == "clip_answer" {
                        if let Some(tx) = v["n"].as_u64().and_then(|n| self.clip.lock().remove(&n))
                        {
                            let _ = tx.try_send(v["text"].as_str().map(str::to_string));
                        }
                        continue;
                    }
                    if v["t"] == "resize" {
                        let mut h = self.hello.lock();
                        h["cols"] = v["cols"].clone();
                        h["rows"] = v["rows"].clone();
                    }
                    if let Some(ans) = handle(p, &v)
                        && let Some(q) = v["q"].as_u64()
                    {
                        let _ = tx.try_send(Arc::new(wire::encode_json(
                            &json!({ "t": "r", "q": q, "v": ans }),
                        )));
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
            if let Some(pal) = palette_of(v)
                && rsz_term::palette::set_active(pal)
            {
                p.redraw();
            }
        }
        "close" => p.close(),
        "reply" => p.reply(B64.decode(s("data")).unwrap_or_default()),
        "paste" => p.paste(s("text")),
        "select" => match s("op") {
            "start" => p.select_start(
                u("col") as u16,
                u("row") as u16,
                b("right"),
                select_kind(s("kind")),
            ),
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
        "program" => {
            return Some(
                json!({ "name": p.program_name(), "pid": p.program_pid(), "cwd": p.live_cwd() }),
            );
        }
        "state" => return Some(format!("{:?}", p.state()).into()),
        "alt_screen" => return Some(p.alt_screen().into()),
        "selection" => return Some(p.selection_text().into()),
        "text" => {
            let lines = u("lines") as usize;
            let tail = if lines > 0 {
                p.history_tail(lines)
            } else {
                Vec::new()
            };
            return Some(json!({ "screen": p.screen_text(), "tail": tail }));
        }
        "link" => {
            return Some(match p.link_at(u("col") as u16, u("row") as u16) {
                Some(l) => {
                    json!({ "url": l.url, "row": l.row, "x0": l.x0, "x1": l.x1, "osc8": l.osc8 })
                }
                None => Value::Null,
            });
        }
        "mouse" => return Some(mouse_of(v).is_some_and(|ev| p.mouse(ev)).into()),
        "stats" => {
            let st = p.stats();
            let l = |a: &std::sync::atomic::AtomicU64| a.load(Ordering::Relaxed);
            return Some(
                json!({ "bytes_in": l(&st.bytes_in), "feed_ns": l(&st.feed_ns), "frames": l(&st.frames), "frame_bytes": l(&st.frame_bytes), "snapshot_ns": l(&st.snapshot_ns), "writes": l(&st.writes) }),
            );
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
    #[cfg(unix)]
    unsafe {
        libc::signal(libc::SIGHUP, libc::SIG_IGN);
        libc::signal(libc::SIGPIPE, libc::SIG_IGN);
    }
    // Windows: the app started this one; it starts the real host and
    // ends, so the host's parent is gone and it is in nobody's process
    // tree (stdin, stdout and the log pass on to it).
    #[cfg(windows)]
    if args.get(2).map(String::as_str) != Some("--detached") {
        let started = std::env::current_exe().and_then(|me| {
            let mut cmd = std::process::Command::new(me);
            cmd.arg("--keeper-host")
                .args(args.iter().take(2))
                .arg("--detached");
            crate::win::spawn(&mut cmd)
        });
        return match started {
            Ok(_) => 0,
            Err(e) => {
                println!("error start: {e}");
                1
            }
        };
    }
    // Windows starts a process made with CREATE_NEW_PROCESS_GROUP (as
    // this one is, by win::spawn) with Ctrl+C ignored, and its children
    // inherit that: the shell under ConPTY would never stop on a typed
    // Ctrl+C (hearth#1014). Turn it back on before the shell starts. The
    // host has no console, so no Ctrl+C reaches the host itself.
    #[cfg(windows)]
    // SAFETY: a plain Win32 call with no pointers.
    unsafe {
        windows_sys::Win32::System::Console::SetConsoleCtrlHandler(None, 0);
    }
    let fail = |m: String| {
        println!("error {m}");
        eprintln!("keeper host: {m}");
        1
    };
    let (Some(dir), Some(id)) = (
        args.first().map(PathBuf::from),
        args.get(1).and_then(|s| s.parse::<u32>().ok()),
    ) else {
        return fail("usage: suzuri --keeper-host <dir> <id>".into());
    };
    let mut input = String::new();
    if let Err(e) = std::io::stdin().read_to_string(&mut input) {
        return fail(format!("stdin: {e}"));
    }
    let c: Value = serde_json::from_str(&input).unwrap_or(Value::Null);
    let strs = |v: &Value| {
        v.as_array().map(|a| {
            a.iter()
                .filter_map(|x| x.as_str().map(str::to_string))
                .collect::<Vec<_>>()
        })
    };
    let cfg = PaneConfig {
        cols: c["cols"].as_u64().unwrap_or(80) as u16,
        rows: c["rows"].as_u64().unwrap_or(24) as u16,
        command: strs(&c["command"]),
        cwd: c["cwd"].as_str().map(str::to_string),
        env: c["env"]
            .as_array()
            .map(|a| {
                a.iter()
                    .filter_map(|kv| {
                        Some((
                            kv.get(0)?.as_str()?.to_string(),
                            kv.get(1)?.as_str()?.to_string(),
                        ))
                    })
                    .collect()
            })
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
    if UnixStream::connect(&sock).is_ok() {
        return fail(format!("shell {id} is kept already"));
    }
    let _ = std::fs::remove_file(&sock);
    let listener = match UnixListener::bind(&sock) {
        Ok(l) => l,
        Err(e) => return fail(format!("bind {}: {e}", sock.display())),
    };
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&sock, std::fs::Permissions::from_mode(0o600));
    }
    let started = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
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
        clip: Mutex::default(),
        clip_next: Default::default(),
    });
    // Before the pane: a terminal answers clipboard reads only when a
    // reader was set when it was made.
    let h3 = host.clone();
    rsz_term::set_clipboard_reader(move || h3.read_clipboard());
    let h2 = host.clone();
    let sink: Sink = Arc::new(move |o| h2.out(o));
    // Windows: ConPTY's console host inherits every inheritable handle;
    // it must not get the app's pipes (they would never close) or the log.
    #[cfg(windows)]
    {
        use windows_sys::Win32::Foundation::{HANDLE_FLAG_INHERIT, SetHandleInformation};
        use windows_sys::Win32::System::Console::{
            GetStdHandle, STD_ERROR_HANDLE, STD_INPUT_HANDLE, STD_OUTPUT_HANDLE,
        };
        for which in [STD_INPUT_HANDLE, STD_OUTPUT_HANDLE, STD_ERROR_HANDLE] {
            // SAFETY: clearing a flag on this process's own standard handles.
            unsafe { SetHandleInformation(GetStdHandle(which), HANDLE_FLAG_INHERIT, 0) };
        }
    }
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
    #[cfg(unix)]
    if let Ok(null) = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open("/dev/null")
    {
        use std::os::fd::AsRawFd;
        // SAFETY: dup2 onto the standard descriptors.
        unsafe {
            libc::dup2(null.as_raw_fd(), 0);
            libc::dup2(null.as_raw_fd(), 1);
        }
    }
    // Windows: the same, so the app's end of the pipes sees them close.
    #[cfg(windows)]
    if let Ok(null) = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open("NUL")
    {
        use std::os::windows::io::IntoRawHandle;
        use windows_sys::Win32::Foundation::CloseHandle;
        use windows_sys::Win32::System::Console::{
            GetStdHandle, STD_INPUT_HANDLE, STD_OUTPUT_HANDLE, SetStdHandle,
        };
        let null = null.into_raw_handle();
        for which in [STD_INPUT_HANDLE, STD_OUTPUT_HANDLE] {
            // SAFETY: swapping a standard handle for NUL (kept open for
            // good) and closing the pipe it was; nothing else uses it now.
            unsafe {
                let old = GetStdHandle(which);
                SetStdHandle(which, null);
                if !old.is_null() && old != windows_sys::Win32::Foundation::INVALID_HANDLE_VALUE {
                    CloseHandle(old);
                }
            }
        }
    }
    for stream in listener.incoming() {
        let Ok(stream) = stream else { continue };
        let h = host.clone();
        std::thread::spawn(move || h.serve(stream));
    }
    0
}
