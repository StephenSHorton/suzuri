//! Settings glass modal — state, spring animation, layout.
//!
//! Animation matches **agility-platform** `@agility/ui` animate-ui Dialog
//! (`packages/ui/src/components/animate-ui/primitives/radix/dialog.tsx`):
//!
//! | Layer   | Motion |
//! |---------|--------|
//! | Overlay | opacity 0→1, duration **0.2s**, easeInOut |
//! | Content | spring **stiffness 150 / damping 25**, from **top**: scale 0.8→1, rotateX −20°→0 (2D approx), opacity 0→1 |
//!
//! Drawn as optical glass (same pane model), not plain text over the terminal.

use crate::layout::Rect;

/// Default glass face darken (matches product look). Renderer uses prefs at runtime.
pub const GLASS_DARKEN_DEFAULT: f32 = 0.82;

/// User-tunable chrome prefs (persist for the session; settings modal edits these).
#[derive(Clone, Debug)]
pub struct ChromePrefs {
    /// Canvas UI glyph rain under glass.
    pub rain: bool,
    /// Mouse glass lens.
    pub lens: bool,
    /// Shared glass face darken 0..1 (panes / chips / modal).
    pub glass_darken: f32,
}

impl Default for ChromePrefs {
    fn default() -> Self {
        Self {
            rain: true,
            lens: true,
            glass_darken: GLASS_DARKEN_DEFAULT,
        }
    }
}

impl ChromePrefs {
    pub fn nudge_darken(&mut self, delta: f32) {
        self.glass_darken = (self.glass_darken + delta).clamp(0.0, 0.95);
    }
}

/// Whether the settings modal is open, plus presentation springs + prefs.
#[derive(Clone, Debug)]
pub struct SettingsState {
    /// Desired open/closed.
    pub open: bool,
    /// Session prefs (rain / lens / darken).
    pub prefs: ChromePrefs,
    /// Spring position 0..1 for content (present).
    present: f32,
    present_vel: f32,
    /// Overlay opacity progress 0..1 (0.2s ease toward target).
    overlay: f32,
    /// Last (or parent-filled) display lines for the overlay.
    pub lines: Vec<String>,
}

impl Default for SettingsState {
    fn default() -> Self {
        Self::new()
    }
}

impl SettingsState {
    pub fn new() -> Self {
        Self {
            open: false,
            prefs: ChromePrefs::default(),
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
            lines: Vec::new(),
        }
    }

    /// Toggle rain / lens, or nudge darken, from a key while the modal is open.
    pub fn handle_hotkey(&mut self, key: &str) -> bool {
        match key {
            "1" => {
                self.prefs.rain = !self.prefs.rain;
                true
            }
            "2" => {
                self.prefs.lens = !self.prefs.lens;
                true
            }
            "[" | "-" => {
                self.prefs.nudge_darken(-0.05);
                true
            }
            "]" | "=" | "+" => {
                self.prefs.nudge_darken(0.05);
                true
            }
            _ => false,
        }
    }

    pub fn open(&mut self) {
        self.open = true;
    }

    pub fn close(&mut self) {
        self.open = false;
    }

    pub fn toggle(&mut self) {
        self.open = !self.open;
    }

    /// Still drawing (open or mid exit animation).
    #[inline]
    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01 || self.overlay > 0.01
    }

    /// Content spring 0..1 (agility dialog content).
    #[inline]
    pub fn present(&self) -> f32 {
        self.present.clamp(0.0, 1.0)
    }

    /// Overlay fade 0..1 (agility dialog overlay).
    #[inline]
    pub fn overlay(&self) -> f32 {
        self.overlay.clamp(0.0, 1.0)
    }

    /// Advance springs. Call once per frame with `dt` seconds.
    ///
    /// Content: mass=1, k=150, c=25 (motion/react spring defaults from agility).
    /// Overlay: ~0.2s easeInOut toward target (linear approach ≈ ease for our use).
    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let target = if self.open { 1.0 } else { 0.0 };

        // --- content spring (stiffness 150, damping 25) ---
        const K: f32 = 150.0;
        const C: f32 = 25.0;
        let force = -K * (self.present - target) - C * self.present_vel;
        self.present_vel += force * dt;
        self.present += self.present_vel * dt;
        // Soft clamp with settle when nearly done
        if (self.present - target).abs() < 0.001 && self.present_vel.abs() < 0.01 {
            self.present = target;
            self.present_vel = 0.0;
        }

        // --- overlay 0.2s easeInOut toward target ---
        // easeInOut cubic on a linear clock toward target at rate 1/0.2
        const OVERLAY_DUR: f32 = 0.2;
        let step = dt / OVERLAY_DUR;
        if self.overlay < target {
            self.overlay = (self.overlay + step).min(target);
        } else if self.overlay > target {
            self.overlay = (self.overlay - step).max(target);
        }
    }

    /// Ease content for opacity / scale (slight ease on spring for nicer feel).
    pub fn content_ease(&self) -> f32 {
        let t = self.present();
        // smoothstep — similar visual weight to blur fade clearing
        t * t * (3.0 - 2.0 * t)
    }

    /// Overlay dim strength (matches agility `bg-black/50` × ease).
    pub fn scrim_alpha(&self) -> f32 {
        // easeInOut on overlay progress
        let t = self.overlay();
        let e = t * t * (3.0 - 2.0 * t);
        e * 0.50
    }

    /// Base modal rect centered in the window (pre-animation).
    pub fn base_modal_rect(window_w: f32, window_h: f32) -> Rect {
        let w = (window_w - 32.0).min(420.0).max(280.0);
        let h = (window_h - 64.0).min(360.0).max(220.0);
        Rect::new(
            (window_w - w) * 0.5,
            (window_h - h) * 0.5,
            w,
            h,
        )
    }

    /// Animated modal rect — agility content from **top**:
    /// scale 0.8→1, slight Y squash (rotateX −20° approx), drop from above.
    pub fn animated_modal_rect(&self, window_w: f32, window_h: f32) -> Rect {
        let base = Self::base_modal_rect(window_w, window_h);
        let t = self.content_ease();
        // initial scale 0.8 → 1.0
        let sx = 0.8 + 0.2 * t;
        // rotateX: foreshorten Y more when closed (≈ cos(20°) * scale)
        let sy = 0.72 + 0.28 * t;
        // from top: start higher, settle to center
        let y_nudge = -28.0 * (1.0 - t);

        let cx = base.x + base.w * 0.5;
        let cy = base.y + base.h * 0.5 + y_nudge;
        let w = base.w * sx;
        let h = base.h * sy;
        Rect::new(cx - w * 0.5, cy - h * 0.5, w, h)
    }

    /// Snapshot of status for UI (renderer will draw these as text labels).
    pub fn display_lines(
        &self,
        pty_active: bool,
        cols: u16,
        rows: u16,
        tab_count: usize,
    ) -> Vec<String> {
        let shell = if pty_active {
            "PTY: active (live shell)"
        } else {
            "PTY: mock fallback (no live shell)"
        };
        let rain = if self.prefs.rain { "on" } else { "off" };
        let lens = if self.prefs.lens { "on" } else { "off" };
        let darken_pct = (self.prefs.glass_darken * 100.0).round() as i32;

        vec![
            "suzuri-chrome 1.0.0".into(),
            "native GPU shell · winit + wgpu · no React / HTML / Chromium".into(),
            String::new(),
            "toggles".into(),
            format!("  [1] glyph rain     {rain}"),
            format!("  [2] mouse lens     {lens}"),
            format!("  [ / ]  glass darken  {darken_pct}%"),
            String::new(),
            "status".into(),
            format!("  {shell}"),
            format!("  grid  {cols}×{rows}"),
            format!("  tabs  {tab_count}"),
            String::new(),
            "keys".into(),
            "  Esc       close settings".into(),
            "  ⌘/,       toggle settings".into(),
            "  ⌘K       command palette".into(),
            "  ⌘/       keyboard shortcuts".into(),
            "  ⌘T / ⌘W  new tab / close pane".into(),
            "  ⇧⌘D/E    split right / down".into(),
            "  ⌘V       paste".into(),
            "  wheel    scrollback".into(),
        ]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn new_starts_closed() {
        let s = SettingsState::new();
        assert!(!s.open);
        assert!(!s.visible());
        assert!(s.lines.is_empty());
    }

    #[test]
    fn open_close_toggle() {
        let mut s = SettingsState::new();
        s.open();
        assert!(s.open);
        s.close();
        assert!(!s.open);
        s.toggle();
        assert!(s.open);
        s.toggle();
        assert!(!s.open);
    }

    #[test]
    fn spring_opens_and_closes() {
        let mut s = SettingsState::new();
        s.open();
        for _ in 0..120 {
            s.tick(1.0 / 60.0);
        }
        assert!(s.present() > 0.95, "present={}", s.present());
        assert!(s.overlay() > 0.95);
        s.close();
        for _ in 0..120 {
            s.tick(1.0 / 60.0);
        }
        assert!(s.present() < 0.05, "present={}", s.present());
        assert!(!s.visible() || s.present() < 0.05);
    }

    #[test]
    fn display_lines_mentions_core_facts() {
        let s = SettingsState::new();
        let mock = s.display_lines(false, 80, 24, 2).join("\n");
        assert!(mock.contains("suzuri-chrome"));
        assert!(mock.contains("1.0.0"));
        assert!(mock.contains("mock fallback"));
        assert!(mock.contains("80×24"));
        assert!(mock.contains("tabs  2"));
        assert!(mock.contains("glyph rain"));
        assert!(mock.contains("Esc"));

        let live = s.display_lines(true, 120, 40, 1).join("\n");
        assert!(live.contains("PTY: active"));
        assert!(live.contains("120×40"));
    }

    #[test]
    fn hotkeys_toggle_prefs() {
        let mut s = SettingsState::new();
        assert!(s.prefs.rain);
        assert!(s.handle_hotkey("1"));
        assert!(!s.prefs.rain);
        assert!(s.handle_hotkey("2"));
        assert!(!s.prefs.lens);
        let d0 = s.prefs.glass_darken;
        assert!(s.handle_hotkey("]"));
        assert!(s.prefs.glass_darken > d0);
    }
}
