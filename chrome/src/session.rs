//! Chrome session: tabs (pages) each with a split-pane tree of leaves.
//!
//! PTY ownership stays in the app (`HashMap<pane_id, PtySession>`).

use std::collections::HashMap;

use crate::cells::{theme, CellGrid};
use crate::panes::{FocusDir, RemoveResult, SplitAxis, SplitNode};
use crate::shell::{self, ShellOutput};

/// One terminal leaf (grid + cwd). PTY lives in the app, keyed by `id`.
#[derive(Clone, Debug)]
pub struct Pane {
    pub id: u64,
    pub title: String,
    pub busy: bool,
    pub grid: CellGrid,
    pub pty_mode: bool,
    pub cwd: String,
    /// Local command draft for this pane's input strip.
    pub draft: String,
}

/// One chrome-strip tab that may hold a split tree of panes.
#[derive(Clone, Debug)]
pub struct Tab {
    pub id: u64,
    pub title: String,
    pub root: SplitNode,
    pub focus_pane: u64,
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
}

impl ChromeSession {
    pub fn new(cols: u16, rows: u16) -> Self {
        let pane_id = 1;
        let mut panes = HashMap::new();
        panes.insert(
            pane_id,
            Pane {
                id: pane_id,
                title: "shell 1".into(),
                busy: false,
                grid: CellGrid::new(cols, rows),
                pty_mode: false,
                cwd: initial_cwd(),
                draft: String::new(),
            },
        );
        let tab = Tab {
            id: 1,
            title: "shell 1".into(),
            root: SplitNode::leaf(pane_id),
            focus_pane: pane_id,
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

    pub fn tick_splits(&mut self, dt: f32) -> bool {
        let mut moving = false;
        for tab in &mut self.tabs {
            moving |= tab.root.tick(dt);
        }
        moving
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
            Pane {
                id: pane_id,
                title: format!("shell {pane_id}"),
                busy: false,
                grid: CellGrid::new(cols, rows),
                pty_mode: false,
                cwd,
                draft: String::new(),
            },
        );
        self.tabs.push(Tab {
            id: tab_id,
            title: format!("shell {pane_id}"),
            root: SplitNode::leaf(pane_id),
            focus_pane: pane_id,
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
        let tab = self.tabs.remove(pos);
        let pane_ids = tab.root.leaf_ids();
        for pid in &pane_ids {
            self.panes.remove(pid);
        }
        if self.active_id == id {
            let next = self
                .tabs
                .get(pos)
                .or_else(|| self.tabs.get(pos.saturating_sub(1)));
            self.active_id = next.map(|t| t.id).unwrap_or(self.tabs[0].id);
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

    pub fn next_tab(&mut self) {
        if self.tabs.is_empty() {
            return;
        }
        let pos = self
            .tabs
            .iter()
            .position(|t| t.id == self.active_id)
            .unwrap_or(0);
        let next = (pos + 1) % self.tabs.len();
        self.active_id = self.tabs[next].id;
    }

    pub fn prev_tab(&mut self) {
        if self.tabs.is_empty() {
            return;
        }
        let pos = self
            .tabs
            .iter()
            .position(|t| t.id == self.active_id)
            .unwrap_or(0);
        let next = if pos == 0 {
            self.tabs.len() - 1
        } else {
            pos - 1
        };
        self.active_id = self.tabs[next].id;
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
            Pane {
                id: new_id,
                title: format!("shell {new_id}"),
                busy: false,
                grid: CellGrid::new(cols, rows),
                pty_mode: false,
                cwd,
                draft: String::new(),
            },
        );
        Some(new_id)
    }

    /// Close focused pane. Returns removed pane ids (for PTY teardown).
    /// If last pane in tab, closes the tab (unless last tab).
    pub fn close_focused_pane_or_tab(&mut self) -> CloseOutcome {
        let tab_id = self.active_id;
        let focus = self.focus_pane_id();
        let leaf_count = self
            .active_tab()
            .map(|t| t.root.leaf_ids().len())
            .unwrap_or(1);

        if leaf_count <= 1 {
            let removed = self.close_tab(tab_id);
            if removed.is_empty() {
                CloseOutcome::QuitApp
            } else {
                CloseOutcome::ClosedPanes(removed)
            }
        } else {
            let tab = self.tabs.iter_mut().find(|t| t.id == tab_id).unwrap();
            match tab.root.remove_leaf(focus) {
                RemoveResult::Removed { focus_hint } => {
                    tab.focus_pane = focus_hint;
                    self.panes.remove(&focus);
                    CloseOutcome::ClosedPanes(vec![focus])
                }
                RemoveResult::RemovedEmpty => {
                    // shouldn't happen with leaf_count > 1
                    CloseOutcome::None
                }
                RemoveResult::NotFound => CloseOutcome::None,
            }
        }
    }

    pub fn set_focus_pane(&mut self, pane_id: u64) {
        if !self.panes.contains_key(&pane_id) {
            return;
        }
        if let Some(tab) = self
            .tabs
            .iter_mut()
            .find(|t| t.root.contains_pane(pane_id))
        {
            self.active_id = tab.id;
            tab.focus_pane = pane_id;
        }
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
    QuitApp,
    ClosedPanes(Vec<u64>),
}

impl Default for ChromeSession {
    fn default() -> Self {
        Self::new(80, 24)
    }
}

fn initial_cwd() -> String {
    std::env::current_dir()
        .map(|p| p.to_string_lossy().into_owned())
        .or_else(|_| std::env::var("HOME"))
        .unwrap_or_else(|_| "/".into())
}

pub fn display_path(cwd: &str) -> String {
    let cwd = cwd.trim();
    if cwd.is_empty() {
        return String::new();
    }
    let home = std::env::var("HOME").unwrap_or_default();
    if home.is_empty() {
        return cwd.to_string();
    }
    if cwd == home {
        return "~".into();
    }
    let prefix = format!("{home}/");
    if let Some(rest) = cwd.strip_prefix(&prefix) {
        return format!("~/{rest}");
    }
    cwd.to_string()
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
    fn tabs_new_close() {
        let mut s = ChromeSession::default();
        let (tid, _pid) = s.new_tab(80, 24);
        assert_eq!(s.tabs.len(), 2);
        let removed = s.close_tab(tid);
        assert_eq!(removed.len(), 1);
        assert_eq!(s.tabs.len(), 1);
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
}
