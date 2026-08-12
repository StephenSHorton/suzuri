//! Ephemeral status toast — product-style frost chip ("Copied", etc.).
//!
//! Non-modal: never captures input. Show on copy actions; ticks down each frame.

use crate::layout::Rect;

/// Default visible duration for short feedback (~product split toasts).
pub const TOAST_DURATION_S: f32 = 1.5;

/// Fade-out window at the end of the countdown.
const FADE_OUT_S: f32 = 0.30;
/// Fade-in window at the start.
const FADE_IN_S: f32 = 0.12;

/// Short-lived status chip (message + remaining seconds + opacity).
#[derive(Clone, Debug)]
pub struct ToastState {
    message: String,
    /// Seconds left while the message is shown; 0 when idle.
    remaining: f32,
    /// Full duration of the current toast (for fade-in).
    duration: f32,
}

impl Default for ToastState {
    fn default() -> Self {
        Self::new()
    }
}

impl ToastState {
    pub fn new() -> Self {
        Self {
            message: String::new(),
            remaining: 0.0,
            duration: TOAST_DURATION_S,
        }
    }

    /// Arm a toast with the default duration (~1.5s).
    pub fn show(&mut self, msg: impl Into<String>) {
        self.show_for(msg, TOAST_DURATION_S);
    }

    /// Arm a toast for a custom duration (clamped to a sensible range).
    pub fn show_for(&mut self, msg: impl Into<String>, duration_s: f32) {
        let msg = msg.into();
        if msg.is_empty() {
            self.clear();
            return;
        }
        self.message = msg;
        self.duration = duration_s.clamp(0.4, 8.0);
        self.remaining = self.duration;
    }

    pub fn clear(&mut self) {
        self.message.clear();
        self.remaining = 0.0;
    }

    /// Advance countdown. No-op when idle.
    pub fn tick(&mut self, dt: f32) {
        if self.remaining <= 0.0 {
            return;
        }
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        self.remaining = (self.remaining - dt).max(0.0);
        if self.remaining <= 0.0 {
            self.message.clear();
        }
    }

    #[inline]
    pub fn visible(&self) -> bool {
        self.remaining > 0.0 && !self.message.is_empty()
    }

    #[inline]
    pub fn message(&self) -> &str {
        &self.message
    }

    #[inline]
    pub fn remaining(&self) -> f32 {
        self.remaining
    }

    /// Opacity 0..1 with short fade-in / fade-out (no spring — smoothstep).
    ///
    /// First frame after [`show`] is already readable (~15% of fade-in) so the
    /// chip is never fully invisible while `remaining > 0`.
    pub fn opacity(&self) -> f32 {
        if !self.visible() {
            return 0.0;
        }
        let elapsed = (self.duration - self.remaining).max(0.0);
        let fade_in = if FADE_IN_S > 0.0 {
            // Seed so t=0 is still slightly on.
            ((elapsed + FADE_IN_S * 0.15) / FADE_IN_S).clamp(0.0, 1.0)
        } else {
            1.0
        };
        let fade_out = if self.remaining < FADE_OUT_S && FADE_OUT_S > 0.0 {
            (self.remaining / FADE_OUT_S).clamp(0.0, 1.0)
        } else {
            1.0
        };
        // Smoothstep-ish for softer edges.
        let t = fade_in.min(fade_out);
        t * t * (3.0 - 2.0 * t)
    }

    /// Bottom-center frost chip sized to the current message.
    pub fn chip_rect(&self, window_w: f32, window_h: f32) -> Rect {
        let chars = self.message.chars().count().max(4) as f32;
        let pad_x = 18.0;
        let h = 30.0;
        let w = (chars * 7.5 + pad_x * 2.0).clamp(88.0, (window_w - 48.0).max(88.0));
        let x = ((window_w - w) * 0.5).max(16.0);
        // Sit above the window edge; clear of warp/input strip feel.
        let y = (window_h - 16.0 - h).max(8.0);
        Rect::new(x, y, w, h)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn show_tick_expire() {
        let mut t = ToastState::new();
        assert!(!t.visible());
        assert_eq!(t.opacity(), 0.0);

        t.show("Copied");
        assert!(t.visible());
        assert_eq!(t.message(), "Copied");
        assert!((t.remaining() - TOAST_DURATION_S).abs() < 0.001);
        assert!(t.opacity() > 0.0);

        // Mid-life: full opacity after fade-in (tick clamps dt ≤ 1/20s).
        for _ in 0..12 {
            t.tick(0.05);
        }
        assert!(t.visible());
        assert!(t.opacity() > 0.9);

        // Drain remaining time in chunks.
        for _ in 0..40 {
            t.tick(0.05);
        }
        assert!(!t.visible());
        assert!(t.message().is_empty());
        assert_eq!(t.remaining(), 0.0);
        assert_eq!(t.opacity(), 0.0);
    }

    #[test]
    fn show_replaces_message() {
        let mut t = ToastState::new();
        t.show("Copied");
        t.tick(0.4);
        t.show("Ticket copied");
        assert_eq!(t.message(), "Ticket copied");
        // Fresh duration, not leftover.
        assert!((t.remaining() - TOAST_DURATION_S).abs() < 0.001);
    }

    #[test]
    fn empty_show_clears() {
        let mut t = ToastState::new();
        t.show("Copied");
        t.show("");
        assert!(!t.visible());
        assert!(t.message().is_empty());
    }

    #[test]
    fn chip_rect_centered() {
        let mut t = ToastState::new();
        t.show("Copied");
        let r = t.chip_rect(800.0, 600.0);
        assert!(r.w > 0.0 && r.h > 0.0);
        let mid = r.x + r.w * 0.5;
        assert!((mid - 400.0).abs() < 1.0);
        assert!(r.y + r.h <= 600.0);
    }
}
