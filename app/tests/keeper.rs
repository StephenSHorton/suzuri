//! The shell keeper end to end, with no window: a host started as the app
//! starts one (`suzuri --keeper-host`), viewers that come and go, a second
//! program on the same shell, and a close that ends it (cards hearth#913,
//! hearth#923 for Windows).

use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, Instant};

use rsz_keeper::{Conn, Event, dir};
use serde_json::json;

/// A short private folder (socket paths are capped at 104 bytes).
struct Folder(PathBuf);
impl Folder {
    fn new(tag: &str) -> Folder {
        #[cfg(unix)]
        let p = PathBuf::from(format!("/tmp/rszk-{tag}-{}", std::process::id()));
        #[cfg(windows)]
        let p = std::env::temp_dir().join(format!("rszk-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&p);
        dir::ensure(&p).unwrap();
        Folder(p)
    }
}
impl Drop for Folder {
    fn drop(&mut self) {
        // Close anything left so no host outlives the test.
        for id in dir::list(&self.0) {
            if let Ok(c) = Conn::connect(&dir::sock_path(&self.0, id), "control", "test", |_| {}) {
                let _ = c.send(&json!({ "t": "close" }));
            }
        }
        std::thread::sleep(Duration::from_millis(400));
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

fn start_host(dir: &Path, id: u32, command: &[&str]) -> u32 {
    let mut child = Command::new(env!("CARGO_BIN_EXE_suzuri"))
        .arg("--keeper-host")
        .arg(dir)
        .arg(id.to_string())
        .env("SHELL", "/bin/sh")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::inherit())
        .spawn()
        .unwrap();
    let cfg = json!({ "cols": 80, "rows": 24, "command": command, "version": "test-1" });
    child.stdin.take().unwrap().write_all(cfg.to_string().as_bytes()).unwrap();
    let mut out = String::new();
    // One line, as the app reads it: on Windows ConPTY's console host may
    // keep the pipe open after the host lets go of it.
    std::io::BufRead::read_line(&mut std::io::BufReader::new(child.stdout.take().unwrap()), &mut out).unwrap();
    assert!(out.starts_with("ready"), "host said {out:?}");
    std::thread::spawn(move || child.wait());
    out.split_whitespace().nth(1).unwrap().parse().unwrap()
}

fn screen(dir: &Path, id: u32) -> String {
    let c = Conn::connect(&dir::sock_path(dir, id), "control", "test", |_| {}).unwrap();
    let v = c.call(json!({ "t": "text", "lines": 200 })).unwrap();
    let mut s: Vec<String> = v["tail"].as_array().unwrap().iter().map(|l| l.as_str().unwrap().to_string()).collect();
    s.push(v["screen"].as_str().unwrap().to_string());
    s.join("\n")
}

fn wait_for(what: &str, mut f: impl FnMut() -> bool) {
    let t = Instant::now();
    while !f() {
        assert!(t.elapsed() < Duration::from_secs(10), "timed out: {what}");
        std::thread::sleep(Duration::from_millis(50));
    }
}

#[cfg(unix)]
fn alive(pid: u32) -> bool {
    unsafe { libc::kill(pid as i32, 0) == 0 }
}

#[cfg(windows)]
fn alive(pid: u32) -> bool {
    use windows_sys::Win32::Foundation::{CloseHandle, STILL_ACTIVE};
    use windows_sys::Win32::System::Threading::{GetExitCodeProcess, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION};
    let h = unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid) };
    if h.is_null() {
        return false;
    }
    let mut code = 0u32;
    let ok = unsafe { GetExitCodeProcess(h, &mut code) } != 0;
    unsafe { CloseHandle(h) };
    ok && code == STILL_ACTIVE as u32
}

/// The shell the tests run, and a line that prints line-1 .. line-5.
#[cfg(unix)]
const SH: &str = "/bin/sh";
#[cfg(unix)]
const FIVE_LINES: &[u8] = b"for i in 1 2 3 4 5; do echo line-$i; done\r";
#[cfg(windows)]
const SH: &str = "cmd.exe";
#[cfg(windows)]
const FIVE_LINES: &[u8] = b"for %i in (1 2 3 4 5) do @echo line-%i\r";

fn viewer(dir: &Path, id: u32) -> (Conn, Arc<AtomicU64>) {
    let frames = Arc::new(AtomicU64::new(0));
    let f = frames.clone();
    let c = Conn::connect(&dir::sock_path(dir, id), "viewer", "test", move |e| {
        if let Event::Frame(_) = e {
            f.fetch_add(1, Ordering::Relaxed);
        }
    })
    .unwrap();
    (c, frames)
}

#[test]
fn a_shell_outlives_its_viewer_and_ends_when_closed() {
    let d = Folder::new("a");
    let host = start_host(&d.0, 7, &[SH]);
    let (a, frames_a) = viewer(&d.0, 7);
    assert_eq!(a.hello["version"], "test-1");
    assert_eq!(a.hello["viewers"], 0);
    let shell = a.hello["shell_pid"].as_u64().unwrap() as u32;
    wait_for("a's first frame", || frames_a.load(Ordering::Relaxed) > 0);
    a.input(FIVE_LINES).unwrap();
    wait_for("the output", || screen(&d.0, 7).contains("line-5"));

    // The viewer goes away without a word (as a kill -9 of the app).
    drop(a);
    std::thread::sleep(Duration::from_millis(300));
    assert!(alive(host) && alive(shell), "the shell must outlive its viewer");

    // A new viewer gets the same shell, its screen and a full frame.
    let (b, frames_b) = viewer(&d.0, 7);
    assert_eq!(b.hello["shell_pid"].as_u64(), Some(shell as u64));
    wait_for("b's full frame", || frames_b.load(Ordering::Relaxed) > 0);
    assert!(screen(&d.0, 7).contains("line-3"));

    // A second program on the same shell at once: both see what it does.
    let (c, frames_c) = viewer(&d.0, 7);
    assert_eq!(c.hello["viewers"], 1);
    wait_for("c's frame", || frames_c.load(Ordering::Relaxed) > 0);
    let (nb, nc) = (frames_b.load(Ordering::Relaxed), frames_c.load(Ordering::Relaxed));
    c.input(b"echo typed-by-c\r").unwrap();
    wait_for("c's typing", || screen(&d.0, 7).contains("typed-by-c"));
    wait_for("frames to both", || frames_b.load(Ordering::Relaxed) > nb && frames_c.load(Ordering::Relaxed) > nc);

    // Closing ends the shell and its host, and the socket goes.
    b.send(&json!({ "t": "close" })).unwrap();
    wait_for("the shell to end", || !alive(shell));
    wait_for("the host to end", || !alive(host));
    assert!(!dir::has(&d.0, 7));
}

#[test]
fn a_host_refuses_an_id_that_is_kept_already() {
    let d = Folder::new("b");
    start_host(&d.0, 3, &[SH]);
    let out = Command::new(env!("CARGO_BIN_EXE_suzuri"))
        .arg("--keeper-host")
        .arg(&d.0)
        .arg("3")
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .output()
        .unwrap();
    assert!(String::from_utf8_lossy(&out.stdout).starts_with("error"), "{:?}", out);
    assert_eq!(dir::list(&d.0), vec![3]);
}

#[test]
fn the_host_answers_the_calls_the_app_makes() {
    let d = Folder::new("c");
    start_host(&d.0, 4, &[SH]);
    let c = Conn::connect(&dir::sock_path(&d.0, 4), "control", "test", |_| {}).unwrap();
    assert_eq!(c.call(json!({ "t": "state" })).unwrap(), "Running");
    assert_eq!(c.call(json!({ "t": "alt_screen" })).unwrap(), false);
    assert_eq!(c.call(json!({ "t": "selection" })).unwrap(), serde_json::Value::Null);
    assert!(c.call(json!({ "t": "stats" })).unwrap()["bytes_in"].is_u64());
    c.send(&json!({ "t": "resize", "cols": 100, "rows": 30 })).unwrap();
    wait_for("the resize", || Conn::connect(&dir::sock_path(&d.0, 4), "control", "t", |_| {}).unwrap().call(json!({ "t": "text" })).unwrap()["screen"].as_str().unwrap().lines().count() <= 30);
}

/// What the extra hop costs: a key typed to the frame that shows it, in
/// the app's own process and through a host. Run with --ignored --nocapture.
#[test]
#[ignore]
#[cfg(unix)]
fn key_to_frame_latency() {
    use std::sync::mpsc::channel;
    let n = 200;
    let median = |mut v: Vec<f64>| {
        v.sort_by(|a, b| a.partial_cmp(b).unwrap());
        (v[v.len() / 2], v[v.len() * 9 / 10])
    };
    // In process.
    let (tx, rx) = channel::<()>();
    let tx = std::sync::Mutex::new(tx);
    let sink: rsz_term::Sink = Arc::new(move |o| {
        if let rsz_term::Out::Frame(_) = o {
            let _ = tx.lock().unwrap().send(());
        }
    });
    let pane = rsz_term::Pane::spawn(rsz_term::PaneConfig { command: Some(vec!["/bin/cat".into()]), ..Default::default() }, sink).unwrap();
    std::thread::sleep(Duration::from_millis(500));
    let mut local = Vec::new();
    for i in 0..n {
        while rx.try_recv().is_ok() {}
        let t = Instant::now();
        pane.write(vec![b'a' + (i % 26) as u8]);
        rx.recv_timeout(Duration::from_secs(2)).unwrap();
        local.push(t.elapsed().as_secs_f64() * 1e3);
        pane.ack(u32::MAX);
        std::thread::sleep(Duration::from_millis(20));
    }
    pane.close();
    // Through a host.
    let d = Folder::new("lat");
    start_host(&d.0, 1, &["/bin/cat"]);
    let (tx, rx) = channel::<()>();
    let tx = std::sync::Mutex::new(tx);
    let c = Conn::connect(&dir::sock_path(&d.0, 1), "viewer", "test", move |e| {
        if let Event::Frame(_) = e {
            let _ = tx.lock().unwrap().send(());
        }
    })
    .unwrap();
    std::thread::sleep(Duration::from_millis(500));
    let mut kept = Vec::new();
    for i in 0..n {
        while rx.try_recv().is_ok() {}
        let t = Instant::now();
        c.input(&[b'a' + (i % 26) as u8]).unwrap();
        rx.recv_timeout(Duration::from_secs(2)).unwrap();
        kept.push(t.elapsed().as_secs_f64() * 1e3);
        c.send(&json!({ "t": "ack", "seq": u32::MAX })).unwrap();
        std::thread::sleep(Duration::from_millis(20));
    }
    let (l50, l90) = median(local);
    let (k50, k90) = median(kept);
    println!("key to frame, {n} keys: in the app p50 {l50:.2} ms p90 {l90:.2} ms; through a host p50 {k50:.2} ms p90 {k90:.2} ms");
}

/// A program that reads the clipboard with OSC 52 and prints what came
/// back: `sh ask.sh <tag>` prints `GOT-<tag>[<base64>]`.
#[cfg(unix)]
const ASK: &str = r#"stty raw -echo
printf '\033]52;c;?\007'
bel=$(printf '\007')
r=''
while :; do c=$(dd bs=1 count=1 2>/dev/null); [ "$c" = "$bel" ] && break; r="$r$c"; done
stty sane
echo "GOT-$1[${r#*;c;}]"
"#;

/// A viewer that answers the host's clipboard asks with `text`, as the
/// app does (app/src/keeper.rs, answer_clipboard).
#[cfg(unix)]
fn answering_viewer(dir: &Path, id: u32, text: &'static str) -> Conn {
    let sock = dir::sock_path(dir, id);
    let s2 = sock.clone();
    Conn::connect(&sock, "viewer", "test", move |e| {
        if let Event::Msg(v) = e
            && v["t"] == "clip_ask"
        {
            let c = Conn::connect(&s2, "control", "test", |_| {}).unwrap();
            c.send(&json!({ "t": "clip_answer", "n": v["n"], "text": text })).unwrap();
        }
    })
    .unwrap()
}

#[test]
#[cfg(unix)]
fn a_clipboard_read_gets_the_clipboard_only_while_a_window_is_attached() {
    let d = Folder::new("clip");
    let script = d.0.join("ask.sh");
    std::fs::write(&script, ASK).unwrap();
    start_host(&d.0, 5, &[SH]);
    let ctl = Conn::connect(&dir::sock_path(&d.0, 5), "control", "test", |_| {}).unwrap();
    let ask = |tag: &str| ctl.input(format!("sh {} {tag}\r", script.display()).as_bytes()).unwrap();

    // No window attached: the program gets an empty answer, not the clipboard.
    ask("none");
    wait_for("the read with no window", || screen(&d.0, 5).contains("GOT-none["));
    assert!(screen(&d.0, 5).contains("GOT-none[]"), "{}", screen(&d.0, 5));

    // A window attached: the program gets the window's clipboard text.
    let v = answering_viewer(&d.0, 5, "hello-clip 硯");
    ask("one");
    wait_for("the read with a window", || screen(&d.0, 5).contains("GOT-one["));
    // base64("hello-clip 硯")
    assert!(screen(&d.0, 5).contains("GOT-one[aGVsbG8tY2xpcCDnoa8=]"), "{}", screen(&d.0, 5));

    // The window goes: empty again.
    drop(v);
    std::thread::sleep(Duration::from_millis(300));
    ask("gone");
    wait_for("the read after the window went", || screen(&d.0, 5).contains("GOT-gone["));
    assert!(screen(&d.0, 5).contains("GOT-gone[]"), "{}", screen(&d.0, 5));
}
