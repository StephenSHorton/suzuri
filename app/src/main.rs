// rust-suzuri: the Tauri shell around rsz-term. The webview is the UI: it
// lays out panes, encodes keys and paints frames. Each pane's frames reach
// it as raw bytes over its own Channel, never as JSON events.
#![cfg_attr(all(not(debug_assertions), windows), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicU32, Ordering};

use parking_lot::Mutex;

mod ws;
use rsz_term::{Out, Pane, PaneConfig, Sink};
use serde::Serialize;
use tauri::ipc::{Channel, InvokeResponseBody};
use tauri::State;

#[derive(Default)]
struct Panes {
    next: AtomicU32,
    map: Mutex<HashMap<u32, Arc<Pane>>>,
    /// Set when RSZ_TRANSPORT=ws: frames go over the socket, not the Channel.
    bus: Option<ws::FrameBus>,
}

impl Panes {
    fn get(&self, id: u32) -> Option<Arc<Pane>> {
        self.map.lock().get(&id).cloned()
    }
}

#[derive(Serialize)]
#[serde(tag = "t")]
enum Msg<'a> {
    #[serde(rename = "title")]
    Title { title: &'a str },
    #[serde(rename = "bell")]
    Bell,
    #[serde(rename = "clip")]
    Clipboard { text: &'a str },
    #[serde(rename = "exit")]
    Exit { code: Option<u32> },
}

fn json(m: &Msg) -> InvokeResponseBody {
    InvokeResponseBody::Json(serde_json::to_string(m).unwrap_or_default())
}

#[tauri::command]
fn pane_open(
    panes: State<'_, Arc<Panes>>,
    cols: u16,
    rows: u16,
    command: Option<Vec<String>>,
    on_frame: Channel<InvokeResponseBody>,
) -> Result<u32, String> {
    let id = panes.next.fetch_add(1, Ordering::Relaxed) + 1;
    let command = command.or_else(|| {
        std::env::var("RSZ_CMD").ok().filter(|s| !s.is_empty()).map(|s| split_args(&s))
    });
    let panes_ref = Arc::clone(&panes);
    let sink: Sink = Arc::new(move |o| {
        let _ = match o {
            Out::Frame(b) => match &panes_ref.bus {
                Some(bus) => {
                    bus.send(id, b);
                    Ok(())
                }
                None => on_frame.send(InvokeResponseBody::Raw(b)),
            },
            Out::Title(t) => on_frame.send(json(&Msg::Title { title: &t })),
            Out::Bell => on_frame.send(json(&Msg::Bell)),
            Out::Clipboard(s) => on_frame.send(json(&Msg::Clipboard { text: &s })),
            Out::Exit(code) => {
                panes_ref.map.lock().remove(&id);
                on_frame.send(json(&Msg::Exit { code }))
            }
        };
    });
    let cwd = std::env::var("RSZ_CWD").ok().filter(|s| !s.is_empty());
    let mut cfg = PaneConfig { cols, rows, command, cwd, ..Default::default() };
    if let Some(n) = std::env::var("RSZ_SCROLLBACK").ok().and_then(|v| v.parse().ok()) {
        cfg.scrollback = n;
    }
    let pane = Pane::spawn(cfg, sink).map_err(|e| e.to_string())?;
    panes.map.lock().insert(id, Arc::new(pane));
    Ok(id)
}

/// Split a command line on spaces, keeping "double-quoted parts" together
/// (Windows paths with spaces).
fn split_args(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut cur = String::new();
    let mut quoted = false;
    let mut any = false;
    for c in s.chars() {
        match c {
            '"' => {
                quoted = !quoted;
                any = true;
            }
            c if c.is_whitespace() && !quoted => {
                if any {
                    out.push(std::mem::take(&mut cur));
                    any = false;
                }
            }
            c => {
                cur.push(c);
                any = true;
            }
        }
    }
    if any {
        out.push(cur);
    }
    out
}

#[tauri::command]
fn pane_write(panes: State<'_, Arc<Panes>>, id: u32, data: String) {
    if let Some(p) = panes.get(id) {
        p.write(data.into_bytes());
    }
}

#[tauri::command]
fn pane_resize(panes: State<'_, Arc<Panes>>, id: u32, cols: u16, rows: u16) {
    if let Some(p) = panes.get(id) {
        p.resize(cols, rows);
    }
}

#[tauri::command]
fn pane_scroll(panes: State<'_, Arc<Panes>>, id: u32, delta: i32) {
    if let Some(p) = panes.get(id) {
        p.scroll(delta);
    }
}

#[tauri::command]
fn pane_focus(panes: State<'_, Arc<Panes>>, id: u32, focused: bool) {
    if let Some(p) = panes.get(id) {
        p.set_focused(focused);
    }
}

#[tauri::command]
fn pane_redraw(panes: State<'_, Arc<Panes>>, id: u32) {
    if let Some(p) = panes.get(id) {
        p.redraw();
    }
}

#[tauri::command]
fn pane_close(panes: State<'_, Arc<Panes>>, id: u32) {
    if let Some(p) = panes.get(id) {
        p.close();
    }
}

#[derive(Serialize)]
struct PaneStats {
    id: u32,
    bytes_in: u64,
    feed_ms: f64,
    frames: u64,
    frame_bytes: u64,
    snapshot_ms: f64,
    writes: u64,
}

#[tauri::command]
fn stats(panes: State<'_, Arc<Panes>>) -> Vec<PaneStats> {
    let map = panes.map.lock();
    let mut out: Vec<PaneStats> = map
        .iter()
        .map(|(id, p)| {
            let s = p.stats();
            let l = |a: &std::sync::atomic::AtomicU64| a.load(Ordering::Relaxed);
            PaneStats {
                id: *id,
                bytes_in: l(&s.bytes_in),
                feed_ms: l(&s.feed_ns) as f64 / 1e6,
                frames: l(&s.frames),
                frame_bytes: l(&s.frame_bytes),
                snapshot_ms: l(&s.snapshot_ns) as f64 / 1e6,
                writes: l(&s.writes),
            }
        })
        .collect();
    out.sort_by_key(|p| p.id);
    out
}

/// Bench/dev knobs read by the UI at start (marble's seams on #255).
#[derive(Serialize)]
struct Boot {
    /// Open this many panes at start (RSZ_PANES).
    panes: u32,
    /// Run the in-app input-latency probe (RSZ_BENCH=1).
    bench: bool,
    /// Seconds the probe runs before it reports.
    bench_secs: u32,
    /// "internal": the UI types into the focused pane itself; "external"
    /// (RSZ_BENCH_PROBE=external): something outside sends real keys and
    /// the UI only measures (Windows: the bench script's SendInput).
    bench_probe: String,
    platform: &'static str,
    /// The frame socket (RSZ_TRANSPORT=ws): "port/token", or empty.
    ws: String,
    /// RSZ_TRACE: the UI logs key events too.
    trace: bool,
}

#[tauri::command]
fn boot(panes: State<'_, Arc<Panes>>) -> Boot {
    let n = |k: &str, d: u32| std::env::var(k).ok().and_then(|v| v.parse().ok()).unwrap_or(d);
    Boot {
        panes: n("RSZ_PANES", 1).clamp(1, 16),
        bench: std::env::var("RSZ_BENCH").is_ok_and(|v| v == "1"),
        bench_secs: n("RSZ_BENCH_SECS", 60),
        bench_probe: std::env::var("RSZ_BENCH_PROBE").unwrap_or_else(|_| "internal".into()),
        platform: std::env::consts::OS,
        ws: panes.bus.as_ref().map(|b| format!("{}/{}", b.port, b.token)).unwrap_or_default(),
        trace: std::env::var_os("RSZ_TRACE").is_some(),
    }
}

/// The UI's latency report: printed and, with RSZ_BENCH_OUT, written as JSON.
#[tauri::command]
fn bench_report(app: tauri::AppHandle, report: serde_json::Value, exit: bool) {
    let text = serde_json::to_string_pretty(&report).unwrap_or_default();
    println!("{text}");
    if let Ok(path) = std::env::var("RSZ_BENCH_OUT") {
        let _ = std::fs::write(path, &text);
    }
    if exit {
        app.exit(0);
    }
}

/// UI log line (errors, dev traces) to stderr.
#[tauri::command]
fn ui_log(line: String) {
    eprintln!("ui: {line}");
}

#[cfg(test)]
mod tests {
    #[test]
    fn split_args_keeps_quotes() {
        assert_eq!(super::split_args(r#""C:\Program Files\x.exe" --a "b c""#), vec![r"C:\Program Files\x.exe", "--a", "b c"]);
        assert_eq!(super::split_args("claude"), vec!["claude"]);
    }
}

fn main() {
    let bus = (std::env::var("RSZ_TRANSPORT").as_deref() == Ok("ws")).then(|| ws::FrameBus::start().expect("frame socket"));
    let panes = Arc::new(Panes { bus, ..Default::default() });
    tauri::Builder::default()
        .manage(panes)
        .invoke_handler(tauri::generate_handler![
            pane_open,
            pane_write,
            pane_resize,
            pane_scroll,
            pane_focus,
            pane_redraw,
            pane_close,
            stats,
            boot,
            bench_report,
            ui_log
        ])
        .run(tauri::generate_context!())
        .expect("run rust-suzuri");
}
