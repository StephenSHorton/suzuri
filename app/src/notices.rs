//! Notices (card #288): Go suzuri's notice cards (notices.go). Programs ask
//! for them with OSC 9 / 777 / 99; host features may post their own with
//! [`Notices::post`]. They show in a small always-on-top panel at a corner
//! of the screen (ui/notice.html), at most 5 at once, newest on top of the
//! stack. A left click focuses the pane that asked (and reports back to it
//! for OSC 99 a=report), a right click dismisses. Each one expires after
//! 3.2 s (6.4 s critical) unless the program says otherwise; hovering the
//! panel holds them. The last 40 stay in a history with an unread count.

use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use parking_lot::Mutex;
use rsz_term::protocols::{self, Notice};
use serde::Serialize;
use tauri::{AppHandle, Emitter, Manager, PhysicalPosition, PhysicalSize, WebviewUrl, WebviewWindowBuilder};

const PANEL: &str = "notices";
const MAX_LIVE: usize = 5;
const MAX_HISTORY: usize = 40;
// Go's geometry, in points (notices.go).
const CARD_W: f64 = 320.0;
const CARD_H: f64 = 72.0;
const GAP: f64 = 8.0;
const MARGIN: f64 = 16.0;
const SCREEN_INSET: f64 = 16.0;

/// Where the panel sits (Go config.NoticePosition).
pub const ANCHORS: [&str; 9] =
    ["bottom-left", "bottom-center", "bottom-right", "center-left", "center", "center-right", "top-left", "top-center", "top-right"];

struct Live {
    key: u64,
    pane: Option<u32>,
    note: Notice,
    until: Option<Instant>,
}

#[derive(Serialize, Clone)]
pub struct Played {
    pub title: String,
    pub body: String,
    /// Unix milliseconds.
    pub at: u64,
    pub pane: Option<u32>,
    pub id: String,
}

#[derive(Serialize, Clone)]
struct Card {
    key: u64,
    title: String,
    body: String,
    /// "", "error" (red) or "warn" (amber) accent.
    tone: &'static str,
}

#[derive(Serialize, Clone)]
struct PanelState {
    cards: Vec<Card>,
    anchor: usize,
    /// A sound to play now: success, fail or published.
    sound: Option<&'static str>,
}

/// Something the app has to do after a notice is clicked or closed.
pub enum Effect {
    /// Bytes for the pane's program (OSC 99 report and close events).
    Reply(u32, Vec<u8>),
    /// Bring the window up and focus this pane.
    Focus(Option<u32>),
}

#[derive(Default)]
struct Inner {
    next: u64,
    live: Vec<Live>,
    history: Vec<Played>,
    unread: usize,
    hover: bool,
    anchor: usize,
}

#[derive(Clone)]
pub struct Notices {
    inner: Arc<Mutex<Inner>>,
    app: AppHandle,
}

impl Notices {
    pub fn new(app: AppHandle) -> Self {
        let anchor = go_anchor().and_then(|a| ANCHORS.iter().position(|x| *x == a)).unwrap_or(0);
        let n = Self { inner: Arc::new(Mutex::new(Inner { anchor, ..Default::default() })), app };
        let tick = n.clone();
        std::thread::Builder::new()
            .name("rsz-notices".into())
            .spawn(move || loop {
                std::thread::sleep(Duration::from_millis(100));
                tick.expire();
            })
            .expect("notice thread");
        n
    }

    /// Post a notice (Go postDeskNote). `pane` is the pane that asked, if any.
    pub fn post(&self, pane: Option<u32>, mut n: Notice) -> Vec<Effect> {
        let (focused, visible) = self.window_state();
        match n.occasion.as_str() {
            "unfocused" if focused => return Vec::new(),
            "invisible" if focused || visible => return Vec::new(),
            _ => {}
        }
        if n.title.trim().is_empty() && n.body.trim().is_empty() {
            return Vec::new();
        }
        if n.title.is_empty() {
            n.title = std::mem::take(&mut n.body);
        }
        let expire = match n.expire_ms {
            Some(0) => None,
            Some(ms) => Some(Duration::from_millis(ms as u64)),
            None if n.urgency >= 2 => Some(Duration::from_millis(6400)),
            None => Some(Duration::from_millis(3200)),
        };
        let until = expire.map(|d| Instant::now() + d);
        let mut effects = Vec::new();
        let sound = sound_for(&n.sound, n.urgency);
        {
            let mut g = self.inner.lock();
            remember(&mut g, pane, &n);
            if !n.id.is_empty() {
                if let Some(l) = g.live.iter_mut().find(|l| l.note.id == n.id && l.pane == pane) {
                    l.note = n;
                    l.until = until;
                    drop(g);
                    self.present(None);
                    return effects;
                }
            }
            if g.live.len() >= MAX_LIVE {
                let at = g.live.iter().position(|l| l.until.is_some()).unwrap_or(0);
                let dropped = g.live.remove(at);
                effects.extend(close_event(&dropped));
            }
            g.next += 1;
            let key = g.next;
            g.live.push(Live { key, pane, note: n, until });
        }
        self.present(sound);
        effects
    }

    /// A host message as a notice card (Go postHostToast), e.g. "Copied".
    #[allow(dead_code)] // for host features (#294 notify, copy and so on)
    pub fn toast(&self, title: &str, body: &str) {
        let mut n = Notice {
            id: String::new(),
            title: title.into(),
            body: body.into(),
            sound: "silent".into(),
            icon: String::new(),
            urgency: 1,
            occasion: "always".into(),
            expire_ms: None,
            report: false,
            focus: true,
            close_event: false,
        };
        n.title.truncate(180);
        let _ = self.post(None, n);
    }

    /// OSC 99 p=close from a program.
    pub fn close(&self, pane: u32, id: &str) {
        let mut g = self.inner.lock();
        let before = g.live.len();
        g.live.retain(|l| !(l.pane == Some(pane) && l.note.id == id));
        let changed = g.live.len() != before;
        drop(g);
        if changed {
            self.present(None);
        }
    }

    /// OSC 99 p=alive: the ids of this pane's notices still showing.
    pub fn alive_reply(&self, pane: u32, id: &str) -> Vec<u8> {
        let g = self.inner.lock();
        let ids: Vec<String> = g.live.iter().filter(|l| l.pane == Some(pane) && !l.note.id.is_empty()).map(|l| l.note.id.clone()).collect();
        protocols::alive_reply(id, &ids)
    }

    /// A left click on a card (Go activateNotice).
    pub fn activate(&self, key: u64) -> Vec<Effect> {
        let Some(l) = self.take(key) else { return Vec::new() };
        let mut out = Vec::new();
        if l.note.focus {
            out.push(Effect::Focus(l.pane));
        }
        if l.note.report {
            if let Some(p) = l.pane {
                out.push(Effect::Reply(p, protocols::activate_report(&l.note.id)));
            }
        }
        out.extend(close_event(&l));
        out
    }

    /// A right click on a card (Go dismissNoticeAt).
    pub fn dismiss(&self, key: u64) -> Vec<Effect> {
        self.take(key).map(|l| close_event(&l).into_iter().collect()).unwrap_or_default()
    }

    pub fn set_hover(&self, hover: bool) {
        self.inner.lock().hover = hover;
    }

    pub fn set_anchor(&self, anchor: &str) {
        if let Some(i) = ANCHORS.iter().position(|a| *a == anchor) {
            self.inner.lock().anchor = i;
            self.present(None);
        }
    }

    /// The history, newest first, and how many are unread.
    /// The panel page asks for the cards once it has loaded: the panel is
    /// made on the first post, and that post's event can reach the page
    /// before its listener exists, which lost a lone first card.
    pub fn resync(&self) {
        if !self.inner.lock().live.is_empty() {
            self.present(None);
        }
    }

    /// Cards on screen now (the notify CLI waits for them to go).
    pub fn live_count(&self) -> usize {
        self.inner.lock().live.len()
    }

    pub fn history(&self) -> (Vec<Played>, usize) {
        let g = self.inner.lock();
        (g.history.clone(), g.unread)
    }

    pub fn mark_read(&self) {
        self.inner.lock().unread = 0;
        let _ = self.app.emit("notice-bell", 0usize);
    }

    fn take(&self, key: u64) -> Option<Live> {
        let mut g = self.inner.lock();
        let at = g.live.iter().position(|l| l.key == key)?;
        let l = g.live.remove(at);
        drop(g);
        self.present(None);
        Some(l)
    }

    fn expire(&self) {
        let now = Instant::now();
        let mut g = self.inner.lock();
        if g.hover || g.live.is_empty() {
            return;
        }
        let (dead, keep): (Vec<Live>, Vec<Live>) = std::mem::take(&mut g.live).into_iter().partition(|l| l.until.is_some_and(|u| now >= u));
        g.live = keep;
        drop(g);
        if dead.is_empty() {
            return;
        }
        let effects: Vec<Effect> = dead.iter().filter_map(close_event).collect();
        crate::notice_effects(&self.app, effects);
        self.present(None);
    }

    fn window_state(&self) -> (bool, bool) {
        let Some(w) = self.app.get_webview_window("main") else { return (false, false) };
        let focused = w.is_focused().unwrap_or(false);
        let visible = w.is_visible().unwrap_or(false) && !w.is_minimized().unwrap_or(false);
        (focused, visible)
    }

    /// Size and place the panel for the cards, and hand them to it.
    fn present(&self, sound: Option<&'static str>) {
        let (cards, anchor) = {
            let g = self.inner.lock();
            let cards: Vec<Card> = g
                .live
                .iter()
                .map(|l| Card {
                    key: l.key,
                    title: l.note.title.clone(),
                    body: l.note.body.clone(),
                    tone: if l.note.urgency >= 2 || l.note.icon == "error" {
                        "error"
                    } else if matches!(l.note.icon.as_str(), "warn" | "warning") {
                        "warn"
                    } else {
                        ""
                    },
                })
                .collect();
            (cards, g.anchor)
        };
        let app = self.app.clone();
        let _ = self.app.run_on_main_thread(move || {
            let Some(panel) = panel(&app) else { return };
            if cards.is_empty() {
                let _ = panel.emit_to(PANEL, "notices", PanelState { cards, anchor, sound });
                let _ = panel.hide();
                return;
            }
            place(&app, &panel, cards.len(), anchor);
            let _ = panel.emit_to(PANEL, "notices", PanelState { cards, anchor, sound });
            if !panel.is_visible().unwrap_or(false) {
                let _ = panel.show();
            }
        });
        let unread = self.inner.lock().unread;
        let _ = self.app.emit("notice-bell", unread);
    }
}

/// The corner Go suzuri's config picks (notice_position), until rust-suzuri
/// has settings of its own (#283 calls notice_anchor then).
fn go_anchor() -> Option<String> {
    #[cfg(target_os = "macos")]
    let dir = std::env::var_os("HOME").map(|h| std::path::PathBuf::from(h).join("Library/Application Support"));
    #[cfg(windows)]
    let dir = std::env::var_os("APPDATA").map(std::path::PathBuf::from);
    #[cfg(not(any(target_os = "macos", windows)))]
    let dir = std::env::var_os("XDG_CONFIG_HOME").map(std::path::PathBuf::from);
    let text = std::fs::read_to_string(dir?.join("suzuri").join("config.json")).ok()?;
    let v: serde_json::Value = serde_json::from_str(&text).ok()?;
    v.get("notice_position")?.as_str().map(str::to_string)
}

fn remember(g: &mut Inner, pane: Option<u32>, n: &Notice) {
    let at = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_millis() as u64).unwrap_or(0);
    if !n.id.is_empty() {
        g.history.retain(|h| h.id != n.id);
    }
    g.history.insert(0, Played { title: n.title.trim().into(), body: n.body.trim().into(), at, pane, id: n.id.clone() });
    g.history.truncate(MAX_HISTORY);
    g.unread += 1;
}

fn close_event(l: &Live) -> Option<Effect> {
    let pane = l.pane.filter(|_| l.note.close_event && !l.note.id.is_empty())?;
    Some(Effect::Reply(pane, protocols::close_report(&l.note.id)))
}

// Go playNoticeSound.
fn sound_for(name: &str, urgency: u8) -> Option<&'static str> {
    match name {
        "silent" => None,
        "error" => Some("fail"),
        "warn" | "warning" | "question" => Some("published"),
        _ if urgency >= 2 => Some("fail"),
        _ => Some("success"),
    }
}

/// The panel window, made on first use: transparent, no frame, above other
/// windows, not in the taskbar, and it doesn't take focus when shown.
fn panel(app: &AppHandle) -> Option<tauri::WebviewWindow> {
    if let Some(w) = app.get_webview_window(PANEL) {
        return Some(w);
    }
    WebviewWindowBuilder::new(app, PANEL, WebviewUrl::App("notice.html".into()))
        .title("suzuri notices")
        .transparent(true)
        .decorations(false)
        .shadow(false)
        .always_on_top(true)
        .visible_on_all_workspaces(true)
        .skip_taskbar(true)
        .resizable(false)
        .focused(false)
        .focusable(false)
        .visible(false)
        .inner_size(CARD_W + 2.0 * MARGIN, CARD_H + 2.0 * MARGIN)
        .build()
        .ok()
}

// Go noticePanelOrigin: the stack sits at the anchor, inset from the screen
// edges, on the screen the main window is on.
fn place(app: &AppHandle, panel: &tauri::WebviewWindow, n: usize, anchor: usize) {
    let w = CARD_W + 2.0 * MARGIN;
    let h = n as f64 * (CARD_H + GAP) - GAP + 2.0 * MARGIN;
    let main = app.get_webview_window("main");
    let monitor = main.as_ref().and_then(|m| m.current_monitor().ok().flatten()).or_else(|| panel.primary_monitor().ok().flatten());
    let Some(mon) = monitor else { return };
    let scale = mon.scale_factor();
    let area = mon.work_area();
    let (ax, ay) = (area.position.x as f64, area.position.y as f64);
    let (aw, ah) = (area.size.width as f64, area.size.height as f64);
    let (pw, ph) = (w * scale, h * scale);
    let inset = (SCREEN_INSET - MARGIN).max(0.0) * scale;
    let x = match anchor % 3 {
        0 => ax + inset,
        1 => ax + (aw - pw) / 2.0,
        _ => ax + aw - pw - inset,
    };
    let y = match anchor / 3 {
        0 => ay + ah - ph - inset,
        1 => ay + (ah - ph) / 2.0,
        _ => ay + inset,
    };
    let _ = panel.set_size(PhysicalSize::new(pw.round() as u32, ph.round() as u32));
    let _ = panel.set_position(PhysicalPosition::new(x.round() as i32, y.round() as i32));
}
