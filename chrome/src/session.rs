//! Chrome session: tabs (pages) each with a split-pane tree of leaves.
//!
//! PTY ownership stays in the app (`HashMap<pane_id, PtySession>`).

use std::collections::HashMap;

use crate::cells::{theme, CellGrid};
use crate::panes::{
    DockEdge, FocusDir, RemoveResult, SoloExitAnim, SplitAxis, SplitNode, TickResult,
};
use crate::shell::{self, ShellOutput};

/// What a leaf pane hosts. Terminals own a PTY; widgets reuse the split tree
/// without a shell (workspace chat, guest processes).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum WidgetKind {
    Workspace,
    Guest,
}

impl WidgetKind {
    pub fn title(self) -> &'static str {
        match self {
            Self::Workspace => "workspace",
            Self::Guest => "guest",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PaneKind {
    Terminal,
    Widget(WidgetKind),
}

impl PaneKind {
    pub fn is_terminal(self) -> bool {
        matches!(self, Self::Terminal)
    }

    pub fn is_workspace(self) -> bool {
        matches!(self, Self::Widget(WidgetKind::Workspace))
    }

    pub fn is_guest(self) -> bool {
        matches!(self, Self::Widget(WidgetKind::Guest))
    }

    pub fn widget(self) -> Option<WidgetKind> {
        match self {
            Self::Widget(k) => Some(k),
            Self::Terminal => None,
        }
    }
}

/// One leaf (grid + cwd). PTY lives in the app, keyed by `id`.
#[derive(Clone, Debug)]
pub struct Pane {
    pub id: u64,
    pub title: String,
    pub kind: PaneKind,
    pub busy: bool,
    pub grid: CellGrid,
    pub pty_mode: bool,
    pub cwd: String,
    /// Local command draft for this pane's input strip.
    pub draft: String,
    /// Manifest id when [`PaneKind`] is a guest. None for terminals / workspace.
    pub guest_id: Option<String>,
    /// Last location the guest reported (`url` message).
    pub guest_url: String,
    /// Shell exited / user closed — animating out; don't re-trigger.
    pub exiting: bool,
}

fn new_terminal_pane(id: u64, cols: u16, rows: u16, cwd: String) -> Pane {
    Pane {
        id,
        title: format!("shell {id}"),
        kind: PaneKind::Terminal,
        busy: false,
        grid: CellGrid::new(cols, rows),
        pty_mode: false,
        cwd,
        draft: String::new(),
        guest_id: None,
        guest_url: String::new(),
        exiting: false,
    }
}

fn default_tab_title(id: u64) -> String {
    format!("tab {id}")
}

fn new_widget_pane(id: u64, kind: WidgetKind, cwd: String) -> Pane {
    Pane {
        id,
        title: kind.title().into(),
        kind: PaneKind::Widget(kind),
        busy: false,
        // Unused — widgets don't paint a cell grid — but keep a tiny buffer so
        // existing session helpers that touch `grid` stay panic-free.
        grid: CellGrid::new(8, 4),
        pty_mode: false,
        cwd,
        draft: String::new(),
        guest_id: None,
        guest_url: String::new(),
        exiting: false,
    }
}

/// Strip + content handoff when closing a tab that isn't the last on its window.
#[derive(Clone, Debug)]
pub struct TabExitAnim {
    /// 1 = just started, 0 = gone.
    pub t: f32,
    elapsed: f32,
    pub next_id: u64,
    /// +1 next is to the right (incoming slides from the right).
    pub dir: f32,
    /// Slide workspace content (only when the closing tab was focused).
    pub slide_content: bool,
}

impl TabExitAnim {
    const DUR: f32 = 0.30;

    pub fn start(next_id: u64, dir: f32, slide_content: bool) -> Self {
        Self {
            t: 1.0,
            elapsed: 0.0,
            next_id,
            dir,
            slide_content,
        }
    }

    pub fn ease(&self) -> f32 {
        let p = (1.0 - self.t).clamp(0.0, 1.0);
        p * p * (3.0 - 2.0 * p)
    }

    /// Returns true while still animating.
    pub fn tick(&mut self, dt: f32) -> bool {
        self.elapsed += dt.clamp(0.0, 1.0 / 20.0);
        let p = (self.elapsed / Self::DUR).clamp(0.0, 1.0);
        let e = p * p * (3.0 - 2.0 * p);
        self.t = 1.0 - e;
        p < 1.0
    }
}

/// One chrome-strip tab that may hold a split tree of panes.
#[derive(Clone, Debug)]
pub struct Tab {
    pub id: u64,
    pub title: String,
    pub root: SplitNode,
    pub focus_pane: u64,
    /// Sole-pane graceful exit (no split branch to reverse-jelly).
    pub solo_exit: Option<SoloExitAnim>,
    /// In-process window this tab is painted on (`0` = first window).
    pub surface: u64,
    /// Non-last-tab close: chip collapses while the next tab pulls over.
    pub exit: Option<TabExitAnim>,
}

/// Application-facing session. Panes are addressable by id across tabs.
#[derive(Clone, Debug)]
pub struct ChromeSession {
    pub tabs: Vec<Tab>,
    pub panes: HashMap<u64, Pane>,
    pub active_id: u64,
    /// Warp command history (newest at end) — shared.
    pub history: Vec<String>,
    pub history_idx: Option<usize>,
    history_stash: String,
    next_tab_id: u64,
    next_pane_id: u64,
    /// Snap-finished pane ids waiting for the host to drop PTYs.
    pending_drops: Vec<u64>,
}

impl ChromeSession {
    pub fn new(cols: u16, rows: u16) -> Self {
        let pane_id = 1;
        let mut panes = HashMap::new();
        panes.insert(
            pane_id,
            new_terminal_pane(pane_id, cols, rows, initial_cwd()),
        );
        let tab = Tab {
            id: 1,
            title: default_tab_title(1),
            root: SplitNode::leaf(pane_id),
            focus_pane: pane_id,
            solo_exit: None,
            surface: 0,
            exit: None,
        };
        Self {
            tabs: vec![tab],
            panes,
            active_id: 1,
            history: Vec::new(),
            history_idx: None,
            history_stash: String::new(),
            next_tab_id: 2,
            next_pane_id: 2,
            pending_drops: Vec::new(),
        }
    }

    pub fn active_tab(&self) -> Option<&Tab> {
        self.tabs.iter().find(|t| t.id == self.active_id)
    }

    pub fn active_tab_mut(&mut self) -> Option<&mut Tab> {
        let id = self.active_id;
        self.tabs.iter_mut().find(|t| t.id == id)
    }

    pub fn focus_pane_id(&self) -> u64 {
        self.active_tab()
            .map(|t| t.focus_pane)
            .unwrap_or(1)
    }

    pub fn active_pane(&self) -> Option<&Pane> {
        let id = self.focus_pane_id();
        self.panes.get(&id)
    }

    pub fn active_pane_mut(&mut self) -> Option<&mut Pane> {
        let id = self.focus_pane_id();
        self.panes.get_mut(&id)
    }

    /// Draft of the focused pane.
    pub fn draft(&self) -> &str {
        self.active_pane()
            .map(|p| p.draft.as_str())
            .unwrap_or("")
    }

    pub fn draft_mut(&mut self) -> &mut String {
        // Ensure we always have a pane.
        let id = self.focus_pane_id();
        &mut self
            .panes
            .get_mut(&id)
            .expect("focus pane")
            .draft
    }

    pub fn display_cwd(&self) -> String {
        self.active_pane()
            .map(|p| display_path(&p.cwd))
            .unwrap_or_default()
    }

    pub fn set_cwd(&mut self, pane_id: u64, path: String) {
        if let Some(p) = self.panes.get_mut(&pane_id) {
            if !path.is_empty() {
                p.cwd = path;
            }
        }
    }

    /// Apply an OSC 0/2 window title to a pane only (not the chrome tab strip).
    ///
    /// Product rule: OSC renames panes; multi-pane tabs keep a sticky strip title.
    /// Empty titles are ignored. Manual rename uses [`Self::rename_focused_pane`] /
    /// [`Self::rename_active_tab`] and is independent of OSC.
    pub fn set_pane_title(&mut self, pane_id: u64, title: String) {
        let title = title.trim();
        if title.is_empty() {
            return;
        }
        if let Some(p) = self.panes.get_mut(&pane_id) {
            p.title = title.to_string();
        }
    }

    /// Leaf count of the active tab (1 = solo).
    pub fn active_leaf_count(&self) -> usize {
        self.active_tab()
            .map(|t| t.root.leaf_ids().len())
            .unwrap_or(0)
    }

    /// Seed string for the rename dialog (current tab or focused pane title).
    pub fn rename_seed(&self, tab: bool) -> String {
        if tab {
            self.active_tab()
                .map(|t| t.title.clone())
                .unwrap_or_default()
        } else {
            self.active_pane()
                .map(|p| p.title.clone())
                .unwrap_or_default()
        }
    }

    /// Manual "Rename tab" — sets the chrome strip title.
    ///
    /// Empty name restores `tab {n}`. Pane titles are never used as a fallback.
    pub fn rename_active_tab(&mut self, name: &str) {
        let name = name.trim();
        if let Some(tab) = self.active_tab_mut() {
            tab.title = if name.is_empty() {
                default_tab_title(tab.id)
            } else {
                name.to_string()
            };
        }
    }

    /// Manual "Rename pane" — sets the focused leaf title only.
    ///
    /// Empty clears to `shell {id}`. The tab strip is never changed.
    pub fn rename_focused_pane(&mut self, name: &str) {
        let name = name.trim();
        let focus = self.focus_pane_id();
        let resolved = if name.is_empty() {
            match self.panes.get(&focus).map(|p| p.kind) {
                Some(PaneKind::Widget(k)) => k.title().to_string(),
                _ => format!("shell {focus}"),
            }
        } else {
            name.to_string()
        };
        if let Some(p) = self.panes.get_mut(&focus) {
            p.title = resolved;
        }
    }

    pub fn apply_cwd_after_command(&mut self, line: &str) {
        let id = self.focus_pane_id();
        let Some(p) = self.panes.get_mut(&id) else {
            return;
        };
        if let Some(next) = cwd_after_command(&p.cwd, line) {
            p.cwd = next;
        }
    }

    pub fn active_grid(&self) -> &CellGrid {
        self.active_pane()
            .map(|p| &p.grid)
            .expect("session always has a focused pane")
    }

    pub fn active_grid_mut(&mut self) -> &mut CellGrid {
        let id = self.focus_pane_id();
        &mut self.panes.get_mut(&id).expect("focus pane").grid
    }

    pub fn grid_mut(&mut self, pane_id: u64) -> Option<&mut CellGrid> {
        self.panes.get_mut(&pane_id).map(|p| &mut p.grid)
    }

    pub fn grid(&self, pane_id: u64) -> Option<&CellGrid> {
        self.panes.get(&pane_id).map(|p| &p.grid)
    }

    /// Advance open/close jelly. Returns finished pane ids to drop PTYs for,
    /// and whether any animation is still moving.
    pub fn tick_splits(&mut self, dt: f32) -> TickResult {
        let mut result = TickResult::default();
        let mut solo_finished: Vec<(u64, u64)> = Vec::new(); // (tab_id, pane_id)

        let mut strip_finished: Vec<u64> = Vec::new();

        for tab in &mut self.tabs {
            let r = tab.root.tick(dt);
            result.moving |= r.moving;
            result.finished_closes.extend(r.finished_closes.iter().copied());

            if let Some(anim) = &mut tab.solo_exit {
                if anim.tick(dt) {
                    result.moving = true;
                } else {
                    solo_finished.push((tab.id, anim.pane_id));
                    tab.solo_exit = None;
                }
            }
            if let Some(anim) = &mut tab.exit {
                if anim.tick(dt) {
                    result.moving = true;
                } else {
                    strip_finished.push(tab.id);
                }
            }
        }

        // Finalize branch closes that finished jelly
        let finished = result.finished_closes.clone();
        for pid in finished {
            self.finalize_pane_close(pid, &mut result);
        }
        result.finished_closes.extend(self.pending_drops.drain(..));
        for (tab_id, pane_id) in solo_finished {
            result.finished_closes.push(pane_id);
            self.finalize_solo_close(tab_id, pane_id, &mut result);
        }
        for tab_id in strip_finished {
            result.finished_closes.extend(self.close_tab(tab_id));
        }
        result
    }

    /// Neighbor that will take this tab's place on the strip (`dir`: +1 = right).
    /// Prefers the immediate left neighbor; the first chip falls through to the right.
    pub fn tab_close_handoff(&self, tab_id: u64) -> Option<(u64, f32)> {
        let surface = self.surface_of_tab(tab_id)?;
        let strip = self.tabs_on_surface(surface);
        let idx = strip.iter().position(|&id| id == tab_id)?;
        if strip.len() <= 1 {
            return None;
        }
        if idx > 0 {
            Some((strip[idx - 1], -1.0))
        } else {
            Some((strip[idx + 1], 1.0))
        }
    }

    /// Close a tab that has neighbors: chip shifts away, next tab pulls over.
    pub fn begin_close_tab(&mut self, tab_id: u64) -> bool {
        if self.is_last_tab_on_surface(tab_id) {
            return self.begin_close_last_window_tab(tab_id);
        }
        if self
            .tabs
            .iter()
            .find(|t| t.id == tab_id)
            .is_some_and(|t| t.exit.is_some() || t.solo_exit.is_some())
        {
            return true;
        }
        let Some((next_id, dir)) = self.tab_close_handoff(tab_id) else {
            return false;
        };
        let slide = self.active_id == tab_id;
        if slide {
            self.active_id = next_id;
        }
        if let Some(tab) = self.tabs.iter_mut().find(|t| t.id == tab_id) {
            for pid in tab.root.leaf_ids() {
                if let Some(p) = self.panes.get_mut(&pid) {
                    p.exiting = true;
                }
            }
        }
        if let Some(tab) = self.tabs.iter_mut().find(|t| t.id == tab_id) {
            tab.exit = Some(TabExitAnim::start(next_id, dir, slide));
        }
        true
    }

    /// Closing tab on this surface, if any: (strip index, anim).
    pub fn tab_exit_on_surface(&self, surface: u64) -> Option<(usize, &TabExitAnim)> {
        let strip = self.tabs_on_surface(surface);
        for (i, id) in strip.iter().enumerate() {
            if let Some(exit) = self
                .tabs
                .iter()
                .find(|t| t.id == *id)
                .and_then(|t| t.exit.as_ref())
            {
                return Some((i, exit));
            }
        }
        None
    }

    /// Last tab on `surface` — close should shrink+fade the window.
    pub fn is_last_tab_on_surface(&self, tab_id: u64) -> bool {
        let Some(surface) = self.surface_of_tab(tab_id) else {
            return false;
        };
        self.tabs_on_surface(surface).len() <= 1
    }

    /// Close a tab that is the last one on its window (shrink + fade).
    pub fn begin_close_last_window_tab(&mut self, tab_id: u64) -> bool {
        let Some(tab) = self.tabs.iter_mut().find(|t| t.id == tab_id) else {
            return false;
        };
        if tab.solo_exit.is_some() {
            return true;
        }
        let pane_id = tab.focus_pane;
        let ids = tab.root.leaf_ids();
        for pid in ids {
            if let Some(p) = self.panes.get_mut(&pid) {
                p.exiting = true;
            }
        }
        if let Some(tab) = self.tabs.iter_mut().find(|t| t.id == tab_id) {
            tab.solo_exit = Some(SoloExitAnim::start_window(pane_id));
        }
        true
    }

    /// Begin graceful exit of a pane (shell died or user closed).
    /// Returns true if an animation was started (or already in progress).
    pub fn begin_close_pane(&mut self, pane_id: u64) -> bool {
        if !self.panes.contains_key(&pane_id) {
            return false;
        }

        let Some(tab_id) = self.tabs.iter().find(|t| {
            t.root.contains_pane(pane_id)
                || t.solo_exit.as_ref().map(|s| s.pane_id) == Some(pane_id)
        }).map(|t| t.id) else {
            return false;
        };

        let already_closing = self.tabs.iter().any(|t| t.id == tab_id && t.root.is_closing(pane_id));
        if self.panes.get(&pane_id).is_some_and(|p| p.exiting) && already_closing {
            return true;
        }

        // Snap other in-progress jelly-closes. Starting a second close on the
        // same branch used to overwrite CloseSide and leave the first pane
        // stuck at exiting=true (× / ⌘W then did nothing).
        self.snap_finish_closes(tab_id, Some(pane_id));

        if !self.panes.contains_key(&pane_id) {
            return true;
        }
        if let Some(p) = self.panes.get_mut(&pane_id) {
            p.exiting = false;
        }

        let last_on_window = self.is_last_tab_on_surface(tab_id);
        let leaf_count = self
            .tabs
            .iter()
            .find(|t| t.id == tab_id)
            .map(|t| t.root.leaf_ids().len())
            .unwrap_or(0);
        if leaf_count <= 1 {
            if let Some(p) = self.panes.get_mut(&pane_id) {
                p.exiting = true;
            }
            if last_on_window {
                if let Some(tab) = self.tabs.iter_mut().find(|t| t.id == tab_id) {
                    if tab.solo_exit.is_none() {
                        tab.solo_exit = Some(SoloExitAnim::start_window(pane_id));
                    }
                }
                return true;
            }
            return self.begin_close_tab(tab_id);
        }

        let started = self
            .tabs
            .iter_mut()
            .find(|t| t.id == tab_id)
            .is_some_and(|tab| tab.root.begin_close_leaf(pane_id));
        if !started {
            return false;
        }
        if let Some(p) = self.panes.get_mut(&pane_id) {
            p.exiting = true;
        }
        if let Some(tab) = self.tabs.iter_mut().find(|t| t.id == tab_id) {
            if tab.focus_pane == pane_id {
                if let Some(other) = tab.root.focus_after_close(pane_id) {
                    tab.focus_pane = other;
                } else if let Some(other) =
                    tab.root.leaf_ids().into_iter().find(|id| *id != pane_id)
                {
                    tab.focus_pane = other;
                }
            }
        }
        true
    }

    /// Instantly collapse every jelly-close on `tab_id` except `keep`.
    fn snap_finish_closes(&mut self, tab_id: u64, keep: Option<u64>) {
        loop {
            let ids: Vec<u64> = match self.tabs.iter().find(|t| t.id == tab_id) {
                Some(tab) => tab
                    .root
                    .closing_leaf_ids()
                    .into_iter()
                    .filter(|id| keep != Some(*id))
                    .collect(),
                None => return,
            };
            if ids.is_empty() {
                return;
            }
            for id in ids {
                let mut dummy = TickResult::default();
                self.finalize_pane_close(id, &mut dummy);
                self.pending_drops.push(id);
            }
        }
    }

    fn finalize_pane_close(&mut self, pane_id: u64, result: &mut TickResult) {
        let Some(tab_idx) = self
            .tabs
            .iter()
            .position(|t| t.root.contains_pane(pane_id) || t.root.is_closing(pane_id))
        else {
            // Already removed from tree — still drop pane map entry
            self.panes.remove(&pane_id);
            return;
        };

        let tab = &mut self.tabs[tab_idx];
        match tab.root.remove_leaf(pane_id) {
            RemoveResult::Removed { focus_hint } => {
                if tab.focus_pane == pane_id || !tab.root.contains_pane(tab.focus_pane) {
                    tab.focus_pane = focus_hint;
                }
                self.panes.remove(&pane_id);
            }
            RemoveResult::RemovedEmpty => {
                // Shouldn't happen for branch close of one of two
                self.panes.remove(&pane_id);
            }
            RemoveResult::NotFound => {
                self.panes.remove(&pane_id);
            }
        }
        let _ = result;
    }

    fn finalize_solo_close(&mut self, tab_id: u64, pane_id: u64, result: &mut TickResult) {
        self.panes.remove(&pane_id);
        if self.tabs.len() <= 1 {
            // Last well closed — empty session; app will quit.
            self.tabs.clear();
            self.panes.clear();
        } else {
            let removed = self.close_tab(tab_id);
            for id in removed {
                if id != pane_id {
                    result.finished_closes.push(id);
                }
            }
        }
    }

    /// Create a new chrome tab with one pane. Returns (tab_id, pane_id).
    pub fn new_tab(&mut self, cols: u16, rows: u16) -> (u64, u64) {
        let pane_id = self.next_pane_id;
        self.next_pane_id = self.next_pane_id.saturating_add(1);
        let tab_id = self.next_tab_id;
        self.next_tab_id = self.next_tab_id.saturating_add(1);

        let cwd = self
            .active_pane()
            .map(|p| p.cwd.clone())
            .unwrap_or_else(initial_cwd);

        self.panes.insert(
            pane_id,
            new_terminal_pane(pane_id, cols, rows, cwd),
        );
        let surface = self
            .active_tab()
            .map(|t| t.surface)
            .unwrap_or(0);
        self.tabs.push(Tab {
            id: tab_id,
            title: default_tab_title(tab_id),
            root: SplitNode::leaf(pane_id),
            focus_pane: pane_id,
            solo_exit: None,
            surface,
            exit: None,
        });
        self.active_id = tab_id;
        (tab_id, pane_id)
    }

    pub fn close_tab(&mut self, id: u64) -> Vec<u64> {
        if self.tabs.len() <= 1 {
            return Vec::new();
        }
        let Some(pos) = self.tabs.iter().position(|t| t.id == id) else {
            return Vec::new();
        };
        let surface = self.tabs[pos].surface;
        let strip_idx = self
            .tabs
            .iter()
            .filter(|t| t.surface == surface)
            .position(|t| t.id == id)
            .unwrap_or(0);
        let tab = self.tabs.remove(pos);
        let pane_ids = tab.root.leaf_ids();
        for pid in &pane_ids {
            self.panes.remove(pid);
        }
        if self.active_id == id {
            let others: Vec<u64> = self
                .tabs
                .iter()
                .filter(|t| t.surface == surface)
                .map(|t| t.id)
                .collect();
            let next = if strip_idx > 0 {
                others.get(strip_idx - 1)
            } else {
                others.first()
            }
            .copied()
            .or_else(|| others.last().copied())
            .or_else(|| self.tabs.first().map(|t| t.id));
            if let Some(nid) = next {
                self.active_id = nid;
            }
        }
        pane_ids
    }

    pub fn select_tab(&mut self, id: u64) -> bool {
        if self.tabs.iter().any(|t| t.id == id) {
            self.active_id = id;
            true
        } else {
            false
        }
    }

    /// Jump to tab by strip index (0-based). Product ⌘1–⌘9.
    pub fn select_tab_index(&mut self, index: usize) -> bool {
        let surface = self.active_tab().map(|t| t.surface).unwrap_or(0);
        let ids = self.tabs_on_surface(surface);
        if let Some(&id) = ids.get(index) {
            self.active_id = id;
            true
        } else {
            false
        }
    }

    /// Active tab's index in its surface strip, if any.
    pub fn active_tab_index(&self) -> Option<usize> {
        let surface = self.active_tab().map(|t| t.surface)?;
        self.tabs_on_surface(surface)
            .iter()
            .position(|id| *id == self.active_id)
    }

    pub fn next_tab(&mut self) {
        let surface = self.active_tab().map(|t| t.surface).unwrap_or(0);
        let ids = self.tabs_on_surface(surface);
        if ids.is_empty() {
            return;
        }
        let pos = ids.iter().position(|id| *id == self.active_id).unwrap_or(0);
        self.active_id = ids[(pos + 1) % ids.len()];
    }

    pub fn prev_tab(&mut self) {
        let surface = self.active_tab().map(|t| t.surface).unwrap_or(0);
        let ids = self.tabs_on_surface(surface);
        if ids.is_empty() {
            return;
        }
        let pos = ids.iter().position(|id| *id == self.active_id).unwrap_or(0);
        let next = if pos == 0 { ids.len() - 1 } else { pos - 1 };
        self.active_id = ids[next];
    }

    pub fn resize_all(&mut self, cols: u16, rows: u16) {
        for p in self.panes.values_mut() {
            p.grid.resize(cols, rows);
        }
    }

    /// Resize a single pane's grid.
    pub fn resize_pane(&mut self, pane_id: u64, cols: u16, rows: u16) {
        if let Some(p) = self.panes.get_mut(&pane_id) {
            p.grid.resize(cols, rows);
        }
    }

    pub fn mark_pane_pty(&mut self, pane_id: u64) {
        if let Some(p) = self.panes.get_mut(&pane_id) {
            p.pty_mode = true;
            p.grid.clear();
        }
    }

    pub fn boot_mock_on_pane(&mut self, pane_id: u64) {
        if let Some(p) = self.panes.get_mut(&pane_id) {
            p.pty_mode = false;
        }
        // Temporarily focus for write helpers
        let prev_tab = self.active_id;
        let prev_focus = self.focus_pane_id();
        if let Some(tab) = self.tabs.iter_mut().find(|t| t.root.contains_pane(pane_id)) {
            self.active_id = tab.id;
            tab.focus_pane = pane_id;
        }
        self.write_banner_and_prompt();
        // restore
        self.active_id = prev_tab;
        if let Some(tab) = self.active_tab_mut() {
            tab.focus_pane = prev_focus;
        }
    }

    pub fn boot_mock_on_active(&mut self) {
        let id = self.focus_pane_id();
        self.boot_mock_on_pane(id);
    }

    /// Split the focused pane. Returns new pane id.
    pub fn split_focused(&mut self, axis: SplitAxis, cols: u16, rows: u16) -> Option<u64> {
        let tab_id = self.active_id;
        let focus = self.focus_pane_id();
        let cwd = self
            .panes
            .get(&focus)
            .map(|p| p.cwd.clone())
            .unwrap_or_else(initial_cwd);

        let new_id = self.next_pane_id;
        self.next_pane_id = self.next_pane_id.saturating_add(1);

        let tab = self.tabs.iter_mut().find(|t| t.id == tab_id)?;
        if !tab.root.split_leaf(focus, new_id, axis) {
            return None;
        }
        tab.focus_pane = new_id;

        self.panes.insert(
            new_id,
            new_terminal_pane(new_id, cols, rows, cwd),
        );
        Some(new_id)
    }

    /// Split the focused pane and insert a widget leaf (no PTY). Returns new id.
    ///
    /// Workspace is a singleton. Guests are not — each call inserts a new leaf.
    pub fn split_focused_widget(
        &mut self,
        axis: SplitAxis,
        kind: WidgetKind,
    ) -> Option<u64> {
        if kind != WidgetKind::Guest {
            if let Some(existing) = self.find_widget(kind) {
                self.set_focus_pane(existing);
                return Some(existing);
            }
        }
        let tab_id = self.active_id;
        let focus = self.focus_pane_id();
        let cwd = self
            .panes
            .get(&focus)
            .map(|p| p.cwd.clone())
            .unwrap_or_else(initial_cwd);

        let new_id = self.next_pane_id;
        self.next_pane_id = self.next_pane_id.saturating_add(1);

        let tab = self.tabs.iter_mut().find(|t| t.id == tab_id)?;
        if !tab.root.split_leaf(focus, new_id, axis) {
            return None;
        }
        tab.focus_pane = new_id;

        self.panes.insert(new_id, new_widget_pane(new_id, kind, cwd));
        Some(new_id)
    }

    /// Split in a guest leaf bound to `guest_id`. Always a new pane.
    pub fn split_focused_guest(
        &mut self,
        axis: SplitAxis,
        guest_id: &str,
        title: &str,
    ) -> Option<u64> {
        let id = self.split_focused_widget(axis, WidgetKind::Guest)?;
        if let Some(p) = self.panes.get_mut(&id) {
            p.guest_id = Some(guest_id.to_string());
            if !title.is_empty() {
                p.title = title.to_string();
            }
        }
        Some(id)
    }

    /// New tab whose only leaf is a guest. No PTY / no leftover shell.
    pub fn new_guest_tab(&mut self, guest_id: &str, title: &str) -> u64 {
        let pane_id = self.next_pane_id;
        self.next_pane_id = self.next_pane_id.saturating_add(1);
        let tab_id = self.next_tab_id;
        self.next_tab_id = self.next_tab_id.saturating_add(1);
        let cwd = self
            .active_pane()
            .map(|p| p.cwd.clone())
            .unwrap_or_else(initial_cwd);
        let mut pane = new_widget_pane(pane_id, WidgetKind::Guest, cwd);
        pane.guest_id = Some(guest_id.to_string());
        // Empty → blank header until the page title arrives (like OSC).
        // Do not seed the engine name here — that reads as a second app.
        pane.title = if title.is_empty() {
            String::new()
        } else {
            title.to_string()
        };
        self.panes.insert(pane_id, pane);
        let surface = self.active_tab().map(|t| t.surface).unwrap_or(0);
        let tab_title = if title.is_empty() {
            default_tab_title(tab_id)
        } else {
            title.to_string()
        };
        self.tabs.push(Tab {
            id: tab_id,
            title: tab_title,
            root: SplitNode::leaf(pane_id),
            focus_pane: pane_id,
            solo_exit: None,
            surface,
            exit: None,
        });
        self.active_id = tab_id;
        pane_id
    }

    /// First non-exiting pane hosting `kind`, if any (any tab).
    pub fn find_widget(&self, kind: WidgetKind) -> Option<u64> {
        self.panes.iter().find_map(|(id, p)| {
            if p.kind == PaneKind::Widget(kind) && !p.exiting {
                Some(*id)
            } else {
                None
            }
        })
    }

    pub fn pane_kind(&self, id: u64) -> PaneKind {
        self.panes
            .get(&id)
            .map(|p| p.kind)
            .unwrap_or(PaneKind::Terminal)
    }

    pub fn is_widget(&self, id: u64) -> bool {
        self.pane_kind(id).widget().is_some()
    }

    pub fn focused_is_workspace(&self) -> bool {
        self.pane_kind(self.focus_pane_id()).is_workspace()
    }

    pub fn focused_is_guest(&self) -> bool {
        self.pane_kind(self.focus_pane_id()).is_guest()
    }

    /// Tab that currently owns `pane_id` in its split tree.
    pub fn tab_id_for_pane(&self, pane_id: u64) -> Option<u64> {
        self.tabs
            .iter()
            .find(|t| t.root.contains_pane(pane_id))
            .map(|t| t.id)
    }

    /// Re-dock `moving` beside `target` (same or other tab). Keeps the Pane + PTY.
    ///
    /// Refuses the last pane of the last tab (nowhere to land without a new window).
    pub fn reparent_pane(&mut self, moving: u64, target: u64, edge: DockEdge) -> bool {
        if moving == target {
            return false;
        }
        if !self.panes.contains_key(&moving) || !self.panes.contains_key(&target) {
            return false;
        }
        if self.panes.get(&moving).is_some_and(|p| p.exiting)
            || self.panes.get(&target).is_some_and(|p| p.exiting)
        {
            return false;
        }
        let Some(src_tab) = self.tab_id_for_pane(moving) else {
            return false;
        };
        let Some(dst_tab) = self.tab_id_for_pane(target) else {
            return false;
        };

        if src_tab == dst_tab {
            let tab = match self.tabs.iter_mut().find(|t| t.id == src_tab) {
                Some(t) => t,
                None => return false,
            };
            if tab.root.leaf_ids().len() <= 1 {
                return false;
            }
            if tab.root.is_closing(target) {
                return false;
            }
            let fallback = match tab.root.remove_leaf(moving) {
                RemoveResult::Removed { focus_hint } => {
                    if tab.focus_pane == moving {
                        tab.focus_pane = focus_hint;
                    }
                    focus_hint
                }
                RemoveResult::RemovedEmpty | RemoveResult::NotFound => return false,
            };
            if !tab.root.split_leaf_edge(target, moving, edge) {
                // Don't leave a live PTY with no leaf (workflow risk).
                let _ = tab.root.split_leaf(fallback, moving, SplitAxis::Vertical);
                return false;
            }
            tab.focus_pane = moving;
            return true;
        }

        // Cross-tab: detach from source, then split the destination target.
        let src_idx = match self.tabs.iter().position(|t| t.id == src_tab) {
            Some(i) => i,
            None => return false,
        };
        let src_sole = self.tabs[src_idx].root.leaf_ids().len() <= 1;
        if src_sole && self.tabs.len() <= 1 {
            return false;
        }

        if src_sole {
            // Drop the empty tab; keep the pane map entry.
            let _ = self.tabs.remove(src_idx);
            if self.active_id == src_tab {
                self.active_id = dst_tab;
            }
        } else if let Some(tab) = self.tabs.iter_mut().find(|t| t.id == src_tab) {
            match tab.root.remove_leaf(moving) {
                RemoveResult::Removed { focus_hint } => {
                    if tab.focus_pane == moving {
                        tab.focus_pane = focus_hint;
                    }
                }
                RemoveResult::RemovedEmpty | RemoveResult::NotFound => return false,
            }
        } else {
            return false;
        }

        let Some(dst) = self.tabs.iter_mut().find(|t| t.id == dst_tab) else {
            return false;
        };
        if dst.root.is_closing(target) {
            return false;
        }
        if !dst.root.split_leaf_edge(target, moving, edge) {
            return false;
        }
        dst.focus_pane = moving;
        self.active_id = dst_tab;
        true
    }

    /// Move `moving` onto `tab_id`, splitting the tab's focused leaf to the right.
    pub fn move_pane_to_tab(&mut self, moving: u64, tab_id: u64) -> bool {
        let Some(target) = self
            .tabs
            .iter()
            .find(|t| t.id == tab_id)
            .map(|t| t.focus_pane)
        else {
            return false;
        };
        if moving == target {
            if self
                .tabs
                .iter()
                .find(|t| t.id == tab_id)
                .map(|t| t.root.leaf_ids().len())
                == Some(1)
            {
                return false;
            }
        }
        self.reparent_pane(moving, target, DockEdge::Right)
    }

    /// Reorder tabs on the same surface. `from`/`to` are indices into that surface's strip.
    pub fn reorder_tab_on_surface(&mut self, surface: u64, from: usize, to: usize) -> bool {
        let ids = self.tabs_on_surface(surface);
        if from == to {
            return false;
        }
        let Some(&tid) = ids.get(from) else {
            return false;
        };
        self.place_tab_on_surface(tid, surface, to)
    }

    /// Move `tab_id` onto `surface` and insert it at strip index `index`
    /// (clamped to the end). Works for same-window reorder and cross-window drop.
    pub fn place_tab_on_surface(&mut self, tab_id: u64, surface: u64, index: usize) -> bool {
        let Some(pos) = self.tabs.iter().position(|t| t.id == tab_id) else {
            return false;
        };
        let mut tab = self.tabs.remove(pos);
        tab.surface = surface;
        let dest: Vec<usize> = self
            .tabs
            .iter()
            .enumerate()
            .filter(|(_, t)| t.surface == surface)
            .map(|(i, _)| i)
            .collect();
        let insert_at = if dest.is_empty() {
            self.tabs.len()
        } else if index >= dest.len() {
            dest[dest.len() - 1] + 1
        } else {
            dest[index]
        };
        self.tabs.insert(insert_at, tab);
        self.active_id = tab_id;
        true
    }

    pub fn set_sash_ratio(&mut self, a_leaf: u64, ratio: f32) -> bool {
        for tab in &mut self.tabs {
            if tab.root.set_ratio(a_leaf, ratio) {
                return true;
            }
        }
        false
    }

    pub fn tabs_on_surface(&self, surface: u64) -> Vec<u64> {
        self.tabs
            .iter()
            .filter(|t| t.surface == surface)
            .map(|t| t.id)
            .collect()
    }

    pub fn new_tab_on_surface(
        &mut self,
        cols: u16,
        rows: u16,
        surface: u64,
    ) -> (u64, u64) {
        let (tid, pid) = self.new_tab(cols, rows);
        if let Some(t) = self.tabs.iter_mut().find(|t| t.id == tid) {
            t.surface = surface;
        }
        (tid, pid)
    }

    /// Move a whole tab (and its leaves) onto another in-process surface.
    pub fn move_tab_to_surface(&mut self, tab_id: u64, surface: u64) -> bool {
        let Some(tab) = self.tabs.iter_mut().find(|t| t.id == tab_id) else {
            return false;
        };
        if tab.surface == surface {
            return false;
        }
        tab.surface = surface;
        self.active_id = tab_id;
        true
    }

    pub fn surface_of_tab(&self, tab_id: u64) -> Option<u64> {
        self.tabs.iter().find(|t| t.id == tab_id).map(|t| t.surface)
    }

    /// Detach `pane_id` into its own tab on `surface`. Sole-pane tabs just move.
    /// Returns the (possibly new) tab id.
    pub fn extract_pane_to_new_tab(&mut self, pane_id: u64, surface: u64) -> Option<u64> {
        if self.panes.get(&pane_id).is_some_and(|p| p.exiting) {
            return None;
        }
        let src_tab = self.tab_id_for_pane(pane_id)?;
        let src_idx = self.tabs.iter().position(|t| t.id == src_tab)?;
        let sole = self.tabs[src_idx].root.leaf_ids().len() <= 1;
        if sole {
            self.tabs[src_idx].surface = surface;
            self.active_id = src_tab;
            return Some(src_tab);
        }
        match self.tabs[src_idx].root.remove_leaf(pane_id) {
            RemoveResult::Removed { focus_hint } => {
                if self.tabs[src_idx].focus_pane == pane_id {
                    self.tabs[src_idx].focus_pane = focus_hint;
                }
            }
            RemoveResult::RemovedEmpty | RemoveResult::NotFound => return None,
        }
        let tab_id = self.next_tab_id;
        self.next_tab_id = self.next_tab_id.saturating_add(1);
        self.tabs.push(Tab {
            id: tab_id,
            title: default_tab_title(tab_id),
            root: SplitNode::leaf(pane_id),
            focus_pane: pane_id,
            solo_exit: None,
            surface,
            exit: None,
        });
        self.active_id = tab_id;
        Some(tab_id)
    }

    /// Close focused pane with jelly animation (same path as shell exit).
    pub fn close_focused_pane_or_tab(&mut self) -> CloseOutcome {
        let focus = self.focus_pane_id();
        if self.begin_close_pane(focus) {
            CloseOutcome::Animating
        } else {
            CloseOutcome::None
        }
    }

    /// True if the session has no tabs left (last pane exited).
    pub fn is_empty(&self) -> bool {
        self.tabs.is_empty()
    }

    /// Solo-exit scale for layout (1 = full, 0 = gone).
    pub fn solo_exit_scale(&self, pane_id: u64) -> Option<f32> {
        for tab in &self.tabs {
            if let Some(anim) = &tab.solo_exit {
                if anim.pane_id == pane_id {
                    return Some(anim.jelly.clamp(0.0, 1.15));
                }
            }
        }
        None
    }

    /// Returns true when the focused leaf (or its tab) actually changed.
    pub fn set_focus_pane(&mut self, pane_id: u64) -> bool {
        if !self.panes.contains_key(&pane_id) {
            return false;
        }
        if let Some(tab) = self
            .tabs
            .iter_mut()
            .find(|t| t.root.contains_pane(pane_id))
        {
            let changed = tab.focus_pane != pane_id || self.active_id != tab.id;
            self.active_id = tab.id;
            tab.focus_pane = pane_id;
            return changed;
        }
        false
    }

    pub fn focus_neighbor(&mut self, dir: FocusDir, area: crate::layout::Rect, gap: f32) {
        let Some(tab) = self.active_tab() else {
            return;
        };
        let focus = tab.focus_pane;
        if let Some(next) = tab.root.neighbor(focus, dir, area, gap) {
            if let Some(t) = self.active_tab_mut() {
                t.focus_pane = next;
            }
        }
    }

    pub fn type_char(&mut self, c: char) {
        if c.is_control() {
            return;
        }
        self.leave_history_browse();
        self.draft_mut().push(c);
    }

    pub fn backspace(&mut self) {
        self.leave_history_browse();
        self.draft_mut().pop();
    }

    pub fn paste_draft(&mut self, text: &str) {
        self.leave_history_browse();
        let d = self.draft_mut();
        for c in text.chars() {
            if !c.is_control() {
                d.push(c);
            } else if c == '\n' || c == '\r' {
                break;
            }
        }
    }

    pub fn push_history(&mut self, line: &str) {
        let line = line.trim_end();
        if line.is_empty() {
            return;
        }
        if self.history.last().map(|s| s.as_str()) != Some(line) {
            self.history.push(line.to_string());
            if self.history.len() > 200 {
                self.history.drain(0..self.history.len() - 200);
            }
        }
        self.history_idx = None;
        self.history_stash.clear();
    }

    pub fn history_up(&mut self) {
        if self.history.is_empty() {
            return;
        }
        match self.history_idx {
            None => {
                self.history_stash = self.draft().to_string();
                let i = self.history.len() - 1;
                self.history_idx = Some(i);
                *self.draft_mut() = self.history[i].clone();
            }
            Some(i) if i > 0 => {
                let i = i - 1;
                self.history_idx = Some(i);
                *self.draft_mut() = self.history[i].clone();
            }
            Some(_) => {}
        }
    }

    pub fn history_down(&mut self) {
        let Some(i) = self.history_idx else {
            return;
        };
        if i + 1 < self.history.len() {
            let i = i + 1;
            self.history_idx = Some(i);
            *self.draft_mut() = self.history[i].clone();
        } else {
            self.history_idx = None;
            *self.draft_mut() = std::mem::take(&mut self.history_stash);
        }
    }

    fn leave_history_browse(&mut self) {
        if self.history_idx.is_some() {
            self.history_idx = None;
            self.history_stash.clear();
        }
    }

    pub fn submit_draft_mock(&mut self) {
        let line = self.draft().trim_end().to_string();
        if line.is_empty() {
            return;
        }
        self.push_history(&line);
        self.active_grid_mut().writeln(&line);

        match shell::run_command(&line) {
            ShellOutput::Clear => {
                self.active_grid_mut().clear();
                self.write_prompt();
            }
            ShellOutput::Lines(rows) => {
                for row in rows {
                    self.write_output_line(&row);
                }
                self.write_prompt();
            }
        }
        self.draft_mut().clear();
    }

    fn write_banner_and_prompt(&mut self) {
        let lines = shell::banner_lines();
        for (i, line) in lines.iter().enumerate() {
            if line.is_empty() {
                self.active_grid_mut().newline();
                continue;
            }
            let fg = match i {
                0 => theme::JADE,
                1 | 3 => theme::DIM,
                _ => theme::FG,
            };
            self.active_grid_mut().writeln_colored(line, fg);
        }
        self.write_prompt();
    }

    fn write_prompt(&mut self) {
        let lines = shell::prompt_lines();
        if let Some(host) = lines.first() {
            self.active_grid_mut().writeln_colored(host, theme::DIM);
        }
        if let Some(glyph) = lines.get(1) {
            self.active_grid_mut().put_str_colored(glyph, theme::JADE);
        }
    }

    fn write_output_line(&mut self, row: &str) {
        let fg = if row.starts_with("mock: command not found") {
            theme::ERR
        } else if row.starts_with("(PTY")
            || row.starts_with("(real")
            || row.starts_with("glyph rain")
            || row.starts_with("chrome is")
            || row.starts_with("rule:")
        {
            theme::DIM
        } else if row.starts_with("inkstone") || row.starts_with("suzuri") {
            theme::JADE
        } else {
            theme::FG
        };
        self.active_grid_mut().writeln_colored(row, fg);
    }
}

#[derive(Clone, Debug)]
pub enum CloseOutcome {
    None,
    /// Last tab/pane — app should exit (immediate, rare).
    QuitApp,
    ClosedPanes(Vec<u64>),
    /// Close animation started; PTY drop happens when tick finishes.
    Animating,
}

impl Default for ChromeSession {
    fn default() -> Self {
        Self::new(80, 24)
    }
}

/// Home directory (`$HOME` / `%USERPROFILE%`) when it exists as a folder.
pub fn user_home_dir() -> Option<String> {
    let home = std::env::var("HOME")
        .or_else(|_| std::env::var("USERPROFILE"))
        .ok()?;
    let home = home.trim();
    if home.is_empty() {
        return None;
    }
    let p = std::path::Path::new(home);
    if p.is_dir() {
        Some(home.to_string())
    } else {
        None
    }
}

/// True when `cwd` is a Dock / installer launch directory, not a user project.
///
/// macOS Launch Services starts `.app` binaries in `Contents/Resources`.
/// Windows installers often set cwd to the folder that contains `suzuri.exe`.
/// A source checkout (`go.mod` + `chrome/Cargo.toml`) is kept so
/// `cd repo && ./suzuri` still opens in the repo.
pub fn is_unhelpful_cwd(cwd: &str) -> bool {
    is_unhelpful_cwd_ex(cwd, current_exe_dir().as_deref())
}

fn current_exe_dir() -> Option<String> {
    std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.to_string_lossy().into_owned()))
}

fn looks_like_source_tree(dir: &std::path::Path) -> bool {
    dir.join("go.mod").is_file() && dir.join("chrome").join("Cargo.toml").is_file()
}

fn is_filesystem_root(cwd_n: &str) -> bool {
    let u = slashify_display(cwd_n);
    if u == "/" {
        return true;
    }
    // Windows drive root: "C:" or "C:/"
    let b = u.as_bytes();
    b.len() <= 3
        && b.first().map(|c| c.is_ascii_alphabetic()).unwrap_or(false)
        && b.get(1) == Some(&b':')
        && (b.len() == 2 || b[2] == b'/')
}

fn is_unhelpful_cwd_ex(cwd: &str, exe_dir: Option<&str>) -> bool {
    let cwd_n = normalize_abs_path(cwd);
    if cwd_n.is_empty() {
        return true;
    }
    // Finder / Dock / many GUI launches start at filesystem root (`/`).
    if is_filesystem_root(&cwd_n) {
        return true;
    }
    let slash = slashify_display(&cwd_n);
    if slash.contains(".app/Contents") {
        return true;
    }
    let Some(exe_dir) = exe_dir else {
        return false;
    };
    let exe_n = normalize_abs_path(exe_dir);
    if exe_n.is_empty() {
        return false;
    }
    if !paths_equal(&cwd_n, &exe_n)
        && !slashify_display(&cwd_n).eq_ignore_ascii_case(&slashify_display(&exe_n))
    {
        return false;
    }
    !looks_like_source_tree(std::path::Path::new(exe_dir))
}

/// Working directory for a newly created pane (and for `chdir` at launch).
///
/// Prefer the process cwd when the user launched from a real project folder;
/// fall back to `$HOME` so Dock / `.app` / installer starts are not
/// `/Applications/suzuri.app/Contents/Resources`.
pub fn initial_cwd() -> String {
    if let Ok(cwd) = std::env::current_dir() {
        let cwd = cwd.to_string_lossy();
        if !is_unhelpful_cwd(&cwd) {
            return cwd.into_owned();
        }
    }
    user_home_dir()
        .or_else(|| std::env::var("HOME").ok().filter(|s| !s.is_empty()))
        .or_else(|| std::env::var("USERPROFILE").ok().filter(|s| !s.is_empty()))
        .unwrap_or_else(|| "/".into())
}

/// `chdir` into [`initial_cwd`] so child shells inherit `~` instead of the
/// `.app` Resources folder. No-op when already there.
pub fn normalize_process_cwd() {
    let want = initial_cwd();
    if want.is_empty() {
        return;
    }
    if let Ok(cur) = std::env::current_dir() {
        if paths_equal(&cur.to_string_lossy(), &want) {
            return;
        }
    }
    let _ = std::env::set_current_dir(&want);
}

/// Compact guest location for the footer path row (cwd analog).
///
/// `https://www.ladybird.org/foo/` → `ladybird.org/foo`. Internal
/// `about:` / `ladybird:` pages are empty so they never paint as a path.
pub fn display_guest_path(url: &str) -> String {
    let raw = url.trim();
    if raw.is_empty() {
        return String::new();
    }
    let lower = raw.to_ascii_lowercase();
    if lower.starts_with("about:") || lower.starts_with("ladybird:") {
        return String::new();
    }
    let mut s = raw.to_string();
    for prefix in ["https://", "http://"] {
        if s.len() >= prefix.len() && s[..prefix.len()].eq_ignore_ascii_case(prefix) {
            s = s[prefix.len()..].to_string();
            break;
        }
    }
    if s.len() >= 4 && s[..4].eq_ignore_ascii_case("www.") {
        s = s[4..].to_string();
    }
    while s.ends_with('/') {
        s.pop();
    }
    s
}

/// Hello title is the engine name, not a page. Chrome should ignore it.
pub fn guest_engine_hello_title(title: &str) -> bool {
    matches!(
        title.trim().to_ascii_lowercase().as_str(),
        "ladybird" | "ladybird browser" | "guest"
    )
}

/// Page title usable as a pane header (not about:, not the raw URL).
pub fn guest_page_title_ok(title: &str, url: &str) -> bool {
    let title = title.trim();
    if title.is_empty() {
        return false;
    }
    let lower = title.to_ascii_lowercase();
    if lower.starts_with("about:") || lower.starts_with("ladybird:") || lower == "new tab" {
        return false;
    }
    let url = url.trim();
    if !url.is_empty() && (title == url || title == display_guest_path(url)) {
        return false;
    }
    true
}

/// Location the footer should keep (`about:` / empty dropped).
pub fn guest_url_ok(url: &str) -> bool {
    !display_guest_path(url).is_empty()
}

/// Shorten `$HOME` / `%USERPROFILE%` → `~` for chrome path display.
///
/// Compares **cleaned** paths so trailing slashes / `.` / `..` still map to `~`.
/// Output always uses `/` after the tilde (product-style).
pub fn display_path(cwd: &str) -> String {
    let cwd = normalize_abs_path(cwd);
    if cwd.is_empty() {
        return String::new();
    }
    let home = std::env::var("HOME")
        .or_else(|_| std::env::var("USERPROFILE"))
        .unwrap_or_default();
    let home = normalize_abs_path(&home);
    if home.is_empty() {
        return slashify_display(&cwd);
    }
    // Exact home (also try canonicalize when both exist on disk).
    if paths_equal(&cwd, &home) {
        return "~".into();
    }
    // Compare with unified separators so Windows USERPROFILE works.
    let cwd_u = slashify_display(&cwd);
    let home_u = slashify_display(&home);
    if paths_equal(&cwd_u, &home_u) || cwd_u.eq_ignore_ascii_case(&home_u) {
        return "~".into();
    }
    let home_prefix = if home_u == "/" {
        "/".to_string()
    } else {
        format!("{home_u}/")
    };
    // Case-insensitive prefix on Windows-style drives.
    let under = if cfg!(windows) {
        cwd_u
            .to_ascii_lowercase()
            .strip_prefix(&home_prefix.to_ascii_lowercase())
            .map(|r| r.to_string())
    } else {
        cwd_u.strip_prefix(&home_prefix).map(|r| r.to_string())
    };
    if let Some(rest) = under {
        if !rest.is_empty() {
            return format!("~/{rest}");
        }
        return "~".into();
    }
    cwd_u
}

/// Display form: backslashes → forward slashes (UI labels).
fn slashify_display(p: &str) -> String {
    p.replace('\\', "/")
}

/// Collapse `//`, trailing `/` (except root), and `.` / `..` components.
fn normalize_abs_path(p: &str) -> String {
    let p = p.trim();
    if p.is_empty() {
        return String::new();
    }
    use std::path::{Component, Path};
    let path = Path::new(p);
    let mut out = std::path::PathBuf::new();
    let mut absolute = false;
    for c in path.components() {
        match c {
            Component::Prefix(pref) => {
                out.push(pref.as_os_str());
            }
            Component::RootDir => {
                absolute = true;
                out.push(std::path::MAIN_SEPARATOR_STR);
            }
            Component::CurDir => {}
            Component::ParentDir => {
                let _ = out.pop();
            }
            Component::Normal(s) => out.push(s),
        }
    }
    let mut s = out.to_string_lossy().into_owned();
    // PathBuf on Unix for "/" + "Users" can look right; ensure no trailing slash.
    if s.len() > 1 && s.ends_with('/') {
        s.pop();
    }
    if absolute && s.is_empty() {
        s = "/".into();
    }
    s
}

fn paths_equal(a: &str, b: &str) -> bool {
    if a == b {
        return true;
    }
    // Resolve symlinks when possible (macOS /var vs /private/var, etc.).
    if let (Ok(ca), Ok(cb)) = (
        std::fs::canonicalize(a),
        std::fs::canonicalize(b),
    ) {
        return ca == cb;
    }
    false
}

fn cwd_after_command(cwd: &str, line: &str) -> Option<String> {
    let line = line.trim();
    if line == "cd" || line == "cd ~" {
        return std::env::var("HOME").ok().filter(|s| !s.is_empty());
    }
    let rest = line.strip_prefix("cd ")?.trim();
    if rest.is_empty() {
        return None;
    }
    let rest = rest.trim_matches(|c| c == '"' || c == '\'');
    if rest == "-" {
        return None;
    }
    if rest.starts_with('/') {
        return Some(rest.to_string());
    }
    if let Some(rel) = rest.strip_prefix("~/") {
        let home = std::env::var("HOME").ok()?;
        return Some(format!("{home}/{rel}"));
    }
    if rest == "~" {
        return std::env::var("HOME").ok();
    }
    let base = if cwd.is_empty() { "/" } else { cwd };
    Some(format!("{}/{}", base.trim_end_matches('/'), rest))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::panes::DockEdge;

    #[test]
    fn app_bundle_cwd_is_unhelpful() {
        assert!(is_unhelpful_cwd_ex(
            "/Applications/suzuri.app/Contents/Resources",
            Some("/Applications/suzuri.app/Contents/MacOS")
        ));
        assert!(is_unhelpful_cwd_ex(
            "/Applications/suzuri.app/Contents/MacOS",
            Some("/Applications/suzuri.app/Contents/MacOS")
        ));
        assert!(!is_unhelpful_cwd_ex(
            "/Users/stephen/projects/foo",
            Some("/Applications/suzuri.app/Contents/MacOS")
        ));
        assert!(!is_unhelpful_cwd_ex("/tmp", Some("/usr/local/bin")));
        assert!(is_unhelpful_cwd_ex("", None));
        // Finder/Dock often start GUI apps at `/`.
        assert!(is_unhelpful_cwd_ex("/", None));
        assert!(is_unhelpful_cwd_ex(
            "/",
            Some("/Applications/suzuri.app/Contents/MacOS")
        ));
        assert!(is_unhelpful_cwd_ex("C:\\", Some("C:\\Program Files\\suzuri")));
        assert!(is_unhelpful_cwd_ex("C:/", None));
    }

    #[test]
    fn initial_cwd_never_returns_app_resources() {
        let cwd = initial_cwd();
        assert!(
            !slashify_display(&cwd).contains(".app/Contents"),
            "initial_cwd={cwd}"
        );
        assert!(!cwd.is_empty());
    }

    #[test]
    fn display_guest_path_strips_scheme() {
        assert_eq!(display_guest_path("https://www.ladybird.org/foo/"), "ladybird.org/foo");
        assert_eq!(display_guest_path("http://localhost:5173/docs"), "localhost:5173/docs");
        assert_eq!(display_guest_path("about:newtab"), "");
        assert_eq!(display_guest_path("about:blank"), "");
        assert!(guest_url_ok("https://ladybird.org"));
        assert!(!guest_url_ok("about:newtab"));
        assert!(guest_engine_hello_title("Ladybird"));
        assert!(!guest_engine_hello_title("Actually independent"));
        assert!(!guest_page_title_ok("https://ladybird.org", "https://ladybird.org"));
        assert!(guest_page_title_ok("Actually independent", "https://ladybird.org"));
        assert!(!guest_page_title_ok("New Tab", ""));
    }

    #[test]
    fn display_path_home_is_tilde() {
        // Unix CI has HOME; Windows runners usually only USERPROFILE.
        let home = std::env::var("HOME")
            .or_else(|_| std::env::var("USERPROFILE"))
            .expect("HOME or USERPROFILE");
        assert_eq!(display_path(&home), "~");
        let slash = if home.contains('\\') { "\\" } else { "/" };
        assert_eq!(display_path(&format!("{home}{slash}")), "~");
        let sub = display_path(&format!("{home}{slash}projects{slash}foo"));
        assert_eq!(sub, "~/projects/foo", "sub={sub}");
        if !cfg!(windows) {
            assert_eq!(display_path("/tmp"), "/tmp");
        }
    }

    #[test]
    fn boot_writes_banner() {
        let mut s = ChromeSession::new(80, 24);
        s.boot_mock_on_active();
        let snap = s.active_grid().snapshot_strings();
        assert!(snap.iter().any(|l| l.contains("suzuri surface")));
    }

    #[test]
    fn split_creates_second_pane() {
        let mut s = ChromeSession::new(80, 24);
        let new = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        assert_ne!(new, 1);
        assert_eq!(s.focus_pane_id(), new);
        assert_eq!(s.active_tab().unwrap().root.leaf_ids().len(), 2);
    }

    #[test]
    fn set_focus_pane_moves_immediately() {
        let mut s = ChromeSession::new(80, 24);
        let first = s.focus_pane_id();
        let second = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        assert_eq!(s.focus_pane_id(), second);
        assert!(s.set_focus_pane(first));
        assert_eq!(s.focus_pane_id(), first);
        assert!(!s.set_focus_pane(first));
        assert!(s.set_focus_pane(second));
        assert_eq!(s.focus_pane_id(), second);
    }

    #[test]
    fn tabs_new_close() {
        let mut s = ChromeSession::default();
        let (tid, _pid) = s.new_tab(80, 24);
        assert_eq!(s.tabs.len(), 2);
        let removed = s.close_tab(tid);
        assert_eq!(removed.len(), 1);
        assert_eq!(s.tabs.len(), 1);
    }

    #[test]
    fn close_tab_selects_left_neighbor_not_first() {
        let mut s = ChromeSession::new(80, 24);
        let (t2, _) = s.new_tab(80, 24);
        let (t3, _) = s.new_tab(80, 24);
        s.select_tab(t3);
        let _ = s.close_tab(t3);
        assert_eq!(s.active_id, t2);
        assert_ne!(s.active_id, 1);
    }

    #[test]
    fn close_tab_stays_on_same_surface() {
        let mut s = ChromeSession::new(80, 24);
        let (a2, _) = s.new_tab(80, 24);
        let (b1, _) = s.new_tab_on_surface(80, 24, 1);
        let (b2, _) = s.new_tab_on_surface(80, 24, 1);
        s.select_tab(b1);
        let _ = s.close_tab(b1);
        assert_eq!(s.active_id, b2);
        assert_eq!(s.surface_of_tab(s.active_id), Some(1));
        let _ = a2;
    }

    #[test]
    fn type_and_backspace() {
        let mut s = ChromeSession::default();
        s.type_char('h');
        s.type_char('i');
        assert_eq!(s.draft(), "hi");
        s.backspace();
        assert_eq!(s.draft(), "h");
    }

    #[test]
    fn rename_tab_sets_strip_title() {
        let mut s = ChromeSession::new(80, 24);
        assert_eq!(s.rename_seed(true), "tab 1");
        s.rename_active_tab("work");
        assert_eq!(s.active_tab().unwrap().title, "work");
        // Empty restores tab N — never the pane title.
        s.rename_active_tab("  ");
        assert_eq!(s.active_tab().unwrap().title, "tab 1");
    }

    #[test]
    fn rename_pane_does_not_change_strip() {
        let mut s = ChromeSession::new(80, 24);
        s.rename_focused_pane("nvim");
        assert_eq!(s.active_pane().unwrap().title, "nvim");
        assert_eq!(s.active_tab().unwrap().title, "tab 1");
        s.set_pane_title(1, "from-osc".into());
        assert_eq!(s.panes.get(&1).unwrap().title, "from-osc");
        assert_eq!(s.active_tab().unwrap().title, "tab 1");
    }

    #[test]
    fn rename_pane_multi_leaves_strip() {
        let mut s = ChromeSession::new(80, 24);
        s.rename_active_tab("sticky");
        let _ = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        assert_eq!(s.active_leaf_count(), 2);
        // Focus is on the new pane; rename it
        s.rename_focused_pane("right");
        assert_eq!(s.active_pane().unwrap().title, "right");
        // Multi-pane strip stays put
        assert_eq!(s.active_tab().unwrap().title, "sticky");
        // OSC pane title still works independently
        let focus = s.focus_pane_id();
        s.set_pane_title(focus, "osc-right".into());
        assert_eq!(s.panes.get(&focus).unwrap().title, "osc-right");
        assert_eq!(s.active_tab().unwrap().title, "sticky");
    }

    #[test]
    fn rename_pane_empty_clears_to_shell_n() {
        let mut s = ChromeSession::new(80, 24);
        s.rename_focused_pane("tmp");
        s.rename_focused_pane("");
        assert_eq!(s.active_pane().unwrap().title, "shell 1");
        assert_eq!(s.active_tab().unwrap().title, "tab 1");
    }

    #[test]
    fn reparent_same_tab_docks_left() {
        let mut s = ChromeSession::new(80, 24);
        let right = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        let left = 1;
        assert!(s.reparent_pane(right, left, DockEdge::Left));
        let ids = s.active_tab().unwrap().root.leaf_ids();
        assert_eq!(ids.len(), 2);
        assert!(s.panes.contains_key(&right));
        assert_eq!(s.focus_pane_id(), right);
    }

    #[test]
    fn reparent_to_other_tab_closes_empty_source() {
        let mut s = ChromeSession::new(80, 24);
        let (_tid, pid) = s.new_tab(80, 24);
        assert_eq!(s.tabs.len(), 2);
        // Move the new tab's sole pane onto the first tab.
        assert!(s.reparent_pane(pid, 1, DockEdge::Right));
        assert_eq!(s.tabs.len(), 1);
        assert!(s.panes.contains_key(&pid));
        assert_eq!(s.active_tab().unwrap().root.leaf_ids().len(), 2);
    }

    #[test]
    fn reorder_tabs_on_surface() {
        let mut s = ChromeSession::new(80, 24);
        let (t2, _) = s.new_tab(80, 24);
        let (t3, _) = s.new_tab(80, 24);
        let _ = t2;
        assert_eq!(s.tabs_on_surface(0).len(), 3);
        assert!(s.reorder_tab_on_surface(0, 0, 2));
        assert_eq!(s.tabs[2].id, 1);
        assert_eq!(s.tabs[0].id, t2);
        assert_eq!(s.tabs[1].id, t3);
    }

    #[test]
    fn place_tab_cross_surface_inserts() {
        let mut s = ChromeSession::new(80, 24);
        let (t2, _) = s.new_tab(80, 24);
        let (t3, _) = s.new_tab(80, 24);
        assert!(s.place_tab_on_surface(t2, 1, 0));
        assert!(s.place_tab_on_surface(t3, 1, 1));
        assert_eq!(s.tabs_on_surface(0), vec![1]);
        assert_eq!(s.tabs_on_surface(1), vec![t2, t3]);
        assert!(s.place_tab_on_surface(1, 1, 1));
        assert!(s.tabs_on_surface(0).is_empty());
        assert_eq!(s.tabs_on_surface(1), vec![t2, 1, t3]);
    }

    #[test]
    fn extract_pane_makes_new_tab() {
        let mut s = ChromeSession::new(80, 24);
        let right = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        let tid = s.extract_pane_to_new_tab(right, 1).unwrap();
        assert_eq!(s.tabs.len(), 2);
        assert_eq!(s.surface_of_tab(tid), Some(1));
        assert_eq!(s.tabs_on_surface(0)[0], 1);
        assert_eq!(s.active_tab().unwrap().root.leaf_ids(), vec![right]);
        assert!(s.panes.contains_key(&right));
        assert!(s.panes.contains_key(&1));
    }

    #[test]
    fn close_tab_handoff_pulls_left_neighbor() {
        let mut s = ChromeSession::new(80, 24);
        let (t2, _) = s.new_tab(80, 24);
        let (t3, _) = s.new_tab(80, 24);
        let _ = t3;
        s.select_tab(t2);
        assert!(s.begin_close_tab(t2));
        assert_eq!(s.tabs.len(), 3, "tab stays until the strip anim finishes");
        assert_eq!(s.active_id, 1, "handoff is the immediate left tab, not the first leftover");
        let exit = s.tabs.iter().find(|t| t.id == t2).unwrap().exit.as_ref().unwrap();
        assert!(exit.slide_content);
        assert!(exit.dir < 0.0);
        let mut dt = 0.0;
        while dt < 1.0 {
            let r = s.tick_splits(1.0 / 60.0);
            dt += 1.0 / 60.0;
            if !r.moving && s.tabs.iter().all(|t| t.exit.is_none()) {
                break;
            }
        }
        assert_eq!(s.tabs.len(), 2);
        assert!(s.tabs.iter().all(|t| t.id != t2));
    }

    #[test]
    fn last_tab_on_surface_fades_window() {
        let mut s = ChromeSession::new(80, 24);
        let _ = s.new_tab(80, 24); // second tab on surface 0
        let (t2, pid2) = s.new_tab_on_surface(80, 24, 1);
        assert!(s.is_last_tab_on_surface(t2));
        assert!(!s.is_last_tab_on_surface(1));
        assert!(s.begin_close_pane(pid2));
        let anim = s
            .tabs
            .iter()
            .find(|t| t.id == t2)
            .and_then(|t| t.solo_exit.as_ref())
            .expect("solo exit");
        assert!(anim.fade_window);
        assert!(anim.opacity() > 0.9);
    }

    #[test]
    fn close_right_stack_pane_focuses_remaining_right() {
        let mut s = ChromeSession::new(80, 24);
        let right_top = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        let right_bottom = s.split_focused(SplitAxis::Horizontal, 40, 12).unwrap();
        assert_eq!(s.focus_pane_id(), right_bottom);
        assert!(s.begin_close_pane(right_bottom));
        assert_eq!(s.focus_pane_id(), right_top);
        assert_ne!(s.focus_pane_id(), 1);
    }

    #[test]
    fn close_left_stack_pane_focuses_remaining_left() {
        let mut s = ChromeSession::new(80, 24);
        let right = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        assert!(s.set_focus_pane(1));
        let left_bottom = s.split_focused(SplitAxis::Horizontal, 40, 12).unwrap();
        assert_eq!(s.focus_pane_id(), left_bottom);
        assert!(s.begin_close_pane(left_bottom));
        assert_eq!(s.focus_pane_id(), 1);
        assert_ne!(s.focus_pane_id(), right);
    }

    #[test]
    fn rapid_close_right_stack_does_not_stick() {
        let mut s = ChromeSession::new(80, 24);
        let right_top = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        let right_bottom = s.split_focused(SplitAxis::Horizontal, 40, 12).unwrap();
        assert!(s.begin_close_pane(right_bottom));
        assert!(s.panes.get(&right_bottom).unwrap().exiting);
        assert!(s.begin_close_pane(right_top));
        assert!(
            !s.panes.contains_key(&right_bottom),
            "first close should snap-finish so the second can start"
        );
        assert!(s.panes.get(&right_top).unwrap().exiting);
        assert!(s.begin_close_pane(1), "remaining pane must still close");
    }

    #[test]
    fn rapid_close_left_stack_does_not_stick() {
        let mut s = ChromeSession::new(80, 24);
        let right = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        assert!(s.set_focus_pane(1));
        let left_bottom = s.split_focused(SplitAxis::Horizontal, 40, 12).unwrap();
        assert!(s.begin_close_pane(left_bottom));
        assert!(s.begin_close_pane(1));
        assert!(
            !s.panes.contains_key(&left_bottom),
            "first close should snap-finish so the second can start"
        );
        assert!(s.panes.get(&1).unwrap().exiting);
        assert!(s.begin_close_pane(right), "remaining pane must still close");
    }

    #[test]
    fn stuck_exiting_flag_recovers() {
        let mut s = ChromeSession::new(80, 24);
        let right = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        s.panes.get_mut(&right).unwrap().exiting = true;
        assert!(
            !s.active_tab().unwrap().root.is_closing(right),
            "precondition: tree is not closing the pane"
        );
        assert!(s.begin_close_pane(right));
        assert!(s.active_tab().unwrap().root.is_closing(right));
    }

    #[test]
    fn sole_pane_on_shared_window_uses_strip_exit() {
        let mut s = ChromeSession::new(80, 24);
        let (t2, _) = s.new_tab(80, 24);
        s.select_tab(1);
        assert!(s.begin_close_pane(1));
        assert!(s.tabs[0].solo_exit.is_none());
        let exit = s.tabs[0].exit.as_ref().expect("strip exit");
        assert!(exit.slide_content);
        assert_eq!(exit.next_id, t2);
    }

    #[test]
    fn extract_sole_pane_moves_tab() {
        let mut s = ChromeSession::new(80, 24);
        let tid = s.extract_pane_to_new_tab(1, 3).unwrap();
        assert_eq!(tid, 1);
        assert_eq!(s.tabs.len(), 1);
        assert_eq!(s.surface_of_tab(1), Some(3));
    }

    #[test]
    fn set_sash_ratio_on_active_split() {
        let mut s = ChromeSession::new(80, 24);
        let _ = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        assert!(s.set_sash_ratio(1, 0.3));
        match &s.active_tab().unwrap().root {
            crate::panes::SplitNode::Branch { ratio, .. } => {
                assert!((*ratio - 0.3).abs() < 1e-4);
            }
            other => panic!("{other:?}"),
        }
    }

    #[test]
    fn reparent_refuses_exiting_target() {
        let mut s = ChromeSession::new(80, 24);
        let right = s.split_focused(SplitAxis::Vertical, 40, 24).unwrap();
        s.panes.get_mut(&1).unwrap().exiting = true;
        assert!(!s.reparent_pane(right, 1, DockEdge::Left));
        assert_eq!(s.active_tab().unwrap().root.leaf_ids().len(), 2);
    }

    #[test]
    fn reparent_refuses_last_pane_of_last_tab() {
        let mut s = ChromeSession::new(80, 24);
        assert!(!s.reparent_pane(1, 1, DockEdge::Right));
        let (_tid, pid) = s.new_tab(80, 24);
        // Can't dock a pane onto itself when it's the sole leaf.
        assert!(!s.move_pane_to_tab(pid, s.active_id));
    }

    #[test]
    fn split_widget_docks_workspace_once() {
        let mut s = ChromeSession::new(80, 24);
        let id = s
            .split_focused_widget(SplitAxis::Vertical, WidgetKind::Workspace)
            .unwrap();
        assert!(s.pane_kind(id).is_workspace());
        assert_eq!(s.panes.get(&id).unwrap().title, "workspace");
        assert_eq!(s.active_tab().unwrap().root.leaf_ids().len(), 2);
        assert_eq!(s.focus_pane_id(), id);
        // Second open focuses the existing leaf — no duplicate widget.
        let again = s
            .split_focused_widget(SplitAxis::Horizontal, WidgetKind::Workspace)
            .unwrap();
        assert_eq!(again, id);
        assert_eq!(
            s.panes
                .values()
                .filter(|p| p.kind.is_workspace())
                .count(),
            1
        );
    }

    #[test]
    fn find_widget_skips_exiting() {
        let mut s = ChromeSession::new(80, 24);
        let id = s
            .split_focused_widget(SplitAxis::Vertical, WidgetKind::Workspace)
            .unwrap();
        assert_eq!(s.find_widget(WidgetKind::Workspace), Some(id));
        s.panes.get_mut(&id).unwrap().exiting = true;
        assert_eq!(s.find_widget(WidgetKind::Workspace), None);
    }

    #[test]
    fn split_guest_is_not_singleton() {
        let mut s = ChromeSession::new(80, 24);
        let a = s
            .split_focused_guest(SplitAxis::Vertical, "example", "Example")
            .unwrap();
        let b = s
            .split_focused_guest(SplitAxis::Horizontal, "example", "Example")
            .unwrap();
        assert_ne!(a, b);
        assert!(s.pane_kind(a).is_guest());
        assert!(s.pane_kind(b).is_guest());
        assert_eq!(s.panes.get(&a).unwrap().guest_id.as_deref(), Some("example"));
        assert_eq!(
            s.panes
                .values()
                .filter(|p| p.kind.is_guest())
                .count(),
            2
        );
    }

    #[test]
    fn guest_tab_has_no_terminal() {
        let mut s = ChromeSession::new(80, 24);
        let id = s.new_guest_tab("example", "Example");
        assert!(s.pane_kind(id).is_guest());
        assert!(s.focused_is_guest());
        let tab = s.active_tab().unwrap();
        assert_eq!(tab.root.leaf_ids(), vec![id]);
    }

    #[test]
    fn guest_tab_without_name_is_a_normal_tab() {
        let mut s = ChromeSession::new(80, 24);
        let id = s.new_guest_tab("example", "");
        assert_eq!(s.panes.get(&id).unwrap().title, "");
        assert_eq!(s.active_tab().unwrap().title, "tab 2");
    }
}
