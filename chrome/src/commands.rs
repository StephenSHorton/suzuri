//! Command palette + keyboard shortcuts registry (product suzuri subset).

use crate::input::is_mac;

/// Host action for palette / shortcuts.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CommandAction {
    OpenSettings,
    OpenHelp,
    OpenPalette,
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
            title: "Toggle mouse lens",
            desc: "Settings 2 · Appearance".into(),
            category: "Appearance",
            action: CommandAction::ToggleLens,
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
    fn palette_spring() {
        let mut p = PaletteState::new();
        p.open_palette();
        for _ in 0..90 {
            p.tick(1.0 / 60.0);
        }
        assert!(p.present() > 0.9);
    }
}
