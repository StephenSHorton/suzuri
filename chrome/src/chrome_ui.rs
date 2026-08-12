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
    fn target_scale(&self, id: ChipId) -> f32 {
        let on = self.hover == Some(id);
        if id == ChipId::NewTab {
            // Ghost +: no idle hover inflate; press still shrinks.
            if on && self.pressed {
                0.92
            } else {
                1.0
            }
        } else if on && self.pressed {
            0.94
        } else if on {
            1.06
        } else {
            1.0
        }
    }

    fn target_press(&self, id: ChipId) -> f32 {
        if self.hover == Some(id) && self.pressed {
            1.0
        } else {
            0.0
        }
    }

    /// Record hit-test hover + cursor. Call before [`tick`].
    ///
    /// Spotlight position only updates while over a chip so leaving freezes the
    /// light under the exit point and lets strength fade out smoothly.
    pub fn set_hover(&mut self, hover: Option<ChipId>, cursor: (f32, f32)) {
        self.hover = hover;
        if hover.is_some() {
            self.hover_pos = [cursor.0, cursor.1];
        }
    }

    /// Advance springs. Call once per frame after hover/pressed are updated.
    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);

        // Spotlight: on while over a chip, spring off when leaving (no hard cut).
        let spot_target = if self.hover.is_some() { 1.0 } else { 0.0 };
        self.spotlight.spring(dt, spot_target, 260.0, 24.0);
        self.spotlight.value = self.spotlight.value.clamp(0.0, 1.0);

        // Ensure currently interactive chips exist in the maps.
        let mut ids: Vec<ChipId> = self.scale.keys().copied().collect();
        if let Some(h) = self.hover {
            if !ids.contains(&h) {
                ids.push(h);
            }
        }
        // Always track logo / newtab / caffeine so they settle after leave.
        for extra in [ChipId::Logo, ChipId::NewTab, ChipId::Caffeine] {
            if !ids.contains(&extra) {
                ids.push(extra);
            }
        }
        // Keep a few tab slots warm (scale settles after hover leave).
        for i in 0..8 {
            let t = ChipId::Tab(i);
            if !ids.contains(&t) && (self.scale.contains_key(&t) || self.press.contains_key(&t)) {
                ids.push(t);
            }
        }

        for id in ids {
            let ts = self.target_scale(id);
            let tp = self.target_press(id);
            let s = self.scale.entry(id).or_insert_with(|| AnimF::at(1.0));
            // Snappy but smooth — slightly stiffer on press-in.
            let (k, c) = if self.pressed && self.hover == Some(id) {
                (420.0, 28.0)
            } else {
                (280.0, 24.0)
            };
            s.spring(dt, ts, k, c);

            let p = self.press.entry(id).or_insert_with(|| AnimF::at(0.0));
            p.spring(dt, tp, 360.0, 26.0);
            p.value = p.value.clamp(0.0, 1.0);
        }

        // Drop fully settled idle entries so the map doesn't grow forever.
        self.scale.retain(|id, a| {
            let idle = self.hover != Some(*id)
                && !self.pressed
                && (a.value - 1.0).abs() < 0.002
                && a.vel.abs() < 0.01;
            !idle || matches!(id, ChipId::Logo | ChipId::NewTab | ChipId::Caffeine)
        });
    }

    /// Animated spotlight strength 0..1 (shader `hover.w`).
    pub fn spotlight(&self) -> f32 {
        self.spotlight.value.clamp(0.0, 1.0)
    }

    /// Spotlight center (logical px) — last position while over a chip.
    pub fn spotlight_pos(&self) -> [f32; 2] {
        self.hover_pos
    }

    /// Current animated scale for a chip.
    pub fn scale_for(&self, id: ChipId) -> f32 {
        self.scale
            .get(&id)
            .map(|a| a.value)
            .unwrap_or(1.0)
            .clamp(0.85, 1.15)
    }

    /// Animated full-chip primary wash (0..1).
    pub fn press_light(&self, id: ChipId) -> f32 {
        self.press
            .get(&id)
            .map(|a| a.value)
            .unwrap_or(0.0)
            .clamp(0.0, 1.0)
    }

    /// Whether the ghost + shell should show (animated press past threshold).
    pub fn ghost_shell_visible(&self, id: ChipId) -> bool {
        self.press_light(id) > 0.04
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
    fn chip_scale_smooths() {
        let mut ui = ChipUi::default();
        ui.set_hover(Some(ChipId::Logo), (10.0, 10.0));
        ui.tick(1.0 / 60.0);
        let s1 = ui.scale_for(ChipId::Logo);
        assert!(s1 > 1.0 && s1 < 1.06, "s1={s1}");
        for _ in 0..60 {
            ui.tick(1.0 / 60.0);
        }
        assert!((ui.scale_for(ChipId::Logo) - 1.06).abs() < 0.01);
        ui.set_hover(None, (10.0, 10.0));
        for _ in 0..60 {
            ui.tick(1.0 / 60.0);
        }
        assert!((ui.scale_for(ChipId::Logo) - 1.0).abs() < 0.02);
    }

    #[test]
    fn spotlight_fades_after_leave() {
        let mut ui = ChipUi::default();
        ui.set_hover(Some(ChipId::Tab(0)), (100.0, 20.0));
        for _ in 0..40 {
            ui.tick(1.0 / 60.0);
        }
        assert!(ui.spotlight() > 0.9);
        let pos = ui.spotlight_pos();
        assert!((pos[0] - 100.0).abs() < 0.1);
        ui.set_hover(None, (200.0, 200.0)); // leave — pos should freeze
        assert!((ui.spotlight_pos()[0] - 100.0).abs() < 0.1);
        ui.tick(1.0 / 60.0);
        let mid = ui.spotlight();
        assert!(mid > 0.0 && mid < 1.0, "mid={mid}");
        for _ in 0..90 {
            ui.tick(1.0 / 60.0);
        }
        assert!(ui.spotlight() < 0.05);
    }
}
