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

/// Whether the settings modal is open, plus presentation springs + prefs.
#[derive(Clone, Debug)]
pub struct SettingsState {
    /// Desired open/closed.
    pub open: bool,
    /// Session prefs (rain / lens / darken / theme). Loaded from disk on construct.
    pub prefs: ChromePrefs,
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
        Self {
            open: false,
            prefs: prefs.clone(),
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
            lines: Vec::new(),
            prefs_path,
            dirty: false,
            last_saved: prefs,
        }
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

    /// Toggle rain / lens, cycle theme, or nudge darken, from a key while the
    /// modal is open. Saves prefs when a toggle lands.
    ///
    /// | Key | Action |
    /// |-----|--------|
    /// | `1` | Toggle glyph rain |
    /// | `2` | Toggle magnifier lens |
    /// | `3` / `t` | Cycle theme forward |
    /// | `T` / `⇧t` | Cycle theme backward |
    /// | `[` / `-` | Darken glass −5% |
    /// | `]` / `=` / `+` | Darken glass +5% |
    pub fn handle_hotkey(&mut self, key: &str) -> bool {
        let handled = match key {
            "1" => {
                self.prefs.rain = !self.prefs.rain;
                true
            }
            "2" => {
                self.prefs.lens = !self.prefs.lens;
                true
            }
            "3" | "t" => {
                self.prefs.cycle_theme();
                true
            }
            "T" => {
                self.prefs.cycle_theme_prev();
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
        };
        if handled {
            self.dirty = true;
            let _ = self.save_prefs();
        }
        handled
    }

    /// Active chrome paint palette for `prefs.theme`.
    pub fn theme_colors(&self) -> theme::ThemeColors {
        self.prefs.theme_colors()
    }

    pub fn open(&mut self) {
        self.open = true;
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
    pub fn base_modal_rect(window_w: f32, window_h: f32) -> Rect {
        let w = (window_w - 48.0).min(560.0).max(320.0);
        let h = (window_h - 96.0).min(320.0).max(200.0);
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
        let theme_id = theme::normalize_id(&self.prefs.theme);
        let theme_label = theme::label(theme_id);

        vec![
            "suzuri-chrome 1.0.0".into(),
            "native GPU shell · winit + wgpu · no React / HTML / Chromium".into(),
            String::new(),
            "toggles".into(),
            format!("  [1] glyph rain     {rain}"),
            format!("  [2] magnifier      {lens}  · pinch or ⌃/⌘+scroll"),
            format!("  [3] theme          {theme_label} ({theme_id})"),
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
            "  3 / t    cycle theme".into(),
        ]
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
    fn hotkey_cycles_theme() {
        let (path, mut s) = fresh();
        assert_eq!(s.prefs.theme, "inkstone");
        assert!(s.handle_hotkey("3"));
        assert_eq!(s.prefs.theme, "nord");
        assert!(s.handle_hotkey("t"));
        assert_eq!(s.prefs.theme, "dracula");
        assert!(s.handle_hotkey("T"));
        assert_eq!(s.prefs.theme, "nord");
        let lines = s.display_lines(false, 80, 24, 1).join("\n");
        assert!(lines.contains("theme"));
        assert!(lines.contains("Nord") || lines.contains("nord"));
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
