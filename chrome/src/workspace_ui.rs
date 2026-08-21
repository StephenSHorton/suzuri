//! Shared workspace chat — default surface is a split pane; modal is a pop-out.
//!
//! Data lives under `~/Library/Application Support/suzuri/workspace/` (macOS)
//! via [`crate::workspace_store`], matching product `internal/workspace`.
//!
//! Public surface used by `app` / `renderer` (preserve):
//! - fields: `open`, `channel`, `draft`, `messages`, `channels`, `members`
//! - glass: `visible`, `open`, `close`, `toggle`, `tick`, `content_ease`,
//!   `scrim_alpha`, `animated_modal_rect`
//! - compose: `insert_char`, `backspace`, `send`, `@mention` picker / complete
//! - channels: `select_channel`
//! - presence / attach: `join_self` (quiet), `attach_path`, `begin_attach`, `pick_and_attach`,
//!   `members_strip_chips`, `cycle_status`, `begin_add_agent`, `begin_set_topic`,
//!   `refresh` / native FS watch + 1s poll fallback while open
//!
//! See `chrome/WORKSPACE_HOOKS.md` for hit-test layout constants and hooks.

use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Instant;

use notify::{EventKind, RecommendedWatcher, RecursiveMode, Watcher};

use crate::layout::Rect;
use crate::workspace_store::{
    agent_kickoff_text, local_human_name, next_availability, normalize_agent_role, presence_chip,
    WorkspaceStore, AGENT_ROLES, DEFAULT_CHANNEL, HISTORY_LIMIT, MAX_BODY_RUNES, STATUS_IDLE,
};
use crate::workspace_sync::{spawn_sync, SyncJob, SyncMode};

/// How often an open workspace reloads from disk as a safety net (and the
/// only path if native FS watch setup fails). MCP / other clients write JSONL.
pub const AUTO_REFRESH_INTERVAL_SECS: f32 = 1.0;

/// Coalesce bursty native FS events (atomic rename + JSONL append) before reload.
pub const WATCH_DEBOUNCE_SECS: f32 = 0.05;

/// How long a pending Ctrl+D delete stays armed before it expires.
pub const DELETE_CONFIRM_SECS: f32 = 2.5;

// Re-export so `workspace_ui::WsMessage` / `WsMember` keep working for app/renderer.
pub use crate::workspace_store::{WsMember, WsMessage};

/// Layout constants shared with renderer + hit-testing (logical px).
pub const MODAL_PAD: f32 = 14.0;
pub const CHANNEL_LIST_W: f32 = 140.0;
pub const CHANNEL_ROW_H: f32 = 28.0;
/// Y offset of first channel row below modal top (title strip).
pub const CHANNEL_LIST_TOP: f32 = MODAL_PAD + 18.0;
pub const COMPOSE_H: f32 = 44.0;
/// Height reserved under the title for the presence strip (message pane).
pub const PRESENCE_STRIP_H: f32 = 18.0;
/// Pinned topic line under the presence strip (does not scroll with chat).
pub const TOPIC_PIN_H: f32 = 16.0;
/// Width of the +Agent chip at the right of the presence strip.
pub const ADD_AGENT_CHIP_W: f32 = 64.0;
/// Connect bar under the topic pin (Share / Join / live status).
pub const CONNECT_BAR_H: f32 = 22.0;
/// Hide the channel rail when the pane is narrower than this (logical px).
pub const COMPACT_INNER_W: f32 = 360.0;
/// Ticket paste cap (iroh EndpointAddr JSON can exceed message body limits).
pub const MAX_TICKET_RUNES: usize = 64 * 1024;
/// Floor for visible bubbles (larger panes raise this via [`WorkspaceUi::visible_bubble_cap`]).
pub const VISIBLE_BUBBLE_CAP: usize = 14;
/// Vertical gap between chat bubbles.
pub const BUBBLE_GAP: f32 = 8.0;
/// Min bubble height (header + body + padding).
pub const BUBBLE_MIN_H: f32 = 48.0;

/// Product `memberPalette` — stable FNV-picked colors for participants.
pub const MEMBER_PALETTE: &[[f32; 3]] = &[
    [0.498, 0.859, 1.0],   // #7FDBFF aqua
    [1.0, 0.863, 0.0],     // #FFDC00 gold
    [1.0, 0.522, 0.106],   // #FF851B orange
    [0.694, 0.051, 0.788], // #B10DC9 purple
    [0.180, 0.800, 0.251], // #2ECC40 green
    [1.0, 0.255, 0.212],   // #FF4136 red
    [0.224, 0.800, 0.800], // #39CCCC teal
    [0.941, 0.071, 0.745], // #F012BE fuchsia
    [0.004, 1.0, 0.439],   // #01FF70 lime
    [0.498, 0.859, 0.792], // #7FDBCA mint
    [0.902, 0.859, 0.455], // #E6DB74 soft yellow
    [0.682, 0.506, 1.0],   // #AE81FF soft violet
    [0.400, 0.851, 0.937], // #66D9EF soft cyan
    [0.992, 0.592, 0.122], // #FD971F soft orange
    [0.651, 0.886, 0.180], // #A6E22E soft green
    [0.976, 0.149, 0.447], // #F92672 pink
];

/// Glyphs from product `memberGlyphs` (subset that bitmap fonts usually cover).
pub const MEMBER_GLYPHS: &[&str] = &[
    "◆", "◇", "●", "○", "▲", "△", "■", "□", "★", "☆", "✦", "✧", "◉", "◎", "⬡", "⬢",
];

/// One painted chat bubble (geometry + content for panels / labels).
#[derive(Clone, Debug)]
pub struct MsgBubble {
    pub rect: Rect,
    /// True when this is the local human's message (right-aligned, accent tint).
    pub mine: bool,
    pub system: bool,
    /// Glass face tint RGB (member color or theme accent for mine).
    pub tint: [f32; 3],
    /// Tint strength 0..1 for the glass shader wash.
    pub tint_strength: f32,
    pub header: String,
    pub body: String,
}

/// FNV-1a 32-bit (matches Go `hash/fnv` New32a).
fn fnv1a32(bytes: &[u8]) -> u32 {
    let mut h: u32 = 2166136261;
    for &b in bytes {
        h ^= b as u32;
        h = h.wrapping_mul(16777619);
    }
    h
}

/// Stable member color + glyph (product `memberIdentity`).
pub fn member_identity(name: &str, kind: &str) -> ([f32; 3], &'static str) {
    let mut key = name.trim().to_ascii_lowercase().into_bytes();
    if kind.eq_ignore_ascii_case("agent") {
        key.push(0x01);
    }
    let n = fnv1a32(&key);
    let color = MEMBER_PALETTE[(n as usize) % MEMBER_PALETTE.len()];
    let glyph = MEMBER_GLYPHS[((n >> 8) as usize) % MEMBER_GLYPHS.len()];
    (color, glyph)
}

/// What the compose line is editing.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ComposeMode {
    /// Post a message to the active channel.
    Message,
    /// Create a new channel (draft = channel name).
    NewChannel,
    /// Draft is a filesystem path to attach (Enter → `attach_path`).
    AttachPath,
    /// Pick a +Agent role (`pm` / `engine` / `content`) then copy kickoff.
    PickAgentRole,
    /// Draft is the channel topic (Enter writes `meta.json`).
    SetTopic,
    /// Draft is a workspace P2P ticket (Enter → join).
    JoinTicket,
}

/// Hit-tested regions for one workspace pane. Computed from the host rect so
/// title / presence / rail / connect / compose never share pixels.
#[derive(Clone, Copy, Debug)]
pub struct WorkspaceLayout {
    pub host: Rect,
    pub compact: bool,
    pub rail: Rect,
    pub title: Rect,
    pub presence: Rect,
    pub add_agent: Rect,
    pub topic: Rect,
    pub connect: Rect,
    pub messages: Rect,
    pub compose: Rect,
}

impl WorkspaceLayout {
    pub fn add_agent_label(&self) -> &'static str {
        if self.add_agent.w < 40.0 {
            "+"
        } else {
            "+ Agent"
        }
    }
}

/// One clickable control on the connect bar.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ConnectAction {
    /// Listen and copy a ticket for the other computer.
    Share,
    /// Paste their ticket into compose.
    Join,
    /// Copy the current ticket again.
    Copy,
    /// Cancel join compose, or stop the sidecar.
    Stop,
}

pub struct WorkspaceUi {
    pub open: bool,
    present: f32,
    present_vel: f32,
    overlay: f32,
    /// When set, chat is hosted in this split-pane leaf (not a floating modal).
    pub docked_pane: Option<u64>,
    /// Live glass rect of the host pane, filled each frame by the app.
    host: Option<Rect>,
    pub channel: String,
    pub draft: String,
    pub messages: Vec<WsMessage>,
    pub channels: Vec<String>,
    /// Presence list for paint (product `members.json`).
    pub members: Vec<WsMember>,
    /// Pinned topic for the active channel (`meta.json`).
    pub channel_topic: String,
    /// Kickoff snippet / link ticket waiting for the host to copy (drained by `app`).
    pending_clipboard: Option<String>,
    /// Toast label for the pending clipboard payload.
    clipboard_toast: &'static str,
    /// Ephemeral status (create errors, etc.).
    pub status: String,
    pub mode: ComposeMode,
    /// Selected row in the `@mention` picker (clamped to candidates).
    mention_sel: usize,
    store: WorkspaceStore,
    human: String,
    /// Member id from join_self (used as from_id on every chrome post).
    human_id: String,
    /// Scroll: how many messages from the end are hidden (0 = pin bottom).
    pub scroll: usize,
    /// Accumulator for auto-refresh while open (seconds).
    refresh_accum: f32,
    /// Native recursive watch on the workspace root. `None` if setup failed.
    fs_watch: Option<RecommendedWatcher>,
    /// Set from the notify thread; `tick` reloads when this is true.
    watch_dirty: Arc<AtomicBool>,
    /// Accumulator for watch debounce (seconds).
    watch_debounce: f32,
    /// Armed delete: first Ctrl+D sets slug + time; second confirms.
    delete_pending: Option<(String, Instant)>,
    /// Running `suzuri-workspace-sync` sidecar (listen or join).
    sync_job: Option<SyncJob>,
    /// Machine-mode phase: preparing | ready | connecting | connected | error | stopped
    pub sync_phase: String,
    /// Ticket to share (listen `ready`, or the ticket we joined with).
    pub sync_ticket: String,
    /// Connected peer count from the sidecar.
    pub sync_peers: u64,
}

impl Default for WorkspaceUi {
    fn default() -> Self {
        Self::new()
    }
}

impl WorkspaceUi {
    pub fn new() -> Self {
        Self::from_store(WorkspaceStore::open_default(), local_human_name())
    }

    /// Open against an explicit workspace root (tests / injectable path).
    pub fn open_at(root: impl Into<PathBuf>) -> Self {
        Self::from_store(WorkspaceStore::open_at(root), local_human_name())
    }

    fn from_store(store: WorkspaceStore, human: String) -> Self {
        let (fs_watch, watch_dirty) = start_workspace_watch(store.root());
        let mut s = Self {
            open: false,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
            docked_pane: None,
            host: None,
            channel: DEFAULT_CHANNEL.into(),
            draft: String::new(),
            messages: Vec::new(),
            channels: vec![DEFAULT_CHANNEL.into()],
            members: Vec::new(),
            channel_topic: String::new(),
            pending_clipboard: None,
            clipboard_toast: "Copied",
            status: String::new(),
            mode: ComposeMode::Message,
            mention_sel: 0,
            store,
            human,
            human_id: String::new(),
            scroll: 0,
            refresh_accum: 0.0,
            fs_watch,
            watch_dirty,
            watch_debounce: 0.0,
            delete_pending: None,
            sync_job: None,
            sync_phase: String::new(),
            sync_ticket: String::new(),
            sync_peers: 0,
        };
        s.reload_channels();
        s.reload_messages();
        s.reload_members();
        s.reload_topic();
        // Drain ensure/join events from the initial snapshot load.
        s.watch_dirty.store(false, Ordering::Release);
        s
    }

    /// True when a native recursive watch is running on the workspace root.
    pub fn watch_active(&self) -> bool {
        self.fs_watch.is_some()
    }

    #[cfg(test)]
    fn watch_pending(&self) -> bool {
        self.watch_dirty.load(Ordering::Acquire)
    }

    #[cfg(test)]
    fn clear_watch_dirty(&mut self) {
        self.watch_dirty.store(false, Ordering::Release);
        self.watch_debounce = 0.0;
    }

    /// Drop the watcher so tests can exercise the 1s poll fallback alone.
    #[cfg(test)]
    fn drop_watch(&mut self) {
        self.fs_watch = None;
        self.watch_dirty.store(false, Ordering::Release);
        self.watch_debounce = 0.0;
    }

    pub fn human_name(&self) -> &str {
        &self.human
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01
    }

    pub fn present(&self) -> f32 {
        self.present.clamp(0.0, 1.0)
    }

    /// Docked in a split pane (default product surface).
    pub fn is_docked(&self) -> bool {
        self.docked_pane.is_some() && self.open
    }

    /// Floating overlay card (legacy / pop-out). Not used for the default open path.
    pub fn is_modal(&self) -> bool {
        self.visible() && self.docked_pane.is_none()
    }

    pub fn set_host(&mut self, host: Option<Rect>) {
        self.host = host;
    }

    /// Card used for hit-test + layout: pane glass when docked, else centered modal.
    pub fn card_rect(&self, win_w: f32, win_h: f32) -> Rect {
        self.host
            .unwrap_or_else(|| self.animated_modal_rect(win_w, win_h))
    }

    /// Pack chrome so the rail, title, presence, connect bar, and compose
    /// do not overlap. Compact panes drop the channel rail.
    pub fn layout(&self, win_w: f32, win_h: f32) -> WorkspaceLayout {
        let host = self.card_rect(win_w, win_h);
        let pad = MODAL_PAD;
        let inner = Rect::new(
            host.x + pad,
            host.y + pad,
            (host.w - pad * 2.0).max(0.0),
            (host.h - pad * 2.0).max(0.0),
        );
        let compose_h = COMPOSE_H.min((inner.h * 0.4).max(32.0));
        let compose = Rect::new(
            inner.x,
            inner.y + inner.h - compose_h,
            inner.w,
            compose_h,
        );
        let body_bottom = (compose.y - 6.0).max(inner.y);

        let mut compact = inner.w < COMPACT_INNER_W;
        let mut rail_w = if compact {
            0.0
        } else {
            CHANNEL_LIST_W
                .min(inner.w * 0.32)
                .clamp(72.0, (inner.w * 0.42).max(72.0))
        };
        if !compact && inner.w - rail_w - 10.0 < 160.0 {
            rail_w = (inner.w - 10.0 - 160.0).max(0.0);
        }
        if rail_w < 64.0 {
            compact = true;
            rail_w = 0.0;
        }

        let rail_h = (body_bottom - inner.y).max(0.0);
        let rail = Rect::new(inner.x, inner.y, rail_w, rail_h);
        let col_x = if compact {
            inner.x
        } else {
            inner.x + rail_w + 10.0
        };
        let col_w = (inner.x + inner.w - col_x).max(0.0);

        let header_h = 20.0;
        let add_w = if col_w < 220.0 {
            22.0
        } else {
            ADD_AGENT_CHIP_W
        };
        let add_agent = Rect::new(col_x + col_w - add_w, inner.y, add_w.min(col_w), header_h);

        let docked = self.docked_pane.is_some();
        let (title, presence) = if compact {
            let title_w = (col_w * 0.38).clamp(52.0, 96.0).min(add_agent.x - col_x);
            let title = Rect::new(col_x, inner.y, title_w.max(0.0), header_h);
            let px = title.x + title.w + 6.0;
            let pw = (add_agent.x - px - 6.0).max(0.0);
            (title, Rect::new(px, inner.y, pw, header_h))
        } else if docked {
            // Pane chrome already says "workspace" — don't paint a second title
            // over #general in the rail.
            let pw = (add_agent.x - col_x - 6.0).max(0.0);
            (
                Rect::new(rail.x, rail.y, 0.0, 0.0),
                Rect::new(col_x, inner.y, pw, header_h),
            )
        } else {
            let title = Rect::new(
                rail.x + 6.0,
                rail.y + 2.0,
                (rail.w - 12.0).max(0.0),
                18.0,
            );
            let pw = (add_agent.x - col_x - 6.0).max(0.0);
            (title, Rect::new(col_x, inner.y, pw, header_h))
        };

        let mut y = inner.y + header_h + 2.0;
        let show_topic = body_bottom - y > 80.0;
        let topic = if show_topic {
            let r = Rect::new(col_x, y, col_w, TOPIC_PIN_H);
            y += TOPIC_PIN_H + 2.0;
            r
        } else {
            Rect::new(col_x, y, 0.0, 0.0)
        };

        let stack_connect = col_w < 240.0;
        let connect_h = if stack_connect {
            CONNECT_BAR_H * 2.0 + 2.0
        } else {
            CONNECT_BAR_H
        };
        let show_connect = body_bottom - y > 48.0;
        let connect = if show_connect {
            let r = Rect::new(col_x, y, col_w, connect_h.min(body_bottom - y).max(0.0));
            y += r.h + 4.0;
            r
        } else {
            Rect::new(col_x, y, 0.0, 0.0)
        };

        let messages = Rect::new(col_x, y, col_w, (body_bottom - y).max(0.0));

        WorkspaceLayout {
            host,
            compact,
            rail,
            title,
            presence,
            add_agent,
            topic,
            connect,
            messages,
            compose,
        }
    }

    /// Channel rail width — shrinks in a narrow pane; 0 when compact.
    pub fn channel_list_w(&self, win_w: f32, win_h: f32) -> f32 {
        self.layout(win_w, win_h).rail.w
    }

    /// How many bubbles fit the current card (larger pane → more history).
    pub fn visible_bubble_cap(&self, win_w: f32, win_h: f32) -> usize {
        let usable = self.layout(win_w, win_h).messages.h.max(40.0);
        ((usable / (BUBBLE_MIN_H + BUBBLE_GAP)).floor() as usize).clamp(4, 48)
    }

    pub fn open(&mut self) {
        self.open = true;
        self.mode = ComposeMode::Message;
        self.status.clear();
        self.scroll = 0;
        self.refresh_accum = 0.0;
        self.watch_debounce = 0.0;
        self.delete_pending = None;
        // Join self as human if not already present (product open path).
        self.join_self();
        self.reload_channels();
        self.reload_messages();
        self.reload_members();
        self.reload_topic();
        self.watch_dirty.store(false, Ordering::Release);
        self.status = self.connect_prompt();
    }

    /// Host in a split-pane leaf. Skips the modal spring — jelly split is the motion.
    pub fn dock(&mut self, pane_id: u64) {
        self.docked_pane = Some(pane_id);
        self.open();
        self.present = 1.0;
        self.present_vel = 0.0;
        self.overlay = 0.0;
    }

    /// Register `$USER` as a human member (no-op update if already joined).
    /// Uses a stable local session id so reopen does not mint a new human.
    /// Identity join never posts a #general system line.
    pub fn join_self(&mut self) {
        let sess = crate::workspace_store::local_human_session(&self.human);
        match self.store.join(&self.human, "human", &sess) {
            Ok(m) => {
                self.human_id = m.id;
                self.human = m.name;
            }
            Err(e) => self.status = format!("join: {e}"),
        }
    }

    pub fn close(&mut self) {
        let was_docked = self.docked_pane.is_some();
        self.open = false;
        self.docked_pane = None;
        self.host = None;
        self.draft.clear();
        self.status.clear();
        self.pending_clipboard = None;
        self.mode = ComposeMode::Message;
        self.mention_sel = 0;
        self.refresh_accum = 0.0;
        self.watch_debounce = 0.0;
        self.delete_pending = None;
        if was_docked {
            // No floating modal shrink after a pane close.
            self.present = 0.0;
            self.present_vel = 0.0;
            self.overlay = 0.0;
        }
    }

    pub fn toggle(&mut self) {
        if self.open {
            self.close();
        } else {
            self.open();
        }
    }

    pub fn tick(&mut self, dt: f32) {
        self.poll_sync();
        // Wall time for auto-refresh (do not spring-clamp — long frames still count).
        let wall = dt.max(0.0);
        if self.open {
            if let Some((_, armed_at)) = self.delete_pending {
                if armed_at.elapsed().as_secs_f32() >= DELETE_CONFIRM_SECS {
                    self.delete_pending = None;
                    if self.status.starts_with("Press Ctrl+D again") {
                        self.status.clear();
                    }
                }
            }
            // Native FS watch: reload as soon as disk changes (debounced).
            let mut reloaded = false;
            if self.fs_watch.is_some() && self.watch_dirty.load(Ordering::Acquire) {
                self.watch_debounce += wall;
                if self.watch_debounce >= WATCH_DEBOUNCE_SECS {
                    self.watch_dirty.store(false, Ordering::Release);
                    self.watch_debounce = 0.0;
                    self.reload_from_disk(false);
                    reloaded = true;
                }
            } else {
                self.watch_debounce = 0.0;
            }

            // 1s poll safety net (also the only path if watch setup failed).
            self.refresh_accum += wall;
            if self.refresh_accum >= AUTO_REFRESH_INTERVAL_SECS {
                // Keep residual so hitch-heavy loops do not drift forever.
                self.refresh_accum %= AUTO_REFRESH_INTERVAL_SECS;
                if !reloaded {
                    self.reload_from_disk(false);
                }
            }
        } else {
            self.refresh_accum = 0.0;
            self.watch_debounce = 0.0;
        }

        let dt = wall.clamp(0.0, 1.0 / 20.0);
        let target = if self.open { 1.0 } else { 0.0 };
        const K: f32 = 150.0;
        const C: f32 = 25.0;
        let force = -K * (self.present - target) - C * self.present_vel;
        self.present_vel += force * dt;
        self.present += self.present_vel * dt;
        self.present = self.present.clamp(0.0, 1.08);
        let ov_t = if self.open { 0.5 } else { 0.0 };
        let k = 1.0 - (-dt * 18.0).exp();
        self.overlay += (ov_t - self.overlay) * k;
    }

    pub fn content_ease(&self) -> f32 {
        if self.docked_pane.is_some() {
            return 1.0;
        }
        let t = self.present.clamp(0.0, 1.0);
        t * t * (3.0 - 2.0 * t)
    }

    pub fn scrim_alpha(&self) -> f32 {
        if self.docked_pane.is_some() {
            return 0.0;
        }
        self.overlay.clamp(0.0, 0.5)
    }

    /// Centered overlay card (pop-out / tests). Default open path uses a pane.
    pub fn animated_modal_rect(&self, win_w: f32, win_h: f32) -> Rect {
        let t = self.content_ease();
        // Large: ~92% of the window, capped so ultrawide doesn't go unbounded.
        let base_w = (win_w * 0.92).min(1400.0).max(480.0);
        let base_h = (win_h * 0.88).min(920.0).max(360.0);
        let w = base_w * (0.94 + 0.06 * t);
        let h = base_h * (0.94 + 0.06 * t);
        Rect::new(
            (win_w - w) * 0.5,
            (win_h - h) * 0.46 + (1.0 - t) * -12.0,
            w,
            h,
        )
    }

    // ── compose ──────────────────────────────────────────────────────────────

    pub fn insert_char(&mut self, ch: char) {
        if ch.is_control() {
            return;
        }
        let cap = if self.mode == ComposeMode::JoinTicket {
            MAX_TICKET_RUNES
        } else {
            MAX_BODY_RUNES
        };
        if self.draft.chars().count() >= cap {
            return;
        }
        self.draft.push(ch);
        self.clamp_mention_sel();
    }

    pub fn backspace(&mut self) {
        self.draft.pop();
        self.clamp_mention_sel();
    }

    /// Enter: complete `@mention` if the picker is open; otherwise post / create / attach.
    pub fn send(&mut self) {
        if self.mode == ComposeMode::Message && self.mention_picker_open() {
            let _ = self.complete_mention();
            return;
        }
        match self.mode {
            ComposeMode::NewChannel => self.commit_new_channel(),
            ComposeMode::AttachPath => {
                let path = self.draft.clone();
                self.attach_path(path.trim());
            }
            ComposeMode::PickAgentRole => self.commit_agent_role(),
            ComposeMode::SetTopic => self.commit_topic(),
            ComposeMode::JoinTicket => {
                let ticket = self.draft.clone();
                self.join_with_ticket(ticket.trim());
            }
            ComposeMode::Message => self.post_draft(),
        }
    }

    /// Tab / Shift+Tab: cycle `@mention` picker when open, else cycle channels.
    pub fn tab(&mut self, shift: bool) {
        if self.mode == ComposeMode::Message && self.mention_picker_open() {
            self.cycle_mention(if shift { -1 } else { 1 });
        } else {
            self.cycle_channel(if shift { -1 } else { 1 });
        }
    }

    /// True when message compose has an active `@query` with at least one member match.
    pub fn mention_picker_open(&self) -> bool {
        self.mode == ComposeMode::Message
            && active_mention_query(&self.draft).is_some()
            && !self.mention_candidates().is_empty()
    }

    /// Partial name after `@` for the active trailing mention token, if any.
    pub fn mention_query(&self) -> Option<&str> {
        if self.mode != ComposeMode::Message {
            return None;
        }
        active_mention_query(&self.draft).map(|(_, q)| q)
    }

    /// Members matching the active `@query` prefix (humans first, then name).
    pub fn mention_candidates(&self) -> Vec<&WsMember> {
        let Some(query) = self.mention_query() else {
            return Vec::new();
        };
        let q = query.to_ascii_lowercase();
        let mut out: Vec<&WsMember> = self
            .members
            .iter()
            .filter(|m| m.name.to_ascii_lowercase().starts_with(&q))
            .collect();
        out.sort_by(|a, b| {
            let ak = (a.kind != "human", a.name.to_ascii_lowercase());
            let bk = (b.kind != "human", b.name.to_ascii_lowercase());
            ak.cmp(&bk)
        });
        out
    }

    /// Selected candidate index (0 when picker closed / empty).
    pub fn mention_selected(&self) -> usize {
        let n = self.mention_candidates().len();
        if n == 0 {
            0
        } else {
            self.mention_sel.min(n - 1)
        }
    }

    pub fn cycle_mention(&mut self, delta: i32) {
        let n = self.mention_candidates().len() as i32;
        if n == 0 {
            self.mention_sel = 0;
            return;
        }
        let cur = self.mention_selected() as i32;
        self.mention_sel = (((cur + delta) % n + n) % n) as usize;
    }

    /// Replace the active `@query` with `@Name ` for the selected member.
    pub fn complete_mention(&mut self) -> bool {
        let Some((start, _)) = active_mention_query(&self.draft) else {
            return false;
        };
        let name = {
            let candidates = self.mention_candidates();
            if candidates.is_empty() {
                return false;
            }
            let idx = self.mention_selected();
            candidates[idx].name.clone()
        };
        let mut next = String::with_capacity(self.draft.len() + name.len() + 2);
        next.push_str(&self.draft[..start]);
        next.push('@');
        next.push_str(&name);
        next.push(' ');
        if next.chars().count() > MAX_BODY_RUNES {
            return false;
        }
        self.draft = next;
        self.mention_sel = 0;
        true
    }

    fn clamp_mention_sel(&mut self) {
        let n = self.mention_candidates().len();
        if n == 0 {
            self.mention_sel = 0;
        } else if self.mention_sel >= n {
            self.mention_sel = n - 1;
        }
    }

    /// Compose draft split into plain / `@mention` segments for colored paint.
    /// Mentions match known member names (case-insensitive), painted with member color.
    pub fn compose_highlight_segments(&self) -> Vec<ComposeSeg> {
        highlight_mention_segments(&self.draft, &self.members)
    }

    fn post_draft(&mut self) {
        let body = self.draft.trim().to_string();
        if body.is_empty() {
            return;
        }
        match self
            .store
            .post_as(&self.channel, &body, &self.human_id, &self.human, "human")
        {
            Ok(msg) => {
                self.messages.push(msg);
                if self.messages.len() > HISTORY_LIMIT {
                    let n = self.messages.len() - HISTORY_LIMIT;
                    self.messages.drain(0..n);
                }
                self.draft.clear();
                self.status.clear();
                self.scroll = 0;
            }
            Err(e) => {
                self.status = e;
                eprintln!("workspace post failed: {}", self.status);
            }
        }
    }

    /// Copy `path` into the active channel `files/` and post a file message.
    /// Path string, drop, or native picker (`pick_and_attach`) all land here.
    pub fn attach_path(&mut self, path: impl AsRef<Path>) {
        let path = path.as_ref();
        let path_str = path.to_string_lossy();
        let path_str = path_str.trim();
        if path_str.is_empty() {
            self.status = "file path required".into();
            return;
        }
        match self
            .store
            .upload(&self.channel, path_str, &self.human, "human", "")
        {
            Ok(msg) => {
                let name = msg
                    .file
                    .as_ref()
                    .map(|f| f.name.clone())
                    .unwrap_or_else(|| msg.body.clone());
                self.messages.push(msg);
                if self.messages.len() > HISTORY_LIMIT {
                    let n = self.messages.len() - HISTORY_LIMIT;
                    self.messages.drain(0..n);
                }
                self.draft.clear();
                self.mode = ComposeMode::Message;
                self.status = format!("attached {name}");
                self.scroll = 0;
            }
            Err(e) => {
                self.status = e;
                eprintln!("workspace attach failed: {}", self.status);
            }
        }
    }

    /// Start path-to-attach compose (product Ctrl+U style).
    pub fn begin_attach(&mut self) {
        self.mode = ComposeMode::AttachPath;
        self.draft.clear();
        self.status = "path to attach — Enter to upload".into();
    }

    /// Native OS file picker (macOS / Windows / Linux) then [`attach_path`].
    /// Cancel leaves compose unchanged. Blocking — call from an explicit attach action.
    pub fn pick_and_attach(&mut self) {
        let Some(path) = rfd::FileDialog::new().set_title("Attach file").pick_file() else {
            return;
        };
        self.attach_path(path);
    }

    fn commit_new_channel(&mut self) {
        let name = self.draft.trim().to_string();
        if name.is_empty() {
            self.status = "channel name required".into();
            return;
        }
        match self.store.create_channel(&name, "") {
            Ok(slug) => {
                // Optional system-ish first line (product posts "channel created").
                let _ = self.store.post_as(
                    &slug,
                    "channel created",
                    &self.human_id,
                    &self.human,
                    "human",
                );
                self.channel = slug.clone();
                self.draft.clear();
                self.mode = ComposeMode::Message;
                self.status = format!("created #{slug}");
                self.scroll = 0;
                self.reload_channels();
                self.reload_messages();
            }
            Err(e) => self.status = e,
        }
    }

    /// Start “new channel” compose (Ctrl+N / “+ New” row).
    pub fn begin_new_channel(&mut self) {
        self.mode = ComposeMode::NewChannel;
        self.draft.clear();
        self.status = "new channel name — Enter to create".into();
    }

    /// Cancel new-channel / attach / +Agent / topic mode back to message compose.
    pub fn cancel_mode(&mut self) {
        if self.mode != ComposeMode::Message {
            self.mode = ComposeMode::Message;
            self.draft.clear();
            self.status.clear();
        }
    }

    /// Open the +Agent role picker (does not launch a Grok process).
    pub fn begin_add_agent(&mut self) {
        self.mode = ComposeMode::PickAgentRole;
        self.draft.clear();
        self.status = "pick role: pm · engine · content — copies kickoff".into();
    }

    /// Copy a role kickoff snippet to the host clipboard queue.
    pub fn pick_agent_role(&mut self, role: &str) -> bool {
        let Some(role) = normalize_agent_role(role) else {
            self.status = "role must be pm, engine, or content".into();
            return false;
        };
        let text = agent_kickoff_text(role);
        self.queue_clipboard(text, "Kickoff copied");
        self.mode = ComposeMode::Message;
        self.draft.clear();
        self.status = format!("+Agent {role} kickoff copied — paste into a new Grok session");
        true
    }

    fn commit_agent_role(&mut self) {
        let role = self.draft.clone();
        if !self.pick_agent_role(role.trim()) && self.draft.trim().is_empty() {
            self.status = "pick role: pm · engine · content".into();
        }
    }

    /// Edit the pinned topic for the active channel.
    pub fn begin_set_topic(&mut self) {
        self.mode = ComposeMode::SetTopic;
        self.draft = self.channel_topic.clone();
        self.status = "channel topic — Enter to pin".into();
    }

    fn commit_topic(&mut self) {
        let topic = self.draft.clone();
        match self.store.set_channel_topic(&self.channel, topic.trim()) {
            Ok(saved) => {
                self.channel_topic = saved;
                self.draft.clear();
                self.mode = ComposeMode::Message;
                if self.channel_topic.is_empty() {
                    self.status = "topic cleared".into();
                } else {
                    self.status = format!("topic: {}", self.channel_topic);
                }
            }
            Err(e) => self.status = e,
        }
    }

    /// Host drains this after click/send to copy kickoff text.
    pub fn take_pending_clipboard(&mut self) -> Option<String> {
        self.pending_clipboard.take()
    }

    /// Toast for the last queued clipboard payload (`Kickoff copied` / `Ticket copied`).
    pub fn clipboard_toast(&self) -> &'static str {
        self.clipboard_toast
    }

    fn queue_clipboard(&mut self, text: String, toast: &'static str) {
        if text.is_empty() {
            return;
        }
        self.pending_clipboard = Some(text);
        self.clipboard_toast = toast;
    }

    // ── P2P link (iroh workspace-sync sidecar) ────────────────────────────────

    /// True while the sidecar is running (listening or joined).
    pub fn sync_active(&self) -> bool {
        self.sync_job.is_some()
    }

    /// At least one live peer.
    pub fn sync_is_live(&self) -> bool {
        self.sync_job.is_some() && self.sync_peers > 0
    }

    /// Plain-English line for the connect bar + status footer.
    pub fn connect_prompt(&self) -> String {
        if self.sync_phase == "error" && self.sync_job.is_none() && !self.status.is_empty() {
            return self.status.clone();
        }
        if self.sync_peers > 0 {
            return "Live on both computers".into();
        }
        if self.sync_phase == "connecting" && self.sync_job.is_some() {
            return "Connecting…".into();
        }
        if self.sync_job.is_some() && !self.sync_ticket.is_empty() {
            return "Waiting for them to Join with that ticket".into();
        }
        if self.sync_job.is_some() {
            return "Starting share…".into();
        }
        if self.mode == ComposeMode::JoinTicket {
            return "Paste their ticket below, then Enter".into();
        }
        "Chat with another computer:".into()
    }

    /// Share this workspace: listen and copy a ticket when ready.
    /// If already listening with a ticket, copies it again.
    pub fn share_workspace(&mut self) {
        if self.sync_job.is_some() && !self.sync_ticket.is_empty() {
            self.queue_clipboard(
                self.sync_ticket.clone(),
                "Ticket copied — they paste it under Join",
            );
            self.status = self.connect_prompt();
            return;
        }
        self.start_sync(SyncMode::Listen, "");
    }

    /// Compose: paste a ticket, Enter to join.
    pub fn begin_join_workspace(&mut self) {
        self.mode = ComposeMode::JoinTicket;
        self.draft.clear();
        self.status = self.connect_prompt();
    }

    /// Join with an explicit ticket (palette clipboard path, or compose Enter).
    pub fn join_with_ticket(&mut self, ticket: &str) {
        let ticket = ticket.trim();
        if ticket.is_empty() {
            self.status = "Paste the ticket they copied with Share, then Enter".into();
            return;
        }
        self.mode = ComposeMode::Message;
        self.draft.clear();
        self.start_sync(SyncMode::Join, ticket);
    }

    /// Stop the sidecar. Local chat stays; remote posts stop arriving.
    pub fn disconnect_sync(&mut self) {
        self.cancel_sync();
        self.sync_phase = "stopped".into();
        self.sync_peers = 0;
        self.sync_ticket.clear();
        self.status = "Disconnected — this chat is local again".into();
    }

    /// Copy the current ticket if we have one (⌘C / Copy ticket).
    pub fn copy_link_ticket(&mut self) -> Option<String> {
        if self.sync_ticket.is_empty() {
            return None;
        }
        self.queue_clipboard(
            self.sync_ticket.clone(),
            "Ticket copied — they paste it under Join",
        );
        self.status = self.connect_prompt();
        Some(self.sync_ticket.clone())
    }

    fn start_sync(&mut self, mode: SyncMode, ticket: &str) {
        self.cancel_sync();
        self.sync_peers = 0;
        self.sync_ticket.clear();
        match spawn_sync(mode, self.store.root(), ticket) {
            Ok(job) => {
                self.sync_job = Some(job);
                self.sync_phase = "preparing".into();
                match mode {
                    SyncMode::Listen => {}
                    SyncMode::Join => {
                        self.sync_ticket = ticket.trim().to_string();
                    }
                };
                self.status = self.connect_prompt();
            }
            Err(e) => {
                self.sync_phase = "error".into();
                self.status = e;
            }
        }
    }

    fn cancel_sync(&mut self) {
        if let Some(job) = self.sync_job.take() {
            job.cancel();
        }
    }

    fn poll_sync(&mut self) {
        let Some(job) = self.sync_job.as_ref() else {
            return;
        };
        let mut last_finished = false;
        let mut batch = Vec::new();
        while let Some(u) = job.try_recv() {
            last_finished = u.finished || last_finished;
            batch.push(u);
        }
        let had_events = !batch.is_empty();
        for u in batch {
            if !u.phase.is_empty() {
                self.sync_phase = u.phase;
            }
            if let Some(t) = u.ticket {
                if !t.is_empty() {
                    let first = self.sync_ticket.is_empty();
                    self.sync_ticket = t;
                    if first {
                        self.queue_clipboard(
                            self.sync_ticket.clone(),
                            "Send this ticket to the other computer",
                        );
                    }
                }
            }
            if let Some(n) = u.peers {
                self.sync_peers = n;
            }
            if self.sync_phase == "error" {
                if let Some(msg) = u.message {
                    if !msg.is_empty() {
                        self.status = msg;
                    }
                }
            }
        }
        let finished = last_finished
            || self
                .sync_job
                .as_ref()
                .map(|j| j.is_finished())
                .unwrap_or(false);
        if finished {
            if let Some(job) = self.sync_job.take() {
                drop(job);
            }
            if self.sync_phase != "error" {
                self.sync_peers = 0;
            }
        }
        if (had_events || finished) && self.sync_phase != "error" {
            self.status = self.connect_prompt();
        }
    }

    // ── channels ─────────────────────────────────────────────────────────────

    pub fn select_channel(&mut self, name: &str) {
        if self.channels.iter().any(|c| c == name) {
            self.channel = name.to_string();
            self.mode = ComposeMode::Message;
            self.scroll = 0;
            self.delete_pending = None;
            self.reload_messages();
            self.reload_topic();
        }
    }

    /// Create channel by name without switching compose mode.
    pub fn create_channel(&mut self, name: &str) -> Result<String, String> {
        let slug = self.store.create_channel(name, "")?;
        self.reload_channels();
        self.channel = slug.clone();
        self.scroll = 0;
        self.reload_messages();
        Ok(slug)
    }

    /// Ctrl+D x2: arm then delete the active channel (never `#general`).
    pub fn request_delete_channel(&mut self) {
        if self.mode != ComposeMode::Message {
            self.cancel_mode();
        }
        let slug = self.channel.clone();
        if slug == DEFAULT_CHANNEL || slug.is_empty() {
            self.delete_pending = None;
            self.status = format!("cannot delete #{DEFAULT_CHANNEL}");
            return;
        }
        match &self.delete_pending {
            Some((pending, armed_at))
                if pending == &slug && armed_at.elapsed().as_secs_f32() < DELETE_CONFIRM_SECS =>
            {
                self.confirm_delete_channel();
            }
            _ => {
                self.delete_pending = Some((slug.clone(), Instant::now()));
                self.status = format!("Press Ctrl+D again to delete #{slug}");
            }
        }
    }

    /// Delete the active channel immediately (after confirm). Matches Go `DeleteChannel`.
    pub fn confirm_delete_channel(&mut self) {
        let slug = self.channel.clone();
        self.delete_pending = None;
        match self.store.delete_channel(&slug) {
            Ok(removed) => {
                self.reload_channels();
                // Prefer #general after delete.
                if self.channels.iter().any(|c| c == DEFAULT_CHANNEL) {
                    self.channel = DEFAULT_CHANNEL.into();
                } else if let Some(first) = self.channels.first().cloned() {
                    self.channel = first;
                } else {
                    self.channel = DEFAULT_CHANNEL.into();
                }
                self.mode = ComposeMode::Message;
                self.scroll = 0;
                self.reload_messages();
                self.status = format!("deleted #{removed}");
            }
            Err(e) => self.status = e,
        }
    }

    pub fn cycle_channel(&mut self, delta: i32) {
        if self.channels.is_empty() {
            return;
        }
        let idx = self
            .channels
            .iter()
            .position(|c| c == &self.channel)
            .unwrap_or(0) as i32;
        let n = self.channels.len() as i32;
        let next = ((idx + delta) % n + n) % n;
        let name = self.channels[next as usize].clone();
        self.select_channel(&name);
    }

    /// Reload from disk (MCP / other clients may have written).
    /// Sets status to `"refreshed"` (manual Ctrl+R / mailbox).
    pub fn refresh(&mut self) {
        self.reload_from_disk(true);
    }

    /// Soft reload: same as refresh but no status thrash (auto-poll / mailbox soft).
    /// Preserves stick-to-bottom (`scroll == 0`) or clamps scroll when scrolled up.
    pub fn reload_from_disk(&mut self, announce: bool) {
        let stick_bottom = self.scroll == 0;
        let prev_scroll = self.scroll;
        self.reload_channels();
        self.reload_messages();
        self.reload_members();
        self.reload_topic();
        if stick_bottom {
            self.scroll = 0;
        } else {
            let max = self.messages.len().saturating_sub(1);
            self.scroll = prev_scroll.min(max);
        }
        if announce {
            self.status = "refreshed".into();
        }
    }

    /// Cycle local human availability: idle → working → waiting → blocked → away → idle.
    pub fn cycle_status(&mut self) {
        // Ensure self is in members.json before SetStatus.
        self.join_self();
        let current = self
            .members
            .iter()
            .find(|m| {
                (!self.human_id.is_empty() && m.id == self.human_id)
                    || (m.name == self.human && m.kind == "human")
            })
            .map(|m| m.presence().to_string())
            .unwrap_or_else(|| STATUS_IDLE.into());
        let next = next_availability(&current);
        match self
            .store
            .set_status(&self.human_id, &self.human, next, None)
        {
            Ok(m) => {
                self.reload_members();
                self.status = format!("status: {}", m.presence());
            }
            Err(e) => self.status = e,
        }
    }

    /// Current local human presence code (`idle` if missing).
    pub fn self_status(&self) -> &str {
        self.members
            .iter()
            .find(|m| {
                (!self.human_id.is_empty() && m.id == self.human_id)
                    || (m.name == self.human && m.kind == "human")
            })
            .map(|m| m.presence())
            .unwrap_or(STATUS_IDLE)
    }

    fn reload_channels(&mut self) {
        match self.store.list_channels() {
            Ok(list) => {
                self.channels = list;
                if !self.channels.iter().any(|c| c == &self.channel) {
                    self.channel = self
                        .channels
                        .first()
                        .cloned()
                        .unwrap_or_else(|| DEFAULT_CHANNEL.into());
                }
            }
            Err(e) => self.status = format!("load error: {e}"),
        }
    }

    fn reload_messages(&mut self) {
        match self.store.history(&self.channel, HISTORY_LIMIT) {
            Ok(msgs) => self.messages = msgs,
            Err(e) => {
                self.status = format!("history error: {e}");
                self.messages.clear();
            }
        }
    }

    fn reload_members(&mut self) {
        match self.store.list_members() {
            Ok(list) => self.members = list,
            Err(e) => {
                // Keep prior members; surface error in status only when empty.
                if self.members.is_empty() {
                    self.status = format!("members error: {e}");
                }
            }
        }
    }

    fn reload_topic(&mut self) {
        match self.store.channel_topic(&self.channel) {
            Ok(t) => self.channel_topic = t,
            Err(_) => {}
        }
    }

    /// One chip per member (humans first). Never unique-by-name.
    pub fn members_strip_chips(&self) -> Vec<String> {
        if self.members.is_empty() {
            return vec!["no members yet".into()];
        }
        let mut humans: Vec<&WsMember> = Vec::new();
        let mut agents: Vec<&WsMember> = Vec::new();
        for m in &self.members {
            if m.kind == "human" {
                humans.push(m);
            } else {
                agents.push(m);
            }
        }
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0);
        let ordered: Vec<&WsMember> = humans.into_iter().chain(agents).collect();
        // Soft cap, but never hide a member that shares a display name with one shown.
        const MAX_CHIPS: usize = 8;
        let mut shown_names: Vec<&str> = Vec::new();
        let mut chips: Vec<String> = Vec::new();
        let mut hidden = 0usize;
        for m in ordered {
            let shares = shown_names.iter().any(|n| *n == m.name.as_str());
            if chips.len() >= MAX_CHIPS && !shares {
                hidden += 1;
                continue;
            }
            chips.push(presence_chip(m, &self.members, now));
            shown_names.push(m.name.as_str());
        }
        if hidden > 0 {
            chips.push(format!("+{hidden}"));
        }
        chips
    }

    /// One-line presence strip for renderer paint (humans first, then agents).
    pub fn members_strip_text(&self) -> String {
        self.members_strip_chips().join("  ")
    }

    /// Hit rect for the presence strip (title row, message pane side). Click cycles status.
    /// Stops short of the +Agent chip on the right.
    pub fn presence_strip_rect(&self, win_w: f32, win_h: f32) -> Rect {
        self.layout(win_w, win_h).presence
    }

    /// +Agent chip to the right of the presence strip.
    pub fn add_agent_chip_rect(&self, win_w: f32, win_h: f32) -> Rect {
        self.layout(win_w, win_h).add_agent
    }

    /// Connect bar under the topic pin (sentence + Share/Join or Copy/Disconnect).
    pub fn connect_bar_rect(&self, win_w: f32, win_h: f32) -> Rect {
        self.layout(win_w, win_h).connect
    }

    /// Presence chips that actually fit `max_w` (drop notes, then names, then +N).
    pub fn fitted_presence_chips(&self, max_w: f32) -> Vec<String> {
        let raw = self.members_strip_chips();
        if max_w < 12.0 {
            return Vec::new();
        }
        let tight = max_w < 240.0;
        let mut out: Vec<String> = Vec::new();
        let mut used = 0.0;
        let mut hidden = 0usize;
        let remaining = raw.len();
        for (i, c) in raw.iter().enumerate() {
            let mut label = if tight {
                match c.find(" · ") {
                    Some(p) => c[..p].to_string(),
                    None => c.clone(),
                }
            } else {
                c.clone()
            };
            let w = chip_advance(&label);
            let left = remaining - i - 1;
            let plus_w = if left + hidden > 0 {
                chip_advance(&format!("+{}", left + hidden + 1))
            } else {
                0.0
            };
            if used + w > max_w {
                if out.is_empty() {
                    let chars = ((max_w / 6.4).floor() as usize).max(1);
                    label = truncate_runes(&label, chars);
                    let w = chip_advance(&label);
                    if w <= max_w {
                        out.push(label);
                        used += w;
                    }
                }
                hidden += 1 + left;
                break;
            }
            if used + w + plus_w.min(36.0) > max_w && left > 0 && !out.is_empty() {
                hidden += 1 + left;
                break;
            }
            out.push(label);
            used += w;
        }
        if hidden > 0 {
            let plus = format!("+{hidden}");
            let pw = chip_advance(&plus);
            while !out.is_empty() && used + pw > max_w {
                if let Some(prev) = out.pop() {
                    used -= chip_advance(&prev);
                }
            }
            if used + pw <= max_w {
                out.push(plus);
            }
        }
        out
    }

    /// Buttons on the connect bar, left-to-right.
    pub fn connect_buttons(&self, win_w: f32, win_h: f32) -> Vec<(Rect, &'static str, ConnectAction)> {
        let bar = self.connect_bar_rect(win_w, win_h);
        if bar.w < 8.0 || bar.h < 8.0 {
            return Vec::new();
        }
        let stacked = bar.h > CONNECT_BAR_H + 4.0;
        let btn_h = CONNECT_BAR_H.min(bar.h);
        let btn_y = if stacked {
            bar.y + bar.h - btn_h
        } else {
            bar.y
        };
        let mut right = bar.x + bar.w;
        let mut out: Vec<(Rect, &'static str, ConnectAction)> = Vec::new();
        let mut push = |w: f32, label: &'static str, action: ConnectAction| {
            let w = w.min(bar.w);
            right -= w;
            if right < bar.x - 0.5 {
                right += w;
                return;
            }
            out.push((Rect::new(right, btn_y, w, btn_h), label, action));
            right -= 8.0;
        };
        if self.sync_job.is_some() {
            let stop = if self.sync_is_live() {
                if bar.w < 220.0 {
                    "Stop"
                } else {
                    "Disconnect"
                }
            } else {
                "Cancel"
            };
            push(if stop.len() > 6 { 92.0 } else { 64.0 }, stop, ConnectAction::Stop);
            if !self.sync_ticket.is_empty() {
                let copy = if bar.w < 220.0 { "Copy" } else { "Copy ticket" };
                push(if copy.len() > 5 { 100.0 } else { 56.0 }, copy, ConnectAction::Copy);
            }
        } else if self.mode == ComposeMode::JoinTicket {
            push(72.0, "Cancel", ConnectAction::Stop);
        } else {
            push(56.0, "Join", ConnectAction::Join);
            push(64.0, "Share", ConnectAction::Share);
        }
        out.reverse();
        out
    }

    fn handle_connect_action(&mut self, action: ConnectAction) {
        match action {
            ConnectAction::Share => self.share_workspace(),
            ConnectAction::Join => self.begin_join_workspace(),
            ConnectAction::Copy => {
                let _ = self.copy_link_ticket();
            }
            ConnectAction::Stop => {
                if self.mode == ComposeMode::JoinTicket {
                    self.cancel_mode();
                    self.status = self.connect_prompt();
                } else {
                    self.disconnect_sync();
                }
            }
        }
    }

    /// Pinned topic line under the presence strip (does not scroll).
    pub fn topic_pin_rect(&self, win_w: f32, win_h: f32) -> Rect {
        self.layout(win_w, win_h).topic
    }

    /// Role chip `i` in the +Agent picker (`pm` / `engine` / `content`).
    pub fn agent_role_rect(&self, i: usize, win_w: f32, win_h: f32) -> Rect {
        let pin = self.topic_pin_rect(win_w, win_h);
        let w = 72.0;
        Rect::new(pin.x + i as f32 * (w + 8.0), pin.y, w, TOPIC_PIN_H)
    }

    pub fn topic_pin_text(&self) -> String {
        if self.channel_topic.is_empty() {
            "📌 set topic…".into()
        } else {
            format!("📌 {}", self.channel_topic)
        }
    }

    // ── hit-test ─────────────────────────────────────────────────────────────

    /// Left rail rect holding channel rows (inside the host card).
    pub fn channel_list_rect(&self, win_w: f32, win_h: f32) -> Rect {
        let lay = self.layout(win_w, win_h);
        if lay.compact || lay.rail.w < 8.0 {
            return Rect::new(lay.rail.x, lay.rail.y, 0.0, 0.0);
        }
        let y = if lay.title.h > 8.0 {
            lay.rail.y + 22.0
        } else {
            lay.rail.y + 4.0
        };
        Rect::new(
            lay.rail.x,
            y,
            lay.rail.w,
            (lay.rail.y + lay.rail.h - y).max(0.0),
        )
    }

    /// Hit rect for channel index `i` (same geometry as renderer labels).
    pub fn channel_row_rect(&self, i: usize, win_w: f32, win_h: f32) -> Rect {
        let list = self.channel_list_rect(win_w, win_h);
        Rect::new(
            list.x,
            list.y + i as f32 * CHANNEL_ROW_H,
            list.w,
            CHANNEL_ROW_H,
        )
    }

    /// “+ New channel” row under the last channel.
    pub fn new_channel_row_rect(&self, win_w: f32, win_h: f32) -> Rect {
        self.channel_row_rect(self.channels.len(), win_w, win_h)
    }

    /// Channel slug under `(x,y)`, if any.
    pub fn channel_at(&self, x: f32, y: f32, win_w: f32, win_h: f32) -> Option<String> {
        let list = self.channel_list_rect(win_w, win_h);
        if !list.contains(x, y) {
            return None;
        }
        for (i, ch) in self.channels.iter().enumerate() {
            if self.channel_row_rect(i, win_w, win_h).contains(x, y) {
                return Some(ch.clone());
            }
        }
        None
    }

    /// True if click is on the “+ New channel” row.
    pub fn hits_new_channel(&self, x: f32, y: f32, win_w: f32, win_h: f32) -> bool {
        self.new_channel_row_rect(win_w, win_h).contains(x, y)
    }

    /// Click inside workspace modal: select channel, cycle status, or start new-channel.
    /// Returns true if the click was handled.
    pub fn try_click(&mut self, x: f32, y: f32, win_w: f32, win_h: f32) -> bool {
        if !self.card_rect(win_w, win_h).contains(x, y) {
            return false;
        }
        if let Some(ch) = self.channel_at(x, y, win_w, win_h) {
            self.select_channel(&ch);
            return true;
        }
        if self.hits_new_channel(x, y, win_w, win_h) {
            self.begin_new_channel();
            return true;
        }
        for (r, _, action) in self.connect_buttons(win_w, win_h) {
            if r.contains(x, y) {
                self.handle_connect_action(action);
                return true;
            }
        }
        if self.add_agent_chip_rect(win_w, win_h).contains(x, y) {
            self.begin_add_agent();
            return true;
        }
        if self.mode == ComposeMode::PickAgentRole {
            for (i, role) in AGENT_ROLES.iter().enumerate() {
                if self.agent_role_rect(i, win_w, win_h).contains(x, y) {
                    self.pick_agent_role(role);
                    return true;
                }
            }
        }
        if self.topic_pin_rect(win_w, win_h).contains(x, y) {
            self.begin_set_topic();
            return true;
        }
        if self.presence_strip_rect(win_w, win_h).contains(x, y) {
            self.cycle_status();
            return true;
        }
        // Clicks on message pane / compose keep the modal open (handled).
        true
    }

    /// Visible slice of messages for paint (honors `scroll`, last N).
    pub fn visible_messages(&self, max: usize) -> Vec<&WsMessage> {
        let end = self.messages.len().saturating_sub(self.scroll);
        let start = end.saturating_sub(max);
        self.messages[start..end].iter().collect()
    }

    pub fn scroll_up(&mut self, lines: usize) {
        let max = self.messages.len().saturating_sub(1);
        self.scroll = (self.scroll + lines).min(max);
    }

    pub fn scroll_down(&mut self, lines: usize) {
        self.scroll = self.scroll.saturating_sub(lines);
    }

    /// Message column rect (right of channel list, above compose).
    pub fn message_pane_rect(&self, win_w: f32, win_h: f32) -> Rect {
        self.layout(win_w, win_h).messages
    }

    /// Whether `from` is the local human (mine → right + accent).
    pub fn is_mine(&self, msg: &WsMessage) -> bool {
        msg.from_kind.eq_ignore_ascii_case("human")
            && msg.from.trim().eq_ignore_ascii_case(self.human.trim())
    }

    /// Layout bubbles for the visible window (top → bottom, newest at bottom when stick).
    ///
    /// `accent` is theme primary (jade) used for the local user's bubbles.
    pub fn layout_bubbles(&self, win_w: f32, win_h: f32, accent: [f32; 3]) -> Vec<MsgBubble> {
        let pane = self.message_pane_rect(win_w, win_h);
        let cap = self.visible_bubble_cap(win_w, win_h);
        let msgs = self.visible_messages(cap);
        if msgs.is_empty() {
            return Vec::new();
        }

        let inner_pad = 8.0;
        let col_w = (pane.w - inner_pad * 2.0).max(80.0);
        // Product ~96% of column; keep a side margin for alignment.
        let max_bubble_w = (col_w * 0.92).max(120.0).min(col_w);

        // Measure heights first, then pack from the bottom so stick-bottom feels right.
        let mut measured: Vec<(f32, &WsMessage)> = Vec::with_capacity(msgs.len());
        for msg in &msgs {
            let h = if msg.kind == "system" {
                28.0
            } else {
                // header + body (+ optional second wrap line for long bodies)
                let body_lines = if msg.body.chars().count() > 48 {
                    2.0
                } else {
                    1.0
                };
                (BUBBLE_MIN_H + (body_lines - 1.0) * 14.0).max(BUBBLE_MIN_H)
            };
            measured.push((h, *msg));
        }

        let total_h: f32 = measured.iter().map(|(h, _)| h + BUBBLE_GAP).sum::<f32>() - BUBBLE_GAP;
        let mut y = if total_h < pane.h - inner_pad * 2.0 {
            // Few messages: pin to bottom of pane.
            pane.y + pane.h - inner_pad - total_h
        } else {
            pane.y + inner_pad
        };

        let mut out = Vec::with_capacity(measured.len());
        for (h, msg) in measured {
            if y + h > pane.y + pane.h - 2.0 {
                break;
            }
            let system = msg.kind == "system";
            let mine = !system && self.is_mine(msg);
            let (mem_color, glyph) = member_identity(&msg.from, &msg.from_kind);
            let (tint, tint_strength) = if system {
                ([0.45, 0.5, 0.48], 0.12)
            } else if mine {
                (accent, 0.62)
            } else {
                (mem_color, 0.48)
            };

            let bw = if system {
                col_w
            } else {
                // Hug content a bit: short bodies get a tighter bubble.
                let chars = msg.body.chars().count().max(msg.from.chars().count() + 8);
                let est = (chars as f32 * 7.2 + 28.0).clamp(100.0, max_bubble_w);
                est
            };
            let bx = if system {
                pane.x + inner_pad
            } else if mine {
                pane.x + pane.w - inner_pad - bw
            } else {
                pane.x + inner_pad
            };

            let name = if msg.from.is_empty() {
                "?"
            } else {
                msg.from.as_str()
            };
            let header = if system {
                format!("— {} —", truncate_runes(&msg.body, 48))
            } else if msg.from_kind.eq_ignore_ascii_case("agent") {
                format!("{glyph} {name} · ai")
            } else if msg.kind == "file" {
                format!("{glyph} {name} · file")
            } else {
                format!("{glyph} {name}")
            };
            let body = if system {
                String::new()
            } else if msg.kind == "file" {
                if let Some(f) = &msg.file {
                    format!("📎 {} ({})", f.name, human_bytes(f.bytes))
                } else {
                    truncate_runes(&msg.body, 64)
                }
            } else {
                truncate_runes(&msg.body, 72)
            };

            out.push(MsgBubble {
                rect: Rect::new(bx, y, bw, h),
                mine,
                system,
                tint,
                tint_strength,
                header,
                body,
            });
            y += h + BUBBLE_GAP;
        }
        out
    }
}

/// Recursive native watch on `root`. Returns `(None, flag)` if setup fails;
/// the 1s poll in `tick` then remains the only refresh path.
fn start_workspace_watch(root: &Path) -> (Option<RecommendedWatcher>, Arc<AtomicBool>) {
    let dirty = Arc::new(AtomicBool::new(false));
    let flag = Arc::clone(&dirty);
    let mut watcher =
        match notify::recommended_watcher(move |res: notify::Result<notify::Event>| {
            match res {
                Ok(event) => {
                    // Ignore access/read from our own history() so reloads do not loop.
                    if matches!(event.kind, EventKind::Access(_) | EventKind::Other) {
                        return;
                    }
                    flag.store(true, Ordering::Release);
                }
                Err(_) => flag.store(true, Ordering::Release),
            }
        }) {
            Ok(w) => w,
            Err(_) => return (None, dirty),
        };
    if watcher.watch(root, RecursiveMode::Recursive).is_err() {
        return (None, dirty);
    }
    (Some(watcher), dirty)
}

/// One painted run of compose text (mention highlight).
#[derive(Clone, Debug, PartialEq)]
pub struct ComposeSeg {
    pub text: String,
    /// `Some` member RGB when this run is an `@mention`.
    pub mention_rgb: Option<[f32; 3]>,
}

/// Trailing `@query` token in message compose: `(byte_start, query)`.
fn active_mention_query(draft: &str) -> Option<(usize, &str)> {
    let token_start = match draft.rfind(|c: char| c.is_whitespace()) {
        Some(i) => {
            let ws = draft[i..].chars().next()?;
            i + ws.len_utf8()
        }
        None => 0,
    };
    let token = &draft[token_start..];
    let rest = token.strip_prefix('@')?;
    if rest
        .chars()
        .all(|c| c.is_alphanumeric() || c == '_' || c == '-' || c == '.')
    {
        Some((token_start, rest))
    } else {
        None
    }
}

fn highlight_mention_segments(draft: &str, members: &[WsMember]) -> Vec<ComposeSeg> {
    if draft.is_empty() {
        return Vec::new();
    }
    let mut out = Vec::new();
    let mut plain = String::new();
    let chars: Vec<char> = draft.chars().collect();
    let mut i = 0;
    while i < chars.len() {
        let at_boundary = i == 0 || chars[i - 1].is_whitespace();
        if chars[i] == '@' && at_boundary {
            let mut j = i + 1;
            while j < chars.len()
                && (chars[j].is_alphanumeric()
                    || chars[j] == '_'
                    || chars[j] == '-'
                    || chars[j] == '.')
            {
                j += 1;
            }
            if j > i + 1 {
                let name: String = chars[i + 1..j].iter().collect();
                if let Some(m) = members.iter().find(|m| m.name.eq_ignore_ascii_case(&name)) {
                    if !plain.is_empty() {
                        out.push(ComposeSeg {
                            text: std::mem::take(&mut plain),
                            mention_rgb: None,
                        });
                    }
                    let (rgb, _) = member_identity(&m.name, &m.kind);
                    let typed: String = chars[i..j].iter().collect();
                    out.push(ComposeSeg {
                        text: typed,
                        mention_rgb: Some(rgb),
                    });
                    i = j;
                    continue;
                }
            }
        }
        plain.push(chars[i]);
        i += 1;
    }
    if !plain.is_empty() {
        out.push(ComposeSeg {
            text: plain,
            mention_rgb: None,
        });
    }
    out
}

/// True when `s` looks like an iroh workspace listen ticket (JSON EndpointAddr).
pub fn looks_like_ticket(s: &str) -> bool {
    let s = s.trim();
    let s = s.strip_prefix("ticket:").unwrap_or(s).trim();
    s.starts_with('{') && s.contains("\"id\"") && s.len() >= 24
}

fn chip_advance(s: &str) -> f32 {
    s.chars().count() as f32 * 6.4 + 10.0
}

fn truncate_runes(s: &str, max: usize) -> String {
    let n = s.chars().count();
    if n <= max {
        return s.to_string();
    }
    let mut t: String = s.chars().take(max.saturating_sub(1)).collect();
    t.push('…');
    t
}

fn human_bytes(n: u64) -> String {
    const KB: f64 = 1024.0;
    const MB: f64 = KB * 1024.0;
    let f = n as f64;
    if f >= MB {
        format!("{:.1} MB", f / MB)
    } else if f >= KB {
        format!("{:.1} KB", f / KB)
    } else {
        format!("{n} B")
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::workspace_store::{
        next_availability, STATUS_AWAY, STATUS_BLOCKED, STATUS_IDLE, STATUS_WAITING, STATUS_WORKING,
    };
    use std::fs;
    use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

    fn temp_root(name: &str) -> PathBuf {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or(0);
        let dir = std::env::temp_dir().join(format!(
            "suzuri-ws-ui-{}-{}-{}",
            name,
            std::process::id(),
            nanos
        ));
        let _ = fs::remove_dir_all(&dir);
        dir
    }

    #[test]
    fn member_identity_stable_and_distinct() {
        let (c1, g1) = member_identity("alice", "human");
        let (c2, g2) = member_identity("alice", "human");
        assert_eq!(c1, c2);
        assert_eq!(g1, g2);
        let (c3, _) = member_identity("bob", "human");
        // Different names usually differ; allow rare collision but hash must run.
        let _ = c3;
        let (agent, _) = member_identity("alice", "agent");
        // Agent salt separates same display name.
        assert_ne!(c1, agent);
    }

    #[test]
    fn layout_bubbles_tints_mine_with_accent() {
        let dir = temp_root("bubbles");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.human = "me".into();
        ui.messages.push(WsMessage {
            id: "1".into(),
            channel: "general".into(),
            from_id: "m_me".into(),
            from: "me".into(),
            from_kind: "human".into(),
            kind: "text".into(),
            body: "hello self".into(),
            ts: 1,
            file: None,
            mentions: vec![],
        });
        ui.messages.push(WsMessage {
            id: "2".into(),
            channel: "general".into(),
            from_id: "m_other".into(),
            from: "other".into(),
            from_kind: "human".into(),
            kind: "text".into(),
            body: "hello other".into(),
            ts: 2,
            file: None,
            mentions: vec![],
        });
        let accent = [0.0, 0.9, 0.46];
        let bubbles = ui.layout_bubbles(900.0, 700.0, accent);
        assert_eq!(bubbles.len(), 2);
        let mine = bubbles.iter().find(|b| b.mine).expect("mine bubble");
        assert!((mine.tint[1] - accent[1]).abs() < 0.01);
        assert!(mine.tint_strength > 0.5);
        let other = bubbles.iter().find(|b| !b.mine).expect("other");
        assert!(!other.mine);
        // Other uses member palette, not accent.
        assert!(
            (other.tint[1] - accent[1]).abs() > 0.05 || (other.tint[0] - accent[0]).abs() > 0.05
        );
        // Mine sits further right than other.
        assert!(mine.rect.x > other.rect.x);
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn refresh_reloads_messages_from_disk() {
        let dir = temp_root("refresh");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        let n0 = ui.messages.len();

        // Simulate MCP post: write via a separate store handle.
        let store = WorkspaceStore::open_at(&dir);
        store
            .post("general", "hello from mcp", "agent-bot", "agent")
            .expect("post");

        // UI still has stale snapshot until refresh.
        assert_eq!(ui.messages.len(), n0);
        ui.refresh();
        assert!(
            ui.messages.iter().any(|m| m.body == "hello from mcp"),
            "refresh should load disk messages; got {:?}",
            ui.messages.iter().map(|m| &m.body).collect::<Vec<_>>()
        );
        assert_eq!(ui.status, "refreshed");
        assert_eq!(ui.scroll, 0);
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn docked_card_uses_host_rect_and_no_scrim() {
        let dir = temp_root("dock");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.dock(7);
        assert!(ui.is_docked());
        assert!(!ui.is_modal());
        assert_eq!(ui.scrim_alpha(), 0.0);
        assert!((ui.content_ease() - 1.0).abs() < f32::EPSILON);
        let host = Rect::new(100.0, 80.0, 400.0, 500.0);
        ui.set_host(Some(host));
        let lay = ui.layout(1200.0, 800.0);
        assert!(
            lay.title.w < 1.0,
            "docked pane chrome already names it; interior title overlaps #general"
        );
        let card = ui.card_rect(1200.0, 800.0);
        assert!((card.x - host.x).abs() < 0.01);
        assert!((card.w - host.w).abs() < 0.01);
        assert!((card.h - host.h).abs() < 0.01);
        // Narrow pane shrinks the channel rail.
        assert!(ui.channel_list_w(1200.0, 800.0) < CHANNEL_LIST_W);
        ui.close();
        assert!(!ui.is_docked());
        assert!(!ui.visible());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn modal_card_is_large() {
        let dir = temp_root("modal-size");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.present = 1.0;
        let r = ui.animated_modal_rect(1200.0, 800.0);
        assert!(r.w > 1000.0, "w={}", r.w);
        assert!(r.h > 650.0, "h={}", r.h);
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn soft_reload_preserves_scroll_and_skips_status() {
        let dir = temp_root("soft");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        let store = WorkspaceStore::open_at(&dir);
        for i in 0..5 {
            store
                .post("general", &format!("m{i}"), "writer", "human")
                .unwrap();
        }
        ui.refresh();
        ui.status.clear();
        ui.scroll = 2;
        ui.reload_from_disk(false);
        assert_eq!(ui.scroll, 2);
        assert!(ui.status.is_empty(), "soft reload must not thrash status");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn cycle_status_advances_local_human() {
        let dir = temp_root("cycle");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        assert_eq!(ui.self_status(), STATUS_IDLE);

        ui.cycle_status();
        assert_eq!(ui.self_status(), STATUS_WORKING);
        assert!(ui.status.contains("working"));

        ui.cycle_status();
        assert_eq!(ui.self_status(), STATUS_WAITING);
        ui.cycle_status();
        assert_eq!(ui.self_status(), STATUS_BLOCKED);
        ui.cycle_status();
        assert_eq!(ui.self_status(), STATUS_AWAY);
        ui.cycle_status();
        assert_eq!(ui.self_status(), STATUS_IDLE);

        // Disk persists.
        let store = WorkspaceStore::open_at(&dir);
        let list = store.list_members().unwrap();
        let me = list.iter().find(|m| m.name == ui.human_name()).unwrap();
        assert_eq!(me.presence(), STATUS_IDLE);
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn next_availability_pure_is_exported() {
        // Re-export path used by cycle_status.
        assert_eq!(next_availability(STATUS_IDLE), STATUS_WORKING);
    }

    #[test]
    fn auto_refresh_on_tick_when_open() {
        let dir = temp_root("auto");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        // Isolate the 1s poll fallback from native watch.
        ui.drop_watch();
        let store = WorkspaceStore::open_at(&dir);
        store.post("general", "tick-msg", "bot", "agent").unwrap();
        // Half second — not yet.
        ui.tick(0.5);
        assert!(
            !ui.messages.iter().any(|m| m.body == "tick-msg"),
            "should not refresh before interval"
        );
        // Cross 1s threshold.
        ui.tick(0.6);
        assert!(
            ui.messages.iter().any(|m| m.body == "tick-msg"),
            "auto-refresh should load after ~1s open"
        );
        // Soft: status should not be forced to refreshed.
        assert_ne!(ui.status, "refreshed");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn fs_watch_picks_up_jsonl_write_without_full_poll() {
        let dir = temp_root("watch");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        assert!(
            ui.watch_active(),
            "native FS watch should start on the workspace root"
        );
        ui.clear_watch_dirty();

        let store = WorkspaceStore::open_at(&dir);
        store
            .post("general", "watch-msg", "agent-bot", "agent")
            .expect("post");

        // Wait for the notify thread to flip the dirty flag, then tick past debounce.
        // Do not sleep a full AUTO_REFRESH_INTERVAL_SECS.
        let start = Instant::now();
        let deadline = Duration::from_millis(500);
        while !ui.watch_pending() {
            assert!(
                start.elapsed() < deadline,
                "watch did not observe JSONL write within {deadline:?}"
            );
            std::thread::sleep(Duration::from_millis(5));
        }
        ui.tick(WATCH_DEBOUNCE_SECS + 0.01);
        assert!(
            ui.messages.iter().any(|m| m.body == "watch-msg"),
            "watch should surface JSONL write on a tiny tick; got {:?}",
            ui.messages.iter().map(|m| &m.body).collect::<Vec<_>>()
        );
        assert!(
            start.elapsed().as_secs_f32() < AUTO_REFRESH_INTERVAL_SECS,
            "must observe the write without waiting a full poll interval ({:?})",
            start.elapsed()
        );
        assert_ne!(ui.status, "refreshed");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn mention_query_and_complete() {
        let dir = temp_root("mention");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.human = "alice".into();
        ui.join_self();
        let store = WorkspaceStore::open_at(&dir);
        store.join("bob", "human", "").unwrap();
        store.join("bot", "agent", "s1").unwrap();
        ui.reload_members();

        ui.draft = "hi @b".into();
        assert_eq!(ui.mention_query(), Some("b"));
        let names: Vec<_> = ui
            .mention_candidates()
            .iter()
            .map(|m| m.name.as_str())
            .collect();
        assert!(names.contains(&"bob"));
        assert!(names.contains(&"bot"));
        assert!(ui.mention_picker_open());

        // Prefer humans first in sort — bob before bot.
        assert_eq!(ui.mention_candidates()[0].name, "bob");
        assert!(ui.complete_mention());
        assert_eq!(ui.draft, "hi @bob ");
        assert!(!ui.mention_picker_open());

        // Highlight completed mention.
        let segs = ui.compose_highlight_segments();
        assert!(segs
            .iter()
            .any(|s| s.text == "@bob" && s.mention_rgb.is_some()));

        // Tab cycles when picker open (via tab()).
        ui.draft = "@".into();
        assert!(ui.mention_picker_open());
        let first = ui.mention_candidates()[0].name.clone();
        ui.tab(false);
        assert_ne!(ui.mention_selected(), 0);
        ui.mention_sel = 0;
        ui.send(); // completes, does not post
        assert!(ui.draft.starts_with(&format!("@{first} ")));
        assert!(ui.messages.iter().all(|m| m.body != ui.draft.trim()));

        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn mention_picker_distinguishes_suffixed_names() {
        let dir = temp_root("mention-suffix");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        let store = WorkspaceStore::open_at(&dir);
        let e1 = store.join("engine", "agent", "").unwrap();
        let e2 = store.join("engine", "agent", "").unwrap();
        assert_eq!(e1.name, "engine");
        assert_eq!(e2.name, "engine-2");
        ui.reload_members();

        ui.draft = "@engine".into();
        let names: Vec<_> = ui
            .mention_candidates()
            .iter()
            .map(|m| m.name.as_str())
            .collect();
        assert!(names.contains(&"engine"));
        assert!(names.contains(&"engine-2"));

        ui.draft = "@engine-2".into();
        let names: Vec<_> = ui
            .mention_candidates()
            .iter()
            .map(|m| m.name.as_str())
            .collect();
        assert_eq!(names, vec!["engine-2"]);
        assert!(ui.complete_mention());
        assert_eq!(ui.draft, "@engine-2 ");

        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn active_mention_ignores_mid_word_at() {
        assert!(active_mention_query("email@x").is_none());
        assert_eq!(active_mention_query("@al").unwrap().1, "al");
        assert_eq!(active_mention_query("hey @Al").unwrap().1, "Al");
    }

    #[test]
    fn begin_attach_sets_path_mode() {
        let dir = temp_root("begin-attach");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.begin_attach();
        assert_eq!(ui.mode, ComposeMode::AttachPath);
        assert!(ui.draft.is_empty());
        assert_eq!(ui.status, "path to attach — Enter to upload");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn attach_path_posts_file_message() {
        let dir = temp_root("attach-path");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        let src = dir.join("notes.txt");
        fs::write(&src, b"hello picker").unwrap();
        ui.attach_path(&src);
        assert_eq!(ui.mode, ComposeMode::Message);
        assert_eq!(ui.status, "attached notes.txt");
        assert_eq!(ui.messages.last().map(|m| m.kind.as_str()), Some("file"));
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn chrome_ui_post_uses_joiner_member_id() {
        let dir = temp_root("ui-fromid");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.human = "alice".into();
        ui.open();
        assert!(!ui.human_id.is_empty(), "join_self should store member id");
        ui.draft = "hello from chrome".into();
        ui.send();
        let last = ui.messages.last().expect("posted");
        assert_eq!(last.body, "hello from chrome");
        assert_eq!(last.from_id, ui.human_id);
        assert_eq!(last.from, "alice");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn strip_keeps_engine_and_engine_2() {
        let dir = temp_root("strip-names");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        let store = WorkspaceStore::open_at(&dir);
        store.join("engine", "agent", "s1").unwrap();
        store.join("engine-2", "agent", "s2").unwrap();
        ui.reload_members();
        let text = ui.members_strip_text();
        assert!(text.contains("engine"), "{text}");
        assert!(text.contains("engine-2"), "{text}");
        let chips = ui.members_strip_chips();
        let named: Vec<_> = chips.iter().filter(|c| c.contains("engine")).collect();
        assert!(named.len() >= 2, "expected two engine chips, got {chips:?}");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn open_does_not_post_join_noise() {
        let dir = temp_root("quiet-open");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        assert!(
            ui.messages
                .iter()
                .all(|m| !m.body.contains("joined the workspace")),
            "UI join must be quiet: {:?}",
            ui.messages.iter().map(|m| &m.body).collect::<Vec<_>>()
        );
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn pick_agent_role_queues_kickoff() {
        let dir = temp_root("kickoff-ui");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.begin_add_agent();
        assert_eq!(ui.mode, ComposeMode::PickAgentRole);
        assert!(ui.pick_agent_role("engine"));
        let text = ui.take_pending_clipboard().expect("kickoff");
        assert!(text.contains("engine"));
        assert!(text.contains("workspace_guide"));
        assert!(text.contains("workspace_claim_role"));
        assert!(text.contains("workspace_wait"));
        assert_eq!(ui.mode, ComposeMode::Message);
        assert!(ui.status.contains("engine"));
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn set_topic_pins_above_chat() {
        let dir = temp_root("topic-ui");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        assert!(ui.topic_pin_text().contains("set topic"));
        ui.begin_set_topic();
        ui.draft = "control plane".into();
        ui.send();
        assert_eq!(ui.channel_topic, "control plane");
        assert!(ui.topic_pin_text().contains("control plane"));
        assert_eq!(ui.mode, ComposeMode::Message);
        let pin = ui.topic_pin_rect(900.0, 700.0);
        let pane = ui.message_pane_rect(900.0, 700.0);
        assert!(
            pin.y + pin.h <= pane.y + 0.5,
            "pin must not scroll with chat"
        );
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn looks_like_ticket_accepts_json_endpoint_addr() {
        assert!(looks_like_ticket(
            r#"{"id":"abc123abc123abc123abc123abc123ab"}"#
        ));
        assert!(looks_like_ticket(
            r#"ticket: {"id":"abc123abc123abc123abc123abc123ab"}"#
        ));
        assert!(!looks_like_ticket("hello"));
        assert!(!looks_like_ticket("{short}"));
    }

    #[test]
    fn begin_join_sets_ticket_mode() {
        let dir = temp_root("join-mode");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.begin_join_workspace();
        assert_eq!(ui.mode, ComposeMode::JoinTicket);
        assert!(ui.draft.is_empty());
        assert!(ui.status.contains("ticket"));
        ui.cancel_mode();
        assert_eq!(ui.mode, ComposeMode::Message);
        let _ = fs::remove_dir_all(&dir);
    }

    fn rects_overlap(a: Rect, b: Rect) -> bool {
        a.w > 0.5
            && a.h > 0.5
            && b.w > 0.5
            && b.h > 0.5
            && a.x < b.x + b.w - 0.5
            && b.x < a.x + a.w - 0.5
            && a.y < b.y + b.h - 0.5
            && b.y < a.y + a.h - 0.5
    }

    #[test]
    fn connect_bar_idle_has_share_and_join() {
        let dir = temp_root("connect-bar");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.set_host(Some(Rect::new(0.0, 0.0, 800.0, 600.0)));
        let bar = ui.connect_bar_rect(800.0, 600.0);
        let pin = ui.topic_pin_rect(800.0, 600.0);
        let pane = ui.message_pane_rect(800.0, 600.0);
        assert!(bar.y + 0.5 >= pin.y + pin.h);
        assert!(pane.y + 0.5 >= bar.y + bar.h);
        let labels: Vec<_> = ui
            .connect_buttons(800.0, 600.0)
            .into_iter()
            .map(|(_, label, action)| (label, action))
            .collect();
        assert_eq!(
            labels,
            vec![
                ("Share", ConnectAction::Share),
                ("Join", ConnectAction::Join)
            ]
        );
        assert!(ui.connect_prompt().contains("computer"));
        assert!(!ui.sync_active());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn compact_layout_hides_rail_and_fits_header() {
        let dir = temp_root("compact-lay");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.set_host(Some(Rect::new(0.0, 0.0, 280.0, 420.0)));
        let lay = ui.layout(280.0, 420.0);
        assert!(lay.compact, "280px pane should drop the channel rail");
        assert!(lay.rail.w < 1.0);
        assert!(lay.messages.w > 100.0);
        assert!(lay.add_agent.x + lay.add_agent.w <= lay.host.x + lay.host.w - 10.0);
        assert!(!rects_overlap(lay.presence, lay.add_agent));
        assert!(!rects_overlap(lay.title, lay.presence));
        assert!(!rects_overlap(lay.connect, lay.messages));
        assert!(!rects_overlap(lay.messages, lay.compose));
        let btns = ui.connect_buttons(280.0, 420.0);
        assert!(
            btns.iter().any(|(_, l, _)| *l == "Share"),
            "share still reachable when scrunched: {btns:?}"
        );
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn wide_layout_keeps_rail_and_no_header_overlap() {
        let dir = temp_root("wide-lay");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.set_host(Some(Rect::new(40.0, 40.0, 720.0, 560.0)));
        let lay = ui.layout(900.0, 700.0);
        assert!(!lay.compact);
        assert!(lay.rail.w >= 72.0);
        assert!(!rects_overlap(lay.rail, lay.messages));
        assert!(!rects_overlap(lay.presence, lay.add_agent));
        assert!(!rects_overlap(lay.topic, lay.connect));
        assert!(lay.topic.y + lay.topic.h <= lay.connect.y + 0.5);
        assert!(lay.connect.y + lay.connect.h <= lay.messages.y + 0.5);
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn presence_chips_fit_strip_width() {
        let dir = temp_root("fit-chips");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        let store = WorkspaceStore::open_at(&dir);
        store.join("stephenhorton", "human", "s-a").unwrap();
        store.join("other-human", "human", "s-b").unwrap();
        ui.reload_members();
        let fitted = ui.fitted_presence_chips(90.0);
        let used: f32 = fitted.iter().map(|c| chip_advance(c)).sum();
        assert!(
            used <= 90.0 + 0.5,
            "chips {fitted:?} used {used}px > 90"
        );
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn join_ticket_compose_allows_long_paste() {
        let dir = temp_root("long-ticket");
        let mut ui = WorkspaceUi::open_at(&dir);
        ui.open();
        ui.begin_join_workspace();
        for _ in 0..(MAX_BODY_RUNES + 40) {
            ui.insert_char('a');
        }
        assert!(ui.draft.chars().count() > MAX_BODY_RUNES);
        let _ = fs::remove_dir_all(&dir);
    }
}
