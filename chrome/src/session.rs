//! Chrome session: tabs (pages) each with a split-pane tree of leaves.
//!
//! PTY ownership stays in the app (`HashMap<pane_id, PtySession>`).

use std::collections::HashMap;

use crate::cells::{theme, CellGrid};
use crate::panes::{FocusDir, RemoveResult, SoloExitAnim, SplitAxis, SplitNode, TickResult};
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
    /// Shell exited / user closed — animating out; don't re-trigger.
    pub exiting: bool,
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
                exiting: false,
            },
        );
        let tab = Tab {
            id: 1,
            title: "shell 1".into(),
            root: SplitNode::leaf(pane_id),
            focus_pane: pane_id,
            solo_exit: None,
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

    /// Apply an OSC 0/2 window title to a pane only (not the chrome tab strip).
    ///
    /// Product rule: OSC renames panes; multi-pane tabs keep a sticky strip title.
    /// Empty titles are ignored.
    pub fn set_pane_title(&mut self, pane_id: u64, title: String) {
        let title = title.trim();
        if title.is_empty() {
            return;
        }
        if let Some(p) = self.panes.get_mut(&pane_id) {
            p.title = title.to_string();
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
        }

        // Finalize branch closes that finished jelly
        let finished = result.finished_closes.clone();
        for pid in finished {
            self.finalize_pane_close(pid, &mut result);
        }
        for (tab_id, pane_id) in solo_finished {
            result.finished_closes.push(pane_id);
            self.finalize_solo_close(tab_id, pane_id, &mut result);
        }
        result
    }

    /// Begin graceful exit of a pane (shell died or user closed).
    /// Returns true if an animation was started (or already in progress).
    pub fn begin_close_pane(&mut self, pane_id: u64) -> bool {
        let Some(pane) = self.panes.get_mut(&pane_id) else {
            return false;
        };
        if pane.exiting {
            return true;
        }
        pane.exiting = true;

        let Some(tab) = self
            .tabs
            .iter_mut()
            .find(|t| t.root.contains_pane(pane_id) || t.solo_exit.as_ref().map(|s| s.pane_id) == Some(pane_id))
        else {
            return false;
        };

        let leaf_count = tab.root.leaf_ids().len();
        if leaf_count <= 1 {
            // Sole pane — jelly-scale the whole well, then drop tab/pane.
            if tab.solo_exit.is_none() {
                tab.solo_exit = Some(SoloExitAnim::start(pane_id));
            }
            return true;
        }

        if tab.root.begin_close_leaf(pane_id) {
            // Focus a survivor if we're closing the focused pane.
            if tab.focus_pane == pane_id {
                if let Some(other) = tab.root.leaf_ids().into_iter().find(|id| *id != pane_id) {
                    tab.focus_pane = other;
                }
            }
            true
        } else {
            false
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
                tab.focus_pane = focus_hint;
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
            Pane {
                id: pane_id,
                title: format!("shell {pane_id}"),
                busy: false,
                grid: CellGrid::new(cols, rows),
                pty_mode: false,
                cwd,
                draft: String::new(),
                exiting: false,
            },
        );
        self.tabs.push(Tab {
            id: tab_id,
            title: format!("shell {pane_id}"),
            root: SplitNode::leaf(pane_id),
            focus_pane: pane_id,
            solo_exit: None,
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
                exiting: false,
            },
        );
        Some(new_id)
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

fn initial_cwd() -> String {
    std::env::current_dir()
        .map(|p| p.to_string_lossy().into_owned())
        .or_else(|_| std::env::var("HOME"))
        .unwrap_or_else(|_| "/".into())
}

/// Shorten `$HOME` → `~` for chrome path display (product `displayPath`).
///
/// Compares **cleaned** paths so trailing slashes / `.` / `..` still map to `~`.
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
        return cwd;
    }
    // Exact home (also try canonicalize when both exist on disk).
    if paths_equal(&cwd, &home) {
        return "~".into();
    }
    // ~/rest — only if cwd is under home as a path prefix.
    let home_prefix = if home == "/" {
        "/".to_string()
    } else {
        format!("{home}/")
    };
    if let Some(rest) = cwd.strip_prefix(&home_prefix) {
        if !rest.is_empty() {
            return format!("~/{rest}");
        }
        return "~".into();
    }
    cwd
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

    #[test]
    fn display_path_home_is_tilde() {
        let home = std::env::var("HOME").expect("HOME");
        assert_eq!(display_path(&home), "~");
        assert_eq!(display_path(&format!("{home}/")), "~");
        assert_eq!(display_path(&format!("{home}/.")), "~");
        assert_eq!(
            display_path(&format!("{home}/projects/foo")),
            "~/projects/foo"
        );
        // Unrelated absolute stays absolute
        assert_eq!(display_path("/tmp"), "/tmp");
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
