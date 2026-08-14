//! Split-pane tree with floaty “jelly” open/close animation.
//!
//! Equal H/V splits (product v1). New pane starts collapsed and springs open
//! with overshoot. Closing reverses jelly so the survivor expands.

use crate::layout::Rect;

/// Split axis: vertical = left|right, horizontal = top|bottom.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SplitAxis {
    /// New pane opens to the **right** of the focused leaf.
    Vertical,
    /// New pane opens **below** the focused leaf.
    Horizontal,
}

/// Binary layout tree. Leaves are pane ids owned by the session.
#[derive(Clone, Debug)]
pub enum SplitNode {
    Leaf(u64),
    Branch {
        axis: SplitAxis,
        /// Settled split ratio for the first child (a). Usually 0.5.
        ratio: f32,
        /// Jelly scale for the side that is opening or closing (0 = collapsed, 1 = full).
        jelly: f32,
        jelly_vel: f32,
        /// Spring target for jelly (1 = open, 0 = closing).
        jelly_target: f32,
        /// Which child is collapsing (None = opening / idle, jelly applies to `b`).
        closing: CloseSide,
        a: Box<SplitNode>,
        b: Box<SplitNode>,
    },
}

/// Which child of a branch is mid close-jelly.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub(crate) enum CloseSide {
    #[default]
    None,
    A,
    B,
}

/// Result of advancing close animations.
#[derive(Clone, Debug, Default)]
pub struct TickResult {
    pub moving: bool,
    /// Pane ids whose close jelly finished — caller should remove them.
    pub finished_closes: Vec<u64>,
}

impl SplitNode {
    pub fn leaf(id: u64) -> Self {
        Self::Leaf(id)
    }

    pub fn contains_pane(&self, id: u64) -> bool {
        match self {
            Self::Leaf(p) => *p == id,
            Self::Branch { a, b, .. } => a.contains_pane(id) || b.contains_pane(id),
        }
    }

    pub fn leaf_ids(&self) -> Vec<u64> {
        let mut out = Vec::new();
        self.collect_leaves(&mut out);
        out
    }

    fn collect_leaves(&self, out: &mut Vec<u64>) {
        match self {
            Self::Leaf(id) => out.push(*id),
            Self::Branch { a, b, .. } => {
                a.collect_leaves(out);
                b.collect_leaves(out);
            }
        }
    }

    /// Advance jelly springs (open and close).
    pub fn tick(&mut self, dt: f32) -> TickResult {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let mut result = TickResult::default();
        self.tick_inner(dt, &mut result);
        result
    }

    fn tick_inner(&mut self, dt: f32, result: &mut TickResult) {
        match self {
            Self::Leaf(_) => {}
            Self::Branch {
                jelly,
                jelly_vel,
                jelly_target,
                closing,
                a,
                b,
                ..
            } => {
                // Soft spring — overshoots then settles (jelly feel).
                const K: f32 = 140.0;
                const C: f32 = 14.0;
                let target = *jelly_target;
                let force = -K * (*jelly - target) - C * *jelly_vel;
                *jelly_vel += force * dt;
                *jelly += *jelly_vel * dt;

                if target >= 1.0 {
                    if *jelly > 1.12 {
                        *jelly = 1.12;
                        *jelly_vel *= -0.35;
                    }
                    if *jelly < 0.0 {
                        *jelly = 0.0;
                        *jelly_vel = 0.0;
                    }
                } else {
                    // Closing: allow slight undershoot then settle
                    if *jelly < -0.04 {
                        *jelly = -0.04;
                        *jelly_vel *= -0.35;
                    }
                    if *jelly > 1.15 {
                        *jelly = 1.15;
                    }
                }

                let settled =
                    (*jelly - target).abs() < 0.02 && jelly_vel.abs() < 0.04;
                if settled {
                    *jelly = target;
                    *jelly_vel = 0.0;
                    if target <= 0.0 {
                        match *closing {
                            CloseSide::B => {
                                if let Self::Leaf(id) = b.as_ref() {
                                    result.finished_closes.push(*id);
                                } else {
                                    result.finished_closes.push(b.first_leaf());
                                }
                                *closing = CloseSide::None;
                            }
                            CloseSide::A => {
                                if let Self::Leaf(id) = a.as_ref() {
                                    result.finished_closes.push(*id);
                                } else {
                                    result.finished_closes.push(a.first_leaf());
                                }
                                *closing = CloseSide::None;
                            }
                            CloseSide::None => {}
                        }
                    }
                } else {
                    result.moving = true;
                }

                a.tick_inner(dt, result);
                b.tick_inner(dt, result);
            }
        }
    }

    /// Replace the leaf `focus` with a branch: old leaf as `a`, `new_id` as `b`.
    /// New pane jelly starts at 0 so it balloons open.
    pub fn split_leaf(&mut self, focus: u64, new_id: u64, axis: SplitAxis) -> bool {
        match self {
            Self::Leaf(id) if *id == focus => {
                *self = Self::Branch {
                    axis,
                    ratio: 0.5,
                    jelly: 0.0,
                    jelly_vel: 0.0,
                    jelly_target: 1.0,
                    closing: CloseSide::None,
                    a: Box::new(Self::Leaf(focus)),
                    b: Box::new(Self::Leaf(new_id)),
                };
                true
            }
            Self::Leaf(_) => false,
            Self::Branch { a, b, .. } => {
                a.split_leaf(focus, new_id, axis) || b.split_leaf(focus, new_id, axis)
            }
        }
    }

    /// Like [`split_leaf`], then swap children when the new pane should sit
    /// left / above the target (split_leaf always opens `new_id` as `b`).
    pub fn split_leaf_edge(&mut self, target: u64, new_id: u64, edge: DockEdge) -> bool {
        if !self.split_leaf(target, new_id, edge.axis()) {
            return false;
        }
        if !edge.moving_is_second() {
            self.swap_direct_children(target, new_id);
        }
        true
    }

    fn swap_direct_children(&mut self, a_id: u64, b_id: u64) -> bool {
        match self {
            Self::Leaf(_) => false,
            Self::Branch { a, b, .. } => {
                let is_pair = matches!(
                    (a.as_ref(), b.as_ref()),
                    (Self::Leaf(x), Self::Leaf(y)) if *x == a_id && *y == b_id
                );
                if is_pair {
                    std::mem::swap(a, b);
                    return true;
                }
                a.swap_direct_children(a_id, b_id) || b.swap_direct_children(a_id, b_id)
            }
        }
    }

    /// Start a jelly-close of leaf `id`. Returns false if not found or already sole leaf.
    ///
    /// For a sole leaf, the caller should run a tab-level exit anim instead.
    pub fn begin_close_leaf(&mut self, id: u64) -> bool {
        match self {
            Self::Leaf(_) => false,
            Self::Branch {
                a,
                b,
                jelly,
                jelly_vel,
                jelly_target,
                closing,
                ..
            } => {
                // Prefer recurse so nested closes work.
                if a.begin_close_leaf(id) || b.begin_close_leaf(id) {
                    return true;
                }
                // Direct children that are the leaf (or contain only that leaf as single)
                let a_is = matches!(a.as_ref(), Self::Leaf(p) if *p == id)
                    || (a.leaf_ids() == [id]);
                let b_is = matches!(b.as_ref(), Self::Leaf(p) if *p == id)
                    || (b.leaf_ids() == [id]);

                if b_is {
                    *jelly_target = 0.0;
                    *closing = CloseSide::B;
                    if *jelly < 0.05 {
                        *jelly = 1.0;
                    }
                    *jelly_vel = 0.0;
                    return true;
                }
                if a_is {
                    // Stay put — shrink `a` (left / top) so it doesn't teleport to `b`.
                    *jelly_target = 0.0;
                    *closing = CloseSide::A;
                    if *jelly < 0.05 {
                        *jelly = 1.0;
                    }
                    *jelly_vel = 0.0;
                    return true;
                }
                false
            }
        }
    }

    /// True if this pane is currently mid jelly-close.
    pub fn is_closing(&self, id: u64) -> bool {
        match self {
            Self::Leaf(_) => false,
            Self::Branch {
                closing,
                b,
                a,
                jelly_target,
                ..
            } => {
                if *jelly_target <= 0.0 {
                    match *closing {
                        CloseSide::B if b.contains_pane(id) => return true,
                        CloseSide::A if a.contains_pane(id) => return true,
                        _ => {}
                    }
                }
                a.is_closing(id) || b.is_closing(id)
            }
        }
    }

    /// Remove leaf `id`. If a branch becomes a single child, collapse it.
    pub fn remove_leaf(&mut self, id: u64) -> RemoveResult {
        match self {
            Self::Leaf(p) => {
                if *p == id {
                    RemoveResult::RemovedEmpty
                } else {
                    RemoveResult::NotFound
                }
            }
            Self::Branch { a, b, .. } => match a.remove_leaf(id) {
                RemoveResult::RemovedEmpty => {
                    let other = std::mem::replace(b.as_mut(), Self::Leaf(0));
                    let focus = other.first_leaf();
                    *self = other;
                    RemoveResult::Removed { focus_hint: focus }
                }
                RemoveResult::Removed { focus_hint } => RemoveResult::Removed { focus_hint },
                RemoveResult::NotFound => match b.remove_leaf(id) {
                    RemoveResult::RemovedEmpty => {
                        let other = std::mem::replace(a.as_mut(), Self::Leaf(0));
                        let focus = other.first_leaf();
                        *self = other;
                        RemoveResult::Removed { focus_hint: focus }
                    }
                    other => other,
                },
            },
        }
    }

    pub fn first_leaf(&self) -> u64 {
        match self {
            Self::Leaf(id) => *id,
            Self::Branch { a, .. } => a.first_leaf(),
        }
    }

    /// Pane that should keep focus when `id` closes: the sibling that expands
    /// into the hole (not the first leftover leaf in tree order).
    pub fn focus_after_close(&self, id: u64) -> Option<u64> {
        match self {
            Self::Leaf(_) => None,
            Self::Branch { a, b, .. } => {
                if a.is_only(id) {
                    return Some(b.first_leaf());
                }
                if b.is_only(id) {
                    return Some(a.first_leaf());
                }
                a.focus_after_close(id)
                    .or_else(|| b.focus_after_close(id))
            }
        }
    }

    fn is_only(&self, id: u64) -> bool {
        match self {
            Self::Leaf(p) => *p == id,
            Self::Branch { .. } => self.leaf_ids() == [id],
        }
    }

    /// Split sashes (the gap between children). `a_leaf` identifies the branch.
    pub fn collect_sashes(&self, area: Rect, gap: f32, out: &mut Vec<SashHit>) {
        match self {
            Self::Leaf(_) => {}
            Self::Branch {
                axis,
                ratio,
                jelly,
                a,
                b,
                closing,
                ..
            } => {
                if *closing == CloseSide::B {
                    a.collect_sashes(area, gap, out);
                    return;
                }
                if *closing == CloseSide::A {
                    b.collect_sashes(area, gap, out);
                    return;
                }
                let j = jelly.clamp(0.0, 1.0);
                if j < 0.5 {
                    a.collect_sashes(area, gap, out);
                    return;
                }
                match axis {
                    SplitAxis::Vertical => {
                        let usable = (area.w - gap).max(0.0);
                        let b_w = (usable * (1.0 - *ratio)).min(usable * 0.92).max(0.0);
                        let a_w = (usable - b_w).max(0.0);
                        let sash = Rect::new(area.x + a_w, area.y, gap.max(4.0), area.h);
                        out.push(SashHit {
                            a_leaf: a.first_leaf(),
                            axis: *axis,
                            rect: sash,
                            parent: area,
                        });
                        a.collect_sashes(Rect::new(area.x, area.y, a_w, area.h), gap, out);
                        if b_w > 1.0 {
                            b.collect_sashes(
                                Rect::new(area.x + a_w + gap, area.y, b_w, area.h),
                                gap,
                                out,
                            );
                        }
                    }
                    SplitAxis::Horizontal => {
                        let usable = (area.h - gap).max(0.0);
                        let b_h = (usable * (1.0 - *ratio)).min(usable * 0.92).max(0.0);
                        let a_h = (usable - b_h).max(0.0);
                        let sash = Rect::new(area.x, area.y + a_h, area.w, gap.max(4.0));
                        out.push(SashHit {
                            a_leaf: a.first_leaf(),
                            axis: *axis,
                            rect: sash,
                            parent: area,
                        });
                        a.collect_sashes(Rect::new(area.x, area.y, area.w, a_h), gap, out);
                        if b_h > 1.0 {
                            b.collect_sashes(
                                Rect::new(area.x, area.y + a_h + gap, area.w, b_h),
                                gap,
                                out,
                            );
                        }
                    }
                }
            }
        }
    }

    /// Set the settled split ratio on the branch whose first `a` leaf is `a_leaf`.
    pub fn set_ratio(&mut self, a_leaf: u64, ratio: f32) -> bool {
        let ratio = ratio.clamp(0.15, 0.85);
        match self {
            Self::Leaf(_) => false,
            Self::Branch {
                a, b, ratio: slot, ..
            } => {
                if a.first_leaf() == a_leaf {
                    *slot = ratio;
                    return true;
                }
                a.set_ratio(a_leaf, ratio) || b.set_ratio(a_leaf, ratio)
            }
        }
    }

    /// Layout leaves into `out` with animated jelly sizes. `gap` is sash thickness.
    pub fn layout_into(&self, area: Rect, gap: f32, out: &mut Vec<(u64, Rect)>) {
        match self {
            Self::Leaf(id) => out.push((*id, area)),
            Self::Branch {
                axis,
                ratio,
                jelly,
                closing,
                a,
                b,
                ..
            } => {
                let j = jelly.clamp(0.0, 1.15);
                let close_a = *closing == CloseSide::A;
                match axis {
                    SplitAxis::Vertical => {
                        let usable = (area.w - gap).max(0.0);
                        let (a_w, b_w) = if close_a {
                            let mut share = usable * *ratio * j.min(1.0);
                            if j > 1.0 {
                                share *= 1.0 + (j - 1.0) * 0.35;
                            }
                            let a_w = share.min(usable * 0.92).max(0.0);
                            (a_w, (usable - a_w).max(0.0))
                        } else {
                            let mut share = usable * (1.0 - *ratio) * j.min(1.0);
                            if j > 1.0 {
                                share *= 1.0 + (j - 1.0) * 0.35;
                            }
                            let b_w = share.min(usable * 0.92).max(0.0);
                            ((usable - b_w).max(0.0), b_w)
                        };
                        let a_rect = Rect::new(area.x, area.y, a_w, area.h);
                        let b_rect = Rect::new(area.x + a_w + gap, area.y, b_w, area.h);
                        if a_w > 1.0 {
                            a.layout_into(a_rect, gap, out);
                        }
                        if b_w > 1.0 {
                            b.layout_into(b_rect, gap, out);
                        }
                    }
                    SplitAxis::Horizontal => {
                        let usable = (area.h - gap).max(0.0);
                        let (a_h, b_h) = if close_a {
                            let mut share = usable * *ratio * j.min(1.0);
                            if j > 1.0 {
                                share *= 1.0 + (j - 1.0) * 0.35;
                            }
                            let a_h = share.min(usable * 0.92).max(0.0);
                            (a_h, (usable - a_h).max(0.0))
                        } else {
                            let mut share = usable * (1.0 - *ratio) * j.min(1.0);
                            if j > 1.0 {
                                share *= 1.0 + (j - 1.0) * 0.35;
                            }
                            let b_h = share.min(usable * 0.92).max(0.0);
                            ((usable - b_h).max(0.0), b_h)
                        };
                        let a_rect = Rect::new(area.x, area.y, area.w, a_h);
                        let b_rect = Rect::new(area.x, area.y + a_h + gap, area.w, b_h);
                        if a_h > 1.0 {
                            a.layout_into(a_rect, gap, out);
                        }
                        if b_h > 1.0 {
                            b.layout_into(b_rect, gap, out);
                        }
                    }
                }
            }
        }
    }

    /// Spatial neighbor for focus moves.
    pub fn neighbor(&self, focus: u64, dir: FocusDir, area: Rect, gap: f32) -> Option<u64> {
        let mut rects = Vec::new();
        self.layout_into(area, gap, &mut rects);
        let (_, fr) = rects.iter().find(|(id, _)| *id == focus)?;
        let fcx = fr.x + fr.w * 0.5;
        let fcy = fr.y + fr.h * 0.5;
        let mut best: Option<(f32, u64)> = None;
        for (id, r) in &rects {
            if *id == focus {
                continue;
            }
            let cx = r.x + r.w * 0.5;
            let cy = r.y + r.h * 0.5;
            let ok = match dir {
                FocusDir::Left => cx < fcx - 1.0,
                FocusDir::Right => cx > fcx + 1.0,
                FocusDir::Up => cy < fcy - 1.0,
                FocusDir::Down => cy > fcy + 1.0,
            };
            if !ok {
                continue;
            }
            let dist = (cx - fcx).hypot(cy - fcy);
            if best.map(|(d, _)| dist < d).unwrap_or(true) {
                best = Some((dist, *id));
            }
        }
        best.map(|(_, id)| id)
    }
}

#[derive(Clone, Copy, Debug)]
pub enum FocusDir {
    Left,
    Right,
    Up,
    Down,
}

/// One split sash between two children.
#[derive(Clone, Copy, Debug)]
pub struct SashHit {
    /// First leaf of child `a` — stable id for the branch.
    pub a_leaf: u64,
    pub axis: SplitAxis,
    pub rect: Rect,
    /// Parent area of the branch (for converting pointer → ratio).
    pub parent: Rect,
}

/// Which side of a target pane a dragged leaf should occupy.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DockEdge {
    Left,
    Right,
    Top,
    Bottom,
}

impl DockEdge {
    pub fn axis(self) -> SplitAxis {
        match self {
            Self::Left | Self::Right => SplitAxis::Vertical,
            Self::Top | Self::Bottom => SplitAxis::Horizontal,
        }
    }

    /// `split_leaf` opens the new id as child `b` (right / below).
    pub fn moving_is_second(self) -> bool {
        matches!(self, Self::Right | Self::Bottom)
    }
}

#[derive(Clone, Debug)]
pub enum RemoveResult {
    NotFound,
    /// Leaf was the only node — tree empty (caller drops tab/page).
    RemovedEmpty,
    Removed { focus_hint: u64 },
}

/// Sole-pane (or whole-tab) exit: scale workspace glass from 1 → 0 with jelly.
#[derive(Clone, Debug)]
pub struct SoloExitAnim {
    pub pane_id: u64,
    pub jelly: f32,
    pub jelly_vel: f32,
    /// Last tab on this window — shrink *and* fade the frame out.
    pub fade_window: bool,
    elapsed: f32,
}

impl SoloExitAnim {
    pub fn start(pane_id: u64) -> Self {
        Self {
            pane_id,
            jelly: 1.0,
            jelly_vel: 0.0,
            fade_window: false,
            elapsed: 0.0,
        }
    }

    pub fn start_window(pane_id: u64) -> Self {
        Self {
            pane_id,
            jelly: 1.0,
            jelly_vel: 0.0,
            fade_window: true,
            elapsed: 0.0,
        }
    }

    /// Window alpha. Stays readable a beat, then dissolves.
    pub fn opacity(&self) -> f32 {
        if self.fade_window {
            self.jelly.clamp(0.0, 1.0).powf(1.35)
        } else {
            1.0
        }
    }

    /// Logical-px Gaussian radius for the dissolve blur (0 = sharp).
    pub fn blur_px(&self) -> f32 {
        if !self.fade_window {
            return 0.0;
        }
        let gone = (1.0 - self.jelly.clamp(0.0, 1.0)).powf(0.72);
        gone * 26.0
    }

    /// Returns true while still animating; false when settled closed.
    pub fn tick(&mut self, dt: f32) -> bool {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        if self.fade_window {
            // Timed ease — the jelly spring finishes too fast for a dissolve.
            self.elapsed += dt;
            const DUR: f32 = 0.52;
            let p = (self.elapsed / DUR).clamp(0.0, 1.0);
            let e = p * p * (3.0 - 2.0 * p);
            self.jelly = 1.0 - e;
            return p < 1.0;
        }
        const K: f32 = 160.0;
        const C: f32 = 16.0;
        let target = 0.0;
        let force = -K * (self.jelly - target) - C * self.jelly_vel;
        self.jelly_vel += force * dt;
        self.jelly += self.jelly_vel * dt;
        if self.jelly < -0.05 {
            self.jelly = -0.05;
            self.jelly_vel *= -0.3;
        }
        if self.jelly.abs() < 0.02 && self.jelly_vel.abs() < 0.05 {
            self.jelly = 0.0;
            self.jelly_vel = 0.0;
            return false;
        }
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn split_and_layout_two() {
        let mut root = SplitNode::leaf(1);
        assert!(root.split_leaf(1, 2, SplitAxis::Vertical));
        if let SplitNode::Branch { jelly, jelly_target, .. } = &mut root {
            *jelly = 1.0;
            *jelly_target = 1.0;
        }
        let mut out = Vec::new();
        root.layout_into(Rect::new(0.0, 0.0, 200.0, 100.0), 4.0, &mut out);
        assert_eq!(out.len(), 2);
    }

    #[test]
    fn jelly_tick_settles_open() {
        let mut root = SplitNode::leaf(1);
        root.split_leaf(1, 2, SplitAxis::Horizontal);
        for _ in 0..180 {
            root.tick(1.0 / 60.0);
        }
        if let SplitNode::Branch { jelly, .. } = root {
            assert!((jelly - 1.0).abs() < 0.02, "jelly={jelly}");
        } else {
            panic!("expected branch");
        }
    }

    #[test]
    fn close_jelly_finishes() {
        let mut root = SplitNode::leaf(1);
        root.split_leaf(1, 2, SplitAxis::Vertical);
        // open fully
        if let SplitNode::Branch { jelly, jelly_target, .. } = &mut root {
            *jelly = 1.0;
            *jelly_target = 1.0;
        }
        assert!(root.begin_close_leaf(2));
        let mut finished = false;
        for _ in 0..180 {
            let r = root.tick(1.0 / 60.0);
            if r.finished_closes.contains(&2) {
                finished = true;
                break;
            }
        }
        assert!(finished, "close should complete");
    }

    #[test]
    fn close_left_pane_stays_on_the_left() {
        let mut root = SplitNode::leaf(1);
        root.split_leaf(1, 2, SplitAxis::Vertical);
        root.split_leaf(2, 3, SplitAxis::Horizontal);
        if let SplitNode::Branch { jelly, jelly_target, .. } = &mut root {
            *jelly = 1.0;
            *jelly_target = 1.0;
        }
        assert!(root.begin_close_leaf(1));
        if let SplitNode::Branch { jelly, closing, .. } = &mut root {
            *jelly = 0.45;
            assert_eq!(*closing, CloseSide::A);
        }
        let mut out = Vec::new();
        root.layout_into(Rect::new(0.0, 0.0, 200.0, 100.0), 4.0, &mut out);
        let left = out.iter().find(|(id, _)| *id == 1).expect("left pane still laid out");
        let right = out.iter().find(|(id, _)| *id == 2).expect("right stack still laid out");
        assert!(
            left.1.x < right.1.x,
            "closing left pane must stay on the left, got left.x={} right.x={}",
            left.1.x,
            right.1.x
        );
        assert!(left.1.w < right.1.w, "left pane should be shrinking");
    }

    #[test]
    fn remove_collapses() {
        let mut root = SplitNode::leaf(1);
        root.split_leaf(1, 2, SplitAxis::Vertical);
        match root.remove_leaf(2) {
            RemoveResult::Removed { focus_hint } => assert_eq!(focus_hint, 1),
            other => panic!("{other:?}"),
        }
        assert!(matches!(root, SplitNode::Leaf(1)));
    }

    #[test]
    fn close_right_stack_keeps_focus_on_that_column() {
        // [1] [2]
        //     [3]
        let mut root = SplitNode::leaf(1);
        root.split_leaf(1, 2, SplitAxis::Vertical);
        root.split_leaf(2, 3, SplitAxis::Horizontal);
        assert_eq!(root.focus_after_close(3), Some(2));
        assert_eq!(root.focus_after_close(2), Some(3));
        assert_eq!(root.focus_after_close(1), Some(2));
    }

    #[test]
    fn close_left_stack_keeps_focus_on_that_column() {
        // [1] [3]
        // [2]
        let mut root = SplitNode::leaf(1);
        root.split_leaf(1, 3, SplitAxis::Vertical);
        root.split_leaf(1, 2, SplitAxis::Horizontal);
        assert_eq!(root.focus_after_close(1), Some(2));
        assert_eq!(root.focus_after_close(2), Some(1));
        assert_eq!(root.focus_after_close(3), Some(1));
    }

    #[test]
    fn split_leaf_edge_puts_new_on_left() {
        let mut root = SplitNode::leaf(1);
        assert!(root.split_leaf_edge(1, 2, DockEdge::Left));
        if let SplitNode::Branch { jelly, jelly_target, .. } = &mut root {
            *jelly = 1.0;
            *jelly_target = 1.0;
        }
        let mut out = Vec::new();
        root.layout_into(Rect::new(0.0, 0.0, 200.0, 100.0), 0.0, &mut out);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].0, 2, "new pane should be left (first)");
        assert_eq!(out[1].0, 1);
        assert!(out[0].1.x < out[1].1.x);
    }

    #[test]
    fn sash_ratio_moves_split() {
        let mut root = SplitNode::leaf(1);
        assert!(root.split_leaf(1, 2, SplitAxis::Vertical));
        if let SplitNode::Branch { jelly, jelly_target, .. } = &mut root {
            *jelly = 1.0;
            *jelly_target = 1.0;
        }
        assert!(root.set_ratio(1, 0.25));
        let mut sashes = Vec::new();
        root.collect_sashes(Rect::new(0.0, 0.0, 200.0, 100.0), 8.0, &mut sashes);
        assert_eq!(sashes.len(), 1);
        assert_eq!(sashes[0].a_leaf, 1);
        assert_eq!(sashes[0].axis, SplitAxis::Vertical);
        let mut out = Vec::new();
        root.layout_into(Rect::new(0.0, 0.0, 200.0, 100.0), 8.0, &mut out);
        assert!(out[0].1.w < out[1].1.w, "0.25 ratio should shrink a");
    }

    #[test]
    fn solo_exit_settles() {
        let mut a = SoloExitAnim::start(1);
        let mut moving = true;
        for _ in 0..180 {
            moving = a.tick(1.0 / 60.0);
            if !moving {
                break;
            }
        }
        assert!(!moving);
        assert!(a.jelly.abs() < 0.01);
    }

    #[test]
    fn window_exit_is_slower_and_blurs() {
        let mut a = SoloExitAnim::start_window(1);
        assert!(a.blur_px() < 0.5);
        assert!(a.opacity() > 0.95);
        let mut t = 0.0;
        while a.tick(1.0 / 60.0) {
            t += 1.0 / 60.0;
            if t > 2.0 {
                break;
            }
        }
        assert!(t > 0.45, "dissolve too fast: {t}");
        assert!(t < 0.65, "dissolve too slow: {t}");
        assert!(a.opacity() < 0.02);
        assert!(a.blur_px() > 20.0);
    }
}
