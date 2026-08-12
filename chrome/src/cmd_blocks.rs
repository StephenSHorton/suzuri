//! Host-injected Warp-style command blocks + recent-command ring for MCP.
//!
//! Product: `scrollback.pushBlock` + `recentBlocks` / history kinds
//! (`histBlockRule` / `histBlockCmd`).

use std::collections::VecDeque;

use crate::cells::{theme, CellGrid};
use crate::shell::PROMPT_GLYPH;

/// Max recent command strings retained for MCP `blocks[]`.
pub const RECENT_BLOCKS: usize = 12;

/// Max tagged history lines retained for MCP `history_tail`.
pub const HIST_META_CAP: usize = 40;

/// Per-pane command-block log (side channel for diag; also paints scrollback).
#[derive(Clone, Debug, Default)]
pub struct CmdBlockLog {
    /// Newest last — recent host-injected commands.
    recent: VecDeque<String>,
    /// Tagged history for MCP (text, kind: normal|rule|cmd).
    hist_meta: VecDeque<(String, String)>,
}

impl CmdBlockLog {
    pub fn new() -> Self {
        Self::default()
    }

    /// Record a command block and inject rule + prompt line into `grid` scrollback.
    pub fn push_block(&mut self, cmd: &str, grid: &mut CellGrid, cwd_display: &str) {
        let cmd = cmd.trim_end();
        if cmd.trim().is_empty() {
            return;
        }
        let cols = grid.cols().max(12) as usize;

        self.recent.push_back(cmd.to_string());
        while self.recent.len() > RECENT_BLOCKS {
            self.recent.pop_front();
        }

        // Blank spacer
        self.push_meta("", "normal");
        grid.push_scrollback_text("", None);

        // Rule
        let rule: String = "─".repeat(cols);
        self.push_meta(&rule, "rule");
        grid.push_scrollback_text(&rule, Some(theme::DIM));

        let prompt = PROMPT_GLYPH.trim_end(); // "❯"
        let path = cwd_display.trim();
        for (i, p) in cmd.split('\n').enumerate() {
            let line = if i == 0 {
                if path.is_empty() {
                    format!("{prompt} {p}")
                } else {
                    let mut s = format!("{path} {prompt} {p}");
                    if s.chars().count() > cols {
                        s = s.chars().take(cols).collect();
                    }
                    s
                }
            } else {
                let indent = " ".repeat(prompt.chars().count().saturating_add(1));
                format!("{indent}{p}")
            };
            self.push_meta(&line, "cmd");
            grid.push_scrollback_text(&line, Some(theme::JADE));
        }
    }

    fn push_meta(&mut self, text: &str, kind: &str) {
        self.hist_meta
            .push_back((text.to_string(), kind.to_string()));
        while self.hist_meta.len() > HIST_META_CAP {
            self.hist_meta.pop_front();
        }
    }

    /// Newest-last command strings (for MCP `blocks[]`).
    pub fn recent_blocks(&self) -> Vec<String> {
        self.recent.iter().cloned().collect()
    }

    /// Tagged history for MCP (oldest first).
    pub fn history_meta(&self) -> Vec<(String, String)> {
        self.hist_meta.iter().cloned().collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cells::CellGrid;

    #[test]
    fn push_block_records_recent_and_meta() {
        let mut log = CmdBlockLog::new();
        let mut grid = CellGrid::new(40, 8);
        let before = grid.scrollback_len();
        log.push_block("echo hi", &mut grid, "~/proj");
        assert_eq!(log.recent_blocks(), vec!["echo hi".to_string()]);
        let meta = log.history_meta();
        assert!(meta.iter().any(|(_, k)| k == "rule"));
        assert!(meta.iter().any(|(t, k)| k == "cmd" && t.contains("echo hi")));
        assert!(grid.scrollback_len() > before);
    }

    #[test]
    fn empty_cmd_noop() {
        let mut log = CmdBlockLog::new();
        let mut grid = CellGrid::new(20, 4);
        log.push_block("   ", &mut grid, "");
        assert!(log.recent_blocks().is_empty());
    }

    #[test]
    fn recent_caps_at_limit() {
        let mut log = CmdBlockLog::new();
        let mut grid = CellGrid::new(20, 4);
        for i in 0..20 {
            log.push_block(&format!("cmd{i}"), &mut grid, "");
        }
        assert_eq!(log.recent_blocks().len(), RECENT_BLOCKS);
        assert!(log.recent_blocks().last().unwrap().ends_with("19"));
    }
}
