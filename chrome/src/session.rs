//! Chrome session state: tabs (each with its own cell grid), warp-bar draft.
//!
//! **PTY ownership:** Do **not** store `PtySession` (or ANSI decoder state) inside
//! [`Tab`] / [`ChromeSession`]. `PtySession` is not `Clone` and is heavy (FDs,
//! child process). The app owns a `HashMap<u64, PtySession>` (or parallel vec)
//! keyed by tab id — one PTY per tab when live — and feeds bytes into that tab's
//! grid via `active_grid_mut()` / per-tab grid lookup.

use crate::cells::{theme, CellGrid};
use crate::shell::{self, ShellOutput};

/// One chrome tab: label chip + independent terminal grid.
#[derive(Clone, Debug)]
pub struct Tab {
    pub id: u64,
    pub title: String,
    pub busy: bool,
    pub grid: CellGrid,
    /// True if this tab started with live PTY (vs mock boot banner).
    pub pty_mode: bool,
}

/// Application-facing chrome + per-tab grids. PTY map lives in the app, not here.
#[derive(Clone, Debug)]
pub struct ChromeSession {
    pub tabs: Vec<Tab>,
    pub active_id: u64,
    /// Shared warp-bar command draft (not yet submitted).
    pub draft: String,
    next_tab_id: u64,
}

impl ChromeSession {
    pub fn new(cols: u16, rows: u16) -> Self {
        let tab = Tab {
            id: 1,
            title: "shell 1".into(),
            busy: false,
            grid: CellGrid::new(cols, rows),
            pty_mode: false,
        };
        Self {
            tabs: vec![tab],
            active_id: 1,
            draft: String::new(),
            next_tab_id: 2,
        }
    }

    /// Immutable view of the active tab's grid.
    pub fn active_grid(&self) -> &CellGrid {
        self.active_tab()
            .map(|t| &t.grid)
            .expect("session always has at least one tab")
    }

    /// Mutable view of the active tab's grid.
    pub fn active_grid_mut(&mut self) -> &mut CellGrid {
        let id = self.active_id;
        self.tabs
            .iter_mut()
            .find(|t| t.id == id)
            .map(|t| &mut t.grid)
            .expect("session always has at least one tab")
    }

    pub fn active_tab(&self) -> Option<&Tab> {
        self.tabs.iter().find(|t| t.id == self.active_id)
    }

    pub fn active_tab_mut(&mut self) -> Option<&mut Tab> {
        let id = self.active_id;
        self.tabs.iter_mut().find(|t| t.id == id)
    }

    /// Look up a tab's grid by id.
    pub fn grid_mut(&mut self, id: u64) -> Option<&mut CellGrid> {
        self.tabs.iter_mut().find(|t| t.id == id).map(|t| &mut t.grid)
    }

    pub fn grid(&self, id: u64) -> Option<&CellGrid> {
        self.tabs.iter().find(|t| t.id == id).map(|t| &t.grid)
    }

    /// Create a new tab with an empty grid sized `cols`×`rows`. Selects it.
    pub fn new_tab(&mut self, cols: u16, rows: u16) -> u64 {
        let id = self.next_tab_id;
        self.next_tab_id = self.next_tab_id.saturating_add(1);
        self.tabs.push(Tab {
            id,
            title: format!("shell {id}"),
            busy: false,
            grid: CellGrid::new(cols, rows),
            pty_mode: false,
        });
        self.active_id = id;
        id
    }

    /// Close a tab. Refuses to close the last remaining tab.
    /// Returns whether a tab was removed. Caller should drop any PTY for `id`.
    pub fn close_tab(&mut self, id: u64) -> bool {
        if self.tabs.len() <= 1 {
            return false;
        }
        let Some(pos) = self.tabs.iter().position(|t| t.id == id) else {
            return false;
        };
        self.tabs.remove(pos);
        if self.active_id == id {
            // Prefer neighbor at the same index, else previous.
            let next = self.tabs.get(pos).or_else(|| self.tabs.get(pos.saturating_sub(1)));
            self.active_id = next.map(|t| t.id).unwrap_or(self.tabs[0].id);
        }
        true
    }

    /// Select a tab by id. No-op if unknown.
    pub fn select_tab(&mut self, id: u64) -> bool {
        if self.tabs.iter().any(|t| t.id == id) {
            self.active_id = id;
            true
        } else {
            false
        }
    }

    /// Resize the active tab's grid only.
    pub fn resize_active(&mut self, cols: u16, rows: u16) {
        self.active_grid_mut().resize(cols, rows);
    }

    /// Resize every tab's grid (e.g. window resize — all share the terminal rect).
    pub fn resize_all(&mut self, cols: u16, rows: u16) {
        for tab in &mut self.tabs {
            tab.grid.resize(cols, rows);
        }
    }

    /// Mark the active tab as live-PTY (no mock banner).
    pub fn mark_active_pty(&mut self) {
        if let Some(tab) = self.active_tab_mut() {
            tab.pty_mode = true;
            tab.grid.clear();
        }
    }

    /// Write mock banner + prompt into the **active** grid (mock shell path).
    pub fn boot_mock_on_active(&mut self) {
        if let Some(tab) = self.active_tab_mut() {
            tab.pty_mode = false;
        }
        self.write_banner_and_prompt();
    }

    /// Append a printable character to the warp-bar draft.
    pub fn type_char(&mut self, c: char) {
        if c.is_control() {
            return;
        }
        self.draft.push(c);
    }

    /// Delete the last draft character.
    pub fn backspace(&mut self) {
        self.draft.pop();
    }

    /// Submit the draft via mock shell into the **active** grid.
    /// Empty draft (after trim-end) is ignored.
    pub fn submit_draft_mock(&mut self) {
        let line = self.draft.trim_end().to_string();
        if line.is_empty() {
            return;
        }

        // Echo the submitted line on the current prompt row (cursor already after ❯ ).
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

        self.draft.clear();
    }

    fn write_banner_and_prompt(&mut self) {
        let lines = shell::banner_lines();
        for (i, line) in lines.iter().enumerate() {
            // First line jade title; dim for rule + hint; empty lines plain.
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
        // host line (dim), then glyph (jade) without trailing newline so typed echo continues.
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

impl Default for ChromeSession {
    fn default() -> Self {
        Self::new(80, 24)
    }
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
        assert!(snap.iter().any(|l| l.contains("stephen@inkstone")));
    }

    #[test]
    fn submit_help() {
        let mut s = ChromeSession::new(80, 30);
        s.boot_mock_on_active();
        s.draft = "help".into();
        s.submit_draft_mock();
        assert!(s.draft.is_empty());
        let snap = s.active_grid().snapshot_strings().join("\n");
        assert!(snap.contains("mock commands"));
    }

    #[test]
    fn clear_resets_grid() {
        let mut s = ChromeSession::new(80, 24);
        s.boot_mock_on_active();
        s.draft = "clear".into();
        s.submit_draft_mock();
        let snap = s.active_grid().snapshot_strings();
        // Banner gone; prompt remains.
        assert!(!snap.iter().any(|l| l.contains("suzuri surface")));
        assert!(snap.iter().any(|l| l.contains("stephen@inkstone")));
    }

    #[test]
    fn tabs_new_close_select() {
        let mut s = ChromeSession::default();
        let id2 = s.new_tab(80, 24);
        assert_eq!(s.active_id, id2);
        assert_eq!(s.tabs.len(), 2);
        assert_eq!(s.active_grid().cols(), 80);
        s.select_tab(1);
        assert_eq!(s.active_id, 1);
        assert!(s.close_tab(id2));
        assert_eq!(s.tabs.len(), 1);
        assert!(!s.close_tab(1)); // last tab
    }

    #[test]
    fn each_tab_has_own_grid() {
        let mut s = ChromeSession::new(40, 10);
        s.boot_mock_on_active();
        let id1 = s.active_id;
        let id2 = s.new_tab(40, 10);
        s.boot_mock_on_active();
        s.draft = "help".into();
        s.submit_draft_mock();
        let snap2 = s.active_grid().snapshot_strings().join("\n");
        assert!(snap2.contains("mock commands"));

        s.select_tab(id1);
        let snap1 = s.active_grid().snapshot_strings().join("\n");
        assert!(snap1.contains("suzuri surface"));
        assert!(!snap1.contains("mock commands"));
        assert_eq!(s.tabs.iter().find(|t| t.id == id2).unwrap().id, id2);
    }

    #[test]
    fn resize_active_and_all() {
        let mut s = ChromeSession::new(80, 24);
        let _id2 = s.new_tab(80, 24);
        s.resize_active(100, 30);
        assert_eq!(s.active_grid().cols(), 100);
        assert_eq!(s.active_grid().rows(), 30);
        s.select_tab(1);
        assert_eq!(s.active_grid().cols(), 80);
        s.resize_all(90, 28);
        for tab in &s.tabs {
            assert_eq!(tab.grid.cols(), 90);
            assert_eq!(tab.grid.rows(), 28);
        }
    }

    #[test]
    fn type_and_backspace() {
        let mut s = ChromeSession::default();
        s.type_char('h');
        s.type_char('i');
        s.type_char('\n'); // ignored
        assert_eq!(s.draft, "hi");
        s.backspace();
        assert_eq!(s.draft, "h");
    }

    #[test]
    fn mark_active_pty_clears_grid() {
        let mut s = ChromeSession::new(80, 24);
        s.boot_mock_on_active();
        s.mark_active_pty();
        assert!(s.active_tab().unwrap().pty_mode);
        let snap = s.active_grid().snapshot_strings();
        assert!(!snap.iter().any(|l| l.contains("suzuri surface")));
    }
}
