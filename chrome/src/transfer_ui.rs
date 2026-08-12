//! P2P transfer prompt — shells out to `suzuri-transfer` when available
//! (same sidecar as product Go host).
//!
//! Progress is streamed on a background thread (`transfer_engine`) via NDJSON
//! `--json` events; [`TransferUi::tick`] drains the channel into `status` /
//! `ticket` / phase fields so the render loop can poll without blocking.

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
            job: None,
        }
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01
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
    }

    pub fn close(&mut self) {
        self.cancel_job();
        self.open = false;
        self.buf.clear();
    }

    pub fn is_running(&self) -> bool {
        // Send stays in `ready` while the engine serves the ticket.
        self.job.is_some()
    }

    pub fn tick(&mut self, dt: f32) {
        self.poll_engine();

        let dt = dt.clamp(0.0, 1.0 / 20.0);
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
        // Extra height when ticket / progress is visible.
        let base_h = if !self.ticket.is_empty() || self.total_bytes > 0 || self.is_running() {
            260.0
        } else {
            220.0
        };
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
