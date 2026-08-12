//! Shared workspace chat — glass modal over local channels + messages.
//!
//! Data lives under `~/Library/Application Support/suzuri/workspace/` (macOS)
//! via [`crate::workspace_store`], matching product `internal/workspace`.
//!
//! Public surface used by `app` / `renderer` (preserve):
//! - fields: `open`, `channel`, `draft`, `messages`, `channels`, `members`
//! - glass: `visible`, `open`, `close`, `toggle`, `tick`, `content_ease`,
//!   `scrim_alpha`, `animated_modal_rect`
//! - compose: `insert_char`, `backspace`, `send`
//! - channels: `select_channel`
//! - presence / attach: `join_self`, `attach_path`, `members_strip_text`,
//!   `cycle_status`, `refresh` / soft auto-reload while open
//!
//! See `chrome/WORKSPACE_HOOKS.md` for hit-test layout constants and hooks.

use std::path::{Path, PathBuf};

use crate::layout::Rect;
use crate::workspace_store::{
    local_human_name, member_chip, next_availability, WorkspaceStore, DEFAULT_CHANNEL,
    HISTORY_LIMIT, MAX_BODY_RUNES, STATUS_IDLE,
};

/// How often an open workspace reloads channels / messages / members from disk
/// (MCP posts write JSONL; product `RefreshWorkspaceMsg` equivalent).
pub const AUTO_REFRESH_INTERVAL_SECS: f32 = 1.0;

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
/// Max message bubbles painted at once (also scroll window size).
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
}

pub struct WorkspaceUi {
    pub open: bool,
    present: f32,
    present_vel: f32,
    overlay: f32,
    pub channel: String,
    pub draft: String,
    pub messages: Vec<WsMessage>,
    pub channels: Vec<String>,
    /// Presence list for paint (product `members.json`).
    pub members: Vec<WsMember>,
    /// Ephemeral status (create errors, etc.).
    pub status: String,
    pub mode: ComposeMode,
    store: WorkspaceStore,
    human: String,
    /// Scroll: how many messages from the end are hidden (0 = pin bottom).
    pub scroll: usize,
    /// Accumulator for auto-refresh while open (seconds).
    refresh_accum: f32,
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
        let mut s = Self {
            open: false,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
            channel: DEFAULT_CHANNEL.into(),
            draft: String::new(),
            messages: Vec::new(),
            channels: vec![DEFAULT_CHANNEL.into()],
            members: Vec::new(),
            status: String::new(),
            mode: ComposeMode::Message,
            store,
            human,
            scroll: 0,
            refresh_accum: 0.0,
        };
        s.reload_channels();
        s.reload_messages();
        s.reload_members();
        s
    }

    pub fn human_name(&self) -> &str {
        &self.human
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01
    }

    pub fn open(&mut self) {
        self.open = true;
        self.mode = ComposeMode::Message;
        self.status.clear();
        self.scroll = 0;
        self.refresh_accum = 0.0;
        // Join self as human if not already present (product open path).
        self.join_self();
        self.reload_channels();
        self.reload_messages();
        self.reload_members();
    }

    /// Register `$USER` as a human member (no-op update if already joined).
    pub fn join_self(&mut self) {
        match self.store.join(&self.human, "human", "") {
            Ok(_) => {}
            Err(e) => self.status = format!("join: {e}"),
        }
    }

    pub fn close(&mut self) {
        self.open = false;
        self.draft.clear();
        self.status.clear();
        self.mode = ComposeMode::Message;
        self.refresh_accum = 0.0;
    }

    pub fn toggle(&mut self) {
        if self.open {
            self.close();
        } else {
            self.open();
        }
    }

    pub fn tick(&mut self, dt: f32) {
        // Wall time for auto-refresh (do not spring-clamp — long frames still count).
        let wall = dt.max(0.0);
        if self.open {
            self.refresh_accum += wall;
            if self.refresh_accum >= AUTO_REFRESH_INTERVAL_SECS {
                // Keep residual so hitch-heavy loops do not drift forever.
                self.refresh_accum %= AUTO_REFRESH_INTERVAL_SECS;
                self.reload_from_disk(false);
            }
        } else {
            self.refresh_accum = 0.0;
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
        let t = self.present.clamp(0.0, 1.0);
        t * t * (3.0 - 2.0 * t)
    }

    pub fn scrim_alpha(&self) -> f32 {
        self.overlay.clamp(0.0, 0.5)
    }

    pub fn animated_modal_rect(&self, win_w: f32, win_h: f32) -> Rect {
        let t = self.content_ease();
        let base_w = 720.0_f32.min(win_w * 0.92).max(400.0);
        let base_h = 440.0_f32.min(win_h * 0.78).max(280.0);
        let w = base_w * (0.9 + 0.1 * t);
        let h = base_h * (0.92 + 0.08 * t);
        Rect::new(
            (win_w - w) * 0.5,
            (win_h - h) * 0.42 + (1.0 - t) * -16.0,
            w,
            h,
        )
    }

    // ── compose ──────────────────────────────────────────────────────────────

    pub fn insert_char(&mut self, ch: char) {
        if ch.is_control() {
            return;
        }
        if self.draft.chars().count() >= MAX_BODY_RUNES {
            return;
        }
        self.draft.push(ch);
    }

    pub fn backspace(&mut self) {
        self.draft.pop();
    }

    /// Enter: post message, create channel, or attach path (by [`ComposeMode`]).
    pub fn send(&mut self) {
        match self.mode {
            ComposeMode::NewChannel => self.commit_new_channel(),
            ComposeMode::AttachPath => {
                let path = self.draft.clone();
                self.attach_path(path.trim());
            }
            ComposeMode::Message => self.post_draft(),
        }
    }

    fn post_draft(&mut self) {
        let body = self.draft.trim().to_string();
        if body.is_empty() {
            return;
        }
        match self
            .store
            .post(&self.channel, &body, &self.human, "human")
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
    /// Accepts a filesystem path string (no OS file dialog required).
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

    fn commit_new_channel(&mut self) {
        let name = self.draft.trim().to_string();
        if name.is_empty() {
            self.status = "channel name required".into();
            return;
        }
        match self.store.create_channel(&name, "") {
            Ok(slug) => {
                // Optional system-ish first line (product posts "channel created").
                let _ = self
                    .store
                    .post(&slug, "channel created", &self.human, "human");
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

    /// Cancel new-channel / attach mode back to message compose.
    pub fn cancel_mode(&mut self) {
        if self.mode != ComposeMode::Message {
            self.mode = ComposeMode::Message;
            self.draft.clear();
            self.status.clear();
        }
    }

    // ── channels ─────────────────────────────────────────────────────────────

    pub fn select_channel(&mut self, name: &str) {
        if self.channels.iter().any(|c| c == name) {
            self.channel = name.to_string();
            self.mode = ComposeMode::Message;
            self.scroll = 0;
            self.reload_messages();
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
            .find(|m| m.name == self.human && m.kind == "human")
            .map(|m| m.presence().to_string())
            .unwrap_or_else(|| STATUS_IDLE.into());
        let next = next_availability(&current);
        match self.store.set_status("", &self.human, next, None) {
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
            .find(|m| m.name == self.human && m.kind == "human")
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

    /// One-line presence strip for renderer paint (humans first, then agents).
    pub fn members_strip_text(&self) -> String {
        if self.members.is_empty() {
            return "no members yet".into();
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
        let mut chips: Vec<String> = humans
            .iter()
            .chain(agents.iter())
            .map(|m| member_chip(m))
            .collect();
        // Soft cap so the strip stays one visual line in the modal.
        const MAX_CHIPS: usize = 6;
        let hidden = chips.len().saturating_sub(MAX_CHIPS);
        if hidden > 0 {
            chips.truncate(MAX_CHIPS);
            chips.push(format!("+{hidden}"));
        }
        chips.join("  ")
    }

    /// Hit rect for the presence strip (title row, message pane side). Click cycles status.
    pub fn presence_strip_rect(&self, win_w: f32, win_h: f32) -> Rect {
        let modal = self.animated_modal_rect(win_w, win_h);
        Rect::new(
            modal.x + MODAL_PAD + CHANNEL_LIST_W + 10.0,
            modal.y + 4.0,
            (modal.w - MODAL_PAD * 2.0 - CHANNEL_LIST_W - 10.0).max(40.0),
            PRESENCE_STRIP_H + 4.0,
        )
    }

    // ── hit-test ─────────────────────────────────────────────────────────────

    /// Left rail rect holding channel rows (inside the animated modal).
    pub fn channel_list_rect(&self, win_w: f32, win_h: f32) -> Rect {
        let modal = self.animated_modal_rect(win_w, win_h);
        Rect::new(
            modal.x + MODAL_PAD,
            modal.y + CHANNEL_LIST_TOP,
            CHANNEL_LIST_W,
            (modal.h - CHANNEL_LIST_TOP - COMPOSE_H - MODAL_PAD).max(0.0),
        )
    }

    /// Hit rect for channel index `i` (same geometry as renderer labels).
    pub fn channel_row_rect(&self, i: usize, win_w: f32, win_h: f32) -> Rect {
        let modal = self.animated_modal_rect(win_w, win_h);
        Rect::new(
            modal.x + MODAL_PAD,
            modal.y + CHANNEL_LIST_TOP + i as f32 * CHANNEL_ROW_H,
            CHANNEL_LIST_W,
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
        if !self.animated_modal_rect(win_w, win_h).contains(x, y) {
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
        let modal = self.animated_modal_rect(win_w, win_h);
        let pad = MODAL_PAD;
        let ch_x = modal.x + pad;
        let ch_w = CHANNEL_LIST_W;
        let msg_x = ch_x + ch_w + 10.0;
        let msg_w = (modal.x + modal.w - pad - msg_x).max(40.0);
        let msg_y = modal.y + pad + PRESENCE_STRIP_H;
        let msg_h = (modal.h - pad * 2.0 - COMPOSE_H - PRESENCE_STRIP_H - 4.0).max(40.0);
        Rect::new(msg_x, msg_y, msg_w, msg_h)
    }

    /// Whether `from` is the local human (mine → right + accent).
    pub fn is_mine(&self, msg: &WsMessage) -> bool {
        msg.from_kind.eq_ignore_ascii_case("human")
            && msg.from.trim().eq_ignore_ascii_case(self.human.trim())
    }

    /// Layout bubbles for the visible window (top → bottom, newest at bottom when stick).
    ///
    /// `accent` is theme primary (jade) used for the local user's bubbles.
    pub fn layout_bubbles(
        &self,
        win_w: f32,
        win_h: f32,
        accent: [f32; 3],
    ) -> Vec<MsgBubble> {
        let pane = self.message_pane_rect(win_w, win_h);
        let msgs = self.visible_messages(VISIBLE_BUBBLE_CAP);
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
                let body_lines = if msg.body.chars().count() > 48 { 2.0 } else { 1.0 };
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
        next_availability, STATUS_AWAY, STATUS_BLOCKED, STATUS_IDLE, STATUS_WAITING,
        STATUS_WORKING,
    };
    use std::fs;
    use std::time::{SystemTime, UNIX_EPOCH};

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
            from: "me".into(),
            from_kind: "human".into(),
            kind: "text".into(),
            body: "hello self".into(),
            ts: 1,
            file: None,
        });
        ui.messages.push(WsMessage {
            id: "2".into(),
            channel: "general".into(),
            from: "other".into(),
            from_kind: "human".into(),
            kind: "text".into(),
            body: "hello other".into(),
            ts: 2,
            file: None,
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
        assert!((other.tint[1] - accent[1]).abs() > 0.05 || (other.tint[0] - accent[0]).abs() > 0.05);
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
        let store = WorkspaceStore::open_at(&dir);
        store
            .post("general", "tick-msg", "bot", "agent")
            .unwrap();
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
}


