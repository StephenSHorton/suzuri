// rust-suzuri: the Tauri shell around rsz-term. The webview is the UI: it
// lays out panes, encodes keys and paints frames. Each pane's frames reach
// it as raw bytes over its own Channel, never as JSON events.
#![cfg_attr(all(not(debug_assertions), windows), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicU32, Ordering};

use parking_lot::Mutex;

mod bridge;
mod paste;
mod config;
mod mcp_client;
mod sidecar;
mod transfer;
mod transfer_engine;
mod workspace;
mod window;
mod ws;
use rsz_term::{Out, Pane, PaneConfig, Sink};
use serde::Serialize;
use tauri::ipc::{Channel, InvokeResponseBody};
use tauri::{Manager, State};

#[derive(Default)]
pub(crate) struct Panes {
    next: AtomicU32,
    pub(crate) map: Mutex<HashMap<u32, Arc<Pane>>>,
    /// Last OSC title per pane, and the focused pane (for the agent bridge).
    pub(crate) titles: Mutex<HashMap<u32, String>>,
    pub(crate) focused: AtomicU32,
    /// Set when RSZ_TRANSPORT=ws: frames go over the socket, not the Channel.
    bus: Option<ws::FrameBus>,
}

impl Panes {
    pub(crate) fn get(&self, id: u32) -> Option<Arc<Pane>> {
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
    /// The input bar's view of the shell (#285).
    #[serde(rename = "shell")]
    Shell { integrated: bool, cwd: &'a str, program: bool, prompts: u64 },
    #[serde(rename = "done")]
    CommandDone { exit: Option<i32>, command: &'a str },
}

fn json(m: &Msg) -> InvokeResponseBody {
    InvokeResponseBody::Json(serde_json::to_string(m).unwrap_or_default())
}

#[tauri::command]
fn pane_open(
    panes: State<'_, Arc<Panes>>,
    bridge: State<'_, Option<Arc<bridge::Bridge>>>,
    cols: u16,
    rows: u16,
    command: Option<Vec<String>>,
    from: Option<u32>,
    profile: Option<String>,
    on_frame: Channel<InvokeResponseBody>,
) -> Result<u32, String> {
    let id = panes.next.fetch_add(1, Ordering::Relaxed) + 1;
    let settings = config::load();
    // A Settings profile (or the default one) gives the shell and folder;
    // RSZ_CMD still wins for the bench.
    let (prof_cmd, prof_cwd) = config::profile_launch(&settings, profile.as_deref());
    let command = command
        .or_else(|| std::env::var("RSZ_CMD").ok().filter(|s| !s.is_empty()).map(|s| split_args(&s)))
        .or(prof_cmd);
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
            Out::Title(t) => {
                panes_ref.titles.lock().insert(id, t.clone());
                on_frame.send(json(&Msg::Title { title: &t }))
            }
            Out::Bell => on_frame.send(json(&Msg::Bell)),
            Out::Clipboard(s) => on_frame.send(json(&Msg::Clipboard { text: &s })),
            Out::Shell(st) => on_frame.send(json(&Msg::Shell { integrated: st.integrated, cwd: &st.cwd, program: st.program, prompts: st.prompts })),
            Out::CommandDone { exit, command } => on_frame.send(json(&Msg::CommandDone { exit, command: &command })),
            Out::Exit(code) => {
                panes_ref.map.lock().remove(&id);
                panes_ref.titles.lock().remove(&id);
                on_frame.send(json(&Msg::Exit { code }))
            }
        };
    });
    // A pane split from another starts in that pane's cwd (Go suzuri's cwd.go).
    let cwd = from
        .and_then(|f| panes.get(f))
        .and_then(|p| p.cwd())
        .filter(|d| std::path::Path::new(d).is_dir())
        .or_else(|| std::env::var("RSZ_CWD").ok().filter(|s| !s.is_empty()))
        .or(prof_cwd);
    // RSZ_NO_BAR=1: the plain shell with its own prompt, no input bar.
    let plain_shell = std::env::var_os("RSZ_NO_BAR").is_some();
    let cursor = config::cursor_code(&settings);
    let mut cfg = PaneConfig { cols, rows, command, cwd, plain_shell, cursor, ..Default::default() };
    if let Some(b) = bridge.as_ref() {
        cfg.env.extend(b.pane_env(id));
    }
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

/// The UI painted this pane's frames up to `seq` (Channel transport; the
/// socket carries its own acks).
#[tauri::command]
fn pane_ack(panes: State<'_, Arc<Panes>>, id: u32, seq: u32) {
    if let Some(p) = panes.get(id) {
        p.ack(seq);
    }
}

#[tauri::command]
fn pane_focus(panes: State<'_, Arc<Panes>>, id: u32, focused: bool) {
    if focused {
        panes.focused.store(id, Ordering::Relaxed);
    }
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

/// The settings file (config.rs), normalized.
#[tauri::command]
fn config_get() -> serde_json::Value {
    config::load()
}

/// Merge `patch` into the settings file and save it; returns the result.
/// Live parts reach running panes here (the cursor); the UI applies the rest
/// on its `rsz:config` event.
#[tauri::command]
fn config_set(panes: State<'_, Arc<Panes>>, patch: serde_json::Value) -> Result<serde_json::Value, String> {
    let v = config::update(patch).map_err(|e| e.to_string())?;
    let shape = config::cursor_code(&v);
    for p in panes.map.lock().values() {
        p.set_default_cursor(shape);
    }
    Ok(v)
}

/// What the Settings screen offers and where the file is.
#[tauri::command]
fn config_meta() -> serde_json::Value {
    serde_json::json!({
        "path": config::path().to_string_lossy(),
        "themes": config::THEMES,
        "cursors": config::CURSORS,
        "ansi_maps": config::ANSI_MAPS,
        "font_min": config::FONT_SIZE_MIN,
        "font_max": config::FONT_SIZE_MAX,
        "font_default": config::FONT_SIZE_DEFAULT,
    })
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
    /// The frame socket: "port/token", or empty with RSZ_TRANSPORT=channel.
    ws: String,
    /// RSZ_TRACE: the UI logs key events too.
    trace: bool,
    /// The home directory, shown as ~ in the input bar's cwd line.
    home: String,
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
        home: std::env::var("HOME").or_else(|_| std::env::var("USERPROFILE")).unwrap_or_default(),
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
    // Acks from the socket find their pane through this once it exists.
    static PANES: std::sync::OnceLock<Arc<Panes>> = std::sync::OnceLock::new();
    let on_ack: Arc<dyn Fn(u32, u32) + Send + Sync> = Arc::new(|id, seq| {
        if let Some(p) = PANES.get().and_then(|ps| ps.get(id)) {
            p.ack(seq);
        }
    });
    let bus = (std::env::var("RSZ_TRANSPORT").as_deref() != Ok("channel")).then(|| ws::FrameBus::start(on_ack).expect("frame socket"));
    let panes = Arc::new(Panes { bus, ..Default::default() });
    let _ = PANES.set(Arc::clone(&panes));
    // The agent bridge (bridge.json + AI control); the app runs without it.
    let bridge = match bridge::Bridge::start(Arc::clone(&panes)) {
        Ok(b) => Some(b),
        Err(e) => {
            eprintln!("bridge: not started: {e}");
            None
        }
    };
    let bridge_setup = bridge.clone();
    let bridge_exit = bridge.clone();
    #[allow(unused_mut)]
    let mut builder = tauri::Builder::default();
    #[cfg(windows)]
    if window::wants_single_instance(std::env::var(window::NEW_WINDOW_ENV).ok().as_deref(), std::env::var("RSZ_ALLOW_MULTI").ok().as_deref()) {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, _argv, _cwd| window::focus_main(app)));
    }
    builder
        .setup(move |app| {
            if let Some(b) = &bridge_setup {
                b.attach(app.handle().clone());
                app.manage(Arc::clone(b));
            }
            Ok(())
        })
        .manage(bridge)
        .manage(panes)
        .manage(Arc::new(transfer::Transfers::default()))
        .manage(Arc::new(mcp_client::McpClient::default()))
        .invoke_handler(tauri::generate_handler![
            pane_open,
            pane_write,
            pane_resize,
            pane_scroll,
            pane_focus,
            pane_ack,
            pane_redraw,
            pane_close,
            stats,
            config_get,
            config_set,
            config_meta,
            boot,
            bench_report,
            ui_log,
            paste::paste_image_save,
            transfer::transfer_send,
            transfer::transfer_receive,
            transfer::transfer_cancel,
            transfer::transfer_info,
            workspace::workspace_call,
            workspace::workspace_me,
            bridge::aicontrol_reply,
            bridge::pane_text,
            window::new_window
        ])
        .build(tauri::generate_context!())
        .expect("build rust-suzuri")
        .run(move |_, ev| {
            if let tauri::RunEvent::Exit = ev {
                if let Some(b) = &bridge_exit {
                    b.stop();
                }
            }
        });
}
