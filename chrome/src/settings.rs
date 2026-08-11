//! Minimal settings overlay state for suzuri-chrome.
//!
//! Pure data + display snapshot. No wgpu/winit. Parent owns open/close
//! wiring and feeds `display_lines` into the text layer.

/// Whether the settings overlay is open, plus optional cached display lines.
#[derive(Clone, Debug, Default)]
pub struct SettingsState {
    pub open: bool,
    /// Last (or parent-filled) display lines for the overlay.
    pub lines: Vec<String>,
}

impl SettingsState {
    pub fn new() -> Self {
        Self {
            open: false,
            lines: Vec::new(),
        }
    }

    pub fn open(&mut self) {
        self.open = true;
    }

    pub fn close(&mut self) {
        self.open = false;
    }

    pub fn toggle(&mut self) {
        self.open = !self.open;
    }

    /// Snapshot of status for UI (renderer will draw these as text labels).
    ///
    /// `pty_active`: real PTY connected vs mock-shell fallback.
    /// `cols` / `rows`: cell grid size.
    /// `tab_count`: number of chrome tabs.
    pub fn display_lines(
        &self,
        pty_active: bool,
        cols: u16,
        rows: u16,
        tab_count: usize,
    ) -> Vec<String> {
        let shell = if pty_active {
            "PTY: active (live shell)"
        } else {
            "PTY: mock fallback (no live shell yet)"
        };

        vec![
            "suzuri-chrome".into(),
            "native GPU shell · winit + wgpu · no React / HTML / Chromium".into(),
            String::new(),
            "architecture".into(),
            "  chrome  — smooth UI (tabs, title, warp, glass, rain)".into(),
            "  cell pane — shell / TUI only (snaps to character cells)".into(),
            "  rule: anything that isn't shell output never snaps to a cell".into(),
            String::new(),
            "status".into(),
            format!("  {shell}"),
            format!("  grid  {cols}×{rows}"),
            format!("  tabs  {tab_count}"),
            String::new(),
            "keys".into(),
            "  Esc     close settings".into(),
            "  ⌘/,     toggle settings (Ctrl+, on non-mac)".into(),
        ]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn new_starts_closed() {
        let s = SettingsState::new();
        assert!(!s.open);
        assert!(s.lines.is_empty());
    }

    #[test]
    fn open_close_toggle() {
        let mut s = SettingsState::new();
        s.open();
        assert!(s.open);
        s.close();
        assert!(!s.open);
        s.toggle();
        assert!(s.open);
        s.toggle();
        assert!(!s.open);
    }

    #[test]
    fn display_lines_mentions_core_facts() {
        let s = SettingsState::new();
        let mock = s.display_lines(false, 80, 24, 2).join("\n");
        assert!(mock.contains("suzuri-chrome"));
        assert!(mock.contains("cell pane"));
        assert!(mock.contains("mock fallback"));
        assert!(mock.contains("80×24"));
        assert!(mock.contains("tabs  2"));
        assert!(mock.contains("Esc"));
        assert!(mock.contains("⌘/,"));

        let live = s.display_lines(true, 120, 40, 1).join("\n");
        assert!(live.contains("PTY: active"));
        assert!(live.contains("120×40"));
    }
}
