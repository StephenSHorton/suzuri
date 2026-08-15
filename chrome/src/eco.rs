//! Paint cadence: keep PTYs and MCP live, skip GPU when the window isn't worth it.
//!
//! Shells, TUIs, and Grok keep running regardless of focus. We still drain the
//! PTY, parse ANSI, reply to DA/OSC, and publish `chrome_status.json`. What
//! stops is the wgpu present + rain encode + text reshape loop.

use std::time::Duration;

/// Wake while rain / springs need a present (~60 Hz).
pub const ANIM_WAKE: Duration = Duration::from_millis(16);
/// PTY + mailbox poll when the GPU is idle.
pub const PTY_WAKE: Duration = Duration::from_millis(33);
/// Max present rate for cell updates while unfocused / occluded.
pub const BACKGROUND_PAINT: Duration = Duration::from_millis(80);
/// Caret-only presents while focused (no rain).
pub const CARET_WAKE: Duration = Duration::from_millis(120);

/// What the GPU should do on this wake.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum GpuDemand {
    /// Present every anim frame (rain blit, overlay springs, scroll ease).
    Continuous,
    /// Cells / chrome changed — present, possibly rate-limited.
    Dirty,
    /// Only the caret/cursor wants to blink.
    Caret,
    /// No present. Still poll PTYs.
    Idle,
}

/// Snapshot of the reasons we might paint. Pure data so it can be unit-tested
/// without a window or wgpu device.
#[derive(Clone, Copy, Debug)]
pub struct PaintInput {
    pub app_focused: bool,
    pub occluded: bool,
    pub animate_unfocused: bool,
    pub rain: bool,
    pub ui_animating: bool,
    pub paint_dirty: bool,
    pub caret_live: bool,
}

impl PaintInput {
    /// Decorative GPU (rain, springs, caret) is allowed.
    pub fn effects_live(self) -> bool {
        self.animate_unfocused || (self.app_focused && !self.occluded)
    }
}

pub fn gpu_demand(i: PaintInput) -> GpuDemand {
    let live = i.effects_live();
    if live && (i.rain || i.ui_animating) {
        return GpuDemand::Continuous;
    }
    if i.paint_dirty {
        return GpuDemand::Dirty;
    }
    if live && i.caret_live {
        return GpuDemand::Caret;
    }
    GpuDemand::Idle
}

pub fn wake_delay(i: PaintInput) -> Duration {
    match gpu_demand(i) {
        GpuDemand::Continuous => ANIM_WAKE,
        GpuDemand::Caret => CARET_WAKE,
        GpuDemand::Dirty if i.effects_live() => ANIM_WAKE,
        GpuDemand::Dirty | GpuDemand::Idle => PTY_WAKE,
    }
}

/// Minimum time between Dirty presents. Zero when the window is the focus so
/// streaming TUI output stays snappy; throttled in the background.
pub fn dirty_min_interval(i: PaintInput) -> Duration {
    if i.effects_live() {
        Duration::ZERO
    } else {
        BACKGROUND_PAINT
    }
}

pub fn rain_should_run(i: PaintInput) -> bool {
    i.rain && i.effects_live()
}

/// True while a 0↔1 spring is still moving (open or closing).
pub fn spring_motion(open: bool, present: f32) -> bool {
    let target = if open { 1.0 } else { 0.0 };
    (present - target).abs() > 0.02
}

#[cfg(test)]
mod tests {
    use super::*;

    fn base() -> PaintInput {
        PaintInput {
            app_focused: true,
            occluded: false,
            animate_unfocused: false,
            rain: false,
            ui_animating: false,
            paint_dirty: false,
            caret_live: false,
        }
    }

    #[test]
    fn focused_rain_is_continuous() {
        let mut i = base();
        i.rain = true;
        assert_eq!(gpu_demand(i), GpuDemand::Continuous);
        assert!(rain_should_run(i));
        assert_eq!(wake_delay(i), ANIM_WAKE);
    }

    #[test]
    fn unfocused_rain_does_not_spin() {
        let mut i = base();
        i.app_focused = false;
        i.rain = true;
        i.paint_dirty = false;
        assert_eq!(gpu_demand(i), GpuDemand::Idle);
        assert!(!rain_should_run(i));
        assert_eq!(wake_delay(i), PTY_WAKE);
    }

    #[test]
    fn unfocused_pty_output_is_throttled_dirty() {
        let mut i = base();
        i.app_focused = false;
        i.rain = true;
        i.paint_dirty = true;
        assert_eq!(gpu_demand(i), GpuDemand::Dirty);
        assert_eq!(dirty_min_interval(i), BACKGROUND_PAINT);
        assert!(!rain_should_run(i));
    }

    #[test]
    fn occluded_is_treated_like_unfocused() {
        let mut i = base();
        i.occluded = true;
        i.rain = true;
        i.caret_live = true;
        assert!(!i.effects_live());
        assert_eq!(gpu_demand(i), GpuDemand::Idle);
        assert!(!rain_should_run(i));
    }

    #[test]
    fn animate_unfocused_restores_demo_spin() {
        let mut i = base();
        i.app_focused = false;
        i.occluded = true;
        i.rain = true;
        i.animate_unfocused = true;
        assert!(i.effects_live());
        assert_eq!(gpu_demand(i), GpuDemand::Continuous);
        assert!(rain_should_run(i));
    }

    #[test]
    fn focused_pty_dirty_is_immediate() {
        let mut i = base();
        i.paint_dirty = true;
        assert_eq!(gpu_demand(i), GpuDemand::Dirty);
        assert_eq!(dirty_min_interval(i), Duration::ZERO);
    }

    #[test]
    fn focused_caret_only_is_slow() {
        let mut i = base();
        i.caret_live = true;
        assert_eq!(gpu_demand(i), GpuDemand::Caret);
        assert_eq!(wake_delay(i), CARET_WAKE);
    }

    #[test]
    fn unfocused_does_not_blink_caret() {
        let mut i = base();
        i.app_focused = false;
        i.caret_live = true;
        assert_eq!(gpu_demand(i), GpuDemand::Idle);
    }

    #[test]
    fn settled_overlay_is_not_motion() {
        assert!(!spring_motion(true, 1.0));
        assert!(!spring_motion(false, 0.0));
        assert!(spring_motion(true, 0.4));
        assert!(spring_motion(false, 0.8));
    }
}
