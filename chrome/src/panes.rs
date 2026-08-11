//! Split-pane tree with floaty “jelly” open animation.
//!
//! Equal H/V splits (product v1). New pane starts collapsed and springs open
//! with overshoot so existing panes feel pushed aside.

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
        /// Jelly open for the **second** child (0 = closed, 1 = full). Springs with overshoot.
        jelly: f32,
        jelly_vel: f32,
        a: Box<SplitNode>,
        b: Box<SplitNode>,
    },
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

    /// Advance jelly springs. Returns true if any node is still moving.
    pub fn tick(&mut self, dt: f32) -> bool {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        match self {
            Self::Leaf(_) => false,
            Self::Branch {
                jelly,
                jelly_vel,
                a,
                b,
                ..
            } => {
                // Soft spring — overshoots then settles (jelly feel).
                const K: f32 = 140.0;
                const C: f32 = 14.0;
                let target = 1.0;
                let force = -K * (*jelly - target) - C * *jelly_vel;
                *jelly_vel += force * dt;
                *jelly += *jelly_vel * dt;
                // Allow mild overshoot then clamp soft
                if *jelly > 1.12 {
                    *jelly = 1.12;
                    *jelly_vel *= -0.35;
                }
                if *jelly < 0.0 {
                    *jelly = 0.0;
                    *jelly_vel = 0.0;
                }
                let mut moving = (*jelly - 1.0).abs() > 0.002 || jelly_vel.abs() > 0.01;
                if !moving {
                    *jelly = 1.0;
                    *jelly_vel = 0.0;
                }
                moving |= a.tick(dt);
                moving |= b.tick(dt);
                moving
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

    /// Remove leaf `id`. If a branch becomes a single child, collapse it.
    /// Returns whether the tree still contains any leaf, and the neighbor to focus.
    pub fn remove_leaf(&mut self, id: u64) -> RemoveResult {
        match self {
            Self::Leaf(p) => {
                if *p == id {
                    RemoveResult::RemovedEmpty
                } else {
                    RemoveResult::NotFound
                }
            }
            Self::Branch { a, b, .. } => {
                match a.remove_leaf(id) {
                    RemoveResult::RemovedEmpty => {
                        // a gone — promote b
                        let other = std::mem::replace(b.as_mut(), Self::Leaf(0));
                        let focus = other.first_leaf();
                        *self = other;
                        RemoveResult::Removed { focus_hint: focus }
                    }
                    RemoveResult::Removed { focus_hint } => {
                        RemoveResult::Removed { focus_hint }
                    }
                    RemoveResult::NotFound => match b.remove_leaf(id) {
                        RemoveResult::RemovedEmpty => {
                            let other = std::mem::replace(a.as_mut(), Self::Leaf(0));
                            let focus = other.first_leaf();
                            *self = other;
                            RemoveResult::Removed { focus_hint: focus }
                        }
                        other => other,
                    },
                }
            }
        }
    }

    pub fn first_leaf(&self) -> u64 {
        match self {
            Self::Leaf(id) => *id,
            Self::Branch { a, .. } => a.first_leaf(),
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
                a,
                b,
                ..
            } => {
                let j = jelly.clamp(0.0, 1.15);
                // Effective share for b grows with jelly; a shrinks.
                // When j=0, a has full area; when j=1, ratio/1-ratio split.
                match axis {
                    SplitAxis::Vertical => {
                        let total = area.w;
                        let usable = (total - gap).max(0.0);
                        let b_share = usable * (1.0 - *ratio) * j.min(1.0);
                        // Overshoot: briefly let b exceed its share
                        let b_share = if j > 1.0 {
                            b_share * (1.0 + (j - 1.0) * 0.35)
                        } else {
                            b_share
                        };
                        let b_w = b_share.min(usable * 0.92);
                        let a_w = (usable - b_w).max(0.0);
                        let a_rect = Rect::new(area.x, area.y, a_w, area.h);
                        let b_rect = Rect::new(area.x + a_w + gap, area.y, b_w, area.h);
                        a.layout_into(a_rect, gap, out);
                        if b_w > 1.0 {
                            b.layout_into(b_rect, gap, out);
                        }
                    }
                    SplitAxis::Horizontal => {
                        let total = area.h;
                        let usable = (total - gap).max(0.0);
                        let b_share = usable * (1.0 - *ratio) * j.min(1.0);
                        let b_share = if j > 1.0 {
                            b_share * (1.0 + (j - 1.0) * 0.35)
                        } else {
                            b_share
                        };
                        let b_h = b_share.min(usable * 0.92);
                        let a_h = (usable - b_h).max(0.0);
                        let a_rect = Rect::new(area.x, area.y, area.w, a_h);
                        let b_rect = Rect::new(area.x, area.y + a_h + gap, area.w, b_h);
                        a.layout_into(a_rect, gap, out);
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

#[derive(Clone, Debug)]
pub enum RemoveResult {
    NotFound,
    /// Leaf was the only node — tree empty (caller drops tab/page).
    RemovedEmpty,
    Removed { focus_hint: u64 },
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn split_and_layout_two() {
        let mut root = SplitNode::leaf(1);
        assert!(root.split_leaf(1, 2, SplitAxis::Vertical));
        // Force jelly open
        if let SplitNode::Branch { jelly, .. } = &mut root {
            *jelly = 1.0;
        }
        let mut out = Vec::new();
        root.layout_into(Rect::new(0.0, 0.0, 200.0, 100.0), 4.0, &mut out);
        assert_eq!(out.len(), 2);
        assert!(out[0].1.w < 120.0);
        assert!(out[1].1.w < 120.0);
    }

    #[test]
    fn jelly_tick_settles() {
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
    fn remove_collapses() {
        let mut root = SplitNode::leaf(1);
        root.split_leaf(1, 2, SplitAxis::Vertical);
        match root.remove_leaf(2) {
            RemoveResult::Removed { focus_hint } => assert_eq!(focus_hint, 1),
            other => panic!("{other:?}"),
        }
        assert!(matches!(root, SplitNode::Leaf(1)));
    }
}
