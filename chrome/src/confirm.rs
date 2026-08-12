//! Crush-style yes/no confirm modal (product `internal/chrome/confirm.go`).
//!
//! Used for quit confirmation when sessions are still open. Springs match
//! help / settings glass cards (stiffness 150 / damping 25, overlay 0.2s).

use crate::layout::Rect;

/// Result of a confirm key / click.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ConfirmChoice {
    Yes,
    No,
}

/// Which confirm is showing — yes/no dispatch depends on this.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub enum ConfirmKind {
    #[default]
    Quit,
    /// Install a GitHub release and restart.
    Update,
}

/// Whether the confirm modal is open, plus presentation springs + copy.
#[derive(Clone, Debug)]
pub struct ConfirmState {
    pub open: bool,
    pub title: String,
    pub body: String,
    pub yes_label: String,
    pub no_label: String,
    pub kind: ConfirmKind,
    present: f32,
    present_vel: f32,
    overlay: f32,
}

impl Default for ConfirmState {
    fn default() -> Self {
        Self::new()
    }
}

impl ConfirmState {
    pub fn new() -> Self {
        Self {
            open: false,
            title: String::new(),
            body: String::new(),
            yes_label: "Yes".into(),
            no_label: "Cancel".into(),
            kind: ConfirmKind::Quit,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
        }
    }

    /// Product quit dialog: "Quit suzuri?" / "Close all tabs and quit?".
    pub fn open_quit(&mut self) {
        self.open = true;
        self.kind = ConfirmKind::Quit;
        self.title = "Quit suzuri?".into();
        self.body = "Close all tabs and quit?".into();
        self.yes_label = "Quit".into();
        self.no_label = "Cancel".into();
    }

    /// Product update dialog: "Update suzuri?" / "vX is available. Install and restart?".
    pub fn open_update(&mut self, version: &str) {
        let ver = version.trim().trim_start_matches('v');
        let ver = if ver.is_empty() { "?" } else { ver };
        self.open = true;
        self.kind = ConfirmKind::Update;
        self.title = "Update suzuri?".into();
        self.body = format!("v{ver} is available. Install and restart?");
        self.yes_label = "Update".into();
        self.no_label = "Later".into();
    }

    /// Generic confirm with custom copy.
    pub fn open_with(
        &mut self,
        title: impl Into<String>,
        body: impl Into<String>,
        yes: impl Into<String>,
        no: impl Into<String>,
    ) {
        self.open = true;
        self.kind = ConfirmKind::Quit;
        self.title = title.into();
        self.body = body.into();
        self.yes_label = yes.into();
        self.no_label = no.into();
    }

    pub fn close(&mut self) {
        self.open = false;
    }

    /// Still drawing (open or mid exit animation).
    #[inline]
    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01 || self.overlay > 0.01
    }

    #[inline]
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

    /// Compact glass card — smaller than settings/help.
    pub fn base_modal_rect(window_w: f32, window_h: f32) -> Rect {
        let w = (window_w - 64.0).min(420.0).max(280.0);
        let h = (window_h - 120.0).min(180.0).max(140.0);
        Rect::new(
            (window_w - w) * 0.5,
            (window_h - h) * 0.42,
            w,
            h,
        )
    }

    /// Animated modal rect — same top-scale entrance as settings.
    pub fn animated_modal_rect(&self, window_w: f32, window_h: f32) -> Rect {
        let base = Self::base_modal_rect(window_w, window_h);
        let t = self.content_ease();
        let sx = 0.88 + 0.12 * t;
        let sy = 0.82 + 0.18 * t;
        let w = base.w * sx;
        let h = base.h * sy;
        let y_nudge = -16.0 * (1.0 - t);
        Rect::new(
            (window_w - w) * 0.5,
            base.y + (base.h - h) * 0.5 + y_nudge,
            w,
            h,
        )
    }

    /// Yes / primary action button rect inside the card.
    pub fn yes_rect(&self, window_w: f32, window_h: f32) -> Rect {
        let modal = self.animated_modal_rect(window_w, window_h);
        let pad = 16.0;
        let btn_h = 36.0;
        let gap = 10.0;
        let btn_w = (modal.w - pad * 2.0 - gap) * 0.5;
        Rect::new(
            modal.x + pad,
            modal.y + modal.h - pad - btn_h,
            btn_w,
            btn_h,
        )
    }

    /// No / cancel button rect inside the card.
    pub fn no_rect(&self, window_w: f32, window_h: f32) -> Rect {
        let modal = self.animated_modal_rect(window_w, window_h);
        let pad = 16.0;
        let btn_h = 36.0;
        let gap = 10.0;
        let btn_w = (modal.w - pad * 2.0 - gap) * 0.5;
        Rect::new(
            modal.x + pad + btn_w + gap,
            modal.y + modal.h - pad - btn_h,
            btn_w,
            btn_h,
        )
    }

    /// Hit-test yes/no buttons. Returns [`None`] if outside both.
    pub fn hit_button(&self, x: f32, y: f32, window_w: f32, window_h: f32) -> Option<ConfirmChoice> {
        if !self.open {
            return None;
        }
        if self.yes_rect(window_w, window_h).contains(x, y) {
            return Some(ConfirmChoice::Yes);
        }
        if self.no_rect(window_w, window_h).contains(x, y) {
            return Some(ConfirmChoice::No);
        }
        None
    }

    /// Product key map: Enter / Y → yes, Esc / N → no.
    pub fn handle_key(&self, key: &str) -> Option<ConfirmChoice> {
        if !self.open {
            return None;
        }
        match key {
            "Enter" | "y" | "Y" => Some(ConfirmChoice::Yes),
            "Escape" | "n" | "N" => Some(ConfirmChoice::No),
            _ => None,
        }
    }

    /// Advance springs. Call once per frame with `dt` seconds.
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
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn open_quit_sets_product_copy() {
        let mut c = ConfirmState::new();
        assert!(!c.open);
        c.open_quit();
        assert!(c.open);
        assert_eq!(c.title, "Quit suzuri?");
        assert_eq!(c.body, "Close all tabs and quit?");
        assert_eq!(c.yes_label, "Quit");
        assert_eq!(c.no_label, "Cancel");
        assert_eq!(c.kind, ConfirmKind::Quit);
    }

    #[test]
    fn open_update_sets_product_copy() {
        let mut c = ConfirmState::new();
        c.open_update("v1.2.3");
        assert!(c.open);
        assert_eq!(c.kind, ConfirmKind::Update);
        assert_eq!(c.title, "Update suzuri?");
        assert_eq!(c.body, "v1.2.3 is available. Install and restart?");
        assert_eq!(c.yes_label, "Update");
        assert_eq!(c.no_label, "Later");
        c.open_update("4.0.0");
        assert_eq!(c.body, "v4.0.0 is available. Install and restart?");
    }

    #[test]
    fn keys_yes_no() {
        let mut c = ConfirmState::new();
        assert!(c.handle_key("Enter").is_none());
        c.open_quit();
        assert_eq!(c.handle_key("Enter"), Some(ConfirmChoice::Yes));
        assert_eq!(c.handle_key("y"), Some(ConfirmChoice::Yes));
        assert_eq!(c.handle_key("Y"), Some(ConfirmChoice::Yes));
        assert_eq!(c.handle_key("Escape"), Some(ConfirmChoice::No));
        assert_eq!(c.handle_key("n"), Some(ConfirmChoice::No));
        assert_eq!(c.handle_key("N"), Some(ConfirmChoice::No));
        assert!(c.handle_key("x").is_none());
    }

    #[test]
    fn spring_opens_and_closes() {
        let mut c = ConfirmState::new();
        c.open_quit();
        for _ in 0..90 {
            c.tick(1.0 / 60.0);
        }
        assert!(c.present() > 0.9);
        assert!(c.visible());
        c.close();
        for _ in 0..90 {
            c.tick(1.0 / 60.0);
        }
        assert!(c.present() < 0.05);
        assert!(!c.visible());
    }

    #[test]
    fn button_hit_test() {
        let mut c = ConfirmState::new();
        c.open_quit();
        // Settle spring so animated rect ≈ base.
        for _ in 0..120 {
            c.tick(1.0 / 60.0);
        }
        let (ww, wh) = (800.0, 600.0);
        let yes = c.yes_rect(ww, wh);
        let no = c.no_rect(ww, wh);
        assert_eq!(
            c.hit_button(yes.x + yes.w * 0.5, yes.y + yes.h * 0.5, ww, wh),
            Some(ConfirmChoice::Yes)
        );
        assert_eq!(
            c.hit_button(no.x + no.w * 0.5, no.y + no.h * 0.5, ww, wh),
            Some(ConfirmChoice::No)
        );
        assert!(c.hit_button(0.0, 0.0, ww, wh).is_none());
    }
}
