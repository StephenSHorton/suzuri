//! winit application — multi-tab PTY, settings, chrome interaction.

use std::collections::HashMap;
use std::sync::Arc;

use winit::{
    application::ApplicationHandler,
    dpi::{LogicalPosition, LogicalSize},
    event::{ElementState, MouseButton, WindowEvent},
    event_loop::ActiveEventLoop,
    keyboard::{Key, ModifiersState, NamedKey},
    window::{Window, WindowAttributes, WindowId},
};

use crate::ansi::AnsiDecoder;
use crate::input::{hit_test, is_mac, HitTarget};
use crate::layout::{FrameLayout, Metrics};
use crate::pty::PtySession;
use crate::renderer::{self, Renderer};
use crate::session::ChromeSession;
use crate::settings::SettingsState;

/// Per-tab live shell state (kept out of [`ChromeSession`] — not Clone).
struct TabRuntime {
    pty: Option<PtySession>,
    ansi: AnsiDecoder,
}

pub struct ChromeApp {
    window: Option<Arc<Window>>,
    renderer: Option<Renderer>,
    session: ChromeSession,
    metrics: Metrics,
    cursor: LogicalPosition<f32>,
    warp_focused: bool,
    terminal_focused: bool,
    modifiers: ModifiersState,
    /// One PTY + ANSI decoder per tab id.
    runtimes: HashMap<u64, TabRuntime>,
    settings: SettingsState,
}

impl Default for ChromeApp {
    fn default() -> Self {
        let mut session = ChromeSession::new(80, 24);
        let mut runtimes = HashMap::new();
        let id = session.active_id;
        let (cols, rows) = {
            let g = session.active_grid();
            (g.cols(), g.rows())
        };
        let rt = spawn_tab_runtime(cols, rows, &mut session, id);
        runtimes.insert(id, rt);

        Self {
            window: None,
            renderer: None,
            session,
            metrics: Metrics::default(),
            cursor: LogicalPosition::new(0.0, 0.0),
            warp_focused: true,
            terminal_focused: false,
            modifiers: ModifiersState::empty(),
            runtimes,
            settings: SettingsState::new(),
        }
    }
}

fn spawn_tab_runtime(
    cols: u16,
    rows: u16,
    session: &mut ChromeSession,
    tab_id: u64,
) -> TabRuntime {
    match PtySession::spawn(cols, rows) {
        Ok(pty) => {
            if let Some(tab) = session.tabs.iter_mut().find(|t| t.id == tab_id) {
                tab.grid.clear();
                tab.pty_mode = true;
                tab.busy = false;
            }
            TabRuntime {
                pty: Some(pty),
                ansi: AnsiDecoder::new(),
            }
        }
        Err(e) => {
            eprintln!("suzuri-chrome: PTY for tab {tab_id} failed ({e}) — mock");
            let prev = session.active_id;
            session.select_tab(tab_id);
            session.boot_mock_on_active();
            if prev != tab_id {
                session.select_tab(prev);
            }
            TabRuntime {
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

    fn active_cursor_visible(&self) -> bool {
        self.runtimes
            .get(&self.session.active_id)
            .map(|rt| rt.ansi.cursor_visible)
            .unwrap_or(true)
    }

    fn current_layout(&self) -> FrameLayout {
        if let Some(r) = &self.renderer {
            r.layout(self.session.tabs.len())
        } else {
            FrameLayout::compute(1120.0, 740.0, self.metrics, self.session.tabs.len())
        }
    }

    fn sync_grid_to_terminal(&mut self) {
        let layout = self.current_layout();
        let (cols, rows) = renderer::terminal_grid_size(&layout.terminal);
        let (cur_c, cur_r) = {
            let g = self.session.active_grid();
            (g.cols(), g.rows())
        };
        if cols != cur_c || rows != cur_r {
            self.session.resize_all(cols, rows);
            for rt in self.runtimes.values_mut() {
                if let Some(pty) = &mut rt.pty {
                    let _ = pty.resize(cols, rows);
                }
            }
        }
    }

    /// Drain **all** tab PTYs into their own grids (background tabs keep updating).
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
            }
            if let Some(tab) = self.session.tabs.iter_mut().find(|t| t.id == id) {
                tab.busy = false;
            }
        }
        if let Some(w) = &self.window {
            w.request_redraw();
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
                }
            }
            HitTarget::NewTab => {
                let (cols, rows) = {
                    let g = self.session.active_grid();
                    (g.cols(), g.rows())
                };
                let id = self.session.new_tab(cols, rows);
                let rt = spawn_tab_runtime(cols, rows, &mut self.session, id);
                self.runtimes.insert(id, rt);
            }
            HitTarget::Settings => {
                self.settings.toggle();
            }
            HitTarget::WarpBar => {
                self.warp_focused = true;
                self.terminal_focused = false;
            }
            HitTarget::Terminal => {
                self.terminal_focused = true;
                self.warp_focused = false;
            }
            HitTarget::None => {}
        }

        // Middle-click or future: close tab — for now Cmd+W
        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn close_active_tab_or_quit(&mut self, event_loop: &ActiveEventLoop) {
        let id = self.session.active_id;
        if self.session.tabs.len() <= 1 {
            event_loop.exit();
            return;
        }
        if self.session.close_tab(id) {
            self.runtimes.remove(&id);
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
            if self.settings.open {
                self.settings.close();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            event_loop.exit();
            return;
        }

        let super_or_ctrl = self.modifiers.super_key() || self.modifiers.control_key();
        if super_or_ctrl {
            if let Key::Character(ref s) = event.logical_key {
                match s.as_str() {
                    "," => {
                        self.settings.toggle();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "t" | "T" => {
                        // New tab
                        let (cols, rows) = {
                            let g = self.session.active_grid();
                            (g.cols(), g.rows())
                        };
                        let id = self.session.new_tab(cols, rows);
                        let rt = spawn_tab_runtime(cols, rows, &mut self.session, id);
                        self.runtimes.insert(id, rt);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "w" | "W" => {
                        self.close_active_tab_or_quit(event_loop);
                        return;
                    }
                    _ => {}
                }
            }
        }

        if self.settings.open {
            return;
        }

        // Terminal focus → raw input to **active** tab PTY.
        if self.terminal_focused {
            let id = self.session.active_id;
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

        if !(self.warp_focused || self.terminal_focused) {
            return;
        }

        match &event.logical_key {
            Key::Named(NamedKey::Backspace) => self.session.backspace(),
            Key::Named(NamedKey::Enter) => self.submit_line(),
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

        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn submit_line(&mut self) {
        let line = self.session.draft.trim_end().to_string();
        if line.is_empty() {
            return;
        }
        self.session.draft.clear();
        let id = self.session.active_id;

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
            if let Some(tab) = self.session.tabs.iter_mut().find(|t| t.id == id) {
                tab.busy = true;
            }
        } else {
            self.session.draft = line;
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

        let renderer = pollster::block_on(Renderer::new(window.clone()));
        self.metrics = renderer.metrics();
        self.window = Some(window);
        self.renderer = Some(renderer);
        self.sync_grid_to_terminal();

        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn about_to_wait(&mut self, _event_loop: &ActiveEventLoop) {
        self.drain_all_ptys();
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
            }

            WindowEvent::MouseInput {
                state: ElementState::Pressed,
                button: MouseButton::Left,
                ..
            } => {
                self.handle_click(event_loop);
            }

            WindowEvent::KeyboardInput { event, .. } => {
                self.handle_key(event_loop, &event);
            }

            WindowEvent::Resized(size) => {
                if let (Some(r), Some(w)) = (self.renderer.as_mut(), self.window.as_ref()) {
                    r.resize(size, w.scale_factor() as f32);
                }
                self.sync_grid_to_terminal();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }

            WindowEvent::ScaleFactorChanged { scale_factor, .. } => {
                if let (Some(r), Some(w)) = (self.renderer.as_mut(), self.window.as_ref()) {
                    r.resize(w.inner_size(), scale_factor as f32);
                }
                self.sync_grid_to_terminal();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }

            WindowEvent::RedrawRequested => {
                self.drain_all_ptys();
                let pty_on = self.any_pty_alive();
                let cursor_vis = self.active_cursor_visible();
                if let Some(r) = self.renderer.as_mut() {
                    match r.render(&self.session, &self.settings, pty_on, cursor_vis) {
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
