//! Pure hit-test for window chrome. Logical f32 coords only — no winit types.

use crate::layout::{FrameLayout, Metrics, Rect};

/// Interactive region under a pointer (logical coordinates).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum HitTarget {
    Close,
    Minimize,
    Zoom,
    /// Tab chip index into [`FrameLayout::tab_chips`].
    Tab(usize),
    NewTab,
    Settings,
    WarpBar,
    Terminal,
    /// Title-bar drag region (window move). On macOS, excludes traffic lights.
    TitleDrag,
    None,
}

/// Platform detection for chrome affordances (traffic lights, etc.).
#[inline]
pub fn is_mac() -> bool {
    cfg!(target_os = "macos")
}

/// macOS traffic-light geometry (close, minimize, zoom), top-left origin.
///
/// Positions match standard frameless title-bar placement relative to
/// [`Metrics::title_h`]:
/// - left padding 16px
/// - 12×12 dots
/// - 8px gap between dots
/// - vertically centered in the title strip
pub fn traffic_light_rects(metrics: &Metrics) -> [Rect; 3] {
    const LEFT: f32 = 16.0;
    const DOT: f32 = 12.0;
    const GAP: f32 = 8.0;

    let y = (metrics.title_h - DOT) * 0.5;
    [
        Rect::new(LEFT, y, DOT, DOT),                     // close
        Rect::new(LEFT + DOT + GAP, y, DOT, DOT),         // minimize
        Rect::new(LEFT + 2.0 * (DOT + GAP), y, DOT, DOT), // zoom
    ]
}

/// Right edge of the traffic-light cluster (with a little trailing pad), used to
/// carve title-drag away from the buttons on macOS.
fn traffic_lights_right() -> f32 {
    const LEFT: f32 = 16.0;
    const DOT: f32 = 12.0;
    const GAP: f32 = 8.0;
    const TRAIL: f32 = 8.0;
    LEFT + 3.0 * DOT + 2.0 * GAP + TRAIL
}

/// Hit-test chrome at `(x, y)` in logical pixels (top-left origin).
///
/// Priority: traffic lights (mac) → tab chips / new / settings → warp →
/// terminal → title drag → none.
pub fn hit_test(
    layout: &FrameLayout,
    metrics: &Metrics,
    x: f32,
    y: f32,
    is_mac: bool,
) -> HitTarget {
    if is_mac {
        let lights = traffic_light_rects(metrics);
        if lights[0].contains(x, y) {
            return HitTarget::Close;
        }
        if lights[1].contains(x, y) {
            return HitTarget::Minimize;
        }
        if lights[2].contains(x, y) {
            return HitTarget::Zoom;
        }
    }

    for (i, chip) in layout.tab_chips.iter().enumerate() {
        if chip.contains(x, y) {
            return HitTarget::Tab(i);
        }
    }
    if layout.tab_new.contains(x, y) {
        return HitTarget::NewTab;
    }
    if layout.settings.contains(x, y) {
        return HitTarget::Settings;
    }

    if layout.warp.contains(x, y) {
        return HitTarget::WarpBar;
    }
    if layout.terminal.contains(x, y) {
        return HitTarget::Terminal;
    }

    if layout.title.contains(x, y) {
        if is_mac && x < traffic_lights_right() {
            // Left of title strip reserved for traffic lights (even if miss).
            return HitTarget::None;
        }
        return HitTarget::TitleDrag;
    }

    HitTarget::None
}

#[cfg(test)]
mod tests {
    use super::*;

    fn demo_layout() -> (FrameLayout, Metrics) {
        let m = Metrics::default();
        let layout = FrameLayout::compute(1120.0, 740.0, m, 2);
        (layout, m)
    }

    #[test]
    fn traffic_lights_centered_in_title() {
        let m = Metrics::default();
        let [c, min, z] = traffic_light_rects(&m);
        assert_eq!(c.w, 12.0);
        assert_eq!(c.h, 12.0);
        assert!((c.y - (m.title_h - 12.0) * 0.5).abs() < f32::EPSILON);
        assert!((min.x - (c.x + c.w + 8.0)).abs() < f32::EPSILON);
        assert!((z.x - (min.x + min.w + 8.0)).abs() < f32::EPSILON);
    }

    #[test]
    fn mac_close_hit() {
        let (layout, m) = demo_layout();
        let [close, ..] = traffic_light_rects(&m);
        let t = hit_test(&layout, &m, close.x + 1.0, close.y + 1.0, true);
        assert_eq!(t, HitTarget::Close);
    }

    #[test]
    fn title_drag_skips_traffic_zone_on_mac() {
        let (layout, m) = demo_layout();
        assert_eq!(
            hit_test(&layout, &m, 8.0, m.title_h * 0.5, true),
            HitTarget::None
        );
        assert_eq!(
            hit_test(&layout, &m, 200.0, m.title_h * 0.5, true),
            HitTarget::TitleDrag
        );
        assert_eq!(
            hit_test(&layout, &m, 8.0, m.title_h * 0.5, false),
            HitTarget::TitleDrag
        );
    }

    #[test]
    fn tabs_and_panels() {
        let (layout, m) = demo_layout();
        assert_eq!(layout.tab_chips.len(), 2);
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.tab_chips[0].x + 1.0,
                layout.tab_chips[0].y + 1.0,
                false
            ),
            HitTarget::Tab(0)
        );
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.tab_chips[1].x + 1.0,
                layout.tab_chips[1].y + 1.0,
                false
            ),
            HitTarget::Tab(1)
        );
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.tab_new.x + 1.0,
                layout.tab_new.y + 1.0,
                false
            ),
            HitTarget::NewTab
        );
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.settings.x + 1.0,
                layout.settings.y + 1.0,
                false
            ),
            HitTarget::Settings
        );
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.warp.x + 1.0,
                layout.warp.y + 1.0,
                false
            ),
            HitTarget::WarpBar
        );
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.terminal.x + 1.0,
                layout.terminal.y + 1.0,
                false
            ),
            HitTarget::Terminal
        );
    }

    #[test]
    fn dynamic_tab_count() {
        let m = Metrics::default();
        let layout = FrameLayout::compute(1120.0, 740.0, m, 4);
        assert_eq!(layout.tab_chips.len(), 4);
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.tab_chips[3].x + 1.0,
                layout.tab_chips[3].y + 1.0,
                false
            ),
            HitTarget::Tab(3)
        );
    }
}
