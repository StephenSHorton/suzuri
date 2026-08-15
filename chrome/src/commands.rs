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
    /// Native OS file picker → workspace attach (while workspace open, or opens it).
    WorkspaceAttachFile,
    /// +Agent: pick role and copy a kickoff snippet (does not launch Grok).
    WorkspaceAddAgent,
    /// Edit the pinned channel topic (`meta.json`).
    WorkspaceSetTopic,
    /// Query GitHub Releases (host may open confirm).
    CheckUpdates,
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

/// Primary+Shift chord prefix (joined with `+` before the key via [`chord`]).
fn mod_shift() -> String {
    if is_mac() {
        "⇧+⌘".into()
    } else {
        "Ctrl+Shift".into()
    }
}

/// Primary+Alt/Option chord prefix.
fn mod_alt() -> String {
    if is_mac() {
        "⌥+⌘".into()
    } else {
        "Ctrl+Alt".into()
    }
}

/// Join modifiers and key with `+` (⌘+K, ⇧+⌘+T, Ctrl+Shift+N).
fn chord(mods: &str, key: &str) -> String {
    format!("{mods}+{key}")
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
            desc: format!("{} · {} · Navigate", chord(m, "K"), chord(m, "P")),
            category: "Navigate",
            action: CommandAction::OpenPalette,
        },
        Command {
            id: "settings",
            title: "Settings",
            desc: format!("{} · Appearance", chord(m, ",")),
            category: "Appearance",
            action: CommandAction::OpenSettings,
        },
        Command {
            id: "help",
            title: "Keyboard shortcuts",
            desc: format!("{} · Help", chord(m, "/")),
            category: "Help",
            action: CommandAction::OpenHelp,
        },
        Command {
            id: "notes",
            title: "Notes",
            desc: format!("{} · Notes", chord(&ms, "M")),
            category: "Notes",
            action: CommandAction::OpenNotes,
        },
        Command {
            id: "workspace",
            title: "Workspace",
            desc: "Shared channels · splits as a pane".into(),
            category: "Workspace",
            action: CommandAction::OpenWorkspace,
        },
        Command {
            id: "workspace_cycle_status",
            title: "Cycle workspace status",
            desc: format!("{} · idle→working→waiting·… · Workspace", chord(&ms, "A")),
            category: "Workspace",
            action: CommandAction::CycleWorkspaceStatus,
        },
        Command {
            id: "workspace_attach_file",
            title: "Attach file…",
            desc: format!("{} · native picker · Workspace", chord(&ms, "U")),
            category: "Workspace",
            action: CommandAction::WorkspaceAttachFile,
        },
        Command {
            id: "workspace_add_agent",
            title: "Add agent…",
            desc: "+Agent · copy kickoff · Workspace".into(),
            category: "Workspace",
            action: CommandAction::WorkspaceAddAgent,
        },
        Command {
            id: "workspace_set_topic",
            title: "Set channel topic…",
            desc: format!("{} · pin above chat · Workspace", chord(&ms, "T")),
            category: "Workspace",
            action: CommandAction::WorkspaceSetTopic,
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
            id: "check_updates",
            title: "Check for updates",
            desc: "GitHub Releases · System".into(),
            category: "System",
            action: CommandAction::CheckUpdates,
        },
        Command {
            id: "new_tab",
            title: "New tab",
            desc: format!("{} · Tabs", chord(&ms, "T")),
            category: "Tabs",
            action: CommandAction::NewTab,
        },
        Command {
            id: "close_tab",
            title: "Close tab / pane",
            desc: format!("{} · Tabs · Panes", chord(m, "W")),
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
            desc: format!("{} · Tabs", chord(&ms, "]")),
            category: "Tabs",
            action: CommandAction::NextTab,
        },
        Command {
            id: "prev_tab",
            title: "Previous tab",
            desc: format!("{} · Tabs", chord(&ms, "[")),
            category: "Tabs",
            action: CommandAction::PrevTab,
        },
        Command {
            id: "split_right",
            title: "Split right",
            desc: format!("{} · Panes · jelly open", chord(&ms, "D")),
            category: "Panes",
            action: CommandAction::SplitRight,
        },
        Command {
            id: "split_down",
            title: "Split down",
            desc: format!("{} · Panes · jelly open", chord(&ms, "E")),
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
            desc: format!("{} · Panes", chord(m, "W")),
            category: "Panes",
            action: CommandAction::ClosePane,
        },
        Command {
            id: "focus_left",
            title: "Focus pane left",
            desc: format!("{} · Panes", chord(&ma, "←")),
            category: "Panes",
            action: CommandAction::FocusLeft,
        },
        Command {
            id: "focus_right",
            title: "Focus pane right",
            desc: format!("{} · Panes", chord(&ma, "→")),
            category: "Panes",
            action: CommandAction::FocusRight,
        },
        Command {
            id: "focus_up",
            title: "Focus pane up",
            desc: format!("{} · Panes", chord(&ma, "↑")),
            category: "Panes",
            action: CommandAction::FocusUp,
        },
        Command {
            id: "focus_down",
            title: "Focus pane down",
            desc: format!("{} · Panes", chord(&ma, "↓")),
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
            desc: format!("{} · Window", chord(&ms, "N")),
            category: "Window",
            action: CommandAction::NewWindow,
        },
        Command {
            id: "quit",
            title: "Quit",
            desc: format!("{} · Window", chord(&ms, "Q")),
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
        // Heavier frost than other overlays so the palette reads above panes.
        e * 0.62
    }

    /// Result row height (title + shortcut subtext).
    pub const ROW_H: f32 = 48.0;
    pub const ROW_GAP: f32 = 6.0;
    pub const INPUT_H: f32 = 48.0;
    pub const MAX_ROWS: usize = 6;

    /// Wide horizontal palette card (input + tall result rows for subtitles).
    pub fn modal_rect(&self, window_w: f32, window_h: f32) -> crate::layout::Rect {
        let t = self.content_ease();
        let base_w = (window_w - 48.0).min(680.0).max(360.0);
        // input + pad + 6×(row+gap) + footer air ≈ 48+18+6×54+20 ≈ 410
        let base_h = (window_h - 100.0).min(420.0).max(320.0);
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

/// One row in the product-style shortcuts sheet.
#[derive(Clone, Debug)]
pub struct HelpRow {
    pub keys: String,
    pub desc: &'static str,
}

/// Titled group of shortcut rows (product `helpSectionBlock`).
#[derive(Clone, Debug)]
pub struct HelpSection {
    pub title: &'static str,
    pub rows: Vec<HelpRow>,
}

/// Full product-parity keyboard reference (not the palette registry).
pub fn help_sections() -> Vec<HelpSection> {
    let m = mod_key();
    let ms = mod_shift();
    let ma = mod_alt();
    let clear_line = if is_mac() {
        format!("{} · Esc", chord(m, "⌫"))
    } else {
        format!("Esc · {}", chord(m, "U"))
    };
    let open_url = if is_mac() {
        "⌘-click".into()
    } else {
        "Ctrl-click".into()
    };
    let focus_panes = if is_mac() {
        format!("{} arrows", ma)
    } else {
        "Alt+arrows".into()
    };
    let word_jump = if is_mac() {
        "⌥+← / ⌥+→".into()
    } else {
        "Ctrl+← / Ctrl+→".into()
    };
    let line_ends = if is_mac() {
        format!("{} · Home/End", chord(m, "←→"))
    } else {
        "Home / End".into()
    };
    vec![
        HelpSection {
            title: "Tabs",
            rows: vec![
                HelpRow {
                    keys: chord(&ms, "T"),
                    desc: "New tab",
                },
                HelpRow {
                    keys: chord(&ms, "N"),
                    desc: "New window",
                },
                HelpRow {
                    // Compact: avoid multi-chord soup overflowing the chip.
                    keys: format!("{} · {}", chord(&ms, "[ ]"), chord(m, "Tab")),
                    desc: "Prev / next",
                },
                HelpRow {
                    keys: chord(m, "1-9"),
                    desc: "Jump to tab",
                },
                HelpRow {
                    keys: "Strip ×".into(),
                    desc: "Close tab",
                },
                HelpRow {
                    keys: "Palette".into(),
                    desc: "Rename tab",
                },
            ],
        },
        HelpSection {
            title: "Command line",
            rows: vec![
                HelpRow {
                    keys: "Enter".into(),
                    desc: "Run command",
                },
                HelpRow {
                    keys: "↑ / ↓".into(),
                    desc: "History",
                },
                HelpRow {
                    keys: clear_line,
                    desc: "Clear line",
                },
                HelpRow {
                    keys: chord(m, "C"),
                    desc: "Clear draft",
                },
                HelpRow {
                    keys: chord(m, "V"),
                    desc: "Paste into bar",
                },
            ],
        },
        HelpSection {
            title: "Terminal",
            rows: vec![
                HelpRow {
                    keys: format!("{} · dbl-click", chord(&ms, "C")),
                    desc: "Copy selection",
                },
                HelpRow {
                    keys: "Wheel · PgUp/Dn".into(),
                    desc: "Scrollback",
                },
                HelpRow {
                    keys: open_url,
                    desc: "Open URL",
                },
                HelpRow {
                    keys: "Hover link".into(),
                    desc: "Highlight",
                },
            ],
        },
        HelpSection {
            title: "Panes",
            rows: vec![
                HelpRow {
                    keys: chord(&ms, "D"),
                    desc: "Split right",
                },
                HelpRow {
                    keys: chord(&ms, "E"),
                    desc: "Split down",
                },
                HelpRow {
                    keys: focus_panes,
                    desc: "Focus pane",
                },
                HelpRow {
                    keys: word_jump,
                    desc: "Word jump",
                },
                HelpRow {
                    keys: line_ends,
                    desc: "Line ends",
                },
                HelpRow {
                    keys: "F2".into(),
                    desc: "Rename pane",
                },
                HelpRow {
                    keys: chord(m, "W"),
                    desc: "Close pane",
                },
            ],
        },
        HelpSection {
            title: "Chrome",
            rows: vec![
                HelpRow {
                    keys: format!("{} · {}", chord(m, "K"), chord(m, "P")),
                    desc: "Palette",
                },
                HelpRow {
                    keys: chord(m, ","),
                    desc: "Settings",
                },
                HelpRow {
                    keys: chord(m, "/"),
                    desc: "Help",
                },
                HelpRow {
                    keys: chord(&ms, "M"),
                    desc: "Notes",
                },
                HelpRow {
                    keys: "☕".into(),
                    desc: "Caffeine",
                },
                HelpRow {
                    keys: format!("{} / {}", chord(m, "+"), chord(m, "-")),
                    desc: "Zoom",
                },
                HelpRow {
                    keys: chord(m, "0"),
                    desc: "Reset zoom",
                },
                HelpRow {
                    keys: "Esc".into(),
                    desc: "Dismiss",
                },
            ],
        },
    ]
}

/// Shared geometry for the shortcuts sheet (glass chips + labels must match).
#[derive(Clone, Debug)]
pub struct HelpLayout {
    pub modal: crate::layout::Rect,
    pub pad: f32,
    /// Section header positions: (x, y, title).
    pub headers: Vec<(f32, f32, &'static str)>,
    /// One frost chip per shortcut row: (rect, keys, desc).
    pub rows: Vec<(crate::layout::Rect, String, &'static str)>,
    pub footer_y: f32,
}

impl HelpLayout {
    pub const ROW_H: f32 = 26.0;
    pub const ROW_GAP: f32 = 3.0;
    pub const SEC_GAP: f32 = 6.0;
    pub const HEADER_H: f32 = 14.0;

    /// Layout at full open (ease = 1). Prefer [`Self::with_ease`] while animating.
    pub fn new(window_w: f32, window_h: f32) -> Self {
        Self::with_ease(window_w, window_h, 1.0)
    }

    /// Same geometry as other glass modals: spring `ease` scales size + drops from above
    /// (palette / settings), then packs two-col rows inside the animated card.
    pub fn with_ease(window_w: f32, window_h: f32, ease: f32) -> Self {
        let modal = Self::animated_modal_rect(window_w, window_h, ease);
        let pad = 14.0;
        let sections = help_sections();
        // Prefer two columns so every product row fits with a glass chip.
        let two_col = modal.w >= 520.0;
        let col_gap = 12.0;
        let col_w = if two_col {
            (modal.w - pad * 2.0 - col_gap) * 0.5
        } else {
            modal.w - pad * 2.0
        };
        let left_x = modal.x + pad;
        let right_x = left_x + col_w + col_gap;
        let footer_y = modal.y + modal.h - 22.0;
        let max_y = footer_y - 6.0;
        let mut y_left = modal.y + 40.0;
        let mut y_right = modal.y + 40.0;
        // Split after Command line / Terminal so columns stay balanced.
        let mid = 3; // Tabs + Command line + Terminal | Panes + Chrome
        let mut headers = Vec::new();
        let mut rows = Vec::new();

        for (si, sec) in sections.iter().enumerate() {
            let (x, y) = if two_col && si >= mid {
                (right_x, &mut y_right)
            } else {
                (left_x, &mut y_left)
            };
            if *y + Self::HEADER_H > max_y {
                continue;
            }
            headers.push((x, *y, sec.title));
            *y += Self::HEADER_H + 3.0;
            for row in &sec.rows {
                if *y + Self::ROW_H > max_y {
                    break;
                }
                let r = crate::layout::Rect::new(x, *y, col_w, Self::ROW_H);
                rows.push((r, row.keys.clone(), row.desc));
                *y += Self::ROW_H + Self::ROW_GAP;
            }
            *y += Self::SEC_GAP;
        }

        Self {
            modal,
            pad,
            headers,
            rows,
            footer_y,
        }
    }

    /// Settled size (hit-test / “fully open”).
    pub fn modal_rect(window_w: f32, window_h: f32) -> crate::layout::Rect {
        Self::base_modal_rect(window_w, window_h)
    }

    fn base_modal_rect(window_w: f32, window_h: f32) -> crate::layout::Rect {
        // Two-col stack needs ~40 title + ~15×29 rows + headers ≈ 520+.
        let w = (window_w - 40.0).min(800.0).max(360.0);
        let h = (window_h - 48.0).min(640.0).max(520.0);
        crate::layout::Rect::new((window_w - w) * 0.5, (window_h - h) * 0.40, w, h)
    }

    /// Agility-style content motion (same recipe as palette / settings):
    /// scale 0.88→1 / 0.82→1, slight drop from above.
    pub fn animated_modal_rect(window_w: f32, window_h: f32, ease: f32) -> crate::layout::Rect {
        let base = Self::base_modal_rect(window_w, window_h);
        let t = ease.clamp(0.0, 1.0);
        let sx = 0.88 + 0.12 * t;
        let sy = 0.82 + 0.18 * t;
        let y_nudge = -24.0 * (1.0 - t);
        let cx = base.x + base.w * 0.5;
        let cy = base.y + base.h * 0.5 + y_nudge;
        let w = base.w * sx;
        let h = base.h * sy;
        crate::layout::Rect::new(cx - w * 0.5, cy - h * 0.5, w, h)
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
    /// Height fits title + 4 hint rows + continue chip without overlap.
    pub fn modal_rect(window_w: f32, window_h: f32) -> crate::layout::Rect {
        let w = (window_w - 48.0).min(420.0).max(280.0);
        // 64 title + 4×28 rows + 3×6 gaps + 12 gap + 28 cont + 16 pad ≈ 252
        let h = (window_h - 80.0).min(300.0).max(252.0);
        crate::layout::Rect::new((window_w - w) * 0.5, (window_h - h) * 0.42, w, h)
    }

    /// Shared glass + label geometry so the continue chip and hint rows stay aligned.
    pub fn layout(window_w: f32, window_h: f32) -> SplashLayout {
        SplashLayout::new(Self::modal_rect(window_w, window_h))
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

/// Geometry for the first-run splash card (panels + text must use the same rects).
#[derive(Clone, Debug)]
pub struct SplashLayout {
    pub modal: crate::layout::Rect,
    pub pad: f32,
    /// Frost chips for each shortcut hint row.
    pub rows: Vec<crate::layout::Rect>,
    /// Left column inside each row for the key (text centered in this rect).
    pub key_col_w: f32,
    /// Continue / Enter affordance.
    pub continue_btn: crate::layout::Rect,
}

impl SplashLayout {
    pub fn new(modal: crate::layout::Rect) -> Self {
        let pad = 16.0;
        let row_h = 28.0;
        let gap = 6.0;
        let n_rows = splash_hint_rows().len();
        let mut rows = Vec::with_capacity(n_rows);
        let mut y = modal.y + 64.0;
        for _ in 0..n_rows {
            rows.push(crate::layout::Rect::new(
                modal.x + pad,
                y,
                modal.w - pad * 2.0,
                row_h,
            ));
            y += row_h + gap;
        }
        // Wide enough for "enter  continue" at 11px mono (~16 glyphs × ~6.5).
        let cont_w = 148.0_f32.min(modal.w - pad * 2.0).max(120.0);
        let cont_h = 28.0;
        let continue_btn = crate::layout::Rect::new(
            modal.x + (modal.w - cont_w) * 0.5,
            modal.y + modal.h - pad - cont_h,
            cont_w,
            cont_h,
        );
        // Mac: "⇧+⌘+T"; Linux: "Ctrl+Shift+T". Wide enough for left-aligned chords.
        let key_col_w = if is_mac() { 108.0 } else { 128.0 };
        Self {
            modal,
            pad,
            rows,
            key_col_w,
            continue_btn,
        }
    }

    /// Left-aligned label column X (after the key column + gap).
    pub fn label_x(&self, row: crate::layout::Rect) -> f32 {
        row.x + self.key_col_w + 10.0
    }
}

/// Key-hint rows for the splash body (platform-aware labels).
/// Modifiers and the key are joined with `+` (⌘+K, ⇧+⌘+T).
pub fn splash_hint_rows() -> Vec<(String, &'static str)> {
    if is_mac() {
        vec![
            ("⌘+K · ⌘+P".into(), "commands"),
            ("⌘+,".into(), "settings"),
            ("⌘+/".into(), "shortcuts"),
            ("⇧+⌘+T".into(), "new tab"),
        ]
    } else {
        vec![
            ("Ctrl+K · Ctrl+P".into(), "commands"),
            ("Ctrl+,".into(), "settings"),
            ("Ctrl+/".into(), "shortcuts"),
            ("Ctrl+Shift+T".into(), "new tab"),
        ]
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
    fn registry_contains_workspace_attach_file() {
        let all = default_commands();
        let cmd = all
            .iter()
            .find(|c| c.id == "workspace_attach_file")
            .expect("workspace_attach_file command");
        assert_eq!(cmd.title, "Attach file…");
        assert_eq!(cmd.category, "Workspace");
        assert_eq!(cmd.action, CommandAction::WorkspaceAttachFile);
        let idx = filter_commands(&all, "attach");
        assert!(idx.iter().any(|&i| all[i].id == "workspace_attach_file"));
    }

    #[test]
    fn registry_contains_workspace_add_agent_and_topic() {
        let all = default_commands();
        let add = all
            .iter()
            .find(|c| c.id == "workspace_add_agent")
            .expect("workspace_add_agent");
        assert_eq!(add.action, CommandAction::WorkspaceAddAgent);
        let topic = all
            .iter()
            .find(|c| c.id == "workspace_set_topic")
            .expect("workspace_set_topic");
        assert_eq!(topic.action, CommandAction::WorkspaceSetTopic);
        assert!(filter_commands(&all, "agent")
            .iter()
            .any(|&i| all[i].id == "workspace_add_agent"));
        assert!(filter_commands(&all, "topic")
            .iter()
            .any(|&i| all[i].id == "workspace_set_topic"));
    }

    #[test]
    fn registry_contains_check_updates() {
        let all = default_commands();
        let cmd = all
            .iter()
            .find(|c| c.id == "check_updates")
            .expect("check_updates command");
        assert_eq!(cmd.title, "Check for updates");
        assert_eq!(cmd.category, "System");
        assert_eq!(cmd.action, CommandAction::CheckUpdates);
        let idx = filter_commands(&all, "updates");
        assert!(idx.iter().any(|&i| all[i].id == "check_updates"));
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
        // Plus-separated chords (mac: ⌘+K / ⇧+⌘+T; else Ctrl+…).
        for (key, _) in &rows {
            assert!(
                key.contains('+'),
                "splash key chord should use + separators: {key}"
            );
        }
    }

    #[test]
    fn command_descs_use_plus_chords() {
        let all = default_commands();
        let palette = all.iter().find(|c| c.id == "palette").unwrap();
        assert!(
            palette.desc.contains('+'),
            "palette chord should use +: {}",
            palette.desc
        );
        let new_win = all.iter().find(|c| c.id == "new_window").unwrap();
        assert!(
            new_win.desc.contains('+'),
            "new_window chord should use +: {}",
            new_win.desc
        );
        // Sanity: chord() helper
        assert_eq!(chord("⌘", "K"), "⌘+K");
        assert_eq!(chord("⇧+⌘", "T"), "⇧+⌘+T");
    }

    #[test]
    fn help_sections_cover_product_groups() {
        let secs = help_sections();
        let titles: Vec<_> = secs.iter().map(|s| s.title).collect();
        for need in ["Tabs", "Command line", "Terminal", "Panes", "Chrome"] {
            assert!(titles.contains(&need), "missing section {need}");
        }
        let n: usize = secs.iter().map(|s| s.rows.len()).sum();
        assert!(n >= 20, "expected product-scale shortcut list, got {n}");
    }

    #[test]
    fn help_layout_one_chip_per_row() {
        let lay = HelpLayout::new(1100.0, 800.0);
        let n: usize = help_sections().iter().map(|s| s.rows.len()).sum();
        assert_eq!(
            lay.rows.len(),
            n,
            "glass chips must match every help row"
        );
        assert!(!lay.headers.is_empty());
        // Chips stay above the footer.
        for (r, _, _) in &lay.rows {
            assert!(r.y + r.h < lay.footer_y);
        }
    }

    #[test]
    fn splash_layout_continue_below_rows() {
        let lay = SplashLayout::new(SplashState::modal_rect(800.0, 600.0));
        assert_eq!(lay.rows.len(), 4);
        let last = *lay.rows.last().unwrap();
        assert!(
            lay.continue_btn.y >= last.y + last.h + 4.0,
            "continue chip must sit below last hint row (cont.y={} last.bottom={})",
            lay.continue_btn.y,
            last.y + last.h
        );
        // Continue is horizontally centered in the modal.
        let mid = lay.modal.x + lay.modal.w * 0.5;
        let cont_mid = lay.continue_btn.x + lay.continue_btn.w * 0.5;
        assert!((mid - cont_mid).abs() < 0.5);
    }
}
