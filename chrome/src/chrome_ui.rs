//! Chrome chip hover/press + active-tab jelly connector state.

use crate::layout::Rect;
use std::collections::HashMap;

/// Which chrome chip is under the pointer (for scale / press light).
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum ChipId {
    Tab(usize),
    NewTab,
    Logo,
    Caffeine,
    PaneClose(u64),
    PaneTitle(u64),
    CaptionMin,
    CaptionMax,
    CaptionClose,
}

#[derive(Clone, Copy, Debug)]
struct AnimF {
    value: f32,
    vel: f32,
}

impl AnimF {
    fn at(value: f32) -> Self {
        Self { value, vel: 0.0 }
    }

    /// Critical-ish spring toward `target`.
    fn spring(&mut self, dt: f32, target: f32, k: f32, c: f32) {
        let force = -k * (self.value - target) - c * self.vel;
        self.vel += force * dt;
        self.value += self.vel * dt;
        // Settle noise
        if (self.value - target).abs() < 0.0008 && self.vel.abs() < 0.01 {
            self.value = target;
            self.vel = 0.0;
        }
    }
}

/// Per-frame interaction for nav chips (animated scale + press wash).
#[derive(Clone, Debug)]
pub struct ChipUi {
    pub hover: Option<ChipId>,
    pub pressed: bool,
    /// Last pointer while over a chip (spotlight freezes here on leave for fade-out).
    hover_pos: [f32; 2],
    /// Global spotlight strength 0..1 — springs off so light fades when leaving chips.
    spotlight: AnimF,
    /// Animated scale per chip (defaults to 1 when missing).
    scale: HashMap<ChipId, AnimF>,
    /// Animated press wash 0..1 per chip.
    press: HashMap<ChipId, AnimF>,
}

impl Default for ChipUi {
    fn default() -> Self {
        Self {
            hover: None,
            pressed: false,
            hover_pos: [0.0, 0.0],
            spotlight: AnimF::at(0.0),
            scale: HashMap::new(),
            press: HashMap::new(),
        }
    }
}

impl ChipUi {
    /// Hover dim factor applied to chip labels (1 = full, 0.5 = 50% dim).
    pub const HOVER_DIM: f32 = 0.5;

    fn target_press(&self, id: ChipId) -> f32 {
        // Only press-in wash on mouse down — no hover glow.
        if self.hover == Some(id) && self.pressed {
            0.35 // soft, not full jade flood
        } else {
            0.0
        }
    }

    /// Record hit-test hover + cursor. Call before [`tick`].
    pub fn set_hover(&mut self, hover: Option<ChipId>, cursor: (f32, f32)) {
        self.hover = hover;
        if hover.is_some() {
            self.hover_pos = [cursor.0, cursor.1];
        }
    }

    /// Advance springs. Call once per frame after hover/pressed are updated.
    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);

        // No hover spotlight / scale — keep strength at 0.
        self.spotlight.value = 0.0;
        self.spotlight.vel = 0.0;

        // Ensure currently interactive chips exist for press settle.
        let mut ids: Vec<ChipId> = self.press.keys().copied().collect();
        if let Some(h) = self.hover {
            if !ids.contains(&h) {
                ids.push(h);
            }
        }
        for extra in [ChipId::Logo, ChipId::NewTab, ChipId::Caffeine] {
            if !ids.contains(&extra) {
                ids.push(extra);
            }
        }
        for i in 0..8 {
            let t = ChipId::Tab(i);
            if !ids.contains(&t) && self.press.contains_key(&t) {
                ids.push(t);
            }
        }

        for id in ids {
            // Scale always 1 — no inflate/shrink on hover.
            let s = self.scale.entry(id).or_insert_with(|| AnimF::at(1.0));
            s.value = 1.0;
            s.vel = 0.0;

            let tp = self.target_press(id);
            let p = self.press.entry(id).or_insert_with(|| AnimF::at(0.0));
            p.spring(dt, tp, 360.0, 26.0);
            p.value = p.value.clamp(0.0, 1.0);
        }

        self.scale.retain(|id, a| {
            let idle = self.hover != Some(*id)
                && !self.pressed
                && (a.value - 1.0).abs() < 0.002
                && a.vel.abs() < 0.01;
            !idle || matches!(id, ChipId::Logo | ChipId::NewTab | ChipId::Caffeine)
        });
    }

    /// Spotlight strength — always off (no green hover wash).
    pub fn spotlight(&self) -> f32 {
        0.0
    }

    /// Spotlight center (unused while strength is 0).
    pub fn spotlight_pos(&self) -> [f32; 2] {
        self.hover_pos
    }

    /// Scale always 1 — layout rects stay fixed on hover.
    pub fn scale_for(&self, _id: ChipId) -> f32 {
        1.0
    }

    /// Label / icon multiplier: 50% when hovered, else full.
    /// Ghost + / pane × keep full glyph strength — the shell is the hover signal.
    pub fn hover_dim(&self, id: ChipId) -> f32 {
        if matches!(
            id,
            ChipId::NewTab
                | ChipId::PaneClose(_)
                | ChipId::CaptionMin
                | ChipId::CaptionMax
                | ChipId::CaptionClose
        ) {
            return 1.0;
        }
        if self.hover == Some(id) {
            Self::HOVER_DIM
        } else {
            1.0
        }
    }

    /// Apply hover dim to an RGBA label color.
    pub fn dim_color(&self, id: ChipId, mut rgba: [f32; 4]) -> [f32; 4] {
        let d = self.hover_dim(id);
        rgba[0] *= d;
        rgba[1] *= d;
        rgba[2] *= d;
        // Keep alpha so glass/text still composites; dim is in RGB.
        rgba
    }

    /// Animated press wash (0..1) — only while mouse is down.
    pub fn press_light(&self, id: ChipId) -> f32 {
        self.press
            .get(&id)
            .map(|a| a.value)
            .unwrap_or(0.0)
            .clamp(0.0, 1.0)
    }

    /// Ghost + / pane × shell on hover or press (no idle glass).
    pub fn ghost_shell_visible(&self, id: ChipId) -> bool {
        self.is_lit(id)
    }

    /// Hover or press — glyph should stay at least as bright as idle.
    pub fn is_lit(&self, id: ChipId) -> bool {
        self.hover == Some(id) || self.press_light(id) > 0.04
    }
}

/// Scale a rect about its center.
pub fn scale_rect(r: Rect, s: f32) -> Rect {
    if (s - 1.0).abs() < 0.001 {
        return r;
    }
    let cx = r.x + r.w * 0.5;
    let cy = r.y + r.h * 0.5;
    let nw = r.w * s;
    let nh = r.h * s;
    Rect::new(cx - nw * 0.5, cy - nh * 0.5, nw, nh)
}

/// Jelly tab connector: slides under the active tab and bridges into the workspace.
#[derive(Clone, Debug)]
pub struct TabJelly {
    /// Animated center X of the active tab / neck.
    pub x: f32,
    pub vel_x: f32,
    /// Animated neck width.
    pub w: f32,
    pub vel_w: f32,
    /// How fully the neck connects (0..1), springs open on first show.
    pub connect: f32,
    pub vel_connect: f32,
    seeded: bool,
}

impl Default for TabJelly {
    fn default() -> Self {
        Self {
            x: 0.0,
            vel_x: 0.0,
            w: 96.0,
            vel_w: 0.0,
            connect: 0.0,
            vel_connect: 0.0,
            seeded: false,
        }
    }
}

impl TabJelly {
    /// Spring toward the active tab chip. Call once per frame.
    pub fn tick(&mut self, dt: f32, active_chip: Option<Rect>) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let Some(chip) = active_chip else {
            // No tabs — retract
            self.spring(dt, self.x, 0.0, 0.0);
            return;
        };
        let target_x = chip.x + chip.w * 0.5;
        let target_w = chip.w;
        if !self.seeded {
            self.x = target_x;
            self.w = target_w;
            self.connect = 0.0;
            self.seeded = true;
        }
        self.spring_xy(dt, target_x, target_w);
        // Connect spring → 1
        const K: f32 = 180.0;
        const C: f32 = 18.0;
        let force = -K * (self.connect - 1.0) - C * self.vel_connect;
        self.vel_connect += force * dt;
        self.connect += self.vel_connect * dt;
        if self.connect > 1.08 {
            self.connect = 1.08;
            self.vel_connect *= -0.35;
        }
        if self.connect < 0.0 {
            self.connect = 0.0;
            self.vel_connect = 0.0;
        }
        if (self.connect - 1.0).abs() < 0.01 && self.vel_connect.abs() < 0.05 {
            self.connect = 1.0;
            self.vel_connect = 0.0;
        }
    }

    fn spring_xy(&mut self, dt: f32, tx: f32, tw: f32) {
        // Soft goo slide — lower K so the blob eases between tabs (no snap).
        const K: f32 = 140.0;
        const C: f32 = 18.0;
        let fx = -K * (self.x - tx) - C * self.vel_x;
        self.vel_x += fx * dt;
        self.x += self.vel_x * dt;
        let fw = -K * (self.w - tw) - C * self.vel_w;
        self.vel_w += fw * dt;
        self.w += self.vel_w * dt;
        self.w = self.w.clamp(24.0, 200.0);
    }

    fn spring(&mut self, dt: f32, tx: f32, tw: f32, tconnect: f32) {
        self.spring_xy(dt, tx, tw);
        const K: f32 = 180.0;
        const C: f32 = 18.0;
        let force = -K * (self.connect - tconnect) - C * self.vel_connect;
        self.vel_connect += force * dt;
        self.connect += self.vel_connect * dt;
    }

    /// Geometry of the neck that bridges tab bar → workspace.
    /// `chip_bottom` = bottom y of tab chips, `workspace_top` = top of glass well.
    pub fn neck_rect(&self, chip_bottom: f32, workspace_top: f32) -> Option<Rect> {
        let c = self.connect.clamp(0.0, 1.15);
        if c < 0.02 {
            return None;
        }
        let gap = (workspace_top - chip_bottom).max(0.0);
        // Reach into the pane a bit so it reads as one continuous piece.
        let into_pane = 14.0 * c.min(1.0);
        let h = (gap + into_pane) * c.min(1.0);
        if h < 1.0 {
            return None;
        }
        // Mild overshoot widens the neck
        let w = self.w * (0.92 + 0.08 * c.min(1.0));
        let w = if c > 1.0 {
            w * (1.0 + (c - 1.0) * 0.2)
        } else {
            w
        };
        Some(Rect::new(self.x - w * 0.5, chip_bottom, w, h))
    }

    /// Active tab chip rect (animated position/size for the solid blob).
    pub fn active_chip_rect(&self, chip_y: f32, chip_h: f32) -> Rect {
        Rect::new(self.x - self.w * 0.5, chip_y, self.w, chip_h)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn jelly_settles_on_target() {
        let mut j = TabJelly::default();
        let chip = Rect::new(100.0, 4.0, 96.0, 32.0);
        for _ in 0..120 {
            j.tick(1.0 / 60.0, Some(chip));
        }
        assert!((j.x - 148.0).abs() < 2.0, "x={}", j.x);
        assert!((j.connect - 1.0).abs() < 0.05);
    }

    #[test]
    fn hover_dims_label_not_scale() {
        let mut ui = ChipUi::default();
        ui.set_hover(Some(ChipId::Logo), (10.0, 10.0));
        ui.tick(1.0 / 60.0);
        // No inflate on hover — only RGB dim.
        assert!((ui.scale_for(ChipId::Logo) - 1.0).abs() < 0.001);
        assert!((ui.hover_dim(ChipId::Logo) - ChipUi::HOVER_DIM).abs() < 0.001);
        let c = ui.dim_color(ChipId::Logo, [1.0, 0.5, 0.25, 0.9]);
        assert!((c[0] - 0.5).abs() < 0.001);
        assert!((c[1] - 0.25).abs() < 0.001);
        assert!((c[2] - 0.125).abs() < 0.001);
        assert!((c[3] - 0.9).abs() < 0.001); // alpha preserved
        ui.set_hover(None, (10.0, 10.0));
        assert!((ui.hover_dim(ChipId::Logo) - 1.0).abs() < 0.001);
    }

    #[test]
    fn new_tab_shell_appears_on_hover() {
        let mut ui = ChipUi::default();
        assert!(!ui.ghost_shell_visible(ChipId::NewTab));
        ui.set_hover(Some(ChipId::NewTab), (80.0, 12.0));
        assert!(ui.ghost_shell_visible(ChipId::NewTab));
        assert!(ui.is_lit(ChipId::NewTab));
        assert!((ui.hover_dim(ChipId::NewTab) - 1.0).abs() < 0.001);
        let idle = [0.4, 0.4, 0.4, 0.88];
        let hovered = ui.dim_color(ChipId::NewTab, idle);
        assert!((hovered[0] - idle[0]).abs() < 0.001, "hover must not dim +");
        ui.set_hover(None, (80.0, 12.0));
        assert!(!ui.ghost_shell_visible(ChipId::NewTab));
    }

    #[test]
    fn spotlight_stays_off() {
        let mut ui = ChipUi::default();
        ui.set_hover(Some(ChipId::Tab(0)), (100.0, 20.0));
        for _ in 0..40 {
            ui.tick(1.0 / 60.0);
        }
        // Hover wash removed — spotlight strength is always 0.
        assert!(ui.spotlight() < 0.001);
        let pos = ui.spotlight_pos();
        assert!((pos[0] - 100.0).abs() < 0.1);
        ui.set_hover(None, (200.0, 200.0));
        // Last-in-chip position freezes on leave (unused while strength is 0).
        assert!((ui.spotlight_pos()[0] - 100.0).abs() < 0.1);
        assert!(ui.spotlight() < 0.001);
    }
}
