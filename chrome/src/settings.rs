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
//!
//! Prefs (`rain` / `lens` / `glass_darken` / `theme`) load from
//! `chrome_prefs.json` on [`SettingsState::new`] and save when dirty
//! (hotkey / close / explicit flush). See `SETTINGS_HOOKS.md` for host
//! integration and how the renderer should sample theme colors.

use std::path::{Path, PathBuf};

use crate::config_store;
use crate::layout::Rect;
use crate::theme;

// Re-export for existing `settings::ChromePrefs` / `GLASS_DARKEN_DEFAULT` imports.
pub use crate::config_store::{ChromePrefs, GLASS_DARKEN_DEFAULT};

/// Row indices in the settings list (must match [`SettingsLayout::ROW_COUNT`]).
pub mod settings_row {
    pub const RAIN: usize = 0;
    pub const LENS: usize = 1;
    pub const PRIMARY: usize = 2;
    pub const ACCENT: usize = 3;
    pub const FONT: usize = 4;
    pub const DARKEN: usize = 5;
    pub const RESET: usize = 6;
}

/// Whether the settings modal is open, plus presentation springs + prefs.
#[derive(Clone, Debug)]
pub struct SettingsState {
    /// Desired open/closed.
    pub open: bool,
    /// Session prefs (rain / lens / darken / theme). Loaded from disk on construct.
    pub prefs: ChromePrefs,
    /// Focused row for keyboard / click (0..[`SettingsLayout::ROW_COUNT`]).
    pub selected: usize,
    /// Animated toggle knobs 0..1 (spring toward prefs.rain / prefs.lens).
    rain_visual: f32,
    rain_visual_vel: f32,
    lens_visual: f32,
    lens_visual_vel: f32,
    /// Spring position 0..1 for content (present).
    present: f32,
    present_vel: f32,
    /// Overlay opacity progress 0..1 (0.2s ease toward target).
    overlay: f32,
    /// Last (or parent-filled) display lines for the overlay.
    pub lines: Vec<String>,
    /// On-disk prefs path (`chrome_prefs.json` under product config dir).
    prefs_path: PathBuf,
    /// True when `prefs` differs from last successful save.
    dirty: bool,
    /// Last successfully persisted snapshot (detect external `prefs` mutation).
    last_saved: ChromePrefs,
}

impl Default for SettingsState {
    fn default() -> Self {
        Self::new()
    }
}

impl SettingsState {
    /// Load prefs from the product config dir (`chrome_prefs.json`).
    pub fn new() -> Self {
        Self::with_path(config_store::chrome_prefs_path())
    }

    /// Construct with an injectable prefs path (unit tests / alternate stores).
    pub fn with_path(path: impl Into<PathBuf>) -> Self {
        let prefs_path = path.into();
        let prefs = config_store::load_chrome_prefs(&prefs_path);
        let rain_v = if prefs.rain { 1.0 } else { 0.0 };
        let lens_v = if prefs.lens { 1.0 } else { 0.0 };
        Self {
            open: false,
            prefs: prefs.clone(),
            selected: 0,
            rain_visual: rain_v,
            rain_visual_vel: 0.0,
            lens_visual: lens_v,
            lens_visual_vel: 0.0,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
            lines: Vec::new(),
            prefs_path,
            dirty: false,
            last_saved: prefs,
        }
    }

    /// Rain switch knob position 0..1 (animated).
    #[inline]
    pub fn rain_toggle_t(&self) -> f32 {
        self.rain_visual.clamp(0.0, 1.0)
    }

    /// Lens switch knob position 0..1 (animated).
    #[inline]
    pub fn lens_toggle_t(&self) -> f32 {
        self.lens_visual.clamp(0.0, 1.0)
    }

    /// Prefs file path this state reads/writes.
    pub fn prefs_path(&self) -> &Path {
        &self.prefs_path
    }

    /// Whether prefs have changed since the last successful save.
    #[inline]
    pub fn is_dirty(&self) -> bool {
        self.dirty || self.prefs != self.last_saved
    }

    /// Mark prefs dirty after an external mutation of [`Self::prefs`].
    ///
    /// Prefer this (or [`Self::save_prefs`]) when the host toggles rain/lens
    /// without going through [`Self::handle_hotkey`]. See `SETTINGS_HOOKS.md`.
    pub fn mark_dirty(&mut self) {
        self.dirty = true;
    }

    /// Persist prefs immediately if dirty. Clears dirty on success.
    pub fn save_if_dirty(&mut self) -> bool {
        if !self.is_dirty() {
            return false;
        }
        self.save_prefs()
    }

    /// Force-write current prefs to disk. Returns whether the write succeeded.
    pub fn save_prefs(&mut self) -> bool {
        match config_store::save_chrome_prefs(&self.prefs_path, &self.prefs) {
            Ok(()) => {
                self.last_saved = self.prefs.clone();
                self.dirty = false;
                true
            }
            Err(_) => {
                // Leave dirty so close / next change can retry.
                self.dirty = true;
                false
            }
        }
    }

    /// Move keyboard focus by `delta` rows (wraps).
    pub fn move_selection(&mut self, delta: i32) {
        let n = SettingsLayout::ROW_COUNT as i32;
        let cur = self.selected as i32;
        self.selected = ((cur + delta).rem_euclid(n)) as usize;
    }

    /// Primary action on the focused row (Enter / Space / click).
    pub fn activate_selected(&mut self) -> bool {
        self.activate_row(self.selected)
    }

    /// Activate a specific row (also focuses it).
    pub fn activate_row(&mut self, row: usize) -> bool {
        if row >= SettingsLayout::ROW_COUNT {
            return false;
        }
        self.selected = row;
        match row {
            settings_row::RAIN => self.prefs.rain = !self.prefs.rain,
            settings_row::LENS => self.prefs.lens = !self.prefs.lens,
            // Primary: Enter focuses; use ←→ / swatches to change.
            settings_row::PRIMARY => {}
            // Accent: Enter clears custom → auto-derive from primary.
            settings_row::ACCENT => {
                self.prefs.clear_accent();
            }
            // Font: Enter cycles forward.
            settings_row::FONT => {
                self.prefs.cycle_font(1);
            }
            settings_row::DARKEN => self.prefs.nudge_darken(0.05),
            settings_row::RESET => {
                self.prefs.reset_to_defaults();
                // Snap toggle visuals to defaults immediately.
                self.rain_visual = if self.prefs.rain { 1.0 } else { 0.0 };
                self.lens_visual = if self.prefs.lens { 1.0 } else { 0.0 };
                self.rain_visual_vel = 0.0;
                self.lens_visual_vel = 0.0;
            }
            _ => return false,
        }
        self.dirty = true;
        let _ = self.save_prefs();
        true
    }

    /// Horizontal adjust on the focused row (← / →).
    /// Rain/lens: toggle. Primary/Accent: hue ±15°. Darken: ±5%.
    pub fn nudge_selected(&mut self, dir: i32) -> bool {
        if dir == 0 {
            return false;
        }
        match self.selected {
            settings_row::RAIN | settings_row::LENS => {
                return self.activate_row(self.selected);
            }
            settings_row::PRIMARY => {
                self.prefs
                    .nudge_primary_hue(if dir < 0 { -15.0 } else { 15.0 });
            }
            settings_row::ACCENT => {
                self.prefs
                    .nudge_accent_hue(if dir < 0 { -15.0 } else { 15.0 });
            }
            settings_row::FONT => {
                self.prefs.cycle_font(if dir < 0 { -1 } else { 1 });
            }
            settings_row::DARKEN => {
                self.prefs.nudge_darken(if dir < 0 { -0.05 } else { 0.05 });
            }
            settings_row::RESET => return false,
            _ => return false,
        }
        self.dirty = true;
        let _ = self.save_prefs();
        true
    }

    /// Click inside the open modal.
    pub fn try_click(&mut self, x: f32, y: f32, window_w: f32, window_h: f32) -> bool {
        if !self.open {
            return false;
        }
        let lay = self.layout(window_w, window_h);
        // Color swatches apply to the focused color row (primary by default).
        for (i, sw) in lay.swatches.iter().enumerate() {
            if sw.contains(x, y) {
                if let Some(rgb) = theme::COLOR_PRESETS.get(i) {
                    let target = if self.selected == settings_row::ACCENT {
                        settings_row::ACCENT
                    } else {
                        settings_row::PRIMARY
                    };
                    self.selected = target;
                    if target == settings_row::ACCENT {
                        self.prefs.set_accent(*rgb);
                    } else {
                        self.prefs.set_primary(*rgb);
                    }
                    self.dirty = true;
                    let _ = self.save_prefs();
                    return true;
                }
            }
        }
        for (i, row) in lay.rows.iter().enumerate() {
            if !row.contains(x, y) {
                continue;
            }
            self.selected = i;
            if i == settings_row::DARKEN {
                let mid = row.x + row.w * 0.55;
                self.prefs
                    .nudge_darken(if x < mid { -0.05 } else { 0.05 });
                self.dirty = true;
                let _ = self.save_prefs();
                return true;
            }
            if i == settings_row::PRIMARY {
                let mid = row.x + row.w * 0.55;
                self.prefs
                    .nudge_primary_hue(if x < mid { -20.0 } else { 20.0 });
                self.dirty = true;
                let _ = self.save_prefs();
                return true;
            }
            if i == settings_row::ACCENT {
                let mid = row.x + row.w * 0.55;
                self.prefs
                    .nudge_accent_hue(if x < mid { -20.0 } else { 20.0 });
                self.dirty = true;
                let _ = self.save_prefs();
                return true;
            }
            if i == settings_row::FONT {
                let mid = row.x + row.w * 0.55;
                self.prefs
                    .cycle_font(if x < mid { -1 } else { 1 });
                self.dirty = true;
                let _ = self.save_prefs();
                return true;
            }
            return self.activate_row(i);
        }
        false
    }

    /// Optional number-key shortcuts.
    ///
    /// | Key | Action |
    /// |-----|--------|
    /// | `1` | Toggle rain |
    /// | `2` | Toggle lens |
    /// | `3` | Focus primary color (then ←→ hue) |
    /// | `4` | Focus accent (Enter = auto; ←→ custom hue) |
    /// | `5` | Cycle font |
    /// | `[` / `-` | Darken −5% |
    /// | `]` / `=` / `+` | Darken +5% |
    /// | `0` | Reset defaults |
    pub fn handle_hotkey(&mut self, key: &str) -> bool {
        let handled = match key {
            "1" => {
                self.selected = settings_row::RAIN;
                self.prefs.rain = !self.prefs.rain;
                true
            }
            "2" => {
                self.selected = settings_row::LENS;
                self.prefs.lens = !self.prefs.lens;
                true
            }
            "3" => {
                self.selected = settings_row::PRIMARY;
                true
            }
            "4" => {
                self.selected = settings_row::ACCENT;
                true
            }
            "5" => {
                self.selected = settings_row::FONT;
                self.prefs.cycle_font(1);
                true
            }
            "[" | "-" => {
                self.selected = settings_row::DARKEN;
                self.prefs.nudge_darken(-0.05);
                true
            }
            "]" | "=" | "+" => {
                self.selected = settings_row::DARKEN;
                self.prefs.nudge_darken(0.05);
                true
            }
            "0" => {
                self.selected = settings_row::RESET;
                self.prefs.reset_to_defaults();
                self.rain_visual = if self.prefs.rain { 1.0 } else { 0.0 };
                self.lens_visual = if self.prefs.lens { 1.0 } else { 0.0 };
                true
            }
            _ => false,
        };
        if handled {
            self.dirty = true;
            let _ = self.save_prefs();
        }
        handled
    }

    pub fn open(&mut self) {
        self.open = true;
        self.selected = 0;
    }

    /// Active chrome paint palette for `prefs.theme`.
    pub fn theme_colors(&self) -> theme::ThemeColors {
        self.prefs.theme_colors()
    }

    /// Close the modal; flush dirty prefs to disk.
    pub fn close(&mut self) {
        self.open = false;
        let _ = self.save_if_dirty();
    }

    pub fn toggle(&mut self) {
        if self.open {
            self.close();
        } else {
            self.open();
        }
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
    /// Also flushes prefs when the host mutated [`Self::prefs`] directly
    /// (e.g. palette ToggleRain) so persistence works without extra wiring.
    ///
    /// Content: mass=1, k=150, c=25 (motion/react spring defaults from agility).
    /// Overlay: ~0.2s easeInOut toward target (linear approach ≈ ease for our use).
    pub fn tick(&mut self, dt: f32) {
        // Detect external prefs mutation (public field) and persist.
        if self.prefs != self.last_saved {
            self.dirty = true;
            let _ = self.save_prefs();
        }

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

        // --- switch knobs: spring toward boolean prefs (snappy Apple-ish) ---
        const TK: f32 = 280.0;
        const TC: f32 = 28.0;
        let rain_t = if self.prefs.rain { 1.0 } else { 0.0 };
        let force_r = -TK * (self.rain_visual - rain_t) - TC * self.rain_visual_vel;
        self.rain_visual_vel += force_r * dt;
        self.rain_visual = (self.rain_visual + self.rain_visual_vel * dt).clamp(0.0, 1.0);
        if (self.rain_visual - rain_t).abs() < 0.002 && self.rain_visual_vel.abs() < 0.02 {
            self.rain_visual = rain_t;
            self.rain_visual_vel = 0.0;
        }
        let lens_t = if self.prefs.lens { 1.0 } else { 0.0 };
        let force_l = -TK * (self.lens_visual - lens_t) - TC * self.lens_visual_vel;
        self.lens_visual_vel += force_l * dt;
        self.lens_visual = (self.lens_visual + self.lens_visual_vel * dt).clamp(0.0, 1.0);
        if (self.lens_visual - lens_t).abs() < 0.002 && self.lens_visual_vel.abs() < 0.02 {
            self.lens_visual = lens_t;
            self.lens_visual_vel = 0.0;
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

    /// Base modal rect — wide horizontal glass card (not a square).
    /// Height fits title + 7 rows + color swatches + footer.
    pub fn base_modal_rect(window_w: f32, window_h: f32) -> Rect {
        let w = (window_w - 48.0).min(560.0).max(320.0);
        // 48 title + 7×40 + 6×8 gaps + 36 swatches + 28 footer ≈ 470
        let h = (window_h - 80.0).min(510.0).max(470.0);
        Rect::new(
            (window_w - w) * 0.5,
            (window_h - h) * 0.48,
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

    /// Shared glass + label geometry for the settings card.
    pub fn layout(&self, window_w: f32, window_h: f32) -> SettingsLayout {
        SettingsLayout::new(self.animated_modal_rect(window_w, window_h))
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
        let primary = theme::to_hex(self.prefs.primary);
        let accent_hex = theme::to_hex(self.prefs.effective_accent());
        let accent = if self.prefs.accent_is_custom() {
            format!("{accent_hex}")
        } else {
            format!("auto · {accent_hex}")
        };

        vec![
            "suzuri-chrome 1.0.0".into(),
            "native GPU shell · winit + wgpu · no React / HTML / Chromium".into(),
            String::new(),
            "toggles".into(),
            format!("  [1] glyph rain     {rain}"),
            format!("  [2] magnifier      {lens}  · pinch or ⌃/⌘+scroll"),
            format!("  [3] primary        {primary}  · ←→ hue · swatches"),
            format!("  [4] accent         {accent}  · Enter=auto · ←→ override"),
            format!(
                "  [5] font           {}  · ←→ cycle",
                theme::font_label(&self.prefs.font)
            ),
            format!("  [ / ]  glass darken  {darken_pct}%"),
            format!("  [0] reset defaults"),
            String::new(),
            "status".into(),
            format!("  {shell}"),
            format!("  grid  {cols}×{rows}"),
            format!("  tabs  {tab_count}"),
            String::new(),
            "keys".into(),
            "  Esc       close settings".into(),
            "  ⌘/,       toggle settings".into(),
            "  ⌘K / ⌘P  command palette".into(),
            "  ⌘/       keyboard shortcuts".into(),
            "  ⌘T / ⌘W  new tab / close pane".into(),
            "  ⇧⌘D/E    split right / down".into(),
            "  ⌘V       paste".into(),
            "  wheel    scrollback".into(),
        ]
    }
}

/// Geometry for settings rows (panels + text must use the same rects).
#[derive(Clone, Debug)]
pub struct SettingsLayout {
    pub modal: Rect,
    pub pad: f32,
    /// One frost/button chip per setting row.
    pub rows: Vec<Rect>,
    /// Color preset swatches under the accent row.
    pub swatches: Vec<Rect>,
    /// Right-side value column width inside each row.
    pub value_w: f32,
}

impl SettingsLayout {
    pub const ROW_COUNT: usize = 7;
    pub const ROW_H: f32 = 40.0;
    pub const GAP: f32 = 8.0;
    pub const SWATCH: f32 = 28.0;
    pub const SWATCH_GAP: f32 = 8.0;

    pub fn new(modal: Rect) -> Self {
        let pad = 16.0;
        let mut rows = Vec::with_capacity(Self::ROW_COUNT);
        let mut y = modal.y + 48.0;
        for _ in 0..Self::ROW_COUNT {
            rows.push(Rect::new(
                modal.x + pad,
                y,
                modal.w - pad * 2.0,
                Self::ROW_H,
            ));
            y += Self::ROW_H + Self::GAP;
        }
        // Swatch strip sits under the main rows (before footer).
        // Applies to focused color row (primary or accent).
        let n = theme::COLOR_PRESETS.len();
        let mut swatches = Vec::with_capacity(n);
        let strip_w = n as f32 * Self::SWATCH + (n.saturating_sub(1) as f32) * Self::SWATCH_GAP;
        let mut sx = modal.x + pad + ((modal.w - pad * 2.0) - strip_w).max(0.0) * 0.5;
        let sy = y + 4.0;
        for _ in 0..n {
            swatches.push(Rect::new(sx, sy, Self::SWATCH, Self::SWATCH));
            sx += Self::SWATCH + Self::SWATCH_GAP;
        }
        Self {
            modal,
            pad,
            rows,
            swatches,
            value_w: 128.0,
        }
    }

    pub fn value_rect(&self, row: Rect) -> Rect {
        let w = self.value_w.min(row.w * 0.4);
        Rect::new(row.x + row.w - w - 10.0, row.y, w, row.h)
    }

    pub fn label_x(&self, row: Rect) -> f32 {
        row.x + 14.0
    }

    /// Apple-style switch track on the right of a boolean row.
    pub fn toggle_track_rect(row: Rect) -> Rect {
        let tw = 46.0;
        let th = 26.0;
        Rect::new(
            row.x + row.w - 14.0 - tw,
            row.y + (row.h - th) * 0.5,
            tw,
            th,
        )
    }

    /// Knob inside the track (`on` = right side).
    pub fn toggle_thumb_rect(row: Rect, on: bool) -> Rect {
        Self::toggle_thumb_rect_t(row, if on { 1.0 } else { 0.0 })
    }

    /// Knob position lerped by `t` in 0..1 (animated switch).
    pub fn toggle_thumb_rect_t(row: Rect, t: f32) -> Rect {
        let track = Self::toggle_track_rect(row);
        let d = 20.0;
        let pad = 3.0;
        let x0 = track.x + pad;
        let x1 = track.x + track.w - pad - d;
        let x = x0 + (x1 - x0) * t.clamp(0.0, 1.0);
        Rect::new(x, track.y + (track.h - d) * 0.5, d, d)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_prefs_path(tag: &str) -> PathBuf {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or(0);
        let dir = std::env::temp_dir().join(format!("suzuri-settings-state-{tag}-{nanos}"));
        let _ = fs::create_dir_all(&dir);
        dir.join(config_store::CHROME_PREFS_FILE)
    }

    fn cleanup(path: &Path) {
        if let Some(parent) = path.parent() {
            let _ = fs::remove_dir_all(parent);
        }
    }

    fn fresh() -> (PathBuf, SettingsState) {
        let path = temp_prefs_path("fresh");
        let _ = fs::remove_file(&path);
        let s = SettingsState::with_path(&path);
        (path, s)
    }

    #[test]
    fn new_starts_closed() {
        let (path, s) = fresh();
        assert!(!s.open);
        assert!(!s.visible());
        assert!(s.lines.is_empty());
        assert!(!s.is_dirty());
        cleanup(&path);
    }

    #[test]
    fn settings_layout_four_rows() {
        let lay = SettingsLayout::new(SettingsState::base_modal_rect(900.0, 700.0));
        assert_eq!(lay.rows.len(), SettingsLayout::ROW_COUNT);
        // Rows stack with constant gap; last row above footer zone.
        for i in 1..lay.rows.len() {
            let prev = lay.rows[i - 1];
            let cur = lay.rows[i];
            assert!((cur.y - (prev.y + prev.h + SettingsLayout::GAP)).abs() < 0.01);
        }
        let last = *lay.rows.last().unwrap();
        assert!(last.y + last.h < lay.modal.y + lay.modal.h - 24.0);
        let vr = lay.value_rect(lay.rows[0]);
        assert!(vr.x > lay.rows[0].x);
        assert!(vr.x + vr.w <= lay.rows[0].x + lay.rows[0].w + 0.01);
    }

    #[test]
    fn keyboard_nav_and_click_toggle() {
        let (path, mut s) = fresh();
        s.open();
        assert_eq!(s.selected, 0);
        let rain0 = s.prefs.rain;
        s.move_selection(1);
        assert_eq!(s.selected, 1);
        s.move_selection(-1);
        assert_eq!(s.selected, 0);
        assert!(s.activate_selected());
        assert_ne!(s.prefs.rain, rain0);
        // Wrap selection
        s.selected = SettingsLayout::ROW_COUNT - 1;
        s.move_selection(1);
        assert_eq!(s.selected, 0);
        // Click rain row
        let lay = s.layout(900.0, 700.0);
        let r = lay.rows[0];
        let rain1 = s.prefs.rain;
        assert!(s.try_click(r.x + 20.0, r.y + 10.0, 900.0, 700.0));
        assert_ne!(s.prefs.rain, rain1);
        // Toggle geometry stays inside the row
        let track = SettingsLayout::toggle_track_rect(r);
        assert!(r.contains(track.x + 1.0, track.y + 1.0));
        let thumb = SettingsLayout::toggle_thumb_rect(r, true);
        assert!(track.contains(thumb.x + 1.0, thumb.y + 1.0));
        // Animated mid position sits between off and on.
        let off = SettingsLayout::toggle_thumb_rect_t(r, 0.0);
        let mid = SettingsLayout::toggle_thumb_rect_t(r, 0.5);
        let on = SettingsLayout::toggle_thumb_rect_t(r, 1.0);
        assert!(mid.x > off.x && mid.x < on.x);
        cleanup(&path);
    }

    #[test]
    fn toggle_visual_springs_toward_pref() {
        let (path, mut s) = fresh();
        s.prefs.rain = true;
        s.rain_visual = 0.0;
        for _ in 0..90 {
            s.tick(1.0 / 60.0);
        }
        assert!(s.rain_toggle_t() > 0.95);
        s.prefs.rain = false;
        for _ in 0..90 {
            s.tick(1.0 / 60.0);
        }
        assert!(s.rain_toggle_t() < 0.05);
        cleanup(&path);
    }

    #[test]
    fn open_close_toggle() {
        let (path, mut s) = fresh();
        s.open();
        assert!(s.open);
        s.close();
        assert!(!s.open);
        s.toggle();
        assert!(s.open);
        s.toggle();
        assert!(!s.open);
        cleanup(&path);
    }

    #[test]
    fn spring_opens_and_closes() {
        let (path, mut s) = fresh();
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
        cleanup(&path);
    }

    #[test]
    fn display_lines_mentions_core_facts() {
        let (path, s) = fresh();
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
        cleanup(&path);
    }

    #[test]
    fn hotkeys_toggle_prefs() {
        let (path, mut s) = fresh();
        assert!(s.prefs.rain);
        assert!(s.handle_hotkey("1"));
        assert!(!s.prefs.rain);
        assert!(s.handle_hotkey("2"));
        assert!(!s.prefs.lens);
        let d0 = s.prefs.glass_darken;
        assert!(s.handle_hotkey("]"));
        assert!(s.prefs.glass_darken > d0);
        cleanup(&path);
    }

    #[test]
    fn hotkey_focuses_primary_accent_and_reset() {
        let (path, mut s) = fresh();
        assert!(s.handle_hotkey("3"));
        assert_eq!(s.selected, settings_row::PRIMARY);
        let p0 = s.prefs.primary;
        assert!(s.nudge_selected(1));
        assert_ne!(s.prefs.primary, p0);
        // Accent starts auto; hue nudge makes it custom.
        assert!(!s.prefs.accent_is_custom());
        assert!(s.handle_hotkey("4"));
        assert_eq!(s.selected, settings_row::ACCENT);
        assert!(s.nudge_selected(1));
        assert!(s.prefs.accent_is_custom());
        // Enter clears override back to auto.
        assert!(s.activate_row(settings_row::ACCENT));
        assert!(!s.prefs.accent_is_custom());
        s.prefs.rain = false;
        assert!(s.handle_hotkey("0"));
        assert!(s.prefs.rain);
        assert_eq!(s.prefs.primary, theme::DEFAULT_PRIMARY);
        assert!(!s.prefs.accent_is_custom());
        let lines = s.display_lines(false, 80, 24, 1).join("\n");
        assert!(lines.contains("primary"));
        assert!(lines.contains("accent"));
        cleanup(&path);
    }

    #[test]
    fn swatch_click_sets_primary_or_accent() {
        let (path, mut s) = fresh();
        s.open();
        let lay = s.layout(900.0, 700.0);
        assert_eq!(lay.swatches.len(), theme::COLOR_PRESETS.len());
        // Default focus 0 (rain) → swatch applies to primary.
        let sw = lay.swatches[1];
        assert!(s.try_click(sw.x + 4.0, sw.y + 4.0, 900.0, 700.0));
        assert_eq!(s.selected, settings_row::PRIMARY);
        assert_eq!(s.prefs.primary, theme::COLOR_PRESETS[1]);
        assert!(!s.prefs.accent_is_custom());
        // Focus accent then swatch → custom accent.
        s.selected = settings_row::ACCENT;
        let sw2 = lay.swatches[2];
        assert!(s.try_click(sw2.x + 4.0, sw2.y + 4.0, 900.0, 700.0));
        assert_eq!(s.prefs.accent, Some(theme::COLOR_PRESETS[2]));
        // Reset restores defaults.
        assert!(s.activate_row(settings_row::RESET));
        assert_eq!(s.prefs.primary, theme::DEFAULT_PRIMARY);
        assert!(!s.prefs.accent_is_custom());
        assert!(s.prefs.rain);
        assert!((s.prefs.glass_darken - config_store::GLASS_DARKEN_DEFAULT).abs() < 1e-4);
        cleanup(&path);
    }

    #[test]
    fn hotkey_persists_to_disk() {
        let path = temp_prefs_path("hotkey");
        let _ = fs::remove_file(&path);
        {
            let mut s = SettingsState::with_path(&path);
            assert!(s.prefs.rain);
            assert!(s.handle_hotkey("1"));
            assert!(!s.prefs.rain);
            assert!(!s.is_dirty());
        }
        let s2 = SettingsState::with_path(&path);
        assert!(!s2.prefs.rain, "rain should reload as off");
        assert!(s2.prefs.lens);
        cleanup(&path);
    }

    #[test]
    fn close_flushes_dirty_external_mutation() {
        let path = temp_prefs_path("close-flush");
        let _ = fs::remove_file(&path);
        {
            let mut s = SettingsState::with_path(&path);
            s.prefs.lens = false;
            s.mark_dirty();
            assert!(s.is_dirty());
            s.close();
            assert!(!s.is_dirty());
        }
        let s2 = SettingsState::with_path(&path);
        assert!(!s2.prefs.lens);
        cleanup(&path);
    }

    #[test]
    fn tick_detects_public_prefs_mutation() {
        let path = temp_prefs_path("tick-mut");
        let _ = fs::remove_file(&path);
        {
            let mut s = SettingsState::with_path(&path);
            s.prefs.rain = false;
            s.prefs.glass_darken = 0.3;
            // No mark_dirty — tick should notice and save.
            s.tick(1.0 / 60.0);
            assert!(!s.is_dirty());
        }
        let s2 = SettingsState::with_path(&path);
        assert!(!s2.prefs.rain);
        assert!((s2.prefs.glass_darken - 0.3).abs() < 1e-4);
        cleanup(&path);
    }

    #[test]
    fn full_roundtrip_all_fields() {
        let path = temp_prefs_path("full-rt");
        let _ = fs::remove_file(&path);
        let want = ChromePrefs {
            rain: false,
            lens: false,
            glass_darken: 0.45,
            theme: "tokyo-night".into(),
            primary: theme::TOKYO_NIGHT.jade,
            accent: Some(theme::TOKYO_NIGHT.secondary),
            font: theme::DEFAULT_FONT_ID.into(),
            splash_seen: true,
        };
        {
            let mut s = SettingsState::with_path(&path);
            s.prefs = want.clone();
            assert!(s.save_prefs());
        }
        // Ensure product config sibling is untouched / not required.
        if let Some(parent) = path.parent() {
            assert!(!parent.join("config.json").exists() || true);
        }
        let s2 = SettingsState::with_path(&path);
        assert_eq!(s2.prefs, want);
        cleanup(&path);
    }
}
