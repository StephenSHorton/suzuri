//! P2P transfer prompt — shells out to `suzuri-transfer` when available
//! (same sidecar as product Go host).
//!
//! Progress is streamed on a background thread (`transfer_engine`) via NDJSON
//! `--json` events; [`TransferUi::tick`] drains the channel into `status` /
//! `ticket` / phase fields so the render loop can poll without blocking.
//!
//! OS file drops (send) and ticket copy (⌘C / chip) mirror product
//! `internal/chrome/transfer.go` drop-hover + copy-flash UX.

use std::path::{Path, PathBuf};

use crate::layout::Rect;

#[path = "transfer_engine.rs"]
mod transfer_engine;

use transfer_engine::{spawn_engine, EngineJob, EngineMode};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum TransferMode {
    Send,
    Receive,
}

pub struct TransferUi {
    pub open: bool,
    present: f32,
    present_vel: f32,
    overlay: f32,
    pub mode: TransferMode,
    /// Path (send) or ticket words (receive).
    pub buf: String,
    /// Short status / error / progress line for the glass modal.
    pub status: String,
    /// Ticket string once the engine emits `ready` (send) or seed ticket (receive).
    pub ticket: String,
    /// Machine-mode phase: preparing | ready | receiving | progress | done | error | stopped
    pub phase: String,
    pub done_bytes: u64,
    pub total_bytes: u64,
    /// True while an OS file drag is over the window (send prompt only).
    pub drop_hover: bool,
    /// Transient drop feedback ("dropped — press enter…", multi-item note).
    pub drop_hint: String,
    /// "Copied!" flash after ticket copy; empty when idle.
    pub copy_flash: String,
    /// Seconds remaining to show `copy_flash` (cleared by [`tick`]).
    copy_flash_t: f32,
    /// Extra paths from multi-file drop (engine send takes one; first is in `buf`).
    pub queue: Vec<String>,
    job: Option<EngineJob>,
}

impl Default for TransferUi {
    fn default() -> Self {
        Self::new()
    }
}

impl TransferUi {
    pub fn new() -> Self {
        Self {
            open: false,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
            mode: TransferMode::Send,
            buf: String::new(),
            status: String::new(),
            ticket: String::new(),
            phase: String::new(),
            done_bytes: 0,
            total_bytes: 0,
            drop_hover: false,
            drop_hint: String::new(),
            copy_flash: String::new(),
            copy_flash_t: 0.0,
            queue: Vec::new(),
            job: None,
        }
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01
    }

    /// Host should only route OS drops as transfer-send input when this is true
    /// (matches product `AcceptsFileDrop`).
    pub fn accepts_file_drop(&self) -> bool {
        self.open && self.mode == TransferMode::Send && !self.is_running()
    }

    pub fn open_send(&mut self) {
        self.cancel_job();
        self.mode = TransferMode::Send;
        self.open = true;
        self.buf.clear();
        self.status = "Path to send — Enter to start · Esc cancel".into();
        self.ticket.clear();
        self.phase.clear();
        self.done_bytes = 0;
        self.total_bytes = 0;
        self.drop_hover = false;
        self.drop_hint.clear();
        self.copy_flash.clear();
        self.copy_flash_t = 0.0;
        self.queue.clear();
    }

    pub fn open_receive(&mut self) {
        self.cancel_job();
        self.mode = TransferMode::Receive;
        self.open = true;
        self.buf.clear();
        self.status = "Paste ticket words — Enter to receive · Esc cancel".into();
        self.ticket.clear();
        self.phase.clear();
        self.done_bytes = 0;
        self.total_bytes = 0;
        self.drop_hover = false;
        self.drop_hint.clear();
        self.copy_flash.clear();
        self.copy_flash_t = 0.0;
        self.queue.clear();
    }

    pub fn close(&mut self) {
        self.cancel_job();
        self.open = false;
        self.buf.clear();
        self.drop_hover = false;
        self.drop_hint.clear();
        self.copy_flash.clear();
        self.copy_flash_t = 0.0;
        self.queue.clear();
    }

    pub fn is_running(&self) -> bool {
        // Send stays in `ready` while the engine serves the ticket.
        self.job.is_some()
    }

    /// OS drag-enter / drag-leave feedback (send prompt only).
    pub fn set_drop_hover(&mut self, hover: bool) {
        if !self.accepts_file_drop() {
            self.drop_hover = false;
            return;
        }
        self.drop_hover = hover;
    }

    /// Fill send path from OS file drop. First path → `buf`; extras → `queue`.
    /// Returns `true` if the drop was accepted (caller may redraw).
    pub fn on_paths_dropped<P: AsRef<Path>>(&mut self, paths: &[P]) -> bool {
        if !self.accepts_file_drop() {
            return false;
        }
        self.drop_hover = false;
        let mut clean: Vec<String> = Vec::with_capacity(paths.len());
        for p in paths {
            let s = path_display(p.as_ref());
            let s = s.trim();
            if !s.is_empty() {
                clean.push(s.to_string());
            }
        }
        if clean.is_empty() {
            self.drop_hint = "drop ignored — no path".into();
            self.status = self.drop_hint.clone();
            return true;
        }
        self.buf = clean[0].clone();
        self.queue = clean[1..].to_vec();
        if clean.len() == 1 {
            self.drop_hint = "dropped — press enter to send".into();
            self.status = self.drop_hint.clone();
        } else {
            self.drop_hint = format!("using first of {} items — enter to send", clean.len());
            self.status = self.drop_hint.clone();
        }
        true
    }

    /// Convenience for a single winit `DroppedFile` path.
    pub fn on_path_dropped(&mut self, path: PathBuf) -> bool {
        self.on_paths_dropped(&[path])
    }

    /// Returns the ticket when present and arms the "Copied!" flash.
    /// Caller writes the returned string to the system clipboard (arboard).
    pub fn copy_ticket(&mut self) -> Option<String> {
        if self.ticket.is_empty() {
            return None;
        }
        self.copy_flash = "Copied!".into();
        self.copy_flash_t = 1.6;
        Some(self.ticket.clone())
    }

    pub fn tick(&mut self, dt: f32) {
        self.poll_engine();

        let dt = dt.clamp(0.0, 1.0 / 20.0);
        if self.copy_flash_t > 0.0 {
            self.copy_flash_t = (self.copy_flash_t - dt).max(0.0);
            if self.copy_flash_t <= 0.0 {
                self.copy_flash.clear();
            }
        }

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
        let w = 560.0_f32.min(win_w * 0.88).max(320.0) * (0.9 + 0.1 * t);
        // Extra height for drop zone (send), ticket / progress / copy chip.
        let mut base_h = 220.0_f32;
        if self.mode == TransferMode::Send && !self.is_running() {
            base_h += 36.0; // drop zone row
        }
        if !self.ticket.is_empty() || self.total_bytes > 0 || self.is_running() {
            base_h = base_h.max(280.0);
            if !self.ticket.is_empty() {
                base_h += 28.0; // copy chip row
            }
        }
        let h = base_h * (0.9 + 0.1 * t);
        Rect::new(
            (win_w - w) * 0.5,
            (win_h - h) * 0.4 + (1.0 - t) * -12.0,
            w,
            h,
        )
    }

    pub fn insert_char(&mut self, ch: char) {
        if self.job.is_some() && self.is_running() {
            return;
        }
        self.buf.push(ch);
    }

    pub fn backspace(&mut self) {
        if self.job.is_some() && self.is_running() {
            return;
        }
        self.buf.pop();
    }

    /// Start transfer on a background thread. Updates status/ticket via [`tick`].
    pub fn submit(&mut self) {
        let val = self.buf.trim().to_string();
        if val.is_empty() {
            self.status = "Enter a path or ticket first".into();
            return;
        }

        // Replace any prior job.
        self.cancel_job();
        self.ticket.clear();
        self.done_bytes = 0;
        self.total_bytes = 0;
        self.drop_hover = false;
        self.drop_hint.clear();
        self.copy_flash.clear();
        self.copy_flash_t = 0.0;

        let mode = match self.mode {
            TransferMode::Send => EngineMode::Send,
            TransferMode::Receive => EngineMode::Receive,
        };

        match spawn_engine(mode, &val) {
            Ok(job) => {
                self.phase = "preparing".into();
                self.status = match self.mode {
                    TransferMode::Send => format!("Sending {val} …"),
                    TransferMode::Receive => {
                        self.ticket = val.clone();
                        "Receiving …".into()
                    }
                };
                self.job = Some(job);
            }
            Err(msg) => {
                self.phase = "error".into();
                self.status = msg;
            }
        }
    }

    fn cancel_job(&mut self) {
        if let Some(job) = self.job.take() {
            job.cancel();
        }
    }

    fn poll_engine(&mut self) {
        let Some(job) = self.job.as_ref() else {
            return;
        };

        // Drain all pending updates this frame.
        let mut last_finished = false;
        let mut batch = Vec::new();
        while let Some(u) = job.try_recv() {
            last_finished = u.finished || last_finished;
            batch.push(u);
        }

        for u in batch {
            // Empty-phase "job finished" pings must not wipe the last status line.
            if u.phase.is_empty()
                && u.message.is_none()
                && u.done.is_none()
                && u.ticket.is_none()
            {
                continue;
            }
            if !u.phase.is_empty() {
                // New phase (e.g. ready → progress) clears a stale "Copied!" flash.
                if u.phase != self.phase && u.phase != "ready" {
                    self.copy_flash.clear();
                    self.copy_flash_t = 0.0;
                }
                self.phase = u.phase;
            }
            if let Some(t) = u.ticket {
                if !t.is_empty() {
                    self.ticket = t;
                }
            }
            if let Some(d) = u.done {
                self.done_bytes = d;
            }
            if let Some(t) = u.total {
                self.total_bytes = t;
            }
            self.status = format_status(
                &self.phase,
                u.message.as_deref(),
                self.done_bytes,
                self.total_bytes,
                &self.ticket,
            );
        }

        // Drop finished job handle once the worker thread is done.
        if last_finished || self.job.as_ref().map(|j| j.is_finished()).unwrap_or(false) {
            // Keep final status/ticket; release process handle.
            if let Some(job) = self.job.take() {
                // If not fully joined yet, cancel/drop will join briefly.
                drop(job);
            }
        }
    }
}

fn path_display(p: &Path) -> String {
    p.to_string_lossy().into_owned()
}

fn format_status(
    phase: &str,
    message: Option<&str>,
    done: u64,
    total: u64,
    ticket: &str,
) -> String {
    match phase {
        "ready" => {
            if ticket.is_empty() {
                message.unwrap_or("ticket ready").to_string()
            } else {
                message
                    .unwrap_or("share this ticket · keep suzuri open")
                    .to_string()
            }
        }
        "progress" | "receiving" => {
            if total > 0 {
                let pct = (done as f64 * 100.0 / total as f64).clamp(0.0, 100.0);
                format!(
                    "{phase}  {pct:5.1}%  {} / {}",
                    human_bytes(done),
                    human_bytes(total)
                )
            } else if done > 0 {
                format!("{phase}  {}", human_bytes(done))
            } else {
                message.unwrap_or(phase).to_string()
            }
        }
        "done" => message.unwrap_or("Transfer complete").to_string(),
        "error" => message.unwrap_or("Transfer failed").to_string(),
        "stopped" => message.unwrap_or("Transfer stopped").to_string(),
        "preparing" => message.unwrap_or("preparing…").to_string(),
        _ => message.unwrap_or(phase).to_string(),
    }
}

fn human_bytes(n: u64) -> String {
    const UNIT: u64 = 1024;
    if n < UNIT {
        return format!("{n} B");
    }
    let mut div = UNIT;
    let mut exp = 0;
    let mut v = n / UNIT;
    while v >= UNIT {
        div *= UNIT;
        exp += 1;
        v /= UNIT;
    }
    let label = b"KMGTPE"[exp] as char;
    format!("{:.1} {label}iB", n as f64 / div as f64)
}
