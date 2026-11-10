//! Notices (card #288): Go suzuri's notice cards (notices.go). Programs ask
//! for them with OSC 9 / 777 / 99; host features may post their own with
//! [`Notices::post`]. They post on the desktop as rust-toast cards (native
//! topmost windows that never take focus, stacked in the screen corner the
//! settings' notice_position names), at most 5 at once. A left click
//! focuses the pane that asked (and reports back to it for OSC 99
//! a=report); a right click or the x dismisses. Each one expires after
//! 3.2 s (6.4 s critical) unless the program says otherwise; rust-toast
//! holds them while the cursor is over the stack. The last 40 stay in a
//! history with an unread count. The cards' look is toast_look.rs.

use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use parking_lot::Mutex;
use rsz_term::protocols::{self, Notice};
use serde::Serialize;
use tauri::{AppHandle, Emitter, Manager};
use toast::Kind;

const MAX_LIVE: usize = 5;
const MAX_HISTORY: usize = 40;
/// A notice whose card rust-toast stopped showing counts as expired this
/// long after: a click or dismiss callback (which runs just after its card
/// leaves) takes it first.
const GONE_GRACE: Duration = Duration::from_millis(300);

/// Where the cards sit: the settings' notice_position as an index into
/// config::NOTICE_POSITIONS (normalize keeps it one of them).
fn anchor_of(cfg: &serde_json::Value) -> usize {
    let id = cfg["notice_position"].as_str().unwrap_or("");
    crate::config::NOTICE_POSITIONS.iter().position(|a| *a == id).unwrap_or(0)
}

/// Whether a settings patch sets the notice position (null resets it).
fn sets_position(patch: &serde_json::Value) -> bool {
    patch.get("notice_position").is_some()
}

/// rust-toast's options for an anchor: cards and sounds on (full volume,
/// as the old webview's audio played), no confetti. The positions are
/// rust-toast's corner names.
fn options(anchor: usize) -> toast::Options {
    let corner = crate::config::NOTICE_POSITIONS.get(anchor).copied().unwrap_or(toast::CORNER_BOTTOM_LEFT);
    toast::Options { enabled: true, sound: true, volume: 1.0, corner: corner.into(), confetti: false }.normalized()
}

struct Live {
    key: u64,
    pane: Option<u32>,
    note: Notice,
    /// None never expires.
    expire: Option<Duration>,
    until: Option<Instant>,
    /// rust-toast's key for the card, once it showed.
    shown: Option<u64>,
    /// When rust-toast stopped showing it.
    gone: Option<Instant>,
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
    anchor: usize,
}

#[derive(Clone)]
pub struct Notices {
    inner: Arc<Mutex<Inner>>,
    app: AppHandle,
}

impl Notices {
    pub fn new(app: AppHandle) -> Self {
        let anchor = anchor_of(&crate::config::load());
        toast::configure(crate::toast_look::config());
        toast::set_options(options(anchor));
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
    pub fn post(&self, pane: Option<u32>, n: Notice) -> Vec<Effect> {
        self.post_as(pane, n, true)
    }

    /// `history`: false leaves the card out of the history and the unread
    /// count (Go's placement preview).
    fn post_as(&self, pane: Option<u32>, mut n: Notice, history: bool) -> Vec<Effect> {
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
        {
            // Held while posting, so the rust-toast key that appears is this
            // card's (see show): only this lock posts.
            let mut g = self.inner.lock();
            if history {
                remember(&mut g, pane, &n);
            }
            let same = if n.id.is_empty() { None } else { g.live.iter().position(|l| l.note.id == n.id && l.pane == pane) };
            if let Some(at) = same {
                let l = &mut g.live[at];
                l.note = n;
                l.expire = expire;
                l.until = until;
                self.show(l);
            } else {
                if g.live.len() >= MAX_LIVE {
                    // Ours to drop, not rust-toast's: close it first so its
                    // own push-off never runs.
                    let at = g.live.iter().position(|l| l.until.is_some()).unwrap_or(0);
                    let dropped = g.live.remove(at);
                    toast::close(&card_id(dropped.key));
                    effects.extend(close_event(&dropped));
                }
                g.next += 1;
                let key = g.next;
                g.live.push(Live { key, pane, note: n, expire, until, shown: None, gone: None });
                let l = g.live.last_mut().expect("just pushed");
                self.show(l);
            }
        }
        self.bell();
        effects
    }

    /// Posts l's card, or replaces it in place (its toast id is its key).
    /// The callbacks run on rust-toast's callback thread and hop back
    /// through the AppHandle.
    fn show(&self, l: &mut Live) {
        let (mut note, extra) = card(l.key, &l.note, l.expire);
        let key = l.key;
        let app = self.app.clone();
        note.on_click = Some(Arc::new(move || {
            if let Some(ns) = app.try_state::<Notices>() {
                crate::notice_effects(&app, ns.activate(key));
            }
        }));
        let app = self.app.clone();
        note.on_dismiss = Some(Arc::new(move || {
            if let Some(ns) = app.try_state::<Notices>() {
                crate::notice_effects(&app, ns.dismiss(key));
            }
        }));
        let before = toast::card_keys();
        toast::post(note);
        // A replace keeps its key; a new card (or one re-posted after its
        // card went) gets the one new key.
        if let Some(k) = toast::card_keys().into_iter().find(|k| !before.contains(k)) {
            l.shown = Some(k);
        }
        l.gone = None;
        if let Some(k) = extra {
            toast::play_sound(k);
        }
    }

    /// A host message as a notice card (Go postHostToast), e.g. "copied"
    /// or "font 15px". Messages of one kind replace each other's card.
    pub fn toast(&self, msg: &str) {
        let msg = msg.trim();
        if msg.is_empty() {
            return;
        }
        let title: String = msg.chars().take(180).collect();
        let sound = host_sound(msg);
        let n = Notice {
            id: host_kind(msg).into(),
            title,
            body: String::new(),
            sound: sound.into(),
            // The accent matches the sound: red for a failure, amber for a warning.
            icon: if sound == "info" { String::new() } else { sound.into() },
            urgency: 1,
            occasion: "always".into(),
            expire_ms: host_long(msg).then_some(4000),
            report: false,
            focus: true,
            close_event: false,
        };
        let _ = self.post(None, n);
    }

    /// OSC 99 p=close from a program.
    pub fn close(&self, pane: u32, id: &str) {
        self.inner.lock().live.retain(|l| {
            let hit = l.pane == Some(pane) && l.note.id == id;
            if hit {
                toast::close(&card_id(l.key));
            }
            !hit
        });
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
        if l.note.report
            && let Some(p) = l.pane
        {
            out.push(Effect::Reply(p, protocols::activate_report(&l.note.id)));
        }
        out.extend(close_event(&l));
        out
    }

    /// A right click or the x on a card (Go dismissNoticeAt).
    pub fn dismiss(&self, key: u64) -> Vec<Effect> {
        self.take(key).map(|l| close_event(&l).into_iter().collect()).unwrap_or_default()
    }

    /// Saved settings (config_set): move the cards to the notice position.
    /// The silent sample card (Go setNoticeAnchor with preview) shows only
    /// when this save's `patch` set the position: every window is its own
    /// process, so a save here of anything else may just be catching up
    /// with a move made in another window.
    pub fn apply_config(&self, cfg: &serde_json::Value, patch: &serde_json::Value) {
        let anchor = anchor_of(cfg);
        let moved = std::mem::replace(&mut self.inner.lock().anchor, anchor) != anchor;
        if !moved {
            return;
        }
        toast::set_options(toast::Options { corner: options(anchor).corner, ..toast::current_options() });
        if sets_position(patch) {
            let label = crate::config::NOTICE_POSITION_LABELS[anchor];
            let _ = self.post_as(None, sample(label), false);
        }
    }

    /// Cards on screen now (the notify CLI waits for them to go).
    pub fn live_count(&self) -> usize {
        self.inner.lock().live.len()
    }

    /// The history, newest first, and how many are unread.
    pub fn history(&self) -> (Vec<Played>, usize) {
        let g = self.inner.lock();
        (g.history.clone(), g.unread)
    }

    pub fn mark_read(&self) {
        self.inner.lock().unread = 0;
        let _ = self.app.emit("notice-bell", 0usize);
    }

    /// The bell's unread count to the window.
    fn bell(&self) {
        let unread = self.inner.lock().unread;
        let _ = self.app.emit("notice-bell", unread);
    }

    fn take(&self, key: u64) -> Option<Live> {
        let mut g = self.inner.lock();
        let at = g.live.iter().position(|l| l.key == key)?;
        let l = g.live.remove(at);
        toast::close(&card_id(l.key));
        Some(l)
    }

    /// Drops the notices whose cards rust-toast no longer shows (they
    /// expired; it holds them while the cursor is over the stack) and sends
    /// their close events.
    fn expire(&self) {
        let now = Instant::now();
        let mut g = self.inner.lock();
        if g.live.is_empty() {
            return;
        }
        let keys = toast::card_keys();
        for l in &mut g.live {
            let up = match l.shown {
                Some(k) => keys.contains(&k),
                // Never shown (no cards on this platform): its own timer.
                None => l.until.is_none_or(|u| now < u),
            };
            if up {
                l.gone = None;
            } else {
                l.gone.get_or_insert(now);
            }
        }
        let (dead, keep): (Vec<Live>, Vec<Live>) = std::mem::take(&mut g.live)
            .into_iter()
            .partition(|l| l.gone.is_some_and(|t| now.saturating_duration_since(t) >= GONE_GRACE));
        g.live = keep;
        drop(g);
        if dead.is_empty() {
            return;
        }
        let effects: Vec<Effect> = dead.iter().filter_map(close_event).collect();
        crate::notice_effects(&self.app, effects);
    }

    fn window_state(&self) -> (bool, bool) {
        let Some(w) = self.app.get_webview_window("main") else { return (false, false) };
        let focused = w.is_focused().unwrap_or(false);
        let visible = w.is_visible().unwrap_or(false) && !w.is_minimized().unwrap_or(false);
        (focused, visible)
    }
}

/// The rust-toast id of the notice with key: one per notice, not the
/// program's OSC 99 id, which is only unique within its pane (and often
/// empty).
fn card_id(key: u64) -> String {
    format!("rsz-notice-{key}")
}

/// The card's kind, i.e. its accent: red for critical or an error icon,
/// amber for a warning icon, the plain accent otherwise (the old webview
/// cards' tones).
fn kind_of(n: &Notice) -> Kind {
    if n.urgency >= 2 || n.icon == "error" {
        Kind::Error
    } else if matches!(n.icon.as_str(), "warn" | "warning") {
        Kind::Warn
    } else {
        Kind::Info
    }
}

/// The kind whose rust-toast sound is the one Go plays for n (fail is
/// Error's, published Warn's, success Info's), or None for silent.
fn sound_kind(n: &Notice) -> Option<Kind> {
    match sound_for(&n.sound, n.urgency)? {
        "fail" => Some(Kind::Error),
        "published" => Some(Kind::Warn),
        _ => Some(Kind::Info),
    }
}

/// A notice as a rust-toast note (without its callbacks), and the kind
/// whose sound to play beside it when the card's own sound (from its kind)
/// is not Go's: the card is then posted silent. Notices carry no buttons
/// (rsz_term's Notice has no actions), so the note has none.
fn card(key: u64, n: &Notice, expire: Option<Duration>) -> (toast::Note, Option<Kind>) {
    let kind = kind_of(n);
    let (silent, extra) = match sound_kind(n) {
        None => (true, None),
        Some(k) if toast::sound::sound_for(k, false) == toast::sound::sound_for(kind, false) => (false, None),
        Some(k) => (true, Some(k)),
    };
    let note = toast::Note {
        id: card_id(key),
        title: n.title.clone(),
        body: n.body.clone(),
        kind,
        silent,
        expire: expire.unwrap_or_default(),
        sticky: expire.is_none(),
        ..toast::Note::default()
    };
    (note, extra)
}

/// Go hostToastID: one card per kind of host message ("" stacks).
fn host_kind(msg: &str) -> &'static str {
    let s = msg.to_lowercase();
    let pre = |p: &str| s.starts_with(p);
    if pre("caffeine") {
        "host-caffeine"
    } else if pre("font ") {
        "host-font"
    } else if pre("queued") || pre("ran queued") || pre("cleared ") {
        "host-queue"
    } else if s.contains("update") || s.contains("up to date") || pre("installing") || s.contains("available") {
        "host-update"
    } else if pre("split ") || pre("fork ") || s.ends_with(" panes") || s.ends_with(" tabs") || s == "1 tab" || s == "tab limit" || pre("max ") {
        "host-layout"
    } else if pre("transfer") || pre("send:") || pre("receive:") || pre("ticket ") {
        "host-transfer"
    } else if pre("settings ") || pre("save failed") {
        "host-settings"
    } else if s == "copied" {
        "host-copy"
    } else {
        ""
    }
}

/// Go hostToastSound: error, warn or info.
fn host_sound(msg: &str) -> &'static str {
    let s = msg.to_lowercase();
    if ["fail", "not found", "unavailable", "not allowlisted", "missing session"].iter().any(|w| s.contains(w)) {
        "error"
    } else if ["ignored", "limit", "empty", "deferred", "already off", "fallback"].iter().any(|w| s.contains(w)) || s.starts_with("max ") {
        "warn"
    } else {
        "info"
    }
}

/// Go hostToastLong: update news and opened links stay 4 s.
fn host_long(msg: &str) -> bool {
    let s = msg.to_lowercase();
    ["update", "up to date", "installing", "opened"].iter().any(|w| s.contains(w))
}

/// Go previewNoticeAnchor: a silent card at the new spot.
fn sample(label: &str) -> Notice {
    Notice {
        id: "host-notice-place".into(),
        title: "Notifications".into(),
        body: label.into(),
        sound: "silent".into(),
        icon: String::new(),
        urgency: 1,
        occasion: "always".into(),
        expire_ms: Some(2400),
        report: false,
        focus: false,
        close_event: false,
    }
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

#[cfg(test)]
mod tests {
    use super::*;

    fn notice(sound: &str, icon: &str, urgency: u8) -> Notice {
        Notice {
            id: "x".into(),
            title: "t".into(),
            body: "b".into(),
            sound: sound.into(),
            icon: icon.into(),
            urgency,
            occasion: "always".into(),
            expire_ms: None,
            report: false,
            focus: true,
            close_event: false,
        }
    }

    // Pure: nothing here posts a card (that would draw on the screen).
    #[test]
    fn cards_keep_the_old_tone_and_sound() {
        let (c, extra) = card(7, &notice("info", "", 1), Some(Duration::from_millis(3200)));
        assert_eq!((c.kind, c.silent, extra), (Kind::Info, false, None));
        assert_eq!(c.id, "rsz-notice-7");
        assert_eq!((c.expire, c.sticky), (Duration::from_millis(3200), false));
        assert!(c.actions.is_empty());
        let (c, extra) = card(1, &notice("error", "error", 1), None);
        assert_eq!((c.kind, c.silent, extra, c.sticky), (Kind::Error, false, None, true));
        let (c, extra) = card(1, &notice("warn", "warn", 1), None);
        assert_eq!((c.kind, c.silent, extra), (Kind::Warn, false, None));
        let (c, extra) = card(1, &notice("silent", "error", 2), None);
        assert_eq!((c.kind, c.silent, extra), (Kind::Error, true, None));
        // A sound that is not the kind's own plays beside a silent card.
        let (c, extra) = card(1, &notice("question", "", 1), None);
        assert_eq!((c.kind, c.silent, extra), (Kind::Info, true, Some(Kind::Warn)));
    }

    #[test]
    fn positions_are_rust_toast_corners() {
        for (i, p) in crate::config::NOTICE_POSITIONS.iter().enumerate() {
            assert_eq!(options(i).corner, *p);
        }
    }

    #[test]
    fn host_messages_of_one_kind_share_a_card() {
        assert_eq!(host_kind("caffeine on · 15m"), "host-caffeine");
        assert_eq!(host_kind("Caffeine off · timer ended"), "host-caffeine");
        assert_eq!(host_kind("font 15px"), "host-font");
        assert_eq!(host_kind("font 36px (limit)"), "host-font");
        assert_eq!(host_kind("Up to date (0.4.1)."), "host-update");
        assert_eq!(host_kind("Checking for updates…"), "host-update");
        assert_eq!(host_kind("settings saved"), "host-settings");
        assert_eq!(host_kind("copied"), "host-copy");
        assert_eq!(host_kind("max panes"), "host-layout");
        assert_eq!(host_kind("something else"), "");
    }

    #[test]
    fn host_sounds_follow_go() {
        assert_eq!(host_sound("caffeine failed: no power assertion"), "error");
        assert_eq!(host_sound("Update check failed: offline"), "error");
        assert_eq!(host_sound("caffeine already off"), "warn");
        assert_eq!(host_sound("font 36px (limit)"), "warn");
        assert_eq!(host_sound("copied"), "info");
        assert_eq!(sound_for(host_sound("copied"), 1), Some("success"));
        assert_eq!(sound_for(host_sound("caffeine already off"), 1), Some("published"));
    }

    #[test]
    fn only_a_position_save_previews() {
        use serde_json::json;
        assert!(sets_position(&json!({"notice_position": "top-right"})));
        assert!(!sets_position(&json!({"font_size": 15})));
        assert!(sets_position(&json!({"notice_position": null})));
        assert!(!sets_position(&json!(null)));
    }

    #[test]
    fn update_news_stays_longer() {
        assert!(host_long("Up to date."));
        assert!(host_long("Dev build: updates are off."));
        assert!(!host_long("copied"));
    }
}
