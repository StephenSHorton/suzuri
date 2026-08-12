//! Command palette + keyboard shortcuts registry (product suzuri subset).

use crate::input::is_mac;

/// Host action for palette / shortcuts.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CommandAction {
    OpenSettings,
    OpenHelp,
    OpenPalette,
    OpenNotes,
    OpenWorkspace,
    OpenTransferSend,
    OpenTransferReceive,
    NewTab,
    CloseTab,
    NextTab,
    PrevTab,
    SplitRight,
    SplitDown,
    ClosePane,
    FocusLeft,
    FocusRight,
    FocusUp,
    FocusDown,
    ToggleRain,
    ToggleLens,
    ToggleCaffeine,
    Caffeine15m,
    Caffeine1h,
    CaffeineOff,
    /// Open rename dialog for the chrome strip tab (page).
    RenameTab,
    /// Open rename dialog for the focused pane (F2).
    RenamePane,
    /// Spawn a second OS window (new process of this binary).
    NewWindow,
    /// Soft-reload workspace from disk if open (mailbox / MCP).
    RefreshWorkspace,
    /// Cycle local human presence (idle→working→waiting→blocked→away).
    CycleWorkspaceStatus,
    Quit,
}

#[derive(Clone, Debug)]
pub struct Command {
    pub id: &'static str,
    pub title: &'static str,
    pub desc: String,
    pub category: &'static str,
    pub action: CommandAction,
}

fn mod_key() -> &'static str {
    if is_mac() {
        "⌘"
    } else {
        "Ctrl"
    }
}

fn mod_shift() -> String {
    if is_mac() {
        "⇧⌘".into()
    } else {
        "Ctrl+Shift+".into()
    }
}

fn mod_alt() -> String {
    if is_mac() {
        "⌥⌘".into()
    } else {
        "Ctrl+Alt+".into()
    }
}

/// Full command list for palette + help sheet.
pub fn default_commands() -> Vec<Command> {
    let m = mod_key();
    let ms = mod_shift();
    let ma = mod_alt();
    vec![
        Command {
            id: "palette",
            title: "Command palette",
            desc: format!("{m}K · Navigate"),
            category: "Navigate",
            action: CommandAction::OpenPalette,
        },
        Command {
            id: "settings",
            title: "Settings",
            desc: format!("{m}, · Appearance"),
            category: "Appearance",
            action: CommandAction::OpenSettings,
        },
        Command {
            id: "help",
            title: "Keyboard shortcuts",
            desc: format!("{m}/ · Help"),
            category: "Help",
            action: CommandAction::OpenHelp,
        },
        Command {
            id: "notes",
            title: "Notes",
            desc: format!("{ms}M · Notes"),
            category: "Notes",
            action: CommandAction::OpenNotes,
        },
        Command {
            id: "workspace",
            title: "Workspace",
            desc: "Shared channels · humans + AIs".into(),
            category: "Workspace",
            action: CommandAction::OpenWorkspace,
        },
        Command {
            id: "workspace_cycle_status",
            title: "Cycle workspace status",
            desc: format!("{ms}A · idle→working→waiting·… · Workspace"),
            category: "Workspace",
            action: CommandAction::CycleWorkspaceStatus,
        },
        Command {
            id: "transfer_send",
            title: "Send file (ticket)…",
            desc: "P2P · Transfer".into(),
            category: "Transfer",
            action: CommandAction::OpenTransferSend,
        },
        Command {
            id: "transfer_receive",
            title: "Receive ticket…",
            desc: "P2P · Transfer".into(),
            category: "Transfer",
            action: CommandAction::OpenTransferReceive,
        },
        Command {
            id: "caffeine_toggle",
            title: "Toggle caffeine",
            desc: "☕ strip · prevent sleep · System".into(),
            category: "System",
            action: CommandAction::ToggleCaffeine,
        },
        Command {
            id: "caffeine_15",
            title: "Caffeine 15 minutes",
            desc: "Stay awake 15m · System".into(),
            category: "System",
            action: CommandAction::Caffeine15m,
        },
        Command {
            id: "caffeine_1h",
            title: "Caffeine 1 hour",
            desc: "Stay awake 1h · System".into(),
            category: "System",
            action: CommandAction::Caffeine1h,
        },
        Command {
            id: "caffeine_off",
            title: "Caffeine off",
            desc: "Allow sleep · System".into(),
            category: "System",
            action: CommandAction::CaffeineOff,
        },
        Command {
            id: "new_tab",
            title: "New tab",
            desc: format!("{m}T · Tabs"),
            category: "Tabs",
            action: CommandAction::NewTab,
        },
        Command {
            id: "close_tab",
            title: "Close tab / pane",
            desc: format!("{m}W · Tabs · Panes"),
            category: "Tabs",
            action: CommandAction::CloseTab,
        },
        Command {
            id: "rename_tab",
            title: "Rename tab",
            desc: "Palette · custom strip name · Tabs".into(),
            category: "Tabs",
            action: CommandAction::RenameTab,
        },
        Command {
            id: "next_tab",
            title: "Next tab",
            desc: format!("{ms}] · Tabs"),
            category: "Tabs",
            action: CommandAction::NextTab,
        },
        Command {
            id: "prev_tab",
            title: "Previous tab",
            desc: format!("{ms}[ · Tabs"),
            category: "Tabs",
            action: CommandAction::PrevTab,
        },
        Command {
            id: "split_right",
            title: "Split right",
            desc: format!("{ms}D · Panes · jelly open"),
            category: "Panes",
            action: CommandAction::SplitRight,
        },
        Command {
            id: "split_down",
            title: "Split down",
            desc: format!("{ms}E · Panes · jelly open"),
            category: "Panes",
            action: CommandAction::SplitDown,
        },
        Command {
            id: "rename_pane",
            title: "Rename pane",
            desc: "F2 · custom pane title · Panes".into(),
            category: "Panes",
            action: CommandAction::RenamePane,
        },
        Command {
            id: "close_pane",
            title: "Close pane",
            desc: format!("{m}W · Panes"),
            category: "Panes",
            action: CommandAction::ClosePane,
        },
        Command {
            id: "focus_left",
            title: "Focus pane left",
            desc: format!("{ma}← · Panes"),
            category: "Panes",
            action: CommandAction::FocusLeft,
        },
        Command {
            id: "focus_right",
            title: "Focus pane right",
            desc: format!("{ma}→ · Panes"),
            category: "Panes",
            action: CommandAction::FocusRight,
        },
        Command {
            id: "focus_up",
            title: "Focus pane up",
            desc: format!("{ma}↑ · Panes"),
            category: "Panes",
            action: CommandAction::FocusUp,
        },
        Command {
            id: "focus_down",
            title: "Focus pane down",
            desc: format!("{ma}↓ · Panes"),
            category: "Panes",
            action: CommandAction::FocusDown,
        },
        Command {
            id: "toggle_rain",
            title: "Toggle glyph rain",
            desc: "Settings 1 · Appearance".into(),
            category: "Appearance",
            action: CommandAction::ToggleRain,
        },
        Command {
            id: "toggle_lens",
            title: "Toggle magnifier",
            desc: "Pinch or ⌃/⌘+scroll · Settings 2".into(),
            category: "Appearance",
            action: CommandAction::ToggleLens,
        },
        Command {
            id: "new_window",
            title: "New window",
            desc: format!("{ms}N · Window"),
            category: "Window",
            action: CommandAction::NewWindow,
        },
        Command {
            id: "quit",
            title: "Quit",
            desc: "Esc (when idle) · Window".into(),
            category: "Window",
            action: CommandAction::Quit,
        },
    ]
}

/// Filter commands by query (substring on title / desc / category).
pub fn filter_commands(all: &[Command], query: &str) -> Vec<usize> {
    let q = query.trim().to_lowercase();
    if q.is_empty() {
        return (0..all.len()).collect();
    }
    all.iter()
        .enumerate()
        .filter(|(_, c)| {
            c.title.to_lowercase().contains(&q)
                || c.desc.to_lowercase().contains(&q)
                || c.category.to_lowercase().contains(&q)
                || c.id.contains(q.as_str())
        })
        .map(|(i, _)| i)
        .collect()
}

/// Palette presentation (same agility dialog springs as settings).
#[derive(Clone, Debug)]
pub struct PaletteState {
    pub open: bool,
    pub query: String,
    pub selected: usize,
    present: f32,
    present_vel: f32,
    overlay: f32,
}

impl Default for PaletteState {
    fn default() -> Self {
        Self::new()
    }
}

impl PaletteState {
    pub fn new() -> Self {
        Self {
            open: false,
            query: String::new(),
            selected: 0,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
        }
    }

    pub fn open_palette(&mut self) {
        self.open = true;
        self.query.clear();
        self.selected = 0;
    }

    pub fn close(&mut self) {
        self.open = false;
        self.query.clear();
        self.selected = 0;
    }

    pub fn toggle(&mut self) {
        if self.open {
            self.close();
        } else {
            self.open_palette();
        }
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01 || self.overlay > 0.01
    }

    pub fn present(&self) -> f32 {
        self.present.clamp(0.0, 1.0)
    }

    pub fn content_ease(&self) -> f32 {
        let t = self.present();
        t * t * (3.0 - 2.0 * t)
    }

    pub fn scrim_alpha(&self) -> f32 {
        let t = self.overlay.clamp(0.0, 1.0);
        let e = t * t * (3.0 - 2.0 * t);
        e * 0.50
    }

    /// Wide horizontal palette card (input-first, not a tall square).
    pub fn modal_rect(&self, window_w: f32, window_h: f32) -> crate::layout::Rect {
        let t = self.content_ease();
        let base_w = (window_w - 48.0).min(680.0).max(360.0);
        let base_h = (window_h - 120.0).min(280.0).max(180.0);
        let sx = 0.88 + 0.12 * t;
        let sy = 0.82 + 0.18 * t;
        let w = base_w * sx;
        let h = base_h * sy;
        let y_nudge = -20.0 * (1.0 - t);
        crate::layout::Rect::new(
            (window_w - w) * 0.5,
            (window_h - h) * 0.38 + y_nudge,
            w,
            h,
        )
    }

    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let target = if self.open { 1.0 } else { 0.0 };
        const K: f32 = 150.0;
        const C: f32 = 25.0;
        let force = -K * (self.present - target) - C * self.present_vel;
        self.present_vel += force * dt;
        self.present += self.present_vel * dt;
        if (self.present - target).abs() < 0.001 && self.present_vel.abs() < 0.01 {
            self.present = target;
            self.present_vel = 0.0;
        }
        const OVERLAY_DUR: f32 = 0.2;
        let step = dt / OVERLAY_DUR;
        if self.overlay < target {
            self.overlay = (self.overlay + step).min(target);
        } else if self.overlay > target {
            self.overlay = (self.overlay - step).max(target);
        }
    }

    pub fn move_sel(&mut self, delta: i32, filtered_len: usize) {
        if filtered_len == 0 {
            self.selected = 0;
            return;
        }
        let n = filtered_len as i32;
        let cur = self.selected as i32;
        self.selected = ((cur + delta).rem_euclid(n)) as usize;
    }
}

/// Help / shortcuts overlay (reuses palette-like presentation).
#[derive(Clone, Debug)]
pub struct HelpState {
    pub open: bool,
    present: f32,
    present_vel: f32,
    overlay: f32,
}

impl Default for HelpState {
    fn default() -> Self {
        Self::new()
    }
}

impl HelpState {
    pub fn new() -> Self {
        Self {
            open: false,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
        }
    }

    pub fn open_help(&mut self) {
        self.open = true;
    }

    pub fn close(&mut self) {
        self.open = false;
    }

    pub fn toggle(&mut self) {
        self.open = !self.open;
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01 || self.overlay > 0.01
    }

    pub fn content_ease(&self) -> f32 {
        let t = self.present.clamp(0.0, 1.0);
        t * t * (3.0 - 2.0 * t)
    }

    pub fn scrim_alpha(&self) -> f32 {
        let t = self.overlay.clamp(0.0, 1.0);
        t * t * (3.0 - 2.0 * t) * 0.50
    }

    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let target = if self.open { 1.0 } else { 0.0 };
        const K: f32 = 150.0;
        const C: f32 = 25.0;
        let force = -K * (self.present - target) - C * self.present_vel;
        self.present_vel += force * dt;
        self.present += self.present_vel * dt;
        if (self.present - target).abs() < 0.001 && self.present_vel.abs() < 0.01 {
            self.present = target;
            self.present_vel = 0.0;
        }
        let step = dt / 0.2;
        if self.overlay < target {
            self.overlay = (self.overlay + step).min(target);
        } else if self.overlay > target {
            self.overlay = (self.overlay - step).max(target);
        }
    }
}

/// First-run welcome splash (product `splash.go` card).
///
/// Overlay only — does not block PTY spawn. Dismiss with Enter / Esc / click;
/// host marks `ChromePrefs.splash_seen` and persists.
#[derive(Clone, Debug)]
pub struct SplashState {
    pub open: bool,
    present: f32,
    present_vel: f32,
    overlay: f32,
}

impl Default for SplashState {
    fn default() -> Self {
        Self::new()
    }
}

impl SplashState {
    pub fn new() -> Self {
        Self {
            open: false,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
        }
    }

    pub fn open_splash(&mut self) {
        self.open = true;
    }

    pub fn close(&mut self) {
        self.open = false;
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01 || self.overlay > 0.01
    }

    pub fn content_ease(&self) -> f32 {
        let t = self.present.clamp(0.0, 1.0);
        t * t * (3.0 - 2.0 * t)
    }

    pub fn scrim_alpha(&self) -> f32 {
        let t = self.overlay.clamp(0.0, 1.0);
        t * t * (3.0 - 2.0 * t) * 0.50
    }

    /// Compact welcome card (narrower than help sheet).
    pub fn modal_rect(window_w: f32, window_h: f32) -> crate::layout::Rect {
        let w = (window_w - 48.0).min(420.0).max(280.0);
        let h = (window_h - 80.0).min(280.0).max(200.0);
        crate::layout::Rect::new((window_w - w) * 0.5, (window_h - h) * 0.42, w, h)
    }

    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let target = if self.open { 1.0 } else { 0.0 };
        const K: f32 = 150.0;
        const C: f32 = 25.0;
        let force = -K * (self.present - target) - C * self.present_vel;
        self.present_vel += force * dt;
        self.present += self.present_vel * dt;
        if (self.present - target).abs() < 0.001 && self.present_vel.abs() < 0.01 {
            self.present = target;
            self.present_vel = 0.0;
        }
        let step = dt / 0.2;
        if self.overlay < target {
            self.overlay = (self.overlay + step).min(target);
        } else if self.overlay > target {
            self.overlay = (self.overlay - step).max(target);
        }
    }
}

/// Key-hint rows for the splash body (platform-aware labels).
pub fn splash_hint_rows() -> Vec<(String, &'static str)> {
    let m = mod_key();
    let ms = mod_shift();
    vec![
        (format!("{m}K"), "commands"),
        (format!("{m},"), "settings"),
        (format!("{m}/"), "shortcuts"),
        (format!("{ms}T"), "new tab"),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn filter_finds_split() {
        let all = default_commands();
        let idx = filter_commands(&all, "split");
        assert!(!idx.is_empty());
        assert!(idx.iter().any(|&i| all[i].id.contains("split")));
    }

    #[test]
    fn filter_finds_rename() {
        let all = default_commands();
        let idx = filter_commands(&all, "rename");
        assert!(idx.iter().any(|&i| all[i].id == "rename_tab"));
        assert!(idx.iter().any(|&i| all[i].id == "rename_pane"));
        assert!(all.iter().any(|c| c.action == CommandAction::RenameTab));
        assert!(all.iter().any(|c| c.action == CommandAction::RenamePane));
    }

    #[test]
    fn registry_contains_new_window() {
        let all = default_commands();
        let cmd = all
            .iter()
            .find(|c| c.id == "new_window")
            .expect("new_window command");
        assert_eq!(cmd.title, "New window");
        assert_eq!(cmd.category, "Window");
        assert_eq!(cmd.action, CommandAction::NewWindow);
        assert!(all.iter().any(|c| c.action == CommandAction::NewWindow));
        let idx = filter_commands(&all, "new window");
        assert!(idx.iter().any(|&i| all[i].id == "new_window"));
    }

    #[test]
    fn palette_spring() {
        let mut p = PaletteState::new();
        p.open_palette();
        for _ in 0..90 {
            p.tick(1.0 / 60.0);
        }
        assert!(p.present() > 0.9);
    }

    #[test]
    fn splash_open_close() {
        let mut s = SplashState::new();
        assert!(!s.open);
        assert!(!s.visible());
        s.open_splash();
        assert!(s.open);
        for _ in 0..90 {
            s.tick(1.0 / 60.0);
        }
        assert!(s.visible());
        assert!(s.content_ease() > 0.9);
        assert!(s.scrim_alpha() > 0.4);
        s.close();
        assert!(!s.open);
        for _ in 0..90 {
            s.tick(1.0 / 60.0);
        }
        assert!(!s.visible());
        assert!(s.content_ease() < 0.05);
    }

    #[test]
    fn splash_hints_nonempty() {
        let rows = splash_hint_rows();
        assert_eq!(rows.len(), 4);
        assert!(rows.iter().any(|(_, l)| *l == "commands"));
        assert!(rows.iter().any(|(_, l)| *l == "new tab"));
    }
}
