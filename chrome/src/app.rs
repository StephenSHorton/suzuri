//! winit application — multi-tab, multi-pane PTY, palette, help, chrome.

use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};

use winit::{
    application::ApplicationHandler,
    dpi::{LogicalPosition, LogicalSize, PhysicalPosition},
    event::{
        DeviceEvent, ElementState, MouseButton, MouseScrollDelta, TouchPhase, WindowEvent,
    },
    event_loop::ActiveEventLoop,
    keyboard::{Key, ModifiersState, NamedKey},
    window::{CursorIcon, Fullscreen, Window, WindowAttributes, WindowId},
};

use crate::ansi::AnsiDecoder;
use crate::caffeine::Caffeine;
use crate::chrome_status::{self, PaneSnapExtra, StatusPublisher};
use crate::chrome_ui::{ChipId, ChipUi};
use crate::cmd_blocks::CmdBlockLog;
use crate::echo_filter::EchoFilter;
use crate::commands::{
    default_commands, filter_commands, CommandAction, HelpState, PaletteState, SplashState,
};
use crate::confirm::{ConfirmChoice, ConfirmState};
use crate::control_mailbox::ControlMailbox;
use crate::input::{hit_test, is_mac, HitTarget};
use crate::layout::{FrameLayout, Metrics};
use crate::links::{link_span_at_col, open_url_in_browser, LinkHoverSpan};
use crate::new_window::spawn_new_window;
use crate::notes::NotesState;
use crate::panes::{FocusDir, SplitAxis};
use crate::pty::PtySession;
use crate::rename::{RenameState, RenameTarget};
use crate::renderer::{self, Renderer, CELL_H, CELL_W};
use crate::selection::{clamp_pos, CellPos, Selection};
use crate::session::{ChromeSession, CloseOutcome};
use crate::settings::SettingsState;
use crate::toast::ToastState;
use crate::transfer_ui::TransferUi;
use crate::workspace_ui::WorkspaceUi;

/// Max raw PTY bytes retained for MCP `pty_tail` (recent end of stream).
const PTY_TAIL_CAP: usize = 2048;

/// Per-pane live shell state (keyed by pane id).
struct PaneRuntime {
    pty: Option<PtySession>,
    ansi: AnsiDecoder,
    /// Recent raw PTY bytes (ring-ish: keep last PTY_TAIL_CAP).
    pty_tail: String,
    /// Suppress shell local-echo of warp-submitted lines.
    echo: EchoFilter,
    /// Host-injected command blocks + MCP history kinds.
    blocks: CmdBlockLog,
}

impl PaneRuntime {
    fn push_pty_tail(&mut self, chunk: &str) {
        if chunk.is_empty() {
            return;
        }
        self.pty_tail.push_str(chunk);
        if self.pty_tail.len() > PTY_TAIL_CAP {
            let drop_n = self.pty_tail.len() - PTY_TAIL_CAP;
            // Drop whole chars from the front.
            let mut cut = drop_n;
            while !self.pty_tail.is_char_boundary(cut) && cut < self.pty_tail.len() {
                cut += 1;
            }
            self.pty_tail = self.pty_tail[cut..].to_string();
        }
    }
}

pub struct ChromeApp {
    window: Option<Arc<Window>>,
    renderer: Option<Renderer>,
    session: ChromeSession,
    metrics: Metrics,
    cursor: LogicalPosition<f32>,
    pointer_inside: bool,
    warp_focused: bool,
    terminal_focused: bool,
    modifiers: ModifiersState,
    runtimes: HashMap<u64, PaneRuntime>,
    settings: SettingsState,
    palette: PaletteState,
    help: HelpState,
    confirm: ConfirmState,
    splash: SplashState,
    notes: NotesState,
    workspace_ui: WorkspaceUi,
    transfer: TransferUi,
    rename: RenameState,
    caffeine: Caffeine,
    toast: ToastState,
    commands: Vec<crate::commands::Command>,
    started: Instant,
    clipboard: Option<arboard::Clipboard>,
    chip_ui: ChipUi,
    /// Left-button hit at mouse-down; activation runs on release if still the same.
    press_hit: Option<HitTarget>,
    /// Middle-button hit at mouse-down (tab close on matching release).
    middle_press_hit: Option<HitTarget>,
    /// Last empty title-bar click (time + logical xy) for OS zoom double-click.
    last_title_click: Option<(Instant, f32, f32)>,
    /// Terminal multi-click tracker: time, cell col/abs_row, consecutive count.
    /// Same cell when |dcol|≤1 and |drow|≤1 within [`TERM_MULTI_CLICK_MS`].
    last_term_click: Option<(Instant, u16, usize, u8)>,
    /// Terminal cell selection (absolute document rows).
    term_selection: Selection,
    selecting_term: bool,
    /// URL under the pointer in the focused terminal (for hand cursor / Cmd-click).
    hovered_link: Option<String>,
    /// Exact cell range of [`Self::hovered_link`] for primary hover paint.
    hovered_link_span: Option<LinkHoverSpan>,
    /// True while the OS pointer icon is the hand (link hover).
    link_cursor_on: bool,
    /// Host light IPC: poll `chrome_cmd` under config dir (~250ms).
    control_mailbox: ControlMailbox,
    /// Publish `chrome_status.json` for Go MCP bridge proxy.
    status_publisher: StatusPublisher,
}

impl Default for ChromeApp {
    fn default() -> Self {
        let mut session = ChromeSession::new(80, 24);
        let mut runtimes = HashMap::new();
        let pane_id = session.focus_pane_id();
        let (cols, rows) = {
            let g = session.active_grid();
            (g.cols(), g.rows())
        };
        let rt = spawn_pane_runtime(cols, rows, &mut session, pane_id);
        runtimes.insert(pane_id, rt);

        let mut app = Self {
            window: None,
            renderer: None,
            session,
            metrics: Metrics::default(),
            cursor: LogicalPosition::new(0.0, 0.0),
            pointer_inside: false,
            warp_focused: true,
            terminal_focused: false,
            modifiers: ModifiersState::empty(),
            runtimes,
            settings: SettingsState::new(),
            palette: PaletteState::new(),
            help: HelpState::new(),
            confirm: ConfirmState::new(),
            splash: SplashState::new(),
            notes: NotesState::new(),
            workspace_ui: WorkspaceUi::new(),
            transfer: TransferUi::new(),
            rename: RenameState::new(),
            caffeine: Caffeine::new(),
            toast: ToastState::new(),
            commands: default_commands(),
            started: Instant::now(),
            clipboard: arboard::Clipboard::new().ok(),
            chip_ui: ChipUi::default(),
            press_hit: None,
            middle_press_hit: None,
            last_title_click: None,
            last_term_click: None,
            term_selection: Selection::new(),
            selecting_term: false,
            hovered_link: None,
            hovered_link_span: None,
            link_cursor_on: false,
            control_mailbox: ControlMailbox::new(),
            status_publisher: StatusPublisher::new(),
        };
        // First-run overlay only — PTY already spawned above.
        if !app.settings.prefs.splash_seen {
            app.splash.open_splash();
        }
        app
    }
}

/// Multi-click window for terminal word/line select (product: 500 ms; same cell ±1).
const TERM_MULTI_CLICK_MS: u64 = 500;

fn spawn_pane_runtime(
    cols: u16,
    rows: u16,
    session: &mut ChromeSession,
    pane_id: u64,
) -> PaneRuntime {
    match PtySession::spawn(cols, rows) {
        Ok(pty) => {
            session.mark_pane_pty(pane_id);
            PaneRuntime {
                pty: Some(pty),
                ansi: AnsiDecoder::new(),
                pty_tail: String::new(),
                echo: EchoFilter::new(),
                blocks: CmdBlockLog::new(),
            }
        }
        Err(e) => {
            eprintln!("suzuri-chrome: PTY for pane {pane_id} failed ({e}) — mock");
            session.boot_mock_on_pane(pane_id);
            PaneRuntime {
                pty: None,
                ansi: AnsiDecoder::new(),
                pty_tail: String::new(),
                echo: EchoFilter::new(),
                blocks: CmdBlockLog::new(),
            }
        }
    }
}

impl ChromeApp {
    /// Dismiss splash, mark `splash_seen`, and persist chrome prefs.
    /// Enter / Esc / outside click / continue all go through here.
    fn dismiss_splash(&mut self) {
        let was_open = self.splash.open;
        self.splash.close();
        if was_open || !self.settings.prefs.splash_seen {
            self.settings.prefs.splash_seen = true;
            self.settings.mark_dirty();
            let _ = self.settings.save_prefs();
        }
    }

    fn any_pty_alive(&mut self) -> bool {
        self.runtimes
            .values_mut()
            .any(|rt| rt.pty.as_mut().is_some_and(|p| p.is_alive()))
    }

    fn blink_on(&self) -> bool {
        let phase = self.started.elapsed().as_secs_f32();
        (phase * 1.9).fract() < 0.55
    }

    fn terminal_cursor_visible(&self) -> bool {
        if !self.terminal_focused || self.warp_focused {
            return false;
        }
        let id = self.session.focus_pane_id();
        let ansi_on = self
            .runtimes
            .get(&id)
            .map(|rt| rt.ansi.cursor_visible)
            .unwrap_or(true);
        ansi_on && self.blink_on()
    }

    fn input_caret_alpha(&self) -> f32 {
        if !self.warp_focused {
            return 0.0;
        }
        let t = self.started.elapsed().as_secs_f32();
        let phase = (t * std::f32::consts::TAU / 1.1).sin() * 0.5 + 0.5;
        0.18 + 0.82 * phase
    }

    fn paste_clipboard(&mut self) {
        let Some(cb) = self.clipboard.as_mut() else {
            return;
        };
        let Ok(text) = cb.get_text() else {
            return;
        };
        if text.is_empty() {
            return;
        }
        // Modal text fields first (rename / palette query / notes already handle keys).
        if self.rename.open {
            // Single-line: stop at first newline.
            let line = text.split(['\n', '\r']).next().unwrap_or("");
            self.rename.insert_str(line);
            return;
        }
        if self.palette.open {
            self.palette.query.push_str(text.split(['\n', '\r']).next().unwrap_or(""));
            self.palette.selected = 0;
            return;
        }
        if self.warp_focused {
            self.session.paste_draft(&text);
            return;
        }
        let id = self.session.focus_pane_id();
        if let Some(rt) = self.runtimes.get_mut(&id) {
            if let Some(pty) = &mut rt.pty {
                let _ = pty.write_all(text.as_bytes());
                return;
            }
        }
        self.session.paste_draft(&text);
    }

    fn current_layout(&self) -> FrameLayout {
        let tab_count = self.session.tabs.len();
        let mut layout = if let Some(r) = &self.renderer {
            r.layout(tab_count)
        } else {
            FrameLayout::compute(1120.0, 740.0, self.metrics, tab_count)
        };

        // Apply split-tree leaf rects for the active tab.
        if let Some(tab) = self.session.active_tab() {
            let mut leafs = Vec::new();
            let gap = self.metrics.stack();
            tab.root
                .layout_into(layout.workspace, gap, &mut leafs);
            // Solo-exit: scale the glass toward center while jelly closes.
            if let Some(anim) = &tab.solo_exit {
                let s = anim.jelly.clamp(0.0, 1.15);
                leafs = leafs
                    .into_iter()
                    .map(|(id, r)| {
                        if id == anim.pane_id {
                            let cx = r.x + r.w * 0.5;
                            let cy = r.y + r.h * 0.5;
                            let nw = (r.w * s).max(1.0);
                            let nh = (r.h * s).max(1.0);
                            (
                                id,
                                crate::layout::Rect::new(cx - nw * 0.5, cy - nh * 0.5, nw, nh),
                            )
                        } else {
                            (id, r)
                        }
                    })
                    .collect();
            }
            layout.apply_pane_rects(self.metrics, &leafs, tab.focus_pane);
        }
        layout
    }

    fn sync_grids_to_panes(&mut self) {
        let layout = self.current_layout();
        for pl in &layout.panes {
            let (cols, rows) = renderer::terminal_grid_size(&pl.cells, self.metrics.inset());
            let need = self
                .session
                .grid(pl.pane_id)
                .map(|g| g.cols() != cols || g.rows() != rows)
                .unwrap_or(true);
            if need {
                self.session.resize_pane(pl.pane_id, cols, rows);
                if let Some(rt) = self.runtimes.get_mut(&pl.pane_id) {
                    if let Some(pty) = &mut rt.pty {
                        let _ = pty.resize(cols, rows);
                    }
                }
            }
        }
    }

    fn drain_all_ptys(&mut self) {
        let mut pending: Vec<(u64, String)> = Vec::new();
        for (id, rt) in self.runtimes.iter_mut() {
            if let Some(pty) = &mut rt.pty {
                let chunk = pty.try_read();
                if !chunk.is_empty() {
                    rt.push_pty_tail(&chunk);
                    pending.push((*id, chunk));
                }
            }
        }
        if pending.is_empty() {
            return;
        }
        for (id, chunk) in pending {
            if let Some(rt) = self.runtimes.get_mut(&id) {
                // Suppress local echo of warp-submitted command when armed.
                let filtered = rt.echo.feed(chunk.as_bytes());
                if !filtered.is_empty() {
                    if let Some(grid) = self.session.grid_mut(id) {
                        rt.ansi.feed(grid, &filtered);
                    }
                }
                if let Some(cwd) = rt.ansi.take_cwd() {
                    self.session.set_cwd(id, cwd);
                }
                if let Some(title) = rt.ansi.take_title() {
                    self.session.set_pane_title(id, title);
                }
            }
            if let Some(p) = self.session.panes.get_mut(&id) {
                p.busy = false;
            }
        }
        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn overlay_open(&self) -> bool {
        self.settings.open
            || self.palette.open
            || self.help.open
            || self.confirm.open
            || self.splash.open
            || self.notes.open
            || self.workspace_ui.open
            || self.transfer.open
            || self.rename.open
    }

    fn close_all_overlays(&mut self) {
        if self.splash.open {
            self.dismiss_splash();
        } else {
            self.splash.close();
        }
        self.settings.close();
        self.palette.close();
        self.help.close();
        self.confirm.close();
        self.notes.close();
        self.workspace_ui.close();
        self.transfer.close();
    }

    /// Close every glass modal except the confirm dialog (product `closeModalsExcept`).
    fn close_modals_except_confirm(&mut self) {
        self.settings.close();
        self.palette.close();
        self.help.close();
        self.notes.close();
        self.workspace_ui.close();
        self.transfer.close();
        self.rename.close();
    }

    /// Open rename dialog for tab or pane (closes other modals first).
    fn open_rename(&mut self, target: RenameTarget) {
        self.close_all_overlays();
        let seed = self.session.rename_seed(matches!(target, RenameTarget::Tab));
        self.rename.open_with(target, &seed);
    }

    fn apply_rename(&mut self, target: RenameTarget, name: &str) {
        match target {
            RenameTarget::Tab => self.session.rename_active_tab(name),
            RenameTarget::Pane => self.session.rename_focused_pane(name),
        }
    }

    /// Quit when idle (no live PTY, single tab); otherwise open confirm.
    fn request_quit(&mut self, event_loop: &ActiveEventLoop) {
        if self.needs_quit_confirm() {
            self.close_modals_except_confirm();
            self.confirm.open_quit();
            if let Some(w) = &self.window {
                w.request_redraw();
            }
        } else {
            chrome_status::clear_status();
            event_loop.exit();
        }
    }

    /// Confirm when any PTY is still alive or more than one tab is open.
    fn needs_quit_confirm(&mut self) -> bool {
        self.any_pty_alive() || self.session.tabs.len() > 1
    }

    fn apply_confirm_choice(&mut self, event_loop: &ActiveEventLoop, choice: ConfirmChoice) {
        match choice {
            ConfirmChoice::Yes => {
                self.confirm.close();
                chrome_status::clear_status();
                event_loop.exit();
            }
            ConfirmChoice::No => {
                self.confirm.close();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }
        }
    }

    fn try_palette_click(&mut self, event_loop: &ActiveEventLoop) {
        let layout = self.current_layout();
        let win_w = layout.title.w;
        let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
        let modal = self.palette.modal_rect(win_w, win_h);
        let pad = 14.0;
        let input_h = 48.0;
        let input_bottom = modal.y + pad + 4.0 + input_h;
        let btn_h = 36.0;
        let gap = 6.0;
        let mut y = input_bottom + 12.0;
        let filtered = filter_commands(&self.commands, &self.palette.query);
        let x = self.cursor.x;
        let cy = self.cursor.y;
        // Click on input: stay open, do nothing
        if x >= modal.x + pad
            && x <= modal.x + modal.w - pad
            && cy >= modal.y + pad
            && cy <= input_bottom
        {
            return;
        }
        for (i, &idx) in filtered.iter().enumerate().take(6) {
            if cy >= y && cy <= y + btn_h && x >= modal.x + pad && x <= modal.x + modal.w - pad {
                let action = self.commands[idx].action;
                self.palette.close();
                self.run_action(event_loop, action);
                return;
            }
            y += btn_h + gap;
        }
    }

    fn run_action(&mut self, event_loop: &ActiveEventLoop, action: CommandAction) {
        match action {
            CommandAction::OpenSettings => {
                self.palette.close();
                self.help.close();
                self.notes.close();
                self.rename.close();
                self.settings.open();
            }
            CommandAction::OpenHelp => {
                self.palette.close();
                self.settings.close();
                self.notes.close();
                self.rename.close();
                self.help.open_help();
            }
            CommandAction::OpenPalette => {
                self.settings.close();
                self.help.close();
                self.notes.close();
                self.rename.close();
                self.palette.open_palette();
            }
            CommandAction::OpenNotes => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.workspace_ui.close();
                self.transfer.close();
                self.rename.close();
                self.notes.open();
            }
            CommandAction::OpenWorkspace => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.notes.close();
                self.transfer.close();
                self.rename.close();
                self.workspace_ui.open();
            }
            CommandAction::RefreshWorkspace => {
                // Soft no-op when closed (product RefreshWorkspaceMsg).
                if self.workspace_ui.open {
                    self.workspace_ui.refresh();
                }
            }
            CommandAction::CycleWorkspaceStatus => {
                if !self.workspace_ui.open {
                    self.palette.close();
                    self.settings.close();
                    self.help.close();
                    self.notes.close();
                    self.transfer.close();
                    self.rename.close();
                    self.workspace_ui.open();
                }
                self.workspace_ui.cycle_status();
            }
            CommandAction::OpenTransferSend => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.notes.close();
                self.workspace_ui.close();
                self.rename.close();
                self.transfer.open_send();
            }
            CommandAction::OpenTransferReceive => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.notes.close();
                self.workspace_ui.close();
                self.rename.close();
                self.transfer.open_receive();
            }
            CommandAction::RenameTab => {
                self.open_rename(RenameTarget::Tab);
            }
            CommandAction::RenamePane => {
                self.open_rename(RenameTarget::Pane);
            }
            CommandAction::NewTab => self.new_tab(),
            CommandAction::CloseTab | CommandAction::ClosePane => {
                self.close_pane_or_tab(event_loop);
            }
            CommandAction::NextTab => {
                self.session.next_tab();
            }
            CommandAction::PrevTab => {
                self.session.prev_tab();
            }
            CommandAction::SplitRight => {
                self.split_pane(SplitAxis::Vertical);
            }
            CommandAction::SplitDown => {
                self.split_pane(SplitAxis::Horizontal);
            }
            CommandAction::FocusLeft => self.focus_dir(FocusDir::Left),
            CommandAction::FocusRight => self.focus_dir(FocusDir::Right),
            CommandAction::FocusUp => self.focus_dir(FocusDir::Up),
            CommandAction::FocusDown => self.focus_dir(FocusDir::Down),
            CommandAction::ToggleRain => {
                self.settings.prefs.rain = !self.settings.prefs.rain;
            }
            CommandAction::ToggleLens => {
                self.settings.prefs.lens = !self.settings.prefs.lens;
            }
            CommandAction::ToggleCaffeine => {
                let _ = self.caffeine.toggle();
            }
            CommandAction::Caffeine15m => {
                let _ = self.caffeine.activate(Some(Duration::from_secs(15 * 60)));
            }
            CommandAction::Caffeine1h => {
                let _ = self.caffeine.activate(Some(Duration::from_secs(60 * 60)));
            }
            CommandAction::CaffeineOff => {
                self.caffeine.deactivate();
            }
            CommandAction::NewWindow => {
                if let Err(e) = spawn_new_window() {
                    eprintln!("suzuri-chrome: new window failed: {e}");
                    self.toast.show(format!("New window failed: {e}"));
                }
            }
            CommandAction::Quit => self.request_quit(event_loop),
        }
    }

    fn new_tab(&mut self) {
        let layout = self.current_layout();
        let (cols, rows) = renderer::terminal_grid_size(
            layout.panes.first().map(|p| &p.cells).unwrap_or(&layout.cells),
            self.metrics.inset(),
        );
        let (_tid, pane_id) = self.session.new_tab(cols, rows);
        let rt = spawn_pane_runtime(cols, rows, &mut self.session, pane_id);
        self.runtimes.insert(pane_id, rt);
        self.warp_focused = true;
        self.terminal_focused = false;
    }

    fn split_pane(&mut self, axis: SplitAxis) {
        let layout = self.current_layout();
        // New pane starts small; use a reasonable grid from half workspace.
        let (cols, rows) = {
            let mut half = layout.workspace;
            match axis {
                SplitAxis::Vertical => half.w *= 0.5,
                SplitAxis::Horizontal => half.h *= 0.5,
            }
            // Approximate cells region
            let inset = self.metrics.inset();
            let strip = self.metrics.input_strip_h;
            let cells = crate::layout::Rect::new(
                half.x + inset,
                half.y + inset,
                (half.w - inset * 2.0).max(40.0),
                (half.h - inset * 2.0 - strip).max(40.0),
            );
            renderer::terminal_grid_size(&cells, inset)
        };
        if let Some(new_id) = self.session.split_focused(axis, cols, rows) {
            let rt = spawn_pane_runtime(cols, rows, &mut self.session, new_id);
            self.runtimes.insert(new_id, rt);
            self.warp_focused = true;
            self.terminal_focused = false;
            self.sync_grids_to_panes();
        }
    }

    fn focus_dir(&mut self, dir: FocusDir) {
        let layout = self.current_layout();
        self.session
            .focus_neighbor(dir, layout.workspace, self.metrics.stack());
    }

    fn close_pane_or_tab(&mut self, event_loop: &ActiveEventLoop) {
        match self.session.close_focused_pane_or_tab() {
            CloseOutcome::QuitApp => event_loop.exit(),
            CloseOutcome::ClosedPanes(ids) => {
                for id in ids {
                    self.runtimes.remove(&id);
                }
            }
            CloseOutcome::Animating | CloseOutcome::None => {}
        }
    }

    /// When a shell process exits (`exit`, EOF, crash), jelly-close its pane.
    fn reap_dead_shells(&mut self) {
        let mut dead = Vec::new();
        for (id, rt) in self.runtimes.iter_mut() {
            if let Some(pty) = &mut rt.pty {
                if !pty.is_alive() {
                    // Drain any final output (logout banner, etc.)
                    let chunk = pty.try_read();
                    if !chunk.is_empty() {
                        if let Some(grid) = self.session.grid_mut(*id) {
                            rt.ansi.feed(grid, chunk.as_bytes());
                        }
                    }
                    dead.push(*id);
                }
            }
        }
        for id in dead {
            // Drop the dead PTY handle but keep the pane until anim ends.
            if let Some(rt) = self.runtimes.get_mut(&id) {
                rt.pty = None;
            }
            self.session.begin_close_pane(id);
        }
    }

    fn finish_closed_panes(&mut self, event_loop: &ActiveEventLoop, finished: &[u64]) {
        for id in finished {
            self.runtimes.remove(id);
        }
        if self.session.is_empty() {
            event_loop.exit();
        }
    }

    /// Update chip hover from current pointer + layout (scale / press light).
    fn update_chip_hover(&mut self) {
        let layout = self.current_layout();
        let x = self.cursor.x;
        let y = self.cursor.y;
        let mut hit = None;
        if self.pointer_inside {
            if layout.logo.contains(x, y) {
                hit = Some(ChipId::Logo);
            } else if layout.caffeine.contains(x, y) {
                hit = Some(ChipId::Caffeine);
            } else if layout.tab_new.contains(x, y) {
                hit = Some(ChipId::NewTab);
            } else {
                for (i, chip) in layout.tab_chips.iter().enumerate() {
                    if chip.contains(x, y) {
                        hit = Some(ChipId::Tab(i));
                        break;
                    }
                }
            }
        }
        // Position freezes on leave so the green light can fade out in place.
        self.chip_ui.set_hover(hit, (x, y));
    }

    /// While a pane is on the alt screen (vim, grok, etc.), route keys to PTY;
    /// when it leaves, restore command-line focus.
    fn sync_focus_for_alt_screen(&mut self) {
        let id = self.session.focus_pane_id();
        let alt = self
            .runtimes
            .get(&id)
            .map(|rt| rt.ansi.on_alt_screen())
            .unwrap_or(false);
        if alt {
            self.terminal_focused = true;
            self.warp_focused = false;
        } else if self.terminal_focused {
            // Left alt screen — back to command line unless user explicitly… 
            // (we only auto-clear when we auto-set; keep simple: always restore)
            self.terminal_focused = false;
            self.warp_focused = true;
        }
    }

    fn hit_at_cursor(&self) -> HitTarget {
        let layout = self.current_layout();
        hit_test(
            &layout,
            &self.metrics,
            self.cursor.x,
            self.cursor.y,
            is_mac(),
        )
    }

    /// Map pointer → absolute cell pos in the focused pane's cell well.
    fn term_cell_at_cursor(&self) -> Option<CellPos> {
        if self.overlay_open() {
            return None;
        }
        let layout = self.current_layout();
        let focus = self.session.focus_pane_id();
        let pl = layout.panes.iter().find(|p| p.pane_id == focus)?;
        let cells = pl.cells;
        let x = self.cursor.x;
        let y = self.cursor.y;
        if !cells.contains(x, y) {
            return None;
        }
        let pane = self.session.panes.get(&focus)?;
        let grid = &pane.grid;
        let col = ((x - cells.x) / CELL_W).floor() as i32;
        let row = ((y - cells.y) / CELL_H).floor() as i32;
        let col = col.clamp(0, grid.cols().saturating_sub(1) as i32) as u16;
        let row = row.clamp(0, grid.rows().saturating_sub(1) as i32) as u16;
        let abs = grid.viewport_to_abs(row);
        Some(clamp_pos(grid, col, abs))
    }

    /// Product open-link modifier: macOS Cmd (or Ctrl), elsewhere Ctrl (+no Alt/Shift).
    fn link_open_mod_held(&self) -> bool {
        if is_mac() {
            // darwin: meta || (ctrl && !meta) → either Super or Control
            self.modifiers.super_key() || self.modifiers.control_key()
        } else {
            self.modifiers.control_key()
                && !self.modifiers.alt_key()
                && !self.modifiers.shift_key()
        }
    }

    /// Link under the pointer in the focused terminal (URL + cell span), if any.
    fn link_at_cursor(&self) -> Option<(String, LinkHoverSpan)> {
        if self.overlay_open() {
            return None;
        }
        let pos = self.term_cell_at_cursor()?;
        let id = self.session.focus_pane_id();
        let pane = self.session.panes.get(&id)?;
        let line = pane.grid.line_text_abs(pos.abs_row);
        let span = link_span_at_col(&line, pos.col as usize)?;
        let col0 = span.x0.min(u16::MAX as usize) as u16;
        let col1 = span.x1.min(u16::MAX as usize) as u16;
        if col1 <= col0 {
            return None;
        }
        Some((
            span.url,
            LinkHoverSpan {
                col0,
                col1,
                abs_row: pos.abs_row,
            },
        ))
    }

    /// URL under the pointer in the focused terminal, if any.
    fn link_url_at_cursor(&self) -> Option<String> {
        self.link_at_cursor().map(|(url, _)| url)
    }

    /// Refresh `hovered_link` / span + hand cursor when the pointer is over a terminal URL.
    fn update_link_hover(&mut self) {
        let hit = if self.selecting_term {
            None
        } else {
            self.link_at_cursor()
        };
        match hit {
            Some((url, span)) => {
                self.hovered_link = Some(url);
                self.hovered_link_span = Some(span);
            }
            None => {
                self.hovered_link = None;
                self.hovered_link_span = None;
            }
        }
        let want_hand = self.hovered_link.is_some();
        if want_hand != self.link_cursor_on {
            self.link_cursor_on = want_hand;
            if let Some(w) = &self.window {
                w.set_cursor(if want_hand {
                    CursorIcon::Pointer
                } else {
                    CursorIcon::Default
                });
            }
        }
    }

    fn clear_link_hover(&mut self) {
        self.hovered_link = None;
        self.hovered_link_span = None;
        if self.link_cursor_on {
            self.link_cursor_on = false;
            if let Some(w) = &self.window {
                w.set_cursor(CursorIcon::Default);
            }
        }
    }

    /// Consecutive terminal click count (1 = cell, 2 = word, 3 = line, then wraps to 1).
    /// Same cell when within 500 ms and |dcol|≤1, |drow|≤1 (product multiClick).
    fn term_click_count(&mut self, pos: CellPos) -> u8 {
        let now = Instant::now();
        let count = match self.last_term_click {
            Some((t, col, abs_row, n))
                if now.duration_since(t) < Duration::from_millis(TERM_MULTI_CLICK_MS)
                    && (pos.col as i32 - col as i32).abs() <= 1
                    && (pos.abs_row as i64 - abs_row as i64).abs() <= 1 =>
            {
                let next = n.saturating_add(1);
                if next > 3 {
                    1
                } else {
                    next
                }
            }
            _ => 1,
        };
        self.last_term_click = Some((now, pos.col, pos.abs_row, count));
        count
    }

    /// Apply single / word / line selection at `pos` for the given multi-click count.
    /// Always leaves `selecting_term` so drag continues in that mode.
    fn apply_term_click_selection(&mut self, pos: CellPos, click_count: u8) {
        match click_count {
            1 => {
                self.term_selection.begin(pos);
            }
            2 => {
                let id = self.session.focus_pane_id();
                if let Some(pane) = self.session.panes.get(&id) {
                    self.term_selection.select_word(&pane.grid, pos);
                } else {
                    self.term_selection.select_cell(pos);
                }
            }
            _ => {
                let id = self.session.focus_pane_id();
                let cols = self
                    .session
                    .panes
                    .get(&id)
                    .map(|p| p.grid.cols())
                    .unwrap_or(1);
                self.term_selection.select_line(pos.abs_row, cols);
            }
        }
        self.selecting_term = true;
    }

    fn copy_selection_if_any(&mut self) {
        if self.term_selection.is_empty() {
            return;
        }
        let id = self.session.focus_pane_id();
        let Some(pane) = self.session.panes.get(&id) else {
            return;
        };
        let text = self.term_selection.text(&pane.grid);
        if text.is_empty() {
            return;
        }
        if let Some(cb) = &mut self.clipboard {
            if cb.set_text(text).is_ok() {
                self.toast.show("Copied");
            }
        }
    }

    /// ⌘C / Ctrl+C while transfer is open: prefer ticket when present.
    /// Falls back to terminal selection when transfer is closed (caller gates).
    fn copy_transfer_ticket_if_any(&mut self) -> bool {
        let Some(ticket) = self.transfer.copy_ticket() else {
            return false;
        };
        if let Some(cb) = &mut self.clipboard {
            let _ = cb.set_text(ticket);
        }
        // Product host toast: "ticket copied"; title-case for the frost chip.
        self.toast.show("Ticket copied");
        true
    }

    /// True if the pointer is over any open modal card (input, buttons, body).
    fn pointer_in_open_modal(&self) -> bool {
        let layout = self.current_layout();
        let win_w = layout.title.w;
        let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
        let x = self.cursor.x;
        let y = self.cursor.y;
        if self.settings.visible() && self.settings.animated_modal_rect(win_w, win_h).contains(x, y)
        {
            return true;
        }
        if self.palette.visible() && self.palette.modal_rect(win_w, win_h).contains(x, y) {
            return true;
        }
        if self.help.visible() {
            let r = crate::renderer::overlay_modal_rect_pub(win_w, win_h, 640.0, 360.0);
            if r.contains(x, y) {
                return true;
            }
        }
        if self.confirm.visible()
            && self
                .confirm
                .animated_modal_rect(win_w, win_h)
                .contains(x, y)
        {
            return true;
        }
        if self.splash.visible() {
            let r = SplashState::modal_rect(win_w, win_h);
            if r.contains(x, y) {
                return true;
            }
        }
        if self.notes.visible() && self.notes.animated_modal_rect(win_w, win_h).contains(x, y) {
            return true;
        }
        if self.workspace_ui.visible()
            && self.workspace_ui.animated_modal_rect(win_w, win_h).contains(x, y)
        {
            return true;
        }
        if self.transfer.visible() && self.transfer.animated_modal_rect(win_w, win_h).contains(x, y)
        {
            return true;
        }
        if self.rename.visible() && self.rename.modal_rect(win_w, win_h).contains(x, y) {
            return true;
        }
        false
    }

    /// Activate a control (runs on **mouse-up** when press & release share a target).
    fn handle_activation(&mut self, event_loop: &ActiveEventLoop, target: HitTarget) {
        // Traffic lights always work
        if matches!(
            target,
            HitTarget::Close | HitTarget::Minimize | HitTarget::Zoom
        ) {
            // fall through to match below
        } else if self.overlay_open() || self.notes.visible() || self.workspace_ui.visible()
            || self.transfer.visible()
            || self.palette.visible()
            || self.help.visible()
            || self.confirm.visible()
            || self.splash.visible()
            || self.settings.visible()
            || self.rename.visible()
        {
            // Click **inside** any modal: keep open (don't steal focus to terminal).
            if self.pointer_in_open_modal() {
                // Confirm is topmost: yes/no buttons only.
                if self.confirm.open {
                    let layout = self.current_layout();
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    if let Some(choice) =
                        self.confirm
                            .hit_button(self.cursor.x, self.cursor.y, win_w, win_h)
                    {
                        self.apply_confirm_choice(event_loop, choice);
                    }
                    return;
                }
                // Splash: any click on the card continues (Enter / click continue).
                if self.splash.open {
                    self.dismiss_splash();
                } else if self.palette.open {
                    self.try_palette_click(event_loop);
                } else if self.notes.open {
                    let layout = self.current_layout();
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    self.notes
                        .try_click(self.cursor.x, self.cursor.y, win_w, win_h);
                } else if self.workspace_ui.open {
                    let layout = self.current_layout();
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    self.workspace_ui
                        .try_click(self.cursor.x, self.cursor.y, win_w, win_h);
                } else if self.transfer.visible() && !self.transfer.ticket.is_empty() {
                    // Click progress card / ticket chip → copy ticket (product parity).
                    let _ = self.copy_transfer_ticket_if_any();
                }
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            // Click outside → dismiss
            self.close_all_overlays();
            if let Some(w) = &self.window {
                w.request_redraw();
            }
            return;
        }

        match target {
            HitTarget::Close => self.request_quit(event_loop),
            HitTarget::Minimize => {
                if let Some(w) = &self.window {
                    w.set_minimized(true);
                }
            }
            HitTarget::Zoom => {
                // Green traffic light: true fullscreen (not maximize/zoom).
                // Title-bar double-click still toggles maximized fill.
                if let Some(w) = &self.window {
                    if w.fullscreen().is_some() {
                        w.set_fullscreen(None);
                    } else {
                        w.set_fullscreen(Some(Fullscreen::Borderless(None)));
                    }
                }
            }
            // Title drag is started on press (OS requirement), not here.
            HitTarget::TitleDrag => {}
            HitTarget::Tab(i) => {
                if let Some(tab) = self.session.tabs.get(i) {
                    let id = tab.id;
                    self.session.select_tab(id);
                    self.warp_focused = true;
                    self.terminal_focused = false;
                }
            }
            HitTarget::NewTab => self.new_tab(),
            HitTarget::Caffeine => {
                let _ = self.caffeine.toggle();
            }
            HitTarget::Settings => {
                self.help.close();
                self.palette.close();
                self.notes.close();
                self.settings.toggle();
            }
            HitTarget::WarpBar(pane_id) => {
                self.session.set_focus_pane(pane_id);
                self.warp_focused = true;
                self.terminal_focused = false;
            }
            HitTarget::Terminal(pane_id) => {
                self.session.set_focus_pane(pane_id);
                let alt = self
                    .runtimes
                    .get(&pane_id)
                    .map(|rt| rt.ansi.on_alt_screen())
                    .unwrap_or(false);
                if alt {
                    self.terminal_focused = true;
                    self.warp_focused = false;
                } else {
                    self.warp_focused = true;
                    self.terminal_focused = false;
                }
            }
            HitTarget::None => {}
        }

        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn handle_key(&mut self, event_loop: &ActiveEventLoop, event: &winit::event::KeyEvent) {
        if !event.state.is_pressed() {
            return;
        }

        if matches!(event.logical_key, Key::Named(NamedKey::Escape)) {
            // Confirm is topmost: Esc / N dismisses without quitting.
            if self.confirm.open {
                self.apply_confirm_choice(event_loop, ConfirmChoice::No);
                return;
            }
            // Workspace: Esc cancels new-channel compose before closing.
            if self.workspace_ui.open
                && self.workspace_ui.mode
                    != crate::workspace_ui::ComposeMode::Message
            {
                self.workspace_ui.cancel_mode();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            if self.overlay_open()
                || self.settings.visible()
                || self.palette.visible()
                || self.help.visible()
                || self.confirm.visible()
                || self.splash.visible()
                || self.notes.visible()
                || self.workspace_ui.visible()
                || self.transfer.visible()
                || self.rename.visible()
            {
                self.close_all_overlays();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            // Clear terminal selection before quitting on bare Esc.
            if !self.term_selection.is_empty() || self.selecting_term {
                self.term_selection.clear();
                self.selecting_term = false;
                self.last_term_click = None;
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            self.request_quit(event_loop);
            return;
        }

        // Confirm dialog keys: Enter / Y = yes, N = no (Esc handled above).
        if self.confirm.open {
            let choice = match &event.logical_key {
                Key::Named(NamedKey::Enter) => Some(ConfirmChoice::Yes),
                Key::Character(s) => match s.as_str() {
                    "y" | "Y" => Some(ConfirmChoice::Yes),
                    "n" | "N" => Some(ConfirmChoice::No),
                    _ => None,
                },
                _ => None,
            };
            if let Some(c) = choice {
                self.apply_confirm_choice(event_loop, c);
            }
            return;
        }

        let super_or_ctrl = self.modifiers.super_key() || self.modifiers.control_key();
        let shift = self.modifiers.shift_key();
        let alt = self.modifiers.alt_key();

        // First-run splash: Enter / Space continue (Esc handled above).
        if self.splash.open {
            match &event.logical_key {
                Key::Named(NamedKey::Enter) | Key::Named(NamedKey::Space) => {
                    self.dismiss_splash();
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                    return;
                }
                Key::Character(s) if s.as_str() == " " => {
                    self.dismiss_splash();
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                    return;
                }
                _ => {
                    // Swallow other keys while splash is up (product exclusive modal).
                    return;
                }
            }
        }

        // Modal text surfaces (notes / workspace / transfer) — not terminal.
        if !super_or_ctrl {
            if self.notes.open {
                match &event.logical_key {
                    Key::Named(NamedKey::Backspace) => {
                        self.notes.backspace();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::ArrowLeft) => {
                        self.notes.move_cursor(-1);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::ArrowRight) => {
                        self.notes.move_cursor(1);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::Tab) => {
                        self.notes.cycle_focus(shift);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::Enter) => {
                        self.notes.insert_char('\n');
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Character(s) => {
                        for ch in s.chars() {
                            if !ch.is_control() {
                                self.notes.insert_char(ch);
                            }
                        }
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    _ => {}
                }
            }
            if self.workspace_ui.open {
                match &event.logical_key {
                    Key::Named(NamedKey::Backspace) => {
                        self.workspace_ui.backspace();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::Enter) => {
                        self.workspace_ui.send();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::Tab) => {
                        self.workspace_ui
                            .cycle_channel(if shift { -1 } else { 1 });
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::ArrowUp) => {
                        self.workspace_ui.scroll_up(1);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::ArrowDown) => {
                        self.workspace_ui.scroll_down(1);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Character(s) => {
                        for ch in s.chars() {
                            if !ch.is_control() {
                                self.workspace_ui.insert_char(ch);
                            }
                        }
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    _ => {}
                }
            }
            if self.transfer.open {
                match &event.logical_key {
                    Key::Named(NamedKey::Backspace) => {
                        self.transfer.backspace();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::Enter) => {
                        self.transfer.submit();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Character(s) => {
                        for ch in s.chars() {
                            if !ch.is_control() {
                                self.transfer.insert_char(ch);
                            }
                        }
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    _ => {}
                }
            }
            if self.rename.open {
                match &event.logical_key {
                    Key::Named(NamedKey::Backspace) => {
                        self.rename.backspace();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::Enter) => {
                        let target = self.rename.target;
                        let name = self.rename.commit();
                        self.apply_rename(target, &name);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Character(s) => {
                        self.rename.insert_str(s);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    _ => {}
                }
            }
        }

        // Global shortcuts
        if super_or_ctrl {
            // Notes body undo/redo (⌘Z / ⇧⌘Z) while notes overlay is open.
            if self.notes.open {
                if let Key::Character(ref s) = event.logical_key {
                    match s.as_str() {
                        "z" | "Z" if shift => {
                            let _ = self.notes.redo();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        "z" | "Z" => {
                            let _ = self.notes.undo();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        _ => {}
                    }
                }
            }
            // Workspace: Ctrl+R refresh, Ctrl+Shift+A cycle presence (while open).
            if self.workspace_ui.open {
                if let Key::Character(ref s) = event.logical_key {
                    match s.as_str() {
                        "r" | "R" if !shift => {
                            self.workspace_ui.refresh();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        "a" | "A" if shift => {
                            self.workspace_ui.cycle_status();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        "n" | "N" if !shift => {
                            self.workspace_ui.begin_new_channel();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        "u" | "U" if !shift => {
                            self.workspace_ui.begin_attach();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        _ => {}
                    }
                }
            }
            if let Key::Character(ref s) = event.logical_key {
                let ch = s.as_str();
                match ch {
                    "k" | "K" if !shift => {
                        self.help.close();
                        self.settings.close();
                        self.notes.close();
                        self.palette.toggle();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "/" if !shift => {
                        self.palette.close();
                        self.settings.close();
                        self.notes.close();
                        self.help.toggle();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "," => {
                        self.palette.close();
                        self.help.close();
                        self.notes.close();
                        self.settings.toggle();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "m" | "M" if shift => {
                        self.palette.close();
                        self.settings.close();
                        self.help.close();
                        self.notes.toggle();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "t" | "T" if !shift => {
                        self.new_tab();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "w" | "W" if !shift => {
                        self.close_pane_or_tab(event_loop);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "d" | "D" if shift => {
                        self.split_pane(SplitAxis::Vertical);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "e" | "E" if shift => {
                        self.split_pane(SplitAxis::Horizontal);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    // Ctrl+Shift+N (and ⌘⇧N on mac) — new OS window process.
                    "n" | "N" if shift => {
                        self.run_action(event_loop, CommandAction::NewWindow);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "]" if shift => {
                        self.session.next_tab();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "[" if shift => {
                        self.session.prev_tab();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "v" | "V" if !shift => {
                        self.paste_clipboard();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "c" | "C" if !shift => {
                        // Transfer open + ticket → copy ticket; else terminal selection.
                        if self.transfer.open || self.transfer.visible() {
                            if self.copy_transfer_ticket_if_any() {
                                if let Some(w) = &self.window {
                                    w.request_redraw();
                                }
                                return;
                            }
                        }
                        self.copy_selection_if_any();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    _ => {}
                }
            }
            // ⌥⌘ arrows — pane focus
            if alt {
                if let Key::Named(nk) = &event.logical_key {
                    let dir = match nk {
                        NamedKey::ArrowLeft => Some(FocusDir::Left),
                        NamedKey::ArrowRight => Some(FocusDir::Right),
                        NamedKey::ArrowUp => Some(FocusDir::Up),
                        NamedKey::ArrowDown => Some(FocusDir::Down),
                        _ => None,
                    };
                    if let Some(d) = dir {
                        self.focus_dir(d);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                }
            }
        }

        // F2 — rename focused pane (product keys; no modifiers).
        if !super_or_ctrl
            && !shift
            && !alt
            && !self.overlay_open()
            && matches!(event.logical_key, Key::Named(NamedKey::F2))
        {
            self.open_rename(RenameTarget::Pane);
            if let Some(w) = &self.window {
                w.request_redraw();
            }
            return;
        }

        // Palette open: filter / navigate / run
        if self.palette.open {
            let filtered = filter_commands(&self.commands, &self.palette.query);
            match &event.logical_key {
                Key::Named(NamedKey::ArrowDown) => {
                    self.palette.move_sel(1, filtered.len());
                }
                Key::Named(NamedKey::ArrowUp) => {
                    self.palette.move_sel(-1, filtered.len());
                }
                Key::Named(NamedKey::Enter) => {
                    if let Some(&idx) = filtered.get(self.palette.selected) {
                        let action = self.commands[idx].action;
                        self.palette.close();
                        self.run_action(event_loop, action);
                    }
                }
                Key::Named(NamedKey::Backspace) => {
                    self.palette.query.pop();
                    self.palette.selected = 0;
                }
                Key::Character(s) if !super_or_ctrl => {
                    self.palette.query.push_str(s);
                    self.palette.selected = 0;
                }
                _ => {
                    if let Some(text) = &event.text {
                        if !super_or_ctrl {
                            for c in text.chars() {
                                if !c.is_control() {
                                    self.palette.query.push(c);
                                }
                            }
                            self.palette.selected = 0;
                        }
                    }
                }
            }
            if let Some(w) = &self.window {
                w.request_redraw();
            }
            return;
        }

        if self.settings.open {
            if let Key::Character(ref s) = event.logical_key {
                if self.settings.handle_hotkey(s.as_str()) {
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
            }
            return;
        }

        if self.help.open {
            return;
        }

        // Terminal focus → PTY
        if self.terminal_focused {
            let id = self.session.focus_pane_id();
            if let Some(rt) = self.runtimes.get_mut(&id) {
                if let Some(pty) = &mut rt.pty {
                    match &event.logical_key {
                        Key::Named(NamedKey::Enter) => {
                            let _ = pty.write_all(b"\r");
                        }
                        Key::Named(NamedKey::Backspace) => {
                            let _ = pty.write_all(&[0x7f]);
                        }
                        Key::Named(NamedKey::Tab) => {
                            let _ = pty.write_all(b"\t");
                        }
                        Key::Named(NamedKey::ArrowUp) => {
                            let _ = pty.write_all(b"\x1b[A");
                        }
                        Key::Named(NamedKey::ArrowDown) => {
                            let _ = pty.write_all(b"\x1b[B");
                        }
                        Key::Named(NamedKey::ArrowRight) => {
                            let _ = pty.write_all(b"\x1b[C");
                        }
                        Key::Named(NamedKey::ArrowLeft) => {
                            let _ = pty.write_all(b"\x1b[D");
                        }
                        Key::Character(s) if !super_or_ctrl => {
                            let _ = pty.write_all(s.as_bytes());
                        }
                        _ => {
                            if let Some(text) = &event.text {
                                if !super_or_ctrl {
                                    let _ = pty.write_all(text.as_bytes());
                                }
                            }
                        }
                    }
                    return;
                }
            }
        }

        // Warp / local input
        if self.warp_focused || !self.terminal_focused {
            match &event.logical_key {
                Key::Named(NamedKey::Backspace) => self.session.backspace(),
                Key::Named(NamedKey::Enter) => self.submit_line(),
                Key::Named(NamedKey::ArrowUp) => self.session.history_up(),
                Key::Named(NamedKey::ArrowDown) => self.session.history_down(),
                Key::Character(s) => {
                    if super_or_ctrl {
                        return;
                    }
                    for c in s.chars() {
                        self.session.type_char(c);
                    }
                }
                _ => {
                    if let Some(text) = &event.text {
                        if !super_or_ctrl {
                            for c in text.chars() {
                                if !c.is_control() {
                                    self.session.type_char(c);
                                }
                            }
                        }
                    }
                }
            }
        }

        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn submit_line(&mut self) {
        let line = self.session.draft().trim_end().to_string();
        if line.is_empty() {
            return;
        }
        self.session.draft_mut().clear();
        self.submit_line_text(&line);
    }

    /// Snapshot for MCP bridge: tabs + viewport/live lines + echo/blocks + PTY.
    fn publish_bridge_status(&mut self) {
        let mut extras: Vec<PaneSnapExtra> = Vec::new();
        for (pane_id, rt) in &mut self.runtimes {
            let alive = rt.pty.as_mut().map(|p| p.is_alive()).unwrap_or(false);
            let alt = rt.ansi.on_alt_screen();
            let (echo_armed, echo_cmd, echo_phase) = rt.echo.status();
            let tail = if rt.pty_tail.is_empty() {
                String::new()
            } else {
                format!("{:?}", rt.pty_tail)
            };
            extras.push(PaneSnapExtra {
                pane_id: *pane_id,
                alive,
                alt_screen: alt,
                shell: String::new(),
                pty_tail: tail,
                echo_armed,
                echo_cmd,
                echo_phase,
                blocks: rt.blocks.recent_blocks(),
                history: rt.blocks.history_meta(),
            });
        }
        let snap = chrome_status::snap_from_session(&self.session, &extras);
        self.status_publisher.tick(&snap);
    }

    /// Host / MCP submit path: run `line` as if entered in the warp bar.
    ///
    /// Product parity: inject command block into scrollback, arm echo filter,
    /// then write the line to the PTY.
    fn submit_line_text(&mut self, line: &str) {
        let line = line.trim_end();
        if line.is_empty() {
            return;
        }
        let line = line.to_string();
        self.session.push_history(&line);
        self.session.apply_cwd_after_command(&line);
        let id = self.session.focus_pane_id();
        let cwd_display = self.session.display_cwd();

        // Host command block + echo arm (even for mock path).
        if let Some(rt) = self.runtimes.get_mut(&id) {
            if let Some(grid) = self.session.grid_mut(id) {
                rt.blocks.push_block(&line, grid, &cwd_display);
            }
            rt.echo.arm(&line);
        } else if let Some(grid) = self.session.grid_mut(id) {
            // No runtime — still paint a block for visibility.
            let mut log = CmdBlockLog::new();
            log.push_block(&line, grid, &cwd_display);
        }

        let used_pty = if let Some(rt) = self.runtimes.get_mut(&id) {
            if let Some(pty) = &mut rt.pty {
                let mut buf = line.as_bytes().to_vec();
                buf.push(b'\n');
                let _ = pty.write_all(&buf);
                true
            } else {
                false
            }
        } else {
            false
        };

        if used_pty {
            if let Some(p) = self.session.panes.get_mut(&id) {
                p.busy = true;
            }
        } else {
            *self.session.draft_mut() = line;
            self.session.submit_draft_mock();
        }
    }
}

impl ApplicationHandler for ChromeApp {
    fn resumed(&mut self, event_loop: &ActiveEventLoop) {
        if self.window.is_some() {
            return;
        }

        let attrs = WindowAttributes::default()
            .with_title("suzuri · chrome")
            .with_inner_size(LogicalSize::new(1120.0, 740.0))
            .with_min_inner_size(LogicalSize::new(720.0, 440.0))
            .with_decorations(false)
            .with_transparent(true)
            .with_resizable(true);

        let window = Arc::new(
            event_loop
                .create_window(attrs)
                .expect("create window"),
        );

        #[cfg(target_os = "macos")]
        crate::macos_window::configure_rounded_window(&window, 16.0);

        let renderer = pollster::block_on(Renderer::new(window.clone()));
        self.metrics = renderer.metrics();
        self.window = Some(window);
        self.renderer = Some(renderer);
        self.sync_grids_to_panes();

        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn about_to_wait(&mut self, _event_loop: &ActiveEventLoop) {
        self.drain_all_ptys();
    }

    fn device_event(
        &mut self,
        _event_loop: &ActiveEventLoop,
        _device_id: winit::event::DeviceId,
        event: DeviceEvent,
    ) {
        if let DeviceEvent::MouseMotion { delta: (dx, dy) } = event {
            if !self.pointer_inside {
                if let Some(w) = &self.window {
                    let s = w.inner_size();
                    let scale = w.scale_factor();
                    self.cursor = LogicalPosition::new(
                        (s.width as f64 / scale) as f32 * 0.5,
                        (s.height as f64 / scale) as f32 * 0.5,
                    );
                }
            }
            self.pointer_inside = true;
            let scale = self
                .window
                .as_ref()
                .map(|w| w.scale_factor())
                .unwrap_or(1.0) as f32;
            self.cursor.x = (self.cursor.x + dx as f32 / scale).max(0.0);
            self.cursor.y = (self.cursor.y + dy as f32 / scale).max(0.0);
            if let Some(w) = &self.window {
                let (lw, lh) = {
                    let s = w.inner_size();
                    let sc = w.scale_factor();
                    (s.width as f32 / sc as f32, s.height as f32 / sc as f32)
                };
                self.cursor.x = self.cursor.x.clamp(0.0, lw);
                self.cursor.y = self.cursor.y.clamp(0.0, lh);
            }
            let _ = PhysicalPosition::new(0.0, 0.0);
        }
    }

    fn window_event(
        &mut self,
        event_loop: &ActiveEventLoop,
        _id: WindowId,
        event: WindowEvent,
    ) {
        match event {
            WindowEvent::CloseRequested => self.request_quit(event_loop),

            WindowEvent::ModifiersChanged(mods) => {
                self.modifiers = mods.state();
            }

            WindowEvent::CursorMoved { position, .. } => {
                let scale = self
                    .window
                    .as_ref()
                    .map(|w| w.scale_factor())
                    .unwrap_or(1.0);
                let logical: LogicalPosition<f64> = position.to_logical(scale);
                self.cursor = LogicalPosition::new(logical.x as f32, logical.y as f32);
                self.pointer_inside = true;
                if self.selecting_term {
                    if let Some(pos) = self.term_cell_at_cursor() {
                        let id = self.session.focus_pane_id();
                        if let Some(pane) = self.session.panes.get(&id) {
                            self.term_selection.update_drag(&pane.grid, pos);
                        } else {
                            self.term_selection.update(pos);
                        }
                    }
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
                self.update_link_hover();
            }

            WindowEvent::CursorLeft { .. } => {
                self.pointer_inside = false;
                self.clear_link_hover();
            }

            WindowEvent::MouseInput {
                state: ElementState::Pressed,
                button: MouseButton::Left,
                ..
            } => {
                self.chip_ui.pressed = true;
                let hit = self.hit_at_cursor();
                self.press_hit = Some(hit);
                // Cmd/Ctrl+click on a terminal URL → open browser (no selection).
                if matches!(hit, HitTarget::Terminal(_))
                    && !self.overlay_open()
                    && self.link_open_mod_held()
                {
                    if let Some(url) = self.link_url_at_cursor() {
                        open_url_in_browser(&url);
                        self.press_hit = None;
                        self.selecting_term = false;
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                }
                // Terminal selection: single = cell drag, double = word, triple = line
                // (then drag keeps that mode via update_drag).
                if matches!(hit, HitTarget::Terminal(_)) && !self.overlay_open() {
                    if let Some(pos) = self.term_cell_at_cursor() {
                        let clicks = self.term_click_count(pos);
                        self.apply_term_click_selection(pos, clicks);
                    }
                } else if !matches!(hit, HitTarget::Terminal(_)) {
                    self.term_selection.clear();
                    self.selecting_term = false;
                    self.last_term_click = None;
                }
                // Empty title-bar / nav chrome (not chips): drag, or double-click zoom.
                // macOS: double-click title bar = zoom to fill (maximize), not fullscreen
                // (green button is separate). Same maximize toggle on other platforms.
                if hit == HitTarget::TitleDrag {
                    let x = self.cursor.x;
                    let y = self.cursor.y;
                    let now = Instant::now();
                    let is_double = self
                        .last_title_click
                        .map(|(t, lx, ly)| {
                            now.duration_since(t) < Duration::from_millis(450)
                                && (x - lx).abs() < 6.0
                                && (y - ly).abs() < 6.0
                        })
                        .unwrap_or(false);
                    if is_double {
                        self.last_title_click = None;
                        self.press_hit = None; // cancel activation/drag path
                        if let Some(w) = &self.window {
                            w.set_maximized(!w.is_maximized());
                        }
                    } else {
                        self.last_title_click = Some((now, x, y));
                        // Window drag must start on press (platform).
                        if let Some(w) = &self.window {
                            let _ = w.drag_window();
                        }
                    }
                } else {
                    self.last_title_click = None;
                }
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }

            WindowEvent::MouseInput {
                state: ElementState::Released,
                button: MouseButton::Left,
                ..
            } => {
                self.chip_ui.pressed = false;
                if self.selecting_term {
                    self.term_selection.end();
                    self.selecting_term = false;
                }
                // Activate only on release, and only if still over the same target.
                // Skip chrome activation if we were selecting terminal text.
                if let Some(start) = self.press_hit.take() {
                    let end = self.hit_at_cursor();
                    if start == end
                        && start != HitTarget::TitleDrag
                        && start != HitTarget::None
                        && !matches!(start, HitTarget::Terminal(_))
                    {
                        self.handle_activation(event_loop, start);
                    } else if start == end && matches!(start, HitTarget::Terminal(_)) {
                        // Focus pane on click without killing a drag selection end.
                        self.handle_activation(event_loop, start);
                    }
                }
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }

            WindowEvent::MouseInput {
                state: ElementState::Pressed,
                button: MouseButton::Middle,
                ..
            } => {
                self.middle_press_hit = Some(self.hit_at_cursor());
            }

            WindowEvent::MouseInput {
                state: ElementState::Released,
                button: MouseButton::Middle,
                ..
            } => {
                if let Some(start) = self.middle_press_hit.take() {
                    let end = self.hit_at_cursor();
                    if start == end {
                        if let HitTarget::Tab(i) = start {
                            if let Some(tab) = self.session.tabs.get(i) {
                                let id = tab.id;
                                let removed = self.session.close_tab(id);
                                if removed.is_empty() {
                                    event_loop.exit();
                                } else {
                                    for pid in removed {
                                        self.runtimes.remove(&pid);
                                    }
                                }
                            }
                        }
                    }
                }
            }

            // Trackpad pinch → magnifying glass (grow / shrink bubble + zoom).
            WindowEvent::PinchGesture { delta, phase, .. } => {
                if self.settings.prefs.lens {
                    if matches!(phase, TouchPhase::Started | TouchPhase::Moved) {
                        // winit: positive = magnify in, negative = shrink out
                        let d = delta as f32;
                        if d.is_finite() {
                            // Scale so a normal pinch covers a useful range quickly.
                            if let Some(r) = self.renderer.as_mut() {
                                r.magnify_delta(d * 2.8);
                            }
                        }
                    }
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
            }

            WindowEvent::MouseWheel { delta, .. } => {
                // Ctrl/Cmd + scroll → same magnifier (mice / no trackpad).
                let magnify_mod =
                    self.modifiers.control_key() || self.modifiers.super_key();
                if magnify_mod && self.settings.prefs.lens {
                    let step = match delta {
                        MouseScrollDelta::LineDelta(_, y) => y * 0.18,
                        MouseScrollDelta::PixelDelta(p) => (p.y as f32 / 80.0) * 0.22,
                    };
                    if step.abs() > 1e-5 {
                        if let Some(r) = self.renderer.as_mut() {
                            r.magnify_delta(step);
                        }
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                    }
                } else {
                    let lines = match delta {
                        MouseScrollDelta::LineDelta(_, y) => {
                            if y > 0.0 {
                                3
                            } else if y < 0.0 {
                                -3
                            } else {
                                0
                            }
                        }
                        MouseScrollDelta::PixelDelta(p) => {
                            let y = p.y as f32;
                            if y > 2.0 {
                                3
                            } else if y < -2.0 {
                                -3
                            } else {
                                0
                            }
                        }
                    };
                    if lines != 0 {
                        self.session.active_grid_mut().scroll_view(lines);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                    }
                }
            }

            WindowEvent::KeyboardInput { event, .. } => {
                self.handle_key(event_loop, &event);
            }

            // OS file drop → send path when transfer send prompt is open.
            WindowEvent::HoveredFile(_) => {
                if self.transfer.accepts_file_drop() {
                    self.transfer.set_drop_hover(true);
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
            }
            WindowEvent::HoveredFileCancelled => {
                if self.transfer.drop_hover {
                    self.transfer.set_drop_hover(false);
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
            }
            WindowEvent::DroppedFile(path) => {
                // Workspace open: attach into active channel first (product drop path).
                if self.workspace_ui.open {
                    self.workspace_ui.attach_path(&path);
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                } else if self.transfer.on_path_dropped(path) {
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
            }

            WindowEvent::Resized(size) => {
                if let (Some(r), Some(w)) = (self.renderer.as_mut(), self.window.as_ref()) {
                    r.resize(size, w.scale_factor() as f32);
                }
                self.sync_grids_to_panes();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }

            WindowEvent::ScaleFactorChanged { scale_factor, .. } => {
                if let (Some(r), Some(w)) = (self.renderer.as_mut(), self.window.as_ref()) {
                    r.resize(w.inner_size(), scale_factor as f32);
                }
                self.sync_grids_to_panes();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }

            WindowEvent::CursorEntered { .. } => {
                self.pointer_inside = true;
            }

            WindowEvent::RedrawRequested => {
                self.drain_all_ptys();
                self.reap_dead_shells();
                // Auto raw-PTY focus while a fullscreen TUI owns the alt screen.
                self.sync_focus_for_alt_screen();
                // Phase 2 light IPC: host writes chrome_cmd; fail soft if absent.
                for cmd in self.control_mailbox.poll() {
                    self.run_action(event_loop, cmd.to_action());
                    if matches!(cmd, crate::control_mailbox::ControlCommand::Quit) {
                        return;
                    }
                }
                // MCP / host warp submit (chrome_submit mailbox).
                if let Some(line) = chrome_status::take_submit() {
                    self.submit_line_text(&line);
                }
                // Publish rich status for Go bridge proxy (`chrome_status.json`).
                self.publish_bridge_status();
                let dt = 1.0 / 60.0;
                self.settings.tick(dt);
                self.palette.tick(dt);
                self.help.tick(dt);
                self.confirm.tick(dt);
                self.splash.tick(dt);
                self.notes.tick(dt);
                self.workspace_ui.tick(dt);
                self.transfer.tick(dt);
                self.rename.tick(dt);
                self.toast.tick(dt);
                let _ = self.caffeine.tick();
                let tick = self.session.tick_splits(dt);
                if !tick.finished_closes.is_empty() {
                    self.finish_closed_panes(event_loop, &tick.finished_closes);
                }
                if self.session.is_empty() {
                    chrome_status::clear_status();
                    event_loop.exit();
                    return;
                }
                self.sync_grids_to_panes();
                self.update_chip_hover();
                self.chip_ui.tick(dt);

                let pty_on = self.any_pty_alive();
                let term_cursor = self.terminal_cursor_visible();
                let caret_alpha = self.input_caret_alpha();
                let layout = self.current_layout();

                if let Some(r) = self.renderer.as_mut() {
                    let pointer = self
                        .pointer_inside
                        .then_some((self.cursor.x, self.cursor.y));
                    match r.render(
                        &self.session,
                        &self.settings,
                        &self.palette,
                        &self.help,
                        &self.confirm,
                        &self.splash,
                        &self.notes,
                        &self.workspace_ui,
                        &self.transfer,
                        &self.rename,
                        &self.caffeine,
                        &self.toast,
                        &self.commands,
                        &layout,
                        pty_on,
                        term_cursor,
                        caret_alpha,
                        pointer,
                        &self.chip_ui,
                        &self.term_selection,
                        self.hovered_link_span.as_ref(),
                    ) {
                        Ok(()) => {}
                        Err(wgpu::SurfaceError::Lost | wgpu::SurfaceError::Outdated) => {
                            if let Some(w) = &self.window {
                                r.resize(w.inner_size(), w.scale_factor() as f32);
                            }
                        }
                        Err(wgpu::SurfaceError::OutOfMemory) => {
                            eprintln!("wgpu: out of memory");
                            event_loop.exit();
                        }
                        Err(e) => eprintln!("wgpu render: {e:?}"),
                    }
                }
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }

            _ => {}
        }
    }
}
