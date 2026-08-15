//! Pure hit-test for window chrome. Logical f32 coords only — no winit types.

use crate::layout::{FrameLayout, Metrics, Rect};
use crate::panes::DockEdge;
use crate::session::ChromeSession;

/// Interactive region under a pointer (logical coordinates).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum HitTarget {
    Close,
    Minimize,
    Zoom,
    /// Tab chip index into [`FrameLayout::tab_chips`].
    Tab(usize),
    /// Close (×) on tab strip index.
    TabClose(usize),
    NewTab,
    Settings,
    /// Top-right caffeine cup (left of logo).
    Caffeine,
    /// Local input strip for pane id.
    WarpBar(u64),
    /// Path / divider / header chrome of a pane — grab handle for re-dock drag.
    PaneChrome(u64),
    /// Close × on a pane header.
    PaneClose(u64),
    /// Split sash (a_leaf identifies the branch).
    Sash(u64),
    /// History cells for pane id.
    Terminal(u64),
    /// Scrollbar track / thumb on the right of the cell well (pane id).
    ScrollBar(u64),
    /// Title-bar drag region (window move). On macOS, excludes traffic lights.
    TitleDrag,
    None,
}

/// Pane id under a chrome hit, if the pointer is on that pane's surface.
#[inline]
pub fn pane_id_from_hit(hit: HitTarget) -> Option<u64> {
    match hit {
        HitTarget::Terminal(id)
        | HitTarget::WarpBar(id)
        | HitTarget::ScrollBar(id)
        | HitTarget::PaneChrome(id)
        | HitTarget::PaneClose(id) => Some(id),
        _ => None,
    }
}

/// Width of the scroll hit gutter inside the cell well (logical px).
pub const SCROLL_GUTTER_W: f32 = 10.0;

/// Pointer slop before a terminal press becomes a cell selection.
/// A click under this distance only changes pane focus (same as the keyboard).
pub const TERM_SELECT_DRAG_PX: f32 = 4.0;

/// True once the pointer has moved far enough to start a terminal selection.
#[inline]
pub fn term_select_drag_started(dx: f32, dy: f32) -> bool {
    dx.hypot(dy) >= TERM_SELECT_DRAG_PX
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
    let left = metrics.px(16.0);
    let dot = metrics.px(12.0);
    let gap = metrics.px(8.0);

    let y = (metrics.title_h - dot) * 0.5;
    [
        Rect::new(left, y, dot, dot),                     // close
        Rect::new(left + dot + gap, y, dot, dot),         // minimize
        Rect::new(left + 2.0 * (dot + gap), y, dot, dot), // zoom
    ]
}

/// Right edge of the traffic-light cluster (with a little trailing pad), used to
/// carve title-drag away from the buttons on macOS.
fn traffic_lights_right(metrics: &Metrics) -> f32 {
    let left = metrics.px(16.0);
    let dot = metrics.px(12.0);
    let gap = metrics.px(8.0);
    let trail = metrics.px(8.0);
    left + 3.0 * dot + 2.0 * gap + trail
}

/// Hit-test chrome at `(x, y)` in logical pixels (top-left origin).
///
/// Priority: traffic lights (mac) → tabs / + / logo-settings →
/// input strip (bottom) → history cells → title drag → none.
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

    // Right cluster: caffeine then logo/settings
    if layout.caffeine.contains(x, y) {
        return HitTarget::Caffeine;
    }
    if layout.logo.contains(x, y) || layout.settings.contains(x, y) {
        return HitTarget::Settings;
    }

    // Close × first (sits inside the chip; higher priority than select).
    for (i, close) in layout.tab_closes.iter().enumerate() {
        if close.contains(x, y) {
            return HitTarget::TabClose(i);
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

    for sash in &layout.sashes {
        if sash.rect.contains(x, y) {
            return HitTarget::Sash(sash.a_leaf);
        }
    }

    // Multi-pane: header / path / divider = drag chrome; warp = command line; cells = PTY.
    for pl in &layout.panes {
        if pl.close.w > 1.0 && pl.close.contains(x, y) {
            return HitTarget::PaneClose(pl.pane_id);
        }
        if pl.header.h > 1.0 && pl.header.contains(x, y) {
            return HitTarget::PaneChrome(pl.pane_id);
        }
        if pl.path.contains(x, y) || pl.divider.contains(x, y) {
            return HitTarget::PaneChrome(pl.pane_id);
        }
        if pl.warp.contains(x, y) {
            return HitTarget::WarpBar(pl.pane_id);
        }
        if pl.cells.contains(x, y) {
            // Right gutter: scrollbar (only when the well is wide enough).
            if pl.cells.w > SCROLL_GUTTER_W * 3.0
                && x >= pl.cells.x + pl.cells.w - SCROLL_GUTTER_W
            {
                return HitTarget::ScrollBar(pl.pane_id);
            }
            return HitTarget::Terminal(pl.pane_id);
        }
        // Glass chrome (margins) — select pane but treat as terminal hit for focus
        if pl.glass.contains(x, y) {
            return HitTarget::Terminal(pl.pane_id);
        }
    }

    if layout.title.contains(x, y) {
        if is_mac && x < traffic_lights_right(metrics) {
            return HitTarget::None;
        }
        // Don't drag when over tab chips (already handled); empty bar drag.
        return HitTarget::TitleDrag;
    }

    HitTarget::None
}

/// Where a dragged pane would land if dropped at `(x, y)`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DropKind {
    Edge { pane_id: u64, edge: DockEdge },
    Tab { tab_id: u64 },
    /// Insert a dragged tab before this strip index (same window).
    TabInsert { index: usize },
    /// Pointer left the frame — tear the dragged tab into a new window.
    TearOff,
}

/// Classify a drop. `None` = no legal landing (including over the moving pane).
/// `surface` selects which window's tab strip `layout.tab_chips` maps to.
pub fn classify_drop(
    layout: &FrameLayout,
    session: &ChromeSession,
    surface: u64,
    x: f32,
    y: f32,
    moving: u64,
) -> Option<DropKind> {
    let strip = session.tabs_on_surface(surface);
    for (i, chip) in layout.tab_chips.iter().enumerate() {
        if !chip.contains(x, y) {
            continue;
        }
        let tab_id = *strip.get(i)?;
        let tab = session.tabs.iter().find(|t| t.id == tab_id)?;
        if tab.root.contains_pane(moving) && tab.root.leaf_ids().len() <= 1 {
            return None;
        }
        return Some(DropKind::Tab { tab_id: tab.id });
    }
    for pl in &layout.panes {
        if !pl.glass.contains(x, y) {
            continue;
        }
        if pl.pane_id == moving {
            return None;
        }
        if session.panes.get(&pl.pane_id).is_some_and(|p| p.exiting) {
            return None;
        }
        return Some(DropKind::Edge {
            pane_id: pl.pane_id,
            edge: edge_of(pl.glass, x, y),
        });
    }
    None
}

/// Tab-strip drop on `layout`.
///
/// `from_idx` is this strip's chip index when the tab already lives here.
/// `None` means the tab came from another window (insert, do not collapse).
/// Leaving the **window frame** is TearOff; dropping into the workspace of
/// the same window is a no-op (not a tear).
pub fn classify_tab_drop(
    layout: &FrameLayout,
    x: f32,
    y: f32,
    from_idx: Option<usize>,
) -> Option<DropKind> {
    let win_h = layout.workspace.y + layout.workspace.h;
    let off_frame = y < -8.0 || x < -24.0 || x > layout.title.w + 24.0 || y > win_h + 8.0;
    if off_frame {
        return Some(DropKind::TearOff);
    }
    if layout.tab_chips.is_empty() {
        return match from_idx {
            Some(_) => None,
            None => Some(DropKind::TabInsert { index: 0 }),
        };
    }
    let mut insert = layout.tab_chips.len();
    for (i, chip) in layout.tab_chips.iter().enumerate() {
        if x < chip.x + chip.w * 0.5 {
            insert = i;
            break;
        }
    }
    // Below the tab strip, still inside the window: same-window no-op;
    // cross-window drop docks the tab at the end of this strip.
    if y >= layout.workspace.y {
        return match from_idx {
            Some(_) => None,
            None => Some(DropKind::TabInsert {
                index: layout.tab_chips.len(),
            }),
        };
    }
    let to = match from_idx {
        Some(from) if insert > from => insert.saturating_sub(1),
        Some(_) => insert,
        None => insert,
    };
    if from_idx == Some(to) {
        return None;
    }
    if from_idx.is_some() && to >= layout.tab_chips.len() {
        return None;
    }
    Some(DropKind::TabInsert { index: to })
}

/// Outer 28% of each side is that edge; the center docks to the right.
pub fn edge_of(r: Rect, x: f32, y: f32) -> DockEdge {
    let u = ((x - r.x) / r.w.max(1.0)).clamp(0.0, 1.0);
    let v = ((y - r.y) / r.h.max(1.0)).clamp(0.0, 1.0);
    const BAND: f32 = 0.28;
    let dl = u;
    let dr = 1.0 - u;
    let dt = v;
    let db = 1.0 - v;
    let m = dl.min(dr).min(dt).min(db);
    if m > BAND {
        return DockEdge::Right;
    }
    if (dl - m).abs() < f32::EPSILON {
        DockEdge::Left
    } else if (dr - m).abs() < f32::EPSILON {
        DockEdge::Right
    } else if (dt - m).abs() < f32::EPSILON {
        DockEdge::Top
    } else {
        DockEdge::Bottom
    }
}

/// Physical outer origin so `chip`'s center sits on `screen` (pointer).
/// `scale` is the destination window's scale factor.
pub fn window_origin_for_tab_drop(
    screen: (i32, i32),
    chip: Rect,
    scale: f64,
) -> (i32, i32) {
    let ax = ((chip.x + chip.w * 0.5) as f64 * scale).round() as i32;
    let ay = ((chip.y + chip.h * 0.5) as f64 * scale).round() as i32;
    (screen.0 - ax, screen.1 - ay)
}

/// Highlight strip for a drop edge, inset inside `glass`.
pub fn drop_edge_rect(glass: Rect, edge: DockEdge) -> Rect {
    let t = (glass.w.min(glass.h) * 0.12).clamp(8.0, 28.0);
    match edge {
        DockEdge::Left => Rect::new(glass.x, glass.y, t, glass.h),
        DockEdge::Right => Rect::new(glass.x + glass.w - t, glass.y, t, glass.h),
        DockEdge::Top => Rect::new(glass.x, glass.y, glass.w, t),
        DockEdge::Bottom => Rect::new(glass.x, glass.y + glass.h - t, glass.w, t),
    }
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
        // Empty strip between last chrome control and logo should drag.
        let x = layout.tab_new.x + layout.tab_new.w + 16.0;
        let y = m.title_h * 0.5;
        assert!(
            x < layout.logo.x,
            "need free space between + and logo for drag"
        );
        assert_eq!(hit_test(&layout, &m, x, y, true), HitTarget::TitleDrag);
    }

    #[test]
    fn tabs_and_panels() {
        let (layout, m) = demo_layout();
        assert_eq!(layout.tab_chips.len(), 2);
        assert_eq!(layout.tab_closes.len(), 2);
        // Left band of chip → select (not the close × on the right).
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.tab_chips[0].x + 12.0,
                layout.tab_chips[0].y + layout.tab_chips[0].h * 0.5,
                false
            ),
            HitTarget::Tab(0)
        );
        let c0 = layout.tab_closes[0];
        assert_eq!(
            hit_test(
                &layout,
                &m,
                c0.x + c0.w * 0.5,
                c0.y + c0.h * 0.5,
                false
            ),
            HitTarget::TabClose(0)
        );
        assert_eq!(
            hit_test(
                &layout,
                &m,
                layout.tab_chips[1].x + 12.0,
                layout.tab_chips[1].y + layout.tab_chips[1].h * 0.5,
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
        let w = hit_test(
            &layout,
            &m,
            layout.warp.x + 1.0,
            layout.warp.y + 1.0,
            false,
        );
        assert!(matches!(w, HitTarget::WarpBar(_)));
        let t = hit_test(
            &layout,
            &m,
            layout.cells.x + 1.0,
            layout.cells.y + 1.0,
            false,
        );
        assert!(matches!(t, HitTarget::Terminal(_)));
        let chrome = hit_test(
            &layout,
            &m,
            layout.path.x + 4.0,
            layout.path.y + layout.path.h * 0.5,
            false,
        );
        assert!(
            matches!(chrome, HitTarget::PaneChrome(_)),
            "path strip is the pane grab handle, got {chrome:?}"
        );
        let header = hit_test(
            &layout,
            &m,
            layout.panes[0].header.x + 4.0,
            layout.panes[0].header.y + 4.0,
            false,
        );
        assert!(
            matches!(header, HitTarget::PaneChrome(_)),
            "header is pane chrome, got {header:?}"
        );
        let px = layout.panes[0].close;
        let xhit = hit_test(&layout, &m, px.x + px.w * 0.5, px.y + px.h * 0.5, false);
        assert!(
            matches!(xhit, HitTarget::PaneClose(_)),
            "pane × should close, got {xhit:?}"
        );
    }

    #[test]
    fn pane_id_from_hit_reads_pane_surfaces() {
        assert_eq!(pane_id_from_hit(HitTarget::Terminal(7)), Some(7));
        assert_eq!(pane_id_from_hit(HitTarget::WarpBar(3)), Some(3));
        assert_eq!(pane_id_from_hit(HitTarget::ScrollBar(2)), Some(2));
        assert_eq!(pane_id_from_hit(HitTarget::PaneChrome(9)), Some(9));
        assert_eq!(pane_id_from_hit(HitTarget::PaneClose(4)), Some(4));
        assert_eq!(pane_id_from_hit(HitTarget::TitleDrag), None);
        assert_eq!(pane_id_from_hit(HitTarget::Tab(0)), None);
        assert_eq!(pane_id_from_hit(HitTarget::None), None);
    }

    #[test]
    fn term_select_waits_for_a_small_drag() {
        assert!(!term_select_drag_started(0.0, 0.0));
        assert!(!term_select_drag_started(2.0, 2.0));
        assert!(term_select_drag_started(TERM_SELECT_DRAG_PX, 0.0));
        assert!(term_select_drag_started(3.0, 3.0));
    }

    #[test]
    fn edge_of_corners_pick_nearest_side() {
        let r = Rect::new(0.0, 0.0, 100.0, 100.0);
        assert_eq!(edge_of(r, 5.0, 50.0), crate::panes::DockEdge::Left);
        assert_eq!(edge_of(r, 95.0, 50.0), crate::panes::DockEdge::Right);
        assert_eq!(edge_of(r, 50.0, 5.0), crate::panes::DockEdge::Top);
        assert_eq!(edge_of(r, 50.0, 95.0), crate::panes::DockEdge::Bottom);
        assert_eq!(edge_of(r, 50.0, 50.0), crate::panes::DockEdge::Right);
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
                layout.tab_chips[3].x + 12.0,
                layout.tab_chips[3].y + layout.tab_chips[3].h * 0.5,
                false
            ),
            HitTarget::Tab(3)
        );
    }

    #[test]
    fn tab_drop_reorders_along_strip() {
        let (layout, _) = demo_layout();
        let mid_second = layout.tab_chips[1].x + layout.tab_chips[1].w * 0.6;
        let y = layout.tab_chips[1].y + layout.tab_chips[1].h * 0.5;
        assert_eq!(
            classify_tab_drop(&layout, mid_second, y, Some(0)),
            Some(DropKind::TabInsert { index: 1 })
        );
        assert_eq!(
            classify_tab_drop(&layout, layout.tab_chips[0].x + 4.0, y, Some(0)),
            None
        );
    }

    #[test]
    fn tab_drop_in_workspace_does_not_tear() {
        let (layout, _) = demo_layout();
        let y = layout.workspace.y + layout.workspace.h * 0.4;
        assert_eq!(
            classify_tab_drop(&layout, layout.title.w * 0.5, y, Some(0)),
            None
        );
    }

    #[test]
    fn tab_drop_off_frame_tears() {
        let (layout, _) = demo_layout();
        assert_eq!(
            classify_tab_drop(&layout, 40.0, -40.0, Some(0)),
            Some(DropKind::TearOff)
        );
    }

    #[test]
    fn tab_drop_from_other_window_appends() {
        let (layout, _) = demo_layout();
        let y = layout.workspace.y + 20.0;
        assert_eq!(
            classify_tab_drop(&layout, 80.0, y, None),
            Some(DropKind::TabInsert { index: 2 })
        );
    }

    #[test]
    fn tearoff_origin_puts_chip_under_pointer() {
        let chip = Rect::new(48.0, 6.0, 112.0, 20.0);
        let (x, y) = window_origin_for_tab_drop((800, 400), chip, 2.0);
        // chip center = (104, 16) logical → (208, 32) physical
        assert_eq!((x, y), (592, 368));
    }

    #[test]
    fn classify_drop_uses_surface_tabs() {
        let (layout, _) = demo_layout();
        let mut session = crate::session::ChromeSession::new(80, 24);
        let (t2, pid2) = session.new_tab(80, 24);
        assert!(session.place_tab_on_surface(t2, 1, 0));
        // Surface 0 strip is only tab 1; chip 0 should not resolve to t2.
        let chip = layout.tab_chips[0];
        let drop = classify_drop(
            &layout,
            &session,
            0,
            chip.x + 8.0,
            chip.y + chip.h * 0.5,
            pid2,
        );
        assert_eq!(drop, Some(DropKind::Tab { tab_id: 1 }));
        let _ = t2;
    }
}
