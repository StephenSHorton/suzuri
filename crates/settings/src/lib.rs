//! Suzuri's terminal settings (font, cursor, look, shell profiles and the
//! rest), for any program: their names, defaults and limits, reading and
//! saving the shared file, and a watch that says when another program
//! changed it.
//!
//! The file is one JSON object with Go suzuri's config.json keys
//! (internal/config/config.go), in Suzuri's folder under the OS config dir
//! (Windows %LOCALAPPDATA%\suzuri, macOS ~/Library/Application
//! Support/suzuri, Linux ~/.config/suzuri). Every program using this crate
//! with [`Store::shared`] reads and writes that one file, so a change made in
//! one shows in the others. Keys a program does not know are kept as they
//! are, and a save writes only the keys it changed, so an older copy of this
//! crate never resets a newer program's values. RSZ_CONFIG_DIR overrides the
//! folder (tests, screenshots); there, Go's file is looked for in a
//! go-suzuri subfolder and copied on first run.

use std::path::{Path, PathBuf};
use std::sync::mpsc::{self, RecvTimeoutError};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, SystemTime};

use serde_json::{Map, Value, json};

#[cfg(test)]
mod tests;

pub const FONT_SIZE_DEFAULT: i64 = 14;
pub const FONT_SIZE_MIN: i64 = 10;
pub const FONT_SIZE_MAX: i64 = 36;
pub const FONT_FACE_DEFAULT: &str = "GohuFont uni14 Nerd Font Mono";

/// The look: one of Astryx's seven themes (npm @astryxdesign/theme-<id>),
/// for the whole window, the terminal's colours and the notice cards. The
/// first is the default.
pub const UI_THEMES: &[&str] = &["neutral", "stone", "matcha", "chocolate", "butter", "gothic", "y2k"];
/// The Settings labels, in the same order.
pub const UI_THEME_LABELS: &[&str] = &["Neutral", "Stone", "Matcha", "Chocolate", "Butter", "Gothic", "Y2K"];
/// Its mode; "system" follows the OS. The first is the default.
pub const UI_MODES: &[&str] = &["dark", "light", "system"];
pub const UI_MODE_LABELS: &[&str] = &["Dark", "Light", "System"];
/// Go suzuri's terminal theme and ANSI map, replaced by ui_theme: dropped
/// from the file on load.
const RETIRED_KEYS: &[&str] = &["theme", "shell_ansi_map"];
/// Go's MonoFontFaces: the settings list, bundled face first.
pub fn mono_font_faces() -> Vec<&'static str> {
    if cfg!(target_os = "macos") {
        vec![FONT_FACE_DEFAULT, "Menlo", "SF Mono", "Monaco", "JetBrains Mono", "Fira Code", "Source Code Pro", "Courier New"]
    } else {
        vec![
            FONT_FACE_DEFAULT,
            "Cascadia Mono",
            "Cascadia Code",
            "Consolas",
            "Lucida Console",
            "Courier New",
            "JetBrains Mono",
            "Fira Code",
            "Source Code Pro",
        ]
    }
}

/// Cursor shapes; the first is the default.
pub const CURSORS: &[&str] = &["block", "underline", "bar"];

/// The UI's text size (Settings › Look), in Astryx's text-size categories
/// (iOS Dynamic Type's standard sizes). Only the window chrome's text
/// follows it; the terminal keeps font_size_px.
pub const TEXT_SIZES: &[&str] = &["xs", "s", "m", "l", "xl", "xxl", "xxxl"];
/// The Settings labels, in the same order. "l" is Astryx's own default.
pub const TEXT_SIZE_LABELS: &[&str] = &["Smallest", "Smaller", "Small", "Standard", "Large", "Larger", "Largest"];
/// Suzuri's default: one step above Astryx's, so the chrome's text isn't
/// small against its controls.
pub const TEXT_SIZE_DEFAULT: &str = "xl";

/// Where notice cards sit: the four screen corners rust-toast stacks cards
/// in (toast::CORNER_*). The first is the default.
pub const NOTICE_POSITIONS: &[&str] = &["bottom-left", "bottom-right", "top-left", "top-right"];
/// The Settings labels, in the same order.
pub const NOTICE_POSITION_LABELS: &[&str] = &["Bottom left", "Bottom right", "Top left", "Top right"];

/// Go's nine anchors (and anything else) to the nearest corner: the middle
/// row goes to the bottom, the center column to the right (a tie either
/// way; rust-toast's own default side is the right).
fn notice_corner(p: &str) -> &'static str {
    match p {
        "bottom-left" | "center-left" => "bottom-left",
        "bottom-right" | "bottom-center" | "center-right" | "center" => "bottom-right",
        "top-left" => "top-left",
        "top-right" | "top-center" => "top-right",
        _ => NOTICE_POSITIONS[0],
    }
}

/// What a settings screen offers: every choice with its label, and the
/// limits, so each program's own screen offers the same as Suzuri's.
/// `path` is the shared file.
pub fn meta() -> Value {
    let labels = |ids: &[&str], labels: &[&str]| -> Value {
        Value::Object(ids.iter().zip(labels).map(|(i, l)| ((*i).to_string(), Value::from(*l))).collect())
    };
    json!({
        "path": path().to_string_lossy(),
        "ui_themes": UI_THEMES,
        "ui_theme_labels": labels(UI_THEMES, UI_THEME_LABELS),
        "ui_modes": UI_MODES,
        "ui_mode_labels": labels(UI_MODES, UI_MODE_LABELS),
        "cursors": CURSORS,
        "font_min": FONT_SIZE_MIN,
        "font_max": FONT_SIZE_MAX,
        "font_default": FONT_SIZE_DEFAULT,
        "fonts": mono_font_faces(),
        "notice_positions": NOTICE_POSITIONS,
        "notice_labels": labels(NOTICE_POSITIONS, NOTICE_POSITION_LABELS),
        "text_sizes": TEXT_SIZES,
        "text_size_labels": labels(TEXT_SIZES, TEXT_SIZE_LABELS),
    })
}

// ---- Where the file is.

/// The folder settings live in, named `name` under the OS config dir.
fn dir_named(name: &str) -> PathBuf {
    if let Some(d) = std::env::var_os("RSZ_CONFIG_DIR").filter(|d| !d.is_empty()) {
        let d = PathBuf::from(d);
        return if name == "suzuri" { d.join("go-suzuri") } else { d };
    }
    // Same order as Go suzuri's config.Dir.
    if let Some(base) = std::env::var_os("LOCALAPPDATA").filter(|d| !d.is_empty()) {
        return PathBuf::from(base).join(name);
    }
    let home = std::env::var_os("HOME").map(PathBuf::from);
    if cfg!(target_os = "macos") {
        if let Some(h) = home {
            return h.join("Library/Application Support").join(name);
        }
    } else if let Some(x) = std::env::var_os("XDG_CONFIG_HOME").filter(|d| !d.is_empty()) {
        return PathBuf::from(x).join(name);
    } else if let Some(h) = home {
        return h.join(".config").join(name);
    }
    std::env::temp_dir().join(name)
}

/// The shared settings file: Suzuri's config.json.
pub fn path() -> PathBuf {
    if let Some(d) = std::env::var_os("RSZ_CONFIG_DIR").filter(|d| !d.is_empty()) {
        return PathBuf::from(d).join("config.json");
    }
    dir_named("suzuri").join("config.json")
}

/// Go suzuri's config.json, imported on first run. Outside RSZ_CONFIG_DIR
/// this is [`path`] itself, so there is nothing to import.
pub fn go_path() -> PathBuf {
    dir_named("suzuri").join("config.json")
}

// ---- Defaults and limits.

pub fn default_profiles() -> Value {
    if cfg!(windows) {
        json!([
            {"name": "Default"},
            {"name": "PowerShell", "shell": "powershell.exe -NoLogo -NoProfile"},
            {"name": "Cmd", "shell": "cmd.exe"},
        ])
    } else {
        json!([
            {"name": "Default"},
            {"name": "Zsh", "shell": "/bin/zsh"},
            {"name": "Bash", "shell": "/bin/bash"},
        ])
    }
}

/// Fill defaults and clamp known keys, as Go's config.Normalize does.
/// Unknown keys pass through untouched.
pub fn normalize(v: Value) -> Value {
    let mut m = match v {
        Value::Object(m) => m,
        _ => Map::new(),
    };
    let s = |m: &Map<String, Value>, k: &str| m.get(k).and_then(Value::as_str).map(|s| s.trim().to_lowercase());

    let face = m.get("font_face").and_then(Value::as_str).map(str::trim).filter(|f| !f.is_empty());
    let face = face.unwrap_or(FONT_FACE_DEFAULT).to_string();
    m.insert("font_face".into(), face.into());

    let size = m.get("font_size_px").and_then(Value::as_i64).unwrap_or(FONT_SIZE_DEFAULT);
    m.insert("font_size_px".into(), size.clamp(FONT_SIZE_MIN, FONT_SIZE_MAX).into());

    let cursor = s(&m, "cursor").filter(|c| CURSORS.contains(&c.as_str())).unwrap_or_else(|| CURSORS[0].into());
    m.insert("cursor".into(), cursor.into());

    // The old 16 terminal themes are gone; whoever had one starts on the
    // default look (Neutral, dark).
    for k in RETIRED_KEYS {
        m.remove(*k);
    }
    let theme = s(&m, "ui_theme").filter(|t| UI_THEMES.contains(&t.as_str())).unwrap_or_else(|| UI_THEMES[0].into());
    m.insert("ui_theme".into(), theme.into());
    let mode = s(&m, "ui_mode").filter(|t| UI_MODES.contains(&t.as_str())).unwrap_or_else(|| UI_MODES[0].into());
    m.insert("ui_mode".into(), mode.into());

    // Go's Bg anim: keep animating while another app has focus.
    let anim = m.get("animate_unfocused").and_then(Value::as_bool).unwrap_or(true);
    m.insert("animate_unfocused".into(), anim.into());

    // Terminals back after a reboot: on unless turned off.
    let restore = m.get("restore_sessions").and_then(Value::as_bool).unwrap_or(true);
    m.insert("restore_sessions".into(), restore.into());

    let pos = notice_corner(s(&m, "notice_position").as_deref().unwrap_or(""));
    m.insert("notice_position".into(), pos.into());

    let size = s(&m, "ui_text_size").unwrap_or_default();
    let size = TEXT_SIZES.iter().find(|t| **t == size).copied().unwrap_or(TEXT_SIZE_DEFAULT);
    m.insert("ui_text_size".into(), size.into());

    // Profiles: unique, non-empty names (case-insensitive), never empty.
    let mut seen = std::collections::HashSet::new();
    let mut profiles: Vec<Value> = m
        .get("profiles")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|p| {
            let mut p = p.as_object()?.clone();
            let name = p.get("name").and_then(Value::as_str)?.trim().to_string();
            if name.is_empty() || !seen.insert(name.to_lowercase()) {
                return None;
            }
            p.insert("name".into(), name.into());
            Some(Value::Object(p))
        })
        .collect();
    if profiles.is_empty() {
        profiles = default_profiles().as_array().cloned().unwrap_or_default();
    }
    let names: Vec<String> = profiles.iter().filter_map(|p| p["name"].as_str().map(str::to_string)).collect();
    let active = m.get("active_profile").and_then(Value::as_str).unwrap_or("");
    let active = names.iter().find(|n| n.eq_ignore_ascii_case(active)).unwrap_or(&names[0]).clone();
    m.insert("profiles".into(), Value::Array(profiles));
    m.insert("active_profile".into(), active.into());
    Value::Object(m)
}

// ---- Reading and saving, by path (no locks; [`Store`] adds them).

/// The file as written, if it is a JSON object.
fn read_raw(p: &Path) -> Option<Map<String, Value>> {
    let b = std::fs::read(p).ok()?;
    match serde_json::from_slice::<Value>(&b).ok()? {
        Value::Object(m) => Some(m),
        _ => None,
    }
}

/// Read the settings at `path`, normalized, importing `go` (Go suzuri's
/// file) when `path` does not exist yet. A missing or broken file gives the
/// defaults (a broken one is left on disk as is).
pub fn load_from(path: &Path, go: &Path) -> Value {
    if let Some(m) = read_raw(path) {
        return normalize(Value::Object(m));
    }
    if !path.exists()
        && let Some(m) = read_raw(go)
    {
        let v = normalize(Value::Object(m));
        let _ = save_to(path, &v);
        return v;
    }
    normalize(Value::Object(Map::new()))
}

/// Merge `patch`'s keys into the file at `path` and save it. Returns the
/// whole settings, normalized. Only the patched keys are written (each as
/// normalized, profiles and active_profile together); every other key stays
/// as the file had it, values this crate does not know included. Not locked:
/// see [`Store::update`].
pub fn update_at(path: &Path, go: &Path, patch: Value) -> std::io::Result<Value> {
    let _ = load_from(path, go); // the Go import, on first run
    let mut raw = read_raw(path).unwrap_or_default();
    let Value::Object(patch) = patch else {
        return Ok(normalize(Value::Object(raw)));
    };
    let mut keys: Vec<String> = patch.keys().cloned().collect();
    if keys.iter().any(|k| k == "profiles" || k == "active_profile") {
        keys.extend(["profiles".into(), "active_profile".into()]);
    }
    raw.extend(patch);
    let v = normalize(Value::Object(raw.clone()));
    for k in &keys {
        match v.get(k) {
            Some(x) => raw.insert(k.clone(), x.clone()),
            None => raw.remove(k),
        };
    }
    for k in RETIRED_KEYS {
        raw.remove(*k);
    }
    save_to(path, &Value::Object(raw))?;
    Ok(v)
}

/// An OS lock on `config.json.lock` next to the file, held until dropped.
/// None if it can't be had; the write goes ahead unlocked then. Every
/// program (and Suzuri before this crate) takes this same lock to save.
pub fn lock_file(path: &Path) -> Option<std::fs::File> {
    if let Some(d) = path.parent() {
        std::fs::create_dir_all(d).ok()?;
    }
    let f = std::fs::OpenOptions::new()
        .create(true)
        .truncate(false)
        .write(true)
        .open(path.with_extension("json.lock"))
        .ok()?;
    f.lock().ok()?;
    Some(f)
}

/// Atomic write: a temp file of its own (pid and a counter, so two writers
/// never share one), then rename over the old file. The old file is never
/// deleted first: a failed rename leaves it as it was.
fn save_to(path: &Path, v: &Value) -> std::io::Result<()> {
    use std::sync::atomic::{AtomicU64, Ordering};
    static SEQ: AtomicU64 = AtomicU64::new(0);
    if let Some(d) = path.parent() {
        std::fs::create_dir_all(d)?;
    }
    let mut text = serde_json::to_string_pretty(v).map_err(std::io::Error::other)?;
    text.push('\n');
    let n = SEQ.fetch_add(1, Ordering::Relaxed);
    let tmp = path.with_extension(format!("json.{}.{n}.tmp", std::process::id()));
    std::fs::write(&tmp, text)?;
    // Windows replaces the target too, but fails while another process has
    // it open (a reader, antivirus); try again for a moment.
    let mut tries = if cfg!(windows) { 10 } else { 0 };
    let res = loop {
        match std::fs::rename(&tmp, path) {
            Err(_) if tries > 0 => {
                tries -= 1;
                std::thread::sleep(Duration::from_millis(20));
            }
            r => break r,
        }
    };
    if res.is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
    res
}

// ---- The store: one settings file, locked saves, and its watch.

/// How often [`Store::watch`] looks at the file.
pub const WATCH_POLL: Duration = Duration::from_millis(500);

/// One settings file. [`Store::shared`] is Suzuri's, the one every program
/// shares; [`Store::at`] any other (tests, a program's own copy).
pub struct Store {
    path: PathBuf,
    go: PathBuf,
    /// One writer at a time in this process; [`lock_file`] covers the rest.
    write: Mutex<()>,
    /// The settings as this program last saved or was told them: the watch
    /// reports only what differs, so a program's own saves don't come back
    /// to it.
    seen: Arc<Mutex<Option<Value>>>,
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

impl Store {
    /// The shared file ([`path`]), importing Go suzuri's on first run.
    pub fn shared() -> Self {
        Self::with_import(path(), go_path())
    }

    /// The file at `path`, with no import.
    pub fn at(path: impl Into<PathBuf>) -> Self {
        let path = path.into();
        Self::with_import(path.clone(), path)
    }

    /// The file at `path`, importing `go` when `path` does not exist yet.
    pub fn with_import(path: impl Into<PathBuf>, go: impl Into<PathBuf>) -> Self {
        Self { path: path.into(), go: go.into(), write: Mutex::new(()), seen: Arc::new(Mutex::new(None)) }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    /// The settings, normalized. A missing or broken file gives the defaults.
    /// (A read does not count as seen by the watch: a change read here
    /// before the watch's next look is still reported.)
    pub fn load(&self) -> Value {
        load_from(&self.path, &self.go)
    }

    /// Merge `patch`'s keys into the file and save it, under this process's
    /// lock and the file lock. Returns the whole settings as saved, other
    /// programs' latest changes included: apply all of it, as the watch
    /// will not report those changes again.
    pub fn update(&self, patch: Value) -> std::io::Result<Value> {
        let _g = lock(&self.write);
        let _f = lock_file(&self.path);
        // Held across the save, so the watch can't read our own write first
        // and report it as someone else's.
        let mut seen = lock(&self.seen);
        let v = update_at(&self.path, &self.go, patch)?;
        *seen = Some(v.clone());
        Ok(v)
    }

    /// Calls `f` with the new settings (normalized) each time the file
    /// changes to something other than what this program last saved or was told:
    /// a change made by another program, or by hand. Looks every
    /// [`WATCH_POLL`]; stops when the returned [`Watch`] is dropped.
    pub fn watch(&self, f: impl Fn(Value) + Send + 'static) -> Watch {
        self.watch_every(WATCH_POLL, f)
    }

    /// [`Store::watch`], looking every `every`.
    pub fn watch_every(&self, every: Duration, f: impl Fn(Value) + Send + 'static) -> Watch {
        let (stop, stopped) = mpsc::channel::<()>();
        let path = self.path.clone();
        let seen = Arc::clone(&self.seen);
        {
            let mut s = lock(&seen);
            if s.is_none() {
                *s = Some(load_from(&self.path, &self.go));
            }
        }
        let mut stamp = stamp_of(&path);
        let thread = std::thread::Builder::new()
            .name("rsz-settings-watch".into())
            .spawn(move || {
                loop {
                    match stopped.recv_timeout(every) {
                        Err(RecvTimeoutError::Timeout) => {}
                        _ => return,
                    }
                    let now = stamp_of(&path);
                    if now == stamp {
                        continue;
                    }
                    // A file that is gone or not (yet) a JSON object is
                    // skipped, not taken as the defaults; the next look
                    // tries again.
                    let Some(m) = read_raw(&path) else { continue };
                    stamp = now;
                    let v = normalize(Value::Object(m));
                    {
                        let mut s = lock(&seen);
                        if s.as_ref() == Some(&v) {
                            continue;
                        }
                        *s = Some(v.clone());
                    }
                    f(v);
                }
            })
            .expect("spawn settings watch");
        Watch { stop: Some(stop), thread: Some(thread) }
    }
}

/// When the file last changed, and its size: read only when these move.
fn stamp_of(path: &Path) -> Option<(SystemTime, u64)> {
    let m = std::fs::metadata(path).ok()?;
    Some((m.modified().ok()?, m.len()))
}

/// A running [`Store::watch`]; dropping it stops it.
pub struct Watch {
    stop: Option<mpsc::Sender<()>>,
    thread: Option<std::thread::JoinHandle<()>>,
}

impl Watch {
    /// Keep watching for as long as the program runs.
    pub fn forever(mut self) {
        if let Some(s) = self.stop.take() {
            std::mem::forget(s);
        }
        self.thread.take();
    }
}

impl Drop for Watch {
    fn drop(&mut self) {
        self.stop.take();
        if let Some(t) = self.thread.take()
            && t.thread().id() != std::thread::current().id()
        {
            let _ = t.join();
        }
    }
}

// ---- Reading the settings.

/// A profile's launch: argv (None = the user's shell) and cwd. `name` None
/// is the active profile.
pub fn profile_launch(cfg: &Value, name: Option<&str>) -> (Option<Vec<String>>, Option<String>) {
    let want = name.or_else(|| cfg["active_profile"].as_str()).unwrap_or("");
    let p = cfg["profiles"]
        .as_array()
        .and_then(|ps| ps.iter().find(|p| p["name"].as_str().is_some_and(|n| n.eq_ignore_ascii_case(want))));
    let Some(p) = p else { return (None, None) };
    let shell = p["shell"].as_str().map(str::trim).filter(|s| !s.is_empty()).map(split_args);
    let cwd = p["cwd"].as_str().map(str::trim).filter(|s| !s.is_empty()).map(expand_home);
    (shell, cwd)
}

/// A command line to argv: split on spaces, double quotes group (and are
/// dropped), as a profile's shell is written.
pub fn split_args(s: &str) -> Vec<String> {
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

fn expand_home(p: &str) -> String {
    if let Some(rest) = p.strip_prefix('~') {
        let home = std::env::var("HOME").or_else(|_| std::env::var("USERPROFILE")).unwrap_or_default();
        return format!("{home}{rest}");
    }
    p.to_string()
}

/// The cursor setting as a shape code: 1 block, 2 underline, 3 bar (Suzuri's
/// engine, DECSCUSR's steady shapes).
pub fn cursor_code(cfg: &Value) -> u8 {
    match cfg["cursor"].as_str() {
        Some("underline") => 2,
        Some("bar") => 3,
        _ => 1,
    }
}
