// suzuri: the Tauri shell around rsz-term. The webview is the UI: it
// lays out panes, encodes keys and paints frames. Each pane's frames reach
// it as raw bytes over its own Channel, never as JSON events.
#![cfg_attr(all(not(debug_assertions), windows), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicU32, Ordering};

use parking_lot::Mutex;

mod activity;
mod applog;
mod bridge;
mod caffeine;
mod cli;
mod notes;
mod notify;
mod complete;
mod paste;
mod config;
mod sidecar;
mod window;
mod window_state;
mod notices;
mod update;
mod ws;
use rsz_term::{Link, MouseEvent, MouseKind, Out, Pane, PaneConfig, SelectKind, Sink};
use serde::{Deserialize, Serialize};
use tauri::ipc::{Channel, InvokeResponseBody};
use tauri::{Manager, State};

#[derive(Default)]
pub(crate) struct Panes {
    next: AtomicU32,
    pub(crate) map: Mutex<HashMap<u32, Arc<Pane>>>,
    /// Last OSC title per pane, and the focused pane (for the agent bridge).
    pub(crate) titles: Mutex<HashMap<u32, String>>,
    /// Last size per pane (cols, rows), for the bridge snapshot.
    pub(crate) sizes: Mutex<HashMap<u32, (u16, u16)>>,
    /// The command each pane runs, when not the default shell: a split
    /// runs the same one (Go's tabOpts{shell: focus.shell}).
    launch: Mutex<HashMap<u32, Vec<String>>>,
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
    // Card #288. An image's bytes (base64), once, before it is placed.
    #[serde(rename = "img")]
    Image { id: u32, name: &'a str, path: Option<&'a str>, format: u32, w: u32, h: u32, z: bool, data: String },
    #[serde(rename = "imgdrop")]
    ImageDrop { ids: &'a [u32] },
    /// Images on screen: [image, row, col, cols, rows, z, inline, crop x, y, w, h].
    #[serde(rename = "imgpos")]
    Placements { list: Vec<[i64; 11]> },
    #[serde(rename = "progress")]
    Progress { state: u8, percent: u8 },
    /// The program posted a notification (ui/activity.js: it may want you).
    #[serde(rename = "notice")]
    Notified,
}

fn json(m: &Msg) -> InvokeResponseBody {
    InvokeResponseBody::Json(serde_json::to_string(m).unwrap_or_default())
}

#[tauri::command]
fn pane_open(
    panes: State<'_, Arc<Panes>>,
    bridge: State<'_, Option<Arc<bridge::Bridge>>>,
    notes: State<'_, notices::Notices>,
    app: tauri::AppHandle,
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
    let inherited = from.and_then(|f| panes.launch.lock().get(&f).cloned());
    let env_cmd = std::env::var("RSZ_CMD").ok().filter(|s| !s.is_empty()).map(|s| split_args(&s));
    let command = launch_command(command, env_cmd, profile.is_some(), inherited, prof_cmd);
    let panes_ref = Arc::clone(&panes);
    let notes = notes.inner().clone();
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
            // OSC 52: the app copies. Straight to the system clipboard, so it
            // works without a user gesture in the webview.
            Out::Clipboard(s) => {
                if clip_write(&s).is_err() {
                    let _ = on_frame.send(json(&Msg::Clipboard { text: &s }));
                }
                Ok(())
            }
            Out::Shell(st) => on_frame.send(json(&Msg::Shell { integrated: st.integrated, cwd: &st.cwd, program: st.program, prompts: st.prompts })),
            Out::CommandDone { exit, command } => on_frame.send(json(&Msg::CommandDone { exit, command: &command })),
            Out::Exit(code) => {
                panes_ref.map.lock().remove(&id);
                panes_ref.titles.lock().remove(&id);
                panes_ref.sizes.lock().remove(&id);
                panes_ref.launch.lock().remove(&id);
                on_frame.send(json(&Msg::Exit { code }))
            }
            Out::Notice(n) => {
                let _ = on_frame.send(json(&Msg::Notified));
                notice_effects(&app, notes.post(Some(id), n));
                Ok(())
            }
            Out::NoticeClose(nid) => {
                notes.close(id, &nid);
                Ok(())
            }
            Out::NoticeAlive(nid) => {
                if let Some(p) = panes_ref.get(id) {
                    p.reply(notes.alive_reply(id, &nid));
                }
                Ok(())
            }
            Out::Progress { state, percent } => on_frame.send(json(&Msg::Progress { state, percent })),
            Out::ImageData(d) => {
                use base64::Engine as _;
                let data = base64::engine::general_purpose::STANDARD.encode(&d.data);
                on_frame.send(json(&Msg::Image {
                    id: d.id,
                    name: &d.name,
                    path: d.path.as_deref(),
                    format: d.format,
                    w: d.width,
                    h: d.height,
                    z: d.compressed,
                    data,
                }))
            }
            Out::ImageDrop(ids) => on_frame.send(json(&Msg::ImageDrop { ids: &ids })),
            Out::Placements(list) => {
                let list = list
                    .iter()
                    .map(|p| {
                        let (cx, cy, cw, ch) = p.crop;
                        [p.image as i64, p.row as i64, p.col as i64, p.cols as i64, p.rows as i64, p.z as i64, p.inline as i64, cx as i64, cy as i64, cw as i64, ch as i64]
                    })
                    .collect();
                on_frame.send(json(&Msg::Placements { list }))
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
    panes.sizes.lock().insert(id, (cols, rows));
    let mut cfg = PaneConfig { cols, rows, command, cwd, plain_shell, cursor, ..Default::default() };
    if let Some(b) = bridge.as_ref() {
        cfg.env.extend(b.pane_env(id));
    }
    if let Some(n) = std::env::var("RSZ_SCROLLBACK").ok().and_then(|v| v.parse().ok()) {
        cfg.scrollback = n;
    }
    let launched = cfg.command.clone();
    let pane = Pane::spawn(cfg, sink).map_err(|e| e.to_string())?;
    panes.map.lock().insert(id, Arc::new(pane));
    if let Some(c) = launched {
        panes.launch.lock().insert(id, c);
    }
    Ok(id)
}

/// What a new pane runs: an explicit command, then RSZ_CMD (the bench), then
/// a profile the caller named, then the program of the pane it splits from,
/// then the active profile's shell. None: the login shell.
fn launch_command(
    explicit: Option<Vec<String>>,
    env: Option<Vec<String>>,
    named_profile: bool,
    inherited: Option<Vec<String>>,
    profile: Option<Vec<String>>,
) -> Option<Vec<String>> {
    let first = explicit.or(env);
    if named_profile {
        return first.or(profile);
    }
    first.or(inherited).or(profile)
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

/// Enter in the input bar (#296): a command block and the line, or a place
/// in the queue while the shell is busy. Returns {kind: "sent" | "blank" |
/// "queued", n: queue length}.
#[tauri::command]
fn pane_submit(panes: State<'_, Arc<Panes>>, id: u32, line: String) -> serde_json::Value {
    let Some(p) = panes.get(id) else { return serde_json::Value::Null };
    match p.submit(&line) {
        rsz_term::Submit::Sent => serde_json::json!({ "kind": "sent", "n": 0 }),
        rsz_term::Submit::Blank => serde_json::json!({ "kind": "blank", "n": 0 }),
        rsz_term::Submit::Queued(n) => serde_json::json!({ "kind": "queued", "n": n }),
        rsz_term::Submit::Busy => serde_json::json!({ "kind": "busy", "n": 0 }),
    }
}

/// Run the pane's next queued line if the shell is idle: how many are left,
/// or null when nothing ran (busy, or the queue is empty).
#[tauri::command]
fn pane_queue_flush(panes: State<'_, Arc<Panes>>, id: u32) -> Option<usize> {
    panes.get(id)?.flush_queue()
}

/// Drop the pane's queued lines; how many there were.
#[tauri::command]
fn pane_queue_clear(panes: State<'_, Arc<Panes>>, id: u32) -> usize {
    panes.get(id).map_or(0, |p| p.clear_queue())
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
        panes.sizes.lock().insert(id, (cols, rows));
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

/// The UI's cell size in pixels (inline images are sized in cells).
#[tauri::command]
fn pane_cell_px(panes: State<'_, Arc<Panes>>, id: u32, w: u16, h: u16) {
    if let Some(p) = panes.get(id) {
        p.set_cell_px(w, h);
    }
}

// Card #288: the notice panel (ui/notice.html) and the history.
#[tauri::command]
fn notice_click(app: tauri::AppHandle, notes: State<'_, notices::Notices>, key: u64) {
    notice_effects(&app, notes.activate(key));
}

/// The panel page loaded: send it the cards (see Notices::resync).
#[tauri::command]
fn notice_sync(notes: State<'_, notices::Notices>) {
    notes.resync();
}

#[tauri::command]
fn notice_dismiss(app: tauri::AppHandle, notes: State<'_, notices::Notices>, key: u64) {
    notice_effects(&app, notes.dismiss(key));
}

#[tauri::command]
fn notice_hover(notes: State<'_, notices::Notices>, hover: bool) {
    notes.set_hover(hover);
}

#[tauri::command]
fn notice_anchor(notes: State<'_, notices::Notices>, anchor: String) {
    notes.set_anchor(&anchor);
}

/// A host message from the UI as a notice card (Notices::toast).
#[tauri::command]
fn notice_toast(notes: State<'_, notices::Notices>, text: String) {
    notes.toast(&text);
}

#[derive(Serialize)]
struct NoticeHistory {
    items: Vec<notices::Played>,
    unread: usize,
}

#[tauri::command]
fn notice_history(notes: State<'_, notices::Notices>) -> NoticeHistory {
    let (items, unread) = notes.history();
    NoticeHistory { items, unread }
}

#[tauri::command]
fn notice_read(notes: State<'_, notices::Notices>) {
    notes.mark_read();
}

/// Carry out what a notice click or close asks: write back to the pane, or
/// bring the window up on the pane.
pub fn notice_effects(app: &tauri::AppHandle, effects: Vec<notices::Effect>) {
    use tauri::{Emitter, Manager};
    for e in effects {
        match e {
            notices::Effect::Reply(pane, bytes) => {
                if let Some(p) = app.try_state::<Arc<Panes>>().and_then(|ps| ps.get(pane)) {
                    p.reply(bytes);
                }
            }
            notices::Effect::Focus(pane) => {
                if let Some(w) = app.get_webview_window("main") {
                    let _ = w.show();
                    let _ = w.unminimize();
                    let _ = w.set_focus();
                }
                if let Some(pane) = pane {
                    let _ = app.emit_to("main", "notice-focus", pane);
                }
            }
        }
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
fn config_set(panes: State<'_, Arc<Panes>>, notes: State<'_, notices::Notices>, patch: serde_json::Value) -> Result<serde_json::Value, String> {
    let v = config::update(patch.clone()).map_err(|e| e.to_string())?;
    let shape = config::cursor_code(&v);
    for p in panes.map.lock().values() {
        p.set_default_cursor(shape);
    }
    apply_theme(&v, Some(&panes));
    notes.apply_config(&v, &patch);
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
        "fonts": config::mono_font_faces(),
        "notice_positions": config::NOTICE_POSITIONS,
        "notice_labels": config::NOTICE_POSITIONS.iter().zip(config::NOTICE_POSITION_LABELS).collect::<HashMap<_, _>>(),
    })
}

fn clip_write(text: &str) -> Result<(), String> {
    arboard::Clipboard::new().and_then(|mut c| c.set_text(text.to_owned())).map_err(|e| e.to_string())
}

/// Selection from the UI's pointer: start (cell, word or line), extend
/// (drag) or clear. Cells are of the visible screen.
#[tauri::command]
fn pane_select(panes: State<'_, Arc<Panes>>, id: u32, op: String, col: u16, row: u16, right: bool) {
    let Some(p) = panes.get(id) else { return };
    match op.as_str() {
        "cell" => p.select_start(col, row, right, SelectKind::Cell),
        "word" => p.select_start(col, row, right, SelectKind::Word),
        "line" => p.select_start(col, row, right, SelectKind::Line),
        "extend" => p.select_extend(col, row, right),
        _ => p.select_clear(),
    }
}

/// Copy the pane's selection to the clipboard. Returns whether there was
/// one.
#[tauri::command]
fn pane_copy(panes: State<'_, Arc<Panes>>, id: u32) -> Result<bool, String> {
    let Some(text) = panes.get(id).and_then(|p| p.selection_text()) else { return Ok(false) };
    clip_write(&text)?;
    Ok(true)
}

/// The clipboard's text, for the input bar (which edits it before the
/// shell sees it).
#[tauri::command]
fn clipboard_text() -> Result<String, String> {
    arboard::Clipboard::new().and_then(|mut c| c.get_text()).map_err(|e| e.to_string())
}

/// The clipboard's RGBA image as a PNG.
fn png_bytes(img: &arboard::ImageData) -> Option<Vec<u8>> {
    let mut out = Vec::new();
    let mut enc = png::Encoder::new(&mut out, img.width as u32, img.height as u32);
    enc.set_color(png::ColorType::Rgba);
    enc.set_depth(png::BitDepth::Eight);
    enc.write_header().ok()?.write_image_data(&img.bytes).ok()?;
    Some(out)
}

/// Paste the clipboard's text into the pane, bracketed when the app asked.
#[tauri::command]
fn pane_paste(panes: State<'_, Arc<Panes>>, id: u32, text: Option<String>) -> Result<(), String> {
    let Some(p) = panes.get(id) else { return Ok(()) };
    let text = match text {
        Some(t) => t,
        None => {
            let mut clip = arboard::Clipboard::new().map_err(|e| e.to_string())?;
            // A full-screen app (Claude Code, Grok) gets a copied image as a
            // file path it can attach, ahead of any text (Go suzuri).
            match p.alt_screen().then(|| clip.get_image().ok()).flatten().and_then(|img| png_bytes(&img)) {
                Some(png) => paste::save_image(&std::env::temp_dir(), &png)?.to_string_lossy().into_owned(),
                None => clip.get_text().map_err(|e| e.to_string())?,
            }
        }
    };
    if !text.is_empty() {
        p.paste(&text);
    }
    Ok(())
}

#[derive(Deserialize)]
struct MouseIn {
    /// "press", "release", "move", "wheelup", "wheeldown"
    kind: String,
    button: u8,
    /// Motion with a button held: which one.
    held: Option<u8>,
    col: u16,
    row: u16,
    shift: bool,
    alt: bool,
    ctrl: bool,
}

/// A pointer event for the app's mouse modes. Returns whether the app took
/// it; when not, the UI selects or scrolls instead.
#[tauri::command]
fn pane_mouse(panes: State<'_, Arc<Panes>>, id: u32, ev: MouseIn) -> bool {
    let kind = match ev.kind.as_str() {
        "press" => MouseKind::Press,
        "release" => MouseKind::Release,
        "move" => MouseKind::Motion(ev.held),
        "wheelup" => MouseKind::WheelUp,
        "wheeldown" => MouseKind::WheelDown,
        _ => return false,
    };
    let me = MouseEvent { kind, button: ev.button, col: ev.col, row: ev.row, shift: ev.shift, alt: ev.alt, ctrl: ev.ctrl };
    panes.get(id).is_some_and(|p| p.mouse(me))
}

#[derive(Serialize)]
struct LinkOut {
    url: String,
    row: u16,
    x0: u16,
    x1: u16,
}

/// The link (OSC 8 or a URL in the text) at a visible cell, if any.
#[tauri::command]
fn pane_link(panes: State<'_, Arc<Panes>>, id: u32, col: u16, row: u16) -> Option<LinkOut> {
    let Link { url, row, x0, x1, .. } = panes.get(id)?.link_at(col, row)?;
    Some(LinkOut { url, row, x0, x1 })
}

/// Debug builds: the pane's screen and selection, for ui/selftest.js.
#[tauri::command]
fn debug_pane(panes: State<'_, Arc<Panes>>, id: u32) -> Option<(String, Option<String>)> {
    if !cfg!(debug_assertions) {
        return None;
    }
    let p = panes.get(id)?;
    Some((p.screen_text(), p.selection_text()))
}

/// macOS: ⌘C and ⌘V are the Edit menu's Copy and Paste, which act on the
/// webview's own (empty) selection and text field, while the terminal's
/// selection lives in the core. Swap both predefined items for ones that ask
/// the UI to copy the pane's selection or paste into the pane.
#[cfg(target_os = "macos")]
fn install_menu(app: &tauri::App) -> tauri::Result<()> {
    use tauri::menu::{Menu, MenuItem, MenuItemKind};
    let menu = Menu::default(app.handle())?;
    for item in menu.items()? {
        let MenuItemKind::Submenu(sub) = item else { continue };
        for (i, it) in sub.items()?.into_iter().enumerate() {
            if let MenuItemKind::Predefined(p) = &it {
                let (id, key) = match p.text()?.as_str() {
                    "Copy" => ("rsz-copy", "CmdOrCtrl+C"),
                    "Paste" => ("rsz-paste", "CmdOrCtrl+V"),
                    _ => continue,
                };
                let ours = MenuItem::with_id(app.handle(), id, p.text()?, true, Some(key))?;
                sub.remove(p)?;
                sub.insert(&ours, i)?;
            }
        }
    }
    app.set_menu(menu)?;
    Ok(())
}

/// Open a link with the system: web, mail and file:// links only. A file
/// link to an app, script or other executable is revealed in the file
/// manager rather than run; the reply then says so for the toast.
#[tauri::command]
fn open_url(url: String) -> Result<Option<String>, String> {
    if !rsz_term::input::openable(&url) {
        return Err(format!("not opening {url}: only http, https, mailto and file links open"));
    }
    if url.get(..7).is_some_and(|p| p.eq_ignore_ascii_case("file://")) {
        // Local paths only: another host would be a network share.
        let Some(path) = rsz_term::input::file_url_path(&url) else {
            return Err(format!("not opening {url}: not a local file"));
        };
        let real = std::fs::canonicalize(&path).map_err(|e| format!("{path}: {e}"))?;
        #[cfg(windows)]
        let real = std::path::PathBuf::from(real.to_string_lossy().trim_start_matches(r"\\?\").to_string());
        if runs_when_opened(&path, &real) {
            reveal(&real)?;
            return Ok(Some(format!("revealed {}, not run", real.display())));
        }
        system_open(&real.to_string_lossy())?;
        return Ok(Some(format!("opened {}", real.display())));
    }
    system_open(&url)?;
    Ok(None)
}

/// Whether opening a local file would launch something: an executable
/// extension on the link or its symlink target, a Mac bundle directory, or
/// a file with an exec bit.
fn runs_when_opened(link: &str, real: &std::path::Path) -> bool {
    use rsz_term::input::launches;
    if launches(link) || launches(&real.to_string_lossy()) {
        return true;
    }
    let Ok(meta) = std::fs::metadata(real) else { return true };
    if meta.is_dir() {
        // Packages (.app, .xpc, ...) are directories with an extension.
        return cfg!(target_os = "macos") && real.extension().is_some();
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if meta.permissions().mode() & 0o111 != 0 {
            return true;
        }
    }
    false
}

/// Show a file selected in Finder / Explorer instead of opening it.
fn reveal(path: &std::path::Path) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    let mut cmd = {
        let mut c = std::process::Command::new("open");
        c.arg("-R").arg(path);
        c
    };
    #[cfg(windows)]
    let mut cmd = {
        let mut c = std::process::Command::new("explorer");
        c.arg(format!("/select,{}", path.display()));
        c
    };
    #[cfg(not(any(target_os = "macos", windows)))]
    let mut cmd = {
        let mut c = std::process::Command::new("xdg-open");
        c.arg(path.parent().unwrap_or(path));
        c
    };
    cmd.spawn().map(|_| ()).map_err(|e| e.to_string())
}

fn system_open(target: &str) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    let mut cmd = std::process::Command::new("open");
    #[cfg(windows)]
    let mut cmd = {
        // ShellExecute through url.dll: no cmd.exe, so & and ^ in the URL
        // are not shell syntax.
        let mut c = std::process::Command::new("rundll32");
        c.arg("url.dll,FileProtocolHandler");
        c
    };
    #[cfg(not(any(target_os = "macos", windows)))]
    let mut cmd = std::process::Command::new("xdg-open");
    cmd.arg(target).spawn().map(|_| ()).map_err(|e| e.to_string())
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
    /// Debug builds: RSZ_UI_TEST names a ui/selftest.js scenario to run.
    ui_test: String,
    /// "main" (default): the UI thread paints in requestAnimationFrame.
    /// RSZ_PAINT=worker: an OffscreenCanvas worker paints. It measured no
    /// faster on the Mac or Windows and costs 65-100 MB (#289), so it is
    /// opt-in.
    paint: String,
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
        ui_test: if cfg!(debug_assertions) { std::env::var("RSZ_UI_TEST").unwrap_or_default() } else { String::new() },
        paint: std::env::var("RSZ_PAINT").unwrap_or_else(|_| "main".into()),
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

// ---- appearance: Go suzuri's themes, ANSI map and font settings.

/// Set the palette from the settings' theme and ANSI map; with panes, every
/// pane repaints in the new colours.
fn apply_theme(cfg: &serde_json::Value, panes: Option<&Panes>) {
    let s = |k: &str| cfg.get(k).and_then(|v| v.as_str()).unwrap_or("").to_string();
    let map = rsz_term::palette::AnsiMap::parse(&s("shell_ansi_map")).unwrap_or(rsz_term::palette::AnsiMap::Soft);
    let before = rsz_term::palette::active();
    let now = rsz_term::palette::set_active(&s("theme"), map);
    if let Some(panes) = panes {
        if !std::ptr::eq(before.theme, now.theme) || before.map != now.map {
            for p in panes.map.lock().values() {
                p.redraw();
            }
        }
    }
}

fn hex((r, g, b): rsz_term::palette::Rgb) -> String {
    format!("#{r:02x}{g:02x}{b:02x}")
}

#[derive(Serialize)]
struct ThemeOut {
    id: &'static str,
    label: &'static str,
    /// Chrome roles, as CSS colours.
    chrome: HashMap<&'static str, String>,
}

fn theme_out(t: &rsz_term::palette::ThemeSpec) -> ThemeOut {
    let c = &t.chrome;
    let chrome = HashMap::from([
        ("void", hex(c.void)),
        ("bar", hex(c.bar)),
        ("panel", hex(c.panel)),
        ("primary", hex(c.primary)),
        ("secondary", hex(c.secondary)),
        ("on-primary", hex(c.on_primary)),
        ("text", hex(c.text)),
        ("soft", hex(c.soft)),
        ("dim", hex(c.dim)),
        ("mute", hex(c.mute)),
        ("sel", hex(c.sel)),
        ("match", hex(c.match_)),
        ("cyan", hex(c.cyan)),
        ("border", hex(c.border)),
        ("dim-matte", hex(c.dim_matte)),
        ("term-bg", hex(rsz_term::palette::term_bg(c))),
    ]);
    ThemeOut { id: t.id, label: t.label, chrome }
}

/// The theme catalogue (chrome roles as CSS colours) for ui/appearance.js;
/// the chosen theme and font come from config_get.
#[tauri::command]
fn appearance() -> serde_json::Value {
    serde_json::json!({
        "themes": rsz_term::palette::THEMES.iter().map(theme_out).collect::<Vec<_>>(),
        "default_font_face": config::FONT_FACE_DEFAULT,
        "default_font_size_px": config::FONT_SIZE_DEFAULT,
    })
}

/// UI log line (errors, dev traces) to the app log.
#[tauri::command]
fn ui_log(line: String) {
    log::info!(target: "ui", "{line}");
}

#[cfg(test)]
mod tests {
    #[test]
    fn a_split_runs_the_program_it_split_from() {
        let v = |s: &str| Some(vec![s.to_string()]);
        // A split (no profile named): the source pane's program beats the active profile's.
        assert_eq!(super::launch_command(None, None, false, v("pwsh"), v("zsh")), v("pwsh"));
        // A source on the login shell: the active profile's, as before.
        assert_eq!(super::launch_command(None, None, false, None, v("zsh")), v("zsh"));
        // A named profile (New tab: X) wins over what it split from.
        assert_eq!(super::launch_command(None, None, true, v("pwsh"), v("fish")), v("fish"));
        assert_eq!(super::launch_command(None, None, true, v("pwsh"), None), None);
        // An explicit command and RSZ_CMD win over both.
        assert_eq!(super::launch_command(v("top"), v("bench"), false, v("pwsh"), None), v("top"));
        assert_eq!(super::launch_command(None, v("bench"), false, v("pwsh"), None), v("bench"));
    }

    #[test]
    fn split_args_keeps_quotes() {
        assert_eq!(super::split_args(r#""C:\Program Files\x.exe" --a "b c""#), vec![r"C:\Program Files\x.exe", "--a", "b c"]);
        assert_eq!(super::split_args("claude"), vec!["claude"]);
    }
}

/// Go suzuri subcommands this app does not serve (#330).
const GO_ONLY_SUBCOMMANDS: &[&str] = &["mcp", "workspace-sync", "guest", "chrome"];

fn main() {
    let ctx = tauri::generate_context!();
    let args: Vec<String> = std::env::args().collect();
    if cli::wants_console(&args) {
        cli::attach_parent_console();
    }
    if cli::wants_version(&args) {
        println!("{}", ctx.package_info().version);
        return;
    }
    if notify::wants(&args) {
        std::process::exit(notify::run(&args[2..], ctx));
    }
    // Go suzuri's other subcommands aren't served here (#330: the workspace
    // and its `suzuri mcp` server were dropped). Say so and exit instead of
    // opening a window for a caller that expects a stdio tool.
    if let Some(sub) = args.get(1).filter(|a| GO_ONLY_SUBCOMMANDS.contains(&a.as_str())) {
        eprintln!("suzuri {sub}: not available in this version of suzuri");
        std::process::exit(2);
    }
    // suzuri.log and the panic hook, before any thread exists.
    applog::init();
    // Panes must not inherit the control API of the suzuri this was started
    // from (a dev build run inside Go suzuri): drop those variables first,
    // before any thread exists. The bridge sets its own per pane.
    for k in bridge::INHERITED {
        // SAFETY: single-threaded here, at the very start of main.
        unsafe { std::env::remove_var(k) };
    }
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
    apply_theme(&config::load(), None);
    // OSC 52 "?" reads get the clipboard's text, as Go suzuri answered them.
    rsz_term::set_clipboard_reader(|| arboard::Clipboard::new().and_then(|mut c| c.get_text()).ok());
    // The agent bridge (bridge.json + AI control); the app runs without it.
    let bridge = match bridge::Bridge::start(Arc::clone(&panes)) {
        Ok(b) => Some(b),
        Err(e) => {
            log::warn!("bridge: not started: {e}");
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
        .plugin(tauri_plugin_updater::Builder::new().build())
        .setup(move |app| {
            window_state::install(app.handle());
            app.manage(notices::Notices::new(app.handle().clone()));
            if let Some(crash) = applog::take_crash(&applog::dir()) {
                log::info!("last run crashed: {}", crash.display());
                let _ = app.state::<notices::Notices>().post(None, applog::crash_notice(&crash));
            }
            update::start(app.handle());
            caffeine::install(app.handle(), &app.state::<Arc<caffeine::Caffeine>>());
            if let Some(b) = &bridge_setup {
                b.attach(app.handle().clone());
                app.manage(Arc::clone(b));
            }
            #[cfg(target_os = "macos")]
            install_menu(app)?;
            Ok(())
        })
        .on_menu_event(|app, ev| {
            if ev.id() == "rsz-copy" || ev.id() == "rsz-paste" {
                use tauri::Emitter;
                let _ = app.emit(ev.id().as_ref(), ());
            }
        })
        .manage(bridge)
        .manage(panes)
        .manage(update::Pending::default())
        .manage(caffeine::Caffeine::new())
        .invoke_handler(tauri::generate_handler![
            pane_open,
            pane_write,
            activity::pane_activity,
            activity::activity_notice,
            pane_submit,
            pane_queue_flush,
            pane_queue_clear,
            pane_resize,
            pane_scroll,
            pane_focus,
            pane_ack,
            pane_redraw,
            pane_close,
            pane_select,
            pane_copy,
            pane_paste,
            clipboard_text,
            pane_mouse,
            pane_link,
            open_url,
            debug_pane,
            pane_cell_px,
            notice_click,
            notice_dismiss,
            notice_sync,
            notice_hover,
            notice_anchor,
            notice_toast,
            notice_history,
            notice_read,
            stats,
            config_get,
            config_set,
            config_meta,
            boot,
            bench_report,
            ui_log,
            paste::paste_image_save,
            bridge::aicontrol_reply,
            complete::complete_list,
            bridge::pane_lines,
            window::new_window,
            window::app_quit,
            caffeine::caffeine_on,
            caffeine::caffeine_off,
            caffeine::caffeine_toggle,
            caffeine::caffeine_status,
            appearance,
            notes::notes_load,
            notes::notes_save,
            update::update_check,
            update::update_install
        ])
        .build(ctx)
        .expect("build suzuri")
        .run(move |_app, ev| {
            if let tauri::RunEvent::Exit = ev {
                window_state::flush();
                if let Some(b) = &bridge_exit {
                    b.stop();
                }
            }
        });
}
