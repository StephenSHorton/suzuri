//! winit application — multi-tab, multi-pane PTY, palette, help, chrome.

use std::collections::HashMap;
use std::sync::Arc;
use std::time::Instant;

use winit::{
    application::ApplicationHandler,
    dpi::{LogicalPosition, LogicalSize, PhysicalPosition},
    event::{DeviceEvent, ElementState, MouseButton, MouseScrollDelta, WindowEvent},
    event_loop::ActiveEventLoop,
    keyboard::{Key, ModifiersState, NamedKey},
    window::{Window, WindowAttributes, WindowId},
};

use crate::ansi::AnsiDecoder;
use crate::commands::{
    default_commands, filter_commands, CommandAction, HelpState, PaletteState,
};
use crate::input::{hit_test, is_mac, HitTarget};
use crate::layout::{FrameLayout, Metrics};
use crate::panes::{FocusDir, SplitAxis};
use crate::pty::PtySession;
use crate::renderer::{self, Renderer};
use crate::session::{ChromeSession, CloseOutcome};
use crate::settings::SettingsState;

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
    commands: Vec<crate::commands::Command>,
    started: Instant,
    clipboard: Option<arboard::Clipboard>,
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
            commands: default_commands(),
            started: Instant::now(),
            clipboard: arboard::Clipboard::new().ok(),
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
        self.settings.open || self.palette.open || self.help.open
    }

    fn close_all_overlays(&mut self) {
        self.settings.close();
        self.palette.close();
        self.help.close();
    }

    fn run_action(&mut self, event_loop: &ActiveEventLoop, action: CommandAction) {
        match action {
            CommandAction::OpenSettings => {
                self.palette.close();
                self.help.close();
                self.settings.open();
            }
            CommandAction::OpenHelp => {
                self.palette.close();
                self.settings.close();
                self.help.open_help();
            }
            CommandAction::OpenPalette => {
                self.settings.close();
                self.help.close();
                self.palette.open_palette();
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
            CloseOutcome::None => {}
        }
    }

    fn handle_click(&mut self, event_loop: &ActiveEventLoop) {
        let layout = self.current_layout();
        let target = hit_test(
            &layout,
            &self.metrics,
            self.cursor.x,
            self.cursor.y,
            is_mac(),
        );

        // Overlays: click outside closes
        if self.palette.visible() || self.help.visible() || self.settings.visible() {
            match target {
                HitTarget::Settings if self.settings.visible() => {
                    self.settings.toggle();
                    return;
                }
                HitTarget::Close | HitTarget::Minimize | HitTarget::Zoom => {}
                _ => {
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    // Rough: any overlay modal is centered — dismiss on outside
                    let modal = self.settings.animated_modal_rect(win_w, win_h);
                    if self.settings.visible() && modal.contains(self.cursor.x, self.cursor.y) {
                        return;
                    }
                    // Palette/help use larger centered rects — dismiss outside roughly
                    if self.palette.visible() || self.help.visible() {
                        // click on scrim
                        if target == HitTarget::None
                            || matches!(
                                target,
                                HitTarget::TitleDrag
                                    | HitTarget::Terminal(_)
                                    | HitTarget::WarpBar(_)
                            )
                        {
                            // still check if inside modal approx
                            let mx = win_w * 0.5;
                            let my = win_h * 0.45;
                            let inside = (self.cursor.x - mx).abs() < 220.0
                                && (self.cursor.y - my).abs() < 200.0;
                            if !inside {
                                self.close_all_overlays();
                                if let Some(w) = &self.window {
                                    w.request_redraw();
                                }
                                return;
                            }
                            return; // inside modal — no-op for now
                        }
                    }
                    if self.settings.visible() {
                        self.settings.close();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                }
            }
        }

        match target {
            HitTarget::Close => event_loop.exit(),
            HitTarget::Minimize => {
                if let Some(w) = &self.window {
                    w.set_minimized(true);
                }
            }
            HitTarget::Zoom => {
                if let Some(w) = &self.window {
                    w.set_maximized(!w.is_maximized());
                }
            }
            HitTarget::TitleDrag => {
                if let Some(w) = &self.window {
                    let _ = w.drag_window();
                }
            }
            HitTarget::Tab(i) => {
                if let Some(tab) = self.session.tabs.get(i) {
                    let id = tab.id;
                    self.session.select_tab(id);
                    self.warp_focused = true;
                    self.terminal_focused = false;
                }
            }
            HitTarget::NewTab => self.new_tab(),
            HitTarget::Settings => {
                self.help.close();
                self.palette.close();
                self.settings.toggle();
            }
            HitTarget::WarpBar(pane_id) => {
                self.session.set_focus_pane(pane_id);
                self.warp_focused = true;
                self.terminal_focused = false;
            }
            HitTarget::Terminal(pane_id) => {
                self.session.set_focus_pane(pane_id);
                self.terminal_focused = true;
                self.warp_focused = false;
            }
            HitTarget::None => {}
        }

        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn handle_middle_click(&mut self, event_loop: &ActiveEventLoop) {
        let layout = self.current_layout();
        let target = hit_test(
            &layout,
            &self.metrics,
            self.cursor.x,
            self.cursor.y,
            is_mac(),
        );
        if let HitTarget::Tab(i) = target {
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

    fn handle_key(&mut self, event_loop: &ActiveEventLoop, event: &winit::event::KeyEvent) {
        if !event.state.is_pressed() {
            return;
        }

        if matches!(event.logical_key, Key::Named(NamedKey::Escape)) {
            if self.overlay_open() || self.settings.visible() || self.palette.visible() || self.help.visible()
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

        // Global shortcuts
        if super_or_ctrl {
            if let Key::Character(ref s) = event.logical_key {
                let ch = s.as_str();
                match ch {
                    "k" | "K" if !shift => {
                        self.help.close();
                        self.settings.close();
                        self.palette.toggle();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "/" if !shift => {
                        self.palette.close();
                        self.settings.close();
                        self.help.toggle();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "," => {
                        self.palette.close();
                        self.help.close();
                        self.settings.toggle();
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
                self.handle_click(event_loop);
            }

            WindowEvent::MouseInput {
                state: ElementState::Pressed,
                button: MouseButton::Middle,
                ..
            } => {
                self.handle_middle_click(event_loop);
            }

            WindowEvent::MouseWheel { delta, .. } => {
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
                let dt = 1.0 / 60.0;
                self.settings.tick(dt);
                self.palette.tick(dt);
                self.help.tick(dt);
                let _ = self.session.tick_splits(dt);
                self.sync_grids_to_panes();

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
                        &self.commands,
                        &layout,
                        pty_on,
                        term_cursor,
                        caret_alpha,
                        pointer,
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
