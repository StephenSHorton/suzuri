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


