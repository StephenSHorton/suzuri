//! Tab / pane rename dialog — product `internal/chrome/rename.go` parity.
//!
//! Opens from the command palette (`Rename tab` / `Rename pane`) or **F2**
//! (focused pane). Enter commits; Esc cancels. Empty name clears a custom
//! title (tab falls back to sticky/pane; pane falls back to `shell N`).

use crate::layout::Rect;

/// What the rename dialog renames.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub enum RenameTarget {
    /// Focused shell pane (leaf).
    #[default]
    Pane,
    /// Chrome strip tab (page).
    Tab,
}

/// Glass rename input modal (spring presentation like palette/settings).
#[derive(Clone, Debug)]
pub struct RenameState {
    pub open: bool,
    pub target: RenameTarget,
    pub buffer: String,
    present: f32,
    present_vel: f32,
    overlay: f32,
}

impl Default for RenameState {
    fn default() -> Self {
        Self::new()
    }
}

impl RenameState {
    pub fn new() -> Self {
        Self {
            open: false,
            target: RenameTarget::Pane,
            buffer: String::new(),
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
        }
    }

    /// Open with a seed name (typically current tab/pane title).
    pub fn open_with(&mut self, target: RenameTarget, seed: &str) {
        self.open = true;
        self.target = target;
        self.buffer = seed.trim().to_string();
    }

    pub fn close(&mut self) {
        self.open = false;
        self.buffer.clear();
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01 || self.overlay > 0.01
    }

    pub fn present(&self) -> f32 {
        self.present.clamp(0.0, 1.0)
    }

    pub fn content_ease(&self) -> f32 {
        let t = self.present();
        t * t * (3.0 - 2.0 * t)
    }

    pub fn scrim_alpha(&self) -> f32 {
        let t = self.overlay.clamp(0.0, 1.0);
        let e = t * t * (3.0 - 2.0 * t);
        e * 0.50
    }

    pub fn title(&self) -> &'static str {
        match self.target {
            RenameTarget::Pane => "Rename pane",
            RenameTarget::Tab => "Rename tab",
        }
    }

    /// Compact glass card centered in the window (input-first, not a tall list).
    pub fn modal_rect(&self, window_w: f32, window_h: f32) -> Rect {
        let t = self.content_ease();
        let base_w = (window_w - 48.0).min(420.0).max(280.0);
        let base_h = 148.0_f32;
        let sx = 0.90 + 0.10 * t;
        let sy = 0.85 + 0.15 * t;
        let w = base_w * sx;
        let h = base_h * sy;
        let y_nudge = -16.0 * (1.0 - t);
        Rect::new(
            (window_w - w) * 0.5,
            (window_h - h) * 0.40 + y_nudge,
            w,
            h,
        )
    }

    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let target = if self.open { 1.0 } else { 0.0 };
        const K: f32 = 150.0;
        const C: f32 = 25.0;
        let force = -K * (self.present - target) - C * self.present_vel;
        self.present_vel += force * dt;
        self.present += self.present_vel * dt;
        if (self.present - target).abs() < 0.001 && self.present_vel.abs() < 0.01 {
            self.present = target;
            self.present_vel = 0.0;
        }
        const OVERLAY_DUR: f32 = 0.2;
        let step = dt / OVERLAY_DUR;
        if self.overlay < target {
            self.overlay = (self.overlay + step).min(target);
        } else if self.overlay > target {
            self.overlay = (self.overlay - step).max(target);
        }
    }

    pub fn insert_char(&mut self, c: char) {
        if c >= ' ' && !c.is_control() {
            self.buffer.push(c);
        }
    }

    pub fn insert_str(&mut self, s: &str) {
        for c in s.chars() {
            self.insert_char(c);
        }
    }

    pub fn backspace(&mut self) {
        let mut rs: Vec<char> = self.buffer.chars().collect();
        if rs.pop().is_some() {
            self.buffer = rs.into_iter().collect();
        }
    }

    /// Commit: returns the trimmed name (empty clears custom title). Closes dialog.
    pub fn commit(&mut self) -> String {
        let name = self.buffer.trim().to_string();
        self.close();
        name
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn open_seeds_and_commit() {
        let mut r = RenameState::new();
        r.open_with(RenameTarget::Pane, "  shell 1  ");
        assert!(r.open);
        assert_eq!(r.buffer, "shell 1");
        assert_eq!(r.title(), "Rename pane");
        // Backspace seed, type "work"
        for _ in 0..7 {
            r.backspace();
        }
        r.insert_str("work");
        let name = r.commit();
        assert_eq!(name, "work");
        assert!(!r.open);
        assert!(r.buffer.is_empty());
    }

    #[test]
    fn tab_title_label() {
        let mut r = RenameState::new();
        r.open_with(RenameTarget::Tab, "notes");
        assert_eq!(r.title(), "Rename tab");
    }

    #[test]
    fn spring_opens() {
        let mut r = RenameState::new();
        r.open_with(RenameTarget::Pane, "x");
        for _ in 0..90 {
            r.tick(1.0 / 60.0);
        }
        assert!(r.present() > 0.9);
        assert!(r.visible());
    }
}
