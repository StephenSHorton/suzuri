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
    window::{Fullscreen, Window, WindowAttributes, WindowId},
};

use crate::ansi::AnsiDecoder;
use crate::caffeine::Caffeine;
use crate::chrome_ui::{ChipId, ChipUi};
use crate::commands::{
    default_commands, filter_commands, CommandAction, HelpState, PaletteState,
};
use crate::input::{hit_test, is_mac, HitTarget};
use crate::layout::{FrameLayout, Metrics};
use crate::notes::NotesState;
use crate::panes::{FocusDir, SplitAxis};
use crate::pty::PtySession;
use crate::renderer::{self, Renderer};
use crate::session::{ChromeSession, CloseOutcome};
use crate::settings::SettingsState;
use crate::transfer_ui::TransferUi;
use crate::workspace_ui::WorkspaceUi;

/// Per-pane live shell state (keyed by pane id).
struct PaneRuntime {
    pty: Option<PtySession>,
    ansi: AnsiDecoder,
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
    notes: NotesState,
    workspace_ui: WorkspaceUi,
    transfer: TransferUi,
    caffeine: Caffeine,
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

        Self {
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
            notes: NotesState::new(),
            workspace_ui: WorkspaceUi::new(),
            transfer: TransferUi::new(),
            caffeine: Caffeine::new(),
            commands: default_commands(),
            started: Instant::now(),
            clipboard: arboard::Clipboard::new().ok(),
            chip_ui: ChipUi::default(),
            press_hit: None,
            middle_press_hit: None,
            last_title_click: None,
        }
    }
}

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
            }
        }
        Err(e) => {
            eprintln!("suzuri-chrome: PTY for pane {pane_id} failed ({e}) — mock");
            session.boot_mock_on_pane(pane_id);
            PaneRuntime {
                pty: None,
                ansi: AnsiDecoder::new(),
            }
        }
    }
}

impl ChromeApp {
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
                    pending.push((*id, chunk));
                }
            }
        }
        if pending.is_empty() {
            return;
        }
        for (id, chunk) in pending {
            if let Some(rt) = self.runtimes.get_mut(&id) {
                if let Some(grid) = self.session.grid_mut(id) {
                    rt.ansi.feed(grid, chunk.as_bytes());
                }
                if let Some(cwd) = rt.ansi.take_cwd() {
                    self.session.set_cwd(id, cwd);
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
            || self.notes.open
            || self.workspace_ui.open
            || self.transfer.open
    }

    fn close_all_overlays(&mut self) {
        self.settings.close();
        self.palette.close();
        self.help.close();
        self.notes.close();
        self.workspace_ui.close();
        self.transfer.close();
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
                self.settings.open();
            }
            CommandAction::OpenHelp => {
                self.palette.close();
                self.settings.close();
                self.notes.close();
                self.help.open_help();
            }
            CommandAction::OpenPalette => {
                self.settings.close();
                self.help.close();
                self.notes.close();
                self.palette.open_palette();
            }
            CommandAction::OpenNotes => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.workspace_ui.close();
                self.transfer.close();
                self.notes.open();
            }
            CommandAction::OpenWorkspace => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.notes.close();
                self.transfer.close();
                self.workspace_ui.open();
            }
            CommandAction::OpenTransferSend => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.notes.close();
                self.workspace_ui.close();
                self.transfer.open_send();
            }
            CommandAction::OpenTransferReceive => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.notes.close();
                self.workspace_ui.close();
                self.transfer.open_receive();
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
            CommandAction::Quit => event_loop.exit(),
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
            || self.settings.visible()
        {
            // Click **inside** any modal: keep open (don't steal focus to terminal).
            if self.pointer_in_open_modal() {
                // Palette option clicks / notes list handled later if needed.
                if self.palette.open {
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
            HitTarget::Close => event_loop.exit(),
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
                || self.notes.visible()
                || self.workspace_ui.visible()
                || self.transfer.visible()
            {
                self.close_all_overlays();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            event_loop.exit();
            return;
        }

        let super_or_ctrl = self.modifiers.super_key() || self.modifiers.control_key();
        let shift = self.modifiers.shift_key();
        let alt = self.modifiers.alt_key();

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
        }

        // Global shortcuts
        if super_or_ctrl {
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
        self.session.push_history(&line);
        self.session.apply_cwd_after_command(&line);
        self.session.draft_mut().clear();
        let id = self.session.focus_pane_id();

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
            WindowEvent::CloseRequested => event_loop.exit(),

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
            }

            WindowEvent::CursorLeft { .. } => {
                self.pointer_inside = false;
            }

            WindowEvent::MouseInput {
                state: ElementState::Pressed,
                button: MouseButton::Left,
                ..
            } => {
                self.chip_ui.pressed = true;
                let hit = self.hit_at_cursor();
                self.press_hit = Some(hit);
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
                // Activate only on release, and only if still over the same target.
                if let Some(start) = self.press_hit.take() {
                    let end = self.hit_at_cursor();
                    if start == end && start != HitTarget::TitleDrag && start != HitTarget::None {
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
                let dt = 1.0 / 60.0;
                self.settings.tick(dt);
                self.palette.tick(dt);
                self.help.tick(dt);
                self.notes.tick(dt);
                self.workspace_ui.tick(dt);
                self.transfer.tick(dt);
                let _ = self.caffeine.tick();
                let tick = self.session.tick_splits(dt);
                if !tick.finished_closes.is_empty() {
                    self.finish_closed_panes(event_loop, &tick.finished_closes);
                }
                if self.session.is_empty() {
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
                        &self.notes,
                        &self.workspace_ui,
                        &self.transfer,
                        &self.caffeine,
                        &self.commands,
                        &layout,
                        pty_on,
                        term_cursor,
                        caret_alpha,
                        pointer,
                        &self.chip_ui,
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
