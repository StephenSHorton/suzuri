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
    event_loop::{ActiveEventLoop, ControlFlow},
    keyboard::{Key, ModifiersState, NamedKey},
    window::{CursorIcon, Fullscreen, Window, WindowAttributes, WindowId, WindowLevel},
};

use crate::ansi::AnsiDecoder;
use crate::cells::CellGrid;
use crate::caffeine::Caffeine;
use crate::chrome_status::{self, PaneSnapExtra, StatusPublisher};
use crate::chrome_ui::{ChipId, ChipUi};
use crate::cmd_blocks::{self, CmdBlockLog};
use crate::echo_filter::EchoFilter;
use crate::commands::{
    default_commands, filter_commands, CommandAction, HelpState, PaletteState, SplashState,
};
use crate::confirm::{ConfirmChoice, ConfirmKind, ConfirmState};
use crate::control_mailbox::ControlMailbox;
use crate::input::{
    classify_drop, classify_tab_drop, hit_test, is_mac, window_origin_for_tab_drop, DropKind,
    HitTarget,
};
use crate::layout::{FrameLayout, Metrics};
use crate::links::{link_span_at_col, open_url_in_browser, LinkHoverSpan};
use crate::mouse_pty::encode_mouse_wheel;
use crate::notes::NotesState;
use crate::panes::{FocusDir, SplitAxis};
use crate::pty::PtySession;
use crate::rename::{RenameState, RenameTarget};
use crate::renderer::{self, GhostLayer, Renderer};
use crate::text::MonoCellMetrics;
use crate::selection::{clamp_pos, CellPos, Selection};
use crate::session::{ChromeSession, CloseOutcome, WidgetKind};
use crate::settings::SettingsState;
use crate::toast::ToastState;
use crate::transfer_ui::TransferUi;
use crate::updater::{UpdateEvent, UpdateMailbox};
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
    /// Warp compose owns the prompt: hide PTY paint until the first submit
    /// so a fresh pane is empty (no leaked zsh/cmd prompt).
    suppress_paint: bool,
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
    /// In-process windows (tear-off / New Window). Keyed by winit id.
    surfaces: HashMap<WindowId, Surface>,
    event_win: Option<WindowId>,
    /// Last window that received `Focused(true)` — keyboard + overlays.
    focus_win: Option<WindowId>,
    next_surface_key: u64,
    last_world_tick: Instant,
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
    /// Dragging the terminal scrollbar thumb/track (pane id).
    scroll_dragging: Option<u64>,
    /// URL under the pointer in the focused terminal (for hand cursor / Cmd-click).
    hovered_link: Option<String>,
    /// Exact cell range of [`Self::hovered_link`] for primary hover paint.
    hovered_link_span: Option<LinkHoverSpan>,
    /// True while the OS pointer icon is the hand (link hover).
    link_cursor_on: bool,
    /// Host light IPC: poll `chrome_cmd` under config dir (~250ms).
    control_mailbox: ControlMailbox,
    /// Host updater IPC: `update_req` / `update_evt`.
    update_mailbox: UpdateMailbox,
    /// Publish `chrome_status.json` for Go MCP bridge proxy.
    status_publisher: StatusPublisher,
    /// UI / terminal zoom multiplier (⌘± / ⌘0). 1.0 = design size.
    ui_zoom: f32,

    /// Last applied mono font id (avoid re-resolve every paint).
    applied_font: String,
    /// Accumulated trackpad pixel-delta (macOS sends many sub-line events).
    wheel_accum: f32,
    /// Pane / tab drag (grab path, glass band, or a tab chip).
    pane_drag: Option<LayoutDrag>,
    sash_drag: Option<SashDrag>,
    /// Click-through chip that follows the pointer (not a session surface).
    drag_float: Option<GhostLayer>,
    /// Window whose GPU device owns [`Self::drag_float`].
    ghost_host: Option<WindowId>,
}

enum DragSubject {
    Pane { pane_id: u64, source: crate::layout::Rect },
    Tab { tab_id: u64, from_idx: usize },
}

struct LayoutDrag {
    subject: DragSubject,
    start: (f32, f32),
    active: bool,
    drop: Option<DropKind>,
    dest_surface: u64,
    /// Cursor offset from the ghost window's top-left (logical px).
    grab: (f32, f32),
}

enum SurfaceAdopt {
    Fresh,
    Tab(u64),
    Pane(u64),
}

enum SurfacePlace {
    Cascade,
    UnderPointer,
}

enum PointerLoc {
    Surface { key: u64, x: f32, y: f32 },
    Outside,
}

#[derive(Clone, Copy)]
struct SashDrag {
    a_leaf: u64,
    parent: crate::layout::Rect,
    axis: crate::panes::SplitAxis,
}

struct Surface {
    key: u64,
    window: Arc<Window>,
    renderer: Renderer,
    focus_tab: u64,
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
            scroll_dragging: None,
            hovered_link: None,
            hovered_link_span: None,
            link_cursor_on: false,
            control_mailbox: ControlMailbox::new(),
            update_mailbox: UpdateMailbox::new(),
            ui_zoom: 1.0,
            status_publisher: StatusPublisher::new(),

            applied_font: String::new(),
            wheel_accum: 0.0,
            pane_drag: None,
            sash_drag: None,
            drag_float: None,
            ghost_host: None,
            surfaces: HashMap::new(),
            event_win: None,
            focus_win: None,
            next_surface_key: 1,
            last_world_tick: Instant::now(),
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
    spawn_pane_runtime_px(cols, rows, 0, 0, session, pane_id)
}

fn spawn_pane_runtime_px(
    cols: u16,
    rows: u16,
    pixel_w: u16,
    pixel_h: u16,
    session: &mut ChromeSession,
    pane_id: u64,
) -> PaneRuntime {
    let cwd = session
        .panes
        .get(&pane_id)
        .map(|p| p.cwd.as_str())
        .filter(|s| !s.is_empty());
    match PtySession::spawn_in(cols, rows, pixel_w, pixel_h, cwd) {
        Ok(pty) => {
            session.mark_pane_pty(pane_id);
            PaneRuntime {
                pty: Some(pty),
                ansi: AnsiDecoder::new(),
                pty_tail: String::new(),
                echo: EchoFilter::new(),
                blocks: CmdBlockLog::new(),
                suppress_paint: true,
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
                suppress_paint: true,
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
        if !self.warp_focused && !self.workspace_captures_input() {
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
        if self.workspace_captures_input() {
            for ch in text.chars() {
                if ch == '\n' || ch == '\r' {
                    break;
                }
                if !ch.is_control() {
                    self.workspace_ui.insert_char(ch);
                }
            }
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

    fn event_surface_key(&self) -> u64 {
        self.event_win
            .and_then(|id| self.surfaces.get(&id).map(|s| s.key))
            .unwrap_or(0)
    }

    fn bind_win(&mut self, id: WindowId) {
        self.event_win = Some(id);
        if let Some(s) = self.surfaces.get(&id) {
            self.window = Some(s.window.clone());
        }
    }

    fn renderer(&self) -> Option<&Renderer> {
        let id = self.event_win.or_else(|| self.surfaces.keys().next().copied())?;
        self.surfaces.get(&id).map(|s| &s.renderer)
    }

    fn renderer_mut(&mut self) -> Option<&mut Renderer> {
        let id = self.event_win.or_else(|| self.surfaces.keys().next().copied())?;
        self.surfaces.get_mut(&id).map(|s| &mut s.renderer)
    }

    fn renderer_for(&self, key: u64) -> Option<&Renderer> {
        self.surfaces.values().find(|s| s.key == key).map(|s| &s.renderer)
    }

    fn surface_focus_tab(&self, key: u64) -> Option<u64> {
        if let Some(tid) = self
            .surfaces
            .values()
            .find(|s| s.key == key)
            .map(|s| s.focus_tab)
        {
            if self
                .session
                .tabs
                .iter()
                .any(|t| t.id == tid && t.surface == key)
            {
                return Some(tid);
            }
        }
        self.session.tabs_on_surface(key).first().copied()
    }

    fn set_surface_focus(&mut self, key: u64, tab_id: u64) {
        for s in self.surfaces.values_mut() {
            if s.key == key {
                s.focus_tab = tab_id;
            }
        }
        self.session.select_tab(tab_id);
    }

    fn focus_surface_window(&mut self, id: WindowId) {
        self.focus_win = Some(id);
        self.bind_win(id);
        if let Some(key) = self.surfaces.get(&id).map(|s| s.key) {
            if let Some(tid) = self.surface_focus_tab(key) {
                self.session.select_tab(tid);
            }
        }
    }

    fn remember_event_surface_tab(&mut self) {
        let key = self.event_surface_key();
        let tid = self.session.active_id;
        if self.session.surface_of_tab(tid) == Some(key) {
            for s in self.surfaces.values_mut() {
                if s.key == key {
                    s.focus_tab = tid;
                }
            }
        }
    }

    fn pointer_screen_physical(&self) -> Option<(i32, i32)> {
        let w = self.window.as_ref()?;
        let origin = w.inner_position().ok()?;
        let scale = w.scale_factor();
        Some((
            origin.x + (self.cursor.x as f64 * scale).round() as i32,
            origin.y + (self.cursor.y as f64 * scale).round() as i32,
        ))
    }

    fn is_ghost_win(&self, id: WindowId) -> bool {
        self.drag_float
            .as_ref()
            .is_some_and(|g| g.window.id() == id)
    }

    fn drag_float_logical_size(&self) -> (f32, f32) {
        match self.pane_drag.as_ref().map(|d| &d.subject) {
            Some(DragSubject::Tab { .. }) => (crate::layout::TAB_CHIP_W, 32.0),
            Some(DragSubject::Pane { source, .. }) => (
                (source.w * 0.55).clamp(120.0, 320.0),
                (source.h * 0.40).clamp(64.0, 180.0),
            ),
            None => (crate::layout::TAB_CHIP_W, 32.0),
        }
    }

    fn ensure_drag_float(&mut self, event_loop: &ActiveEventLoop) {
        if self
            .ghost_host
            .is_some_and(|id| !self.surfaces.contains_key(&id))
        {
            self.drag_float = None;
            self.ghost_host = None;
        }
        let (lw, lh) = self.drag_float_logical_size();
        if let Some(g) = &self.drag_float {
            let scale = g.window.scale_factor() as f32;
            let want_w = (lw * scale).round() as u32;
            let want_h = (lh * scale).round() as u32;
            let have = g.window.inner_size();
            if have.width.abs_diff(want_w) > 2 || have.height.abs_diff(want_h) > 2 {
                let _ = g.window.request_inner_size(LogicalSize::new(lw, lh));
            }
            g.window.set_visible(true);
            self.place_drag_float();
            return;
        }
        let attrs = WindowAttributes::default()
            .with_title("suzuri · drag")
            .with_inner_size(LogicalSize::new(lw, lh))
            .with_decorations(false)
            .with_transparent(true)
            .with_resizable(false)
            .with_visible(true)
            .with_active(false)
            .with_window_level(WindowLevel::AlwaysOnTop);
        let Ok(raw) = event_loop.create_window(attrs) else {
            return;
        };
        let window = Arc::new(raw);
        let _ = window.set_cursor_hittest(false);
        #[cfg(target_os = "macos")]
        crate::macos_window::configure_rounded_window(&window, 8.0);
        let Some(host_id) = self
            .event_win
            .filter(|id| self.surfaces.contains_key(id))
            .or_else(|| self.surfaces.keys().next().copied())
        else {
            return;
        };
        let Some(layer) = self
            .surfaces
            .get(&host_id)
            .map(|s| s.renderer.spawn_ghost(window.clone()))
        else {
            return;
        };
        self.ghost_host = Some(host_id);
        self.drag_float = Some(layer);
        self.place_drag_float();
    }

    fn place_drag_float(&self) {
        let Some(g) = &self.drag_float else {
            return;
        };
        let Some(screen) = self.pointer_screen_physical() else {
            return;
        };
        let grab = self
            .pane_drag
            .as_ref()
            .map(|d| d.grab)
            .unwrap_or((48.0, 16.0));
        let scale = g.window.scale_factor();
        let x = screen.0 - (grab.0 as f64 * scale).round() as i32;
        let y = screen.1 - (grab.1 as f64 * scale).round() as i32;
        g.window
            .set_outer_position(winit::dpi::PhysicalPosition::new(x, y));
        g.window.request_redraw();
    }

    fn hide_drag_float(&self) {
        if let Some(g) = &self.drag_float {
            g.window.set_visible(false);
        }
    }

    fn paint_drag_float(&mut self) {
        let Some(title) = self.pane_drag.as_ref().and_then(|d| match &d.subject {
            DragSubject::Tab { tab_id, .. } => self
                .session
                .tabs
                .iter()
                .find(|t| t.id == *tab_id)
                .map(|t| t.title.clone()),
            DragSubject::Pane { pane_id, .. } => self
                .session
                .panes
                .get(pane_id)
                .map(|p| p.title.clone()),
        }) else {
            return;
        };
        let is_tab = self
            .pane_drag
            .as_ref()
            .is_some_and(|d| matches!(d.subject, DragSubject::Tab { .. }));
        let Some(host_id) = self.ghost_host else {
            return;
        };
        if !self.surfaces.contains_key(&host_id) {
            self.drag_float = None;
            self.ghost_host = None;
            return;
        }
        let Some(mut ghost) = self.drag_float.take() else {
            return;
        };
        if let Some(host) = self.surfaces.get(&host_id) {
            let _ = host
                .renderer
                .render_ghost(&mut ghost, &title, &self.settings, is_tab);
        }
        self.drag_float = Some(ghost);
    }

    fn request_redraw(&self) {
        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn request_redraw_all(&self) {
        for s in self.surfaces.values() {
            s.window.request_redraw();
        }
    }

    fn pointer_loc(&self) -> PointerLoc {
        let Some(src_id) = self.event_win.or_else(|| self.surfaces.keys().next().copied()) else {
            return PointerLoc::Outside;
        };
        let Some(src) = self.surfaces.get(&src_id) else {
            return PointerLoc::Outside;
        };
        let scale = src.window.scale_factor();
        let src_size = src.window.inner_size();
        let in_src = self.cursor.x >= 0.0
            && self.cursor.y >= 0.0
            && (self.cursor.x as f64 * scale) < src_size.width as f64
            && (self.cursor.y as f64 * scale) < src_size.height as f64;
        let origin = src.window.inner_position().ok();
        let (sx, sy) = match origin {
            Some(o) => (
                o.x as f64 + self.cursor.x as f64 * scale,
                o.y as f64 + self.cursor.y as f64 * scale,
            ),
            None => {
                return if in_src {
                    PointerLoc::Surface {
                        key: src.key,
                        x: self.cursor.x,
                        y: self.cursor.y,
                    }
                } else {
                    PointerLoc::Outside
                };
            }
        };
        let mut hit = if in_src {
            Some((src.key, self.cursor.x, self.cursor.y))
        } else {
            None
        };
        for (id, s) in &self.surfaces {
            if *id == src_id {
                continue;
            }
            let Ok(o) = s.window.inner_position() else {
                continue;
            };
            let size = s.window.inner_size();
            if sx >= o.x as f64
                && sy >= o.y as f64
                && sx < (o.x + size.width as i32) as f64
                && sy < (o.y + size.height as i32) as f64
            {
                let sc = s.window.scale_factor();
                let lx = ((sx - o.x as f64) / sc) as f32;
                let ly = ((sy - o.y as f64) / sc) as f32;
                hit = Some((s.key, lx, ly));
            }
        }
        match hit {
            Some((key, x, y)) => PointerLoc::Surface { key, x, y },
            None => PointerLoc::Outside,
        }
    }

    fn open_surface(
        &mut self,
        event_loop: &ActiveEventLoop,
        adopt: SurfaceAdopt,
        place: SurfacePlace,
    ) -> Option<u64> {
        let src_size = self.window.as_ref().map(|w| w.inner_size());
        let src_scale = self
            .window
            .as_ref()
            .map(|w| w.scale_factor())
            .unwrap_or(1.0);
        let pos = match place {
            SurfacePlace::Cascade => self
                .window
                .as_ref()
                .and_then(|w| w.outer_position().ok())
                .map(|p| (p.x + 36, p.y + 36))
                .unwrap_or((80, 60)),
            SurfacePlace::UnderPointer => {
                let logical_w = src_size
                    .map(|s| s.width as f64 / src_scale)
                    .unwrap_or(1120.0) as f32;
                let logical_h = src_size
                    .map(|s| s.height as f64 / src_scale)
                    .unwrap_or(740.0) as f32;
                let layout = FrameLayout::compute(logical_w, logical_h, self.metrics, 1);
                let chip = layout.tab_chips.first().copied().unwrap_or(
                    crate::layout::Rect::new(80.0, 6.0, crate::layout::TAB_CHIP_W, 20.0),
                );
                let screen = self.pointer_screen_physical().unwrap_or((80, 60));
                window_origin_for_tab_drop(screen, chip, src_scale)
            }
        };
        let mut attrs = WindowAttributes::default()
            .with_title("suzuri · chrome")
            .with_inner_size(LogicalSize::new(1120.0, 740.0))
            .with_min_inner_size(LogicalSize::new(720.0, 440.0))
            .with_position(winit::dpi::PhysicalPosition::new(pos.0, pos.1))
            .with_decorations(false)
            .with_transparent(true)
            .with_resizable(true);
        if let (SurfacePlace::UnderPointer, Some(size)) = (&place, src_size) {
            attrs = attrs.with_inner_size(size);
        }
        let window = Arc::new(event_loop.create_window(attrs).ok()?);
        #[cfg(target_os = "macos")]
        crate::macos_window::configure_rounded_window(&window, 16.0);
        let renderer = pollster::block_on(Renderer::new(window.clone()));
        let key = self.next_surface_key;
        self.next_surface_key = self.next_surface_key.saturating_add(1);
        let wid = window.id();
        let mut focus_tab = self.session.active_id;
        self.surfaces.insert(
            wid,
            Surface {
                key,
                window: window.clone(),
                renderer,
                focus_tab,
            },
        );
        self.bind_win(wid);
        self.focus_win = Some(wid);
        match adopt {
            SurfaceAdopt::Tab(tab_id) => {
                let _ = self.session.place_tab_on_surface(tab_id, key, usize::MAX);
                focus_tab = tab_id;
            }
            SurfaceAdopt::Pane(pane_id) => {
                if let Some(tid) = self.session.extract_pane_to_new_tab(pane_id, key) {
                    focus_tab = tid;
                }
            }
            SurfaceAdopt::Fresh => {
                let cell = self.cell_metrics();
                let layout = self.layout_for_surface(key);
                let (cols, rows) = renderer::terminal_grid_size_with(
                    layout.panes.first().map(|p| &p.cells).unwrap_or(&layout.cells),
                    cell.w,
                    cell.h,
                );
                let (pw, ph) = self.pty_pixel_size(cols, rows);
                let (tid, pane_id) = self.session.new_tab_on_surface(cols, rows, key);
                let rt = spawn_pane_runtime_px(cols, rows, pw, ph, &mut self.session, pane_id);
                self.runtimes.insert(pane_id, rt);
                focus_tab = tid;
            }
        }
        self.set_surface_focus(key, focus_tab);
        window.focus_window();
        window.request_redraw();
        Some(key)
    }

    fn current_layout(&self) -> FrameLayout {
        self.layout_for_surface(self.event_surface_key())
    }

    fn layout_for_surface(&self, surface: u64) -> FrameLayout {
        let tab_count = self.session.tabs_on_surface(surface).len().max(1);
        let mut layout = if let Some(r) = self.renderer_for(surface).or_else(|| self.renderer()) {
            r.layout(tab_count)
        } else {
            FrameLayout::compute(1120.0, 740.0, self.metrics, tab_count)
        };

        let shown = self.surface_focus_tab(surface);
        let Some(tab) = shown.and_then(|id| self.session.tabs.iter().find(|t| t.id == id)) else {
            return layout;
        };
        let mut leafs = Vec::new();
        let gap = self.metrics.stack();
        tab.root.layout_into(layout.workspace, gap, &mut leafs);
        if let Some(anim) = &tab.solo_exit {
            let s = anim.jelly.clamp(0.0, 1.15);
            if anim.fade_window {
                // Recede, don't collapse to a point — blur + fade finish the dissolve.
                let s = 0.72 + 0.28 * anim.jelly.clamp(0.0, 1.0);
                let wr = layout.workspace;
                let cx = wr.x + wr.w * 0.5;
                let cy = wr.y + wr.h * 0.5;
                leafs = leafs
                    .into_iter()
                    .map(|(id, r)| {
                        let nw = (r.w * s).max(1.0);
                        let nh = (r.h * s).max(1.0);
                        let nx = cx + (r.x + r.w * 0.5 - cx) * s - nw * 0.5;
                        let ny = cy + (r.y + r.h * 0.5 - cy) * s - nh * 0.5;
                        (id, crate::layout::Rect::new(nx, ny, nw, nh))
                    })
                    .collect();
            } else {
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
        }
        let alt_ids: std::collections::HashSet<u64> = self
            .runtimes
            .iter()
            .filter(|(_, rt)| rt.ansi.on_alt_screen())
            .map(|(id, _)| *id)
            .collect();
        layout.apply_pane_rects(self.metrics, &leafs, tab.focus_pane, &|id| {
            alt_ids.contains(&id) || self.session.is_widget(id)
        });
        tab.root
            .collect_sashes(layout.workspace, gap, &mut layout.sashes);
        layout
    }

    /// Mono cell pitch (logical px) — measured Gohu when renderer is up, × zoom.
    fn cell_metrics(&self) -> MonoCellMetrics {
        let base = self
            .renderer()
            .map(|r| r.cell_metrics())
            .unwrap_or_default();
        let z = self.ui_zoom.clamp(0.75, 1.75);
        MonoCellMetrics {
            w: (base.w * z).max(1.0),
            h: (base.h * z).max(1.0),
        }
    }

    fn nudge_zoom(&mut self, delta: f32) {
        self.ui_zoom = (self.ui_zoom + delta).clamp(0.75, 1.75);
        self.sync_grids_to_panes();
        self.toast.show(format!("Zoom {:.0}%", self.ui_zoom * 100.0));
    }

    fn reset_zoom(&mut self) {
        self.ui_zoom = 1.0;
        self.sync_grids_to_panes();
        self.toast.show("Zoom 100%");
    }

    fn close_tab_by_id(&mut self, event_loop: &ActiveEventLoop, tab_id: u64) {
        if self.session.tabs.len() <= 1 {
            if self.needs_quit_confirm() {
                self.request_quit(event_loop);
                return;
            }
            let _ = self.session.begin_close_last_window_tab(tab_id);
            return;
        }
        if self.session.is_last_tab_on_surface(tab_id) {
            let _ = self.session.begin_close_last_window_tab(tab_id);
            return;
        }
        let pane_ids = self.session.close_tab(tab_id);
        for id in pane_ids {
            self.runtimes.remove(&id);
        }
        self.prune_empty_surfaces(event_loop);
    }

    /// Physical pixel size of a terminal grid for `TIOCGWINSZ`.
    fn pty_pixel_size(&self, cols: u16, rows: u16) -> (u16, u16) {
        let cell = self.cell_metrics();
        let scale = self
            .renderer()
            .map(|r| r.scale_factor())
            .unwrap_or(1.0)
            .max(0.5);
        let pw = (cols as f32 * cell.w * scale).round().max(1.0) as u16;
        let ph = (rows as f32 * cell.h * scale).round().max(1.0) as u16;
        (pw, ph)
    }

    fn sync_grids_to_panes(&mut self) {
        let keys: Vec<u64> = if self.surfaces.is_empty() {
            vec![self.event_surface_key()]
        } else {
            self.surfaces.values().map(|s| s.key).collect()
        };
        let cell = self.cell_metrics();
        for key in keys {
            let layout = self.layout_for_surface(key);
            for pl in &layout.panes {
                if self.session.is_widget(pl.pane_id) {
                    continue;
                }
                let (cols, rows) =
                    renderer::terminal_grid_size_with(&pl.cells, cell.w, cell.h);
                let need = self
                    .session
                    .grid(pl.pane_id)
                    .map(|g| g.cols() != cols || g.rows() != rows)
                    .unwrap_or(true);
                if need {
                    self.session.resize_pane(pl.pane_id, cols, rows);
                    let (pw, ph) = self.pty_pixel_size(cols, rows);
                    if let Some(rt) = self.runtimes.get_mut(&pl.pane_id) {
                        if let Some(pty) = &mut rt.pty {
                            let _ = pty.resize_with_pixels(cols, rows, pw, ph);
                        }
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
                    if rt.suppress_paint {
                        // Parse OSC 7 / DA / title without painting the shell prompt.
                        let mut scratch = CellGrid::new(80, 24);
                        rt.ansi.feed(&mut scratch, &filtered);
                    } else if let Some(grid) = self.session.grid_mut(id) {
                        rt.ansi.feed(grid, &filtered);
                    }
                }
                // Device-attribute / probe replies → PTY (never the screen).
                let replies = rt.ansi.take_replies();
                if !replies.is_empty() {
                    if let Some(pty) = &mut rt.pty {
                        for r in replies {
                            let _ = pty.write_all(&r);
                        }
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
            || self.workspace_ui.is_modal()
            || self.transfer.open
            || self.rename.open
    }

    /// Workspace compose / scroll / drop when the pane is focused or still a modal.
    fn workspace_captures_input(&self) -> bool {
        if !self.workspace_ui.open {
            return false;
        }
        if self.workspace_ui.is_modal() {
            return true;
        }
        // Docked: yield to true overlays (palette, settings, …) so ⌘K still types
        // into the filter instead of the chat compose line.
        self.session.focused_is_workspace()
            && !self.palette.open
            && !self.settings.open
            && !self.help.open
            && !self.confirm.open
            && !self.splash.open
            && !self.notes.open
            && !self.transfer.open
            && !self.rename.open
    }

    fn sync_workspace_host(&mut self) {
        let host = self.workspace_ui.docked_pane.and_then(|id| {
            self.current_layout()
                .panes
                .iter()
                .find(|p| p.pane_id == id)
                .map(|p| p.glass)
        });
        self.workspace_ui.set_host(host);
    }

    fn pointer_over_workspace(&self) -> bool {
        if !self.workspace_ui.open {
            return false;
        }
        let layout = self.current_layout();
        let win_w = layout.title.w;
        let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
        if let Some(id) = self.workspace_ui.docked_pane {
            if let Some(pl) = layout.panes.iter().find(|p| p.pane_id == id) {
                return pl.glass.contains(self.cursor.x, self.cursor.y);
            }
        }
        self.workspace_ui.is_modal()
            && self
                .workspace_ui
                .card_rect(win_w, win_h)
                .contains(self.cursor.x, self.cursor.y)
    }

    /// Default open: split the last-focused pane and dock workspace there.
    fn open_workspace_pane(&mut self) {
        if let Some(id) = self.session.find_widget(WidgetKind::Workspace) {
            self.session.set_focus_pane(id);
            self.workspace_ui.dock(id);
            self.sync_workspace_host();
            self.warp_focused = false;
            self.terminal_focused = false;
            return;
        }
        if let Some(id) = self
            .session
            .split_focused_widget(SplitAxis::Vertical, WidgetKind::Workspace)
        {
            self.workspace_ui.dock(id);
            self.sync_workspace_host();
            self.warp_focused = false;
            self.terminal_focused = false;
        }
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
        if self.workspace_ui.is_modal() {
            self.workspace_ui.close();
        }
        self.transfer.close();
    }

    /// Close every glass modal except the confirm dialog (product `closeModalsExcept`).
    fn close_modals_except_confirm(&mut self) {
        self.settings.close();
        self.palette.close();
        self.help.close();
        self.notes.close();
        if self.workspace_ui.is_modal() {
            self.workspace_ui.close();
        }
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
        match (self.confirm.kind, choice) {
            (ConfirmKind::Quit, ConfirmChoice::Yes) => {
                self.confirm.close();
                chrome_status::clear_status();
                event_loop.exit();
            }
            (ConfirmKind::Update, ConfirmChoice::Yes) => {
                self.confirm.close();
                self.toast.show("installing update…");
                self.update_mailbox.request_install();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }
            (ConfirmKind::Update, ConfirmChoice::No) => {
                self.confirm.close();
                self.update_mailbox.request_later();
                self.toast.show("update deferred");
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }
            (_, ConfirmChoice::No) => {
                self.confirm.close();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }
        }
    }

    fn apply_update_events(&mut self) {
        for ev in self.update_mailbox.poll() {
            match ev {
                UpdateEvent::Toast(msg) => self.toast.show(msg),
                UpdateEvent::Offer { version } => {
                    if self.confirm.open && self.confirm.kind == ConfirmKind::Quit {
                        self.toast.show(format!("v{version} available"));
                        continue;
                    }
                    self.close_modals_except_confirm();
                    self.confirm.open_update(&version);
                }
            }
            if let Some(w) = &self.window {
                w.request_redraw();
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
        for (_i, &idx) in filtered.iter().enumerate().take(6) {
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
                if self.workspace_ui.is_modal() {
                    self.workspace_ui.close();
                }
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
                self.open_workspace_pane();
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
                    self.open_workspace_pane();
                }
                self.workspace_ui.cycle_status();
            }
            CommandAction::WorkspaceAttachFile => {
                if !self.workspace_ui.open {
                    self.palette.close();
                    self.settings.close();
                    self.help.close();
                    self.notes.close();
                    self.transfer.close();
                    self.rename.close();
                    self.open_workspace_pane();
                }
                self.workspace_ui.pick_and_attach();
            }
            CommandAction::OpenTransferSend => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.notes.close();
                if self.workspace_ui.is_modal() {
                    self.workspace_ui.close();
                }
                self.rename.close();
                self.transfer.open_send();
            }
            CommandAction::OpenTransferReceive => {
                self.palette.close();
                self.settings.close();
                self.help.close();
                self.notes.close();
                if self.workspace_ui.is_modal() {
                    self.workspace_ui.close();
                }
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
                if self
                    .open_surface(event_loop, SurfaceAdopt::Fresh, SurfacePlace::Cascade)
                    .is_none()
                {
                    self.toast.show("Couldn't open window");
                }
            }
            CommandAction::CheckUpdates => {
                self.palette.close();
                let ver = std::env::var("SUZURI_VERSION").unwrap_or_default();
                let ver = ver.trim();
                if ver.is_empty() || ver == "dev" {
                    self.toast.show("dev build — auto-update off");
                } else {
                    self.toast.show("checking for updates…");
                    self.update_mailbox.request_check();
                }
            }
            CommandAction::Quit => self.request_quit(event_loop),
        }
    }

    fn new_tab(&mut self) {
        let surface = self.event_surface_key();
        let layout = self.layout_for_surface(surface);
        let cell = self.cell_metrics();
        let (cols, rows) = renderer::terminal_grid_size_with(
            layout.panes.first().map(|p| &p.cells).unwrap_or(&layout.cells),
            cell.w,
            cell.h,
        );
        let (pw, ph) = self.pty_pixel_size(cols, rows);
        let (tid, pane_id) = self.session.new_tab_on_surface(cols, rows, surface);
        let rt = spawn_pane_runtime_px(cols, rows, pw, ph, &mut self.session, pane_id);
        self.runtimes.insert(pane_id, rt);
        self.set_surface_focus(surface, tid);
        self.warp_focused = true;
        self.terminal_focused = false;
    }

    fn split_pane(&mut self, axis: SplitAxis) {
        let layout = self.current_layout();
        let cell = self.cell_metrics();
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
            renderer::terminal_grid_size_with(&cells, cell.w, cell.h)
        };
        let (pw, ph) = self.pty_pixel_size(cols, rows);
        if let Some(new_id) = self.session.split_focused(axis, cols, rows) {
            let rt = spawn_pane_runtime_px(cols, rows, pw, ph, &mut self.session, new_id);
            self.runtimes.insert(new_id, rt);
            self.warp_focused = true;
            self.terminal_focused = false;
            self.sync_grids_to_panes();
        }
    }

    /// Apply a pane / tab re-dock. Returns true if a drag was active
    /// (so the mouse-up should not also click-activate chrome).
    fn finish_pane_drag(&mut self, event_loop: &ActiveEventLoop) -> bool {
        let Some(drag) = self.pane_drag.take() else {
            return false;
        };
        if !drag.active {
            return false;
        }
        match (drag.subject, drag.drop) {
            (
                DragSubject::Pane { pane_id, .. },
                Some(DropKind::Edge {
                    pane_id: target,
                    edge,
                }),
            ) => {
                if self.session.reparent_pane(pane_id, target, edge) {
                    self.sync_workspace_host();
                    self.warp_focused = !self.session.is_widget(pane_id);
                    self.terminal_focused = false;
                }
            }
            (DragSubject::Pane { pane_id, .. }, Some(DropKind::Tab { tab_id })) => {
                if self.session.move_pane_to_tab(pane_id, tab_id) {
                    self.sync_workspace_host();
                    self.warp_focused = !self.session.is_widget(pane_id);
                    self.terminal_focused = false;
                }
            }
            (DragSubject::Pane { pane_id, .. }, Some(DropKind::TearOff)) => {
                self.tear_off_pane(event_loop, pane_id);
            }
            (DragSubject::Tab { tab_id, .. }, Some(DropKind::TabInsert { index })) => {
                let _ = self
                    .session
                    .place_tab_on_surface(tab_id, drag.dest_surface, index);
                self.set_surface_focus(drag.dest_surface, tab_id);
            }
            (DragSubject::Tab { tab_id, .. }, Some(DropKind::TearOff)) => {
                self.tear_off_tab(event_loop, tab_id);
            }
            _ => {}
        }
        self.hide_drag_float();
        self.prune_empty_surfaces(event_loop);
        true
    }

    fn tear_off_tab(&mut self, event_loop: &ActiveEventLoop, tab_id: u64) {
        let Some(src_key) = self.session.surface_of_tab(tab_id) else {
            return;
        };
        let remaining = self.session.tabs_on_surface(src_key).len();
        if remaining <= 1 && self.surfaces.len() <= 1 {
            return;
        }
        if self
            .open_surface(event_loop, SurfaceAdopt::Tab(tab_id), SurfacePlace::UnderPointer)
            .is_none()
        {
            self.toast.show("Couldn't open window");
        }
    }

    fn tear_off_pane(&mut self, event_loop: &ActiveEventLoop, pane_id: u64) {
        let Some(src_tab) = self.session.tab_id_for_pane(pane_id) else {
            return;
        };
        let sole = self
            .session
            .tabs
            .iter()
            .find(|t| t.id == src_tab)
            .map(|t| t.root.leaf_ids().len() <= 1)
            .unwrap_or(true);
        if sole {
            self.tear_off_tab(event_loop, src_tab);
            return;
        }
        if self
            .open_surface(
                event_loop,
                SurfaceAdopt::Pane(pane_id),
                SurfacePlace::UnderPointer,
            )
            .is_none()
        {
            self.toast.show("Couldn't open window");
        }
    }

    fn prune_empty_surfaces(&mut self, event_loop: &ActiveEventLoop) {
        let empty: Vec<WindowId> = self
            .surfaces
            .iter()
            .filter(|(_, s)| self.session.tabs_on_surface(s.key).is_empty())
            .map(|(id, _)| *id)
            .collect();
        for id in empty {
            if self.ghost_host == Some(id) {
                self.drag_float = None;
                self.ghost_host = None;
            }
            self.surfaces.remove(&id);
            if self.event_win == Some(id) {
                self.event_win = self.surfaces.keys().next().copied();
                self.window = self
                    .event_win
                    .and_then(|eid| self.surfaces.get(&eid).map(|s| s.window.clone()));
            }
            if self.focus_win == Some(id) {
                self.focus_win = self.event_win;
            }
        }
        if self.surfaces.is_empty() || self.session.is_empty() {
            chrome_status::clear_status();
            event_loop.exit();
            return;
        }
        if let Some(eid) = self.focus_win.or(self.event_win) {
            let grabbed = self
                .surfaces
                .get(&eid)
                .map(|s| (s.key, s.window.clone()));
            if let Some((key, win)) = grabbed {
                win.focus_window();
                if let Some(tid) = self.surface_focus_tab(key) {
                    self.set_surface_focus(key, tid);
                }
            }
        }
    }

    fn close_surface(&mut self, event_loop: &ActiveEventLoop, id: WindowId) {
        if self.surfaces.len() <= 1 {
            self.request_quit(event_loop);
            return;
        }
        let Some(key) = self.surfaces.get(&id).map(|s| s.key) else {
            return;
        };
        let tabs = self.session.tabs_on_surface(key);
        for tid in tabs {
            for pid in self.session.close_tab(tid) {
                self.runtimes.remove(&pid);
            }
        }
        if self.ghost_host == Some(id) {
            self.drag_float = None;
            self.ghost_host = None;
        }
        self.surfaces.remove(&id);
        if self.event_win == Some(id) {
            self.event_win = self.surfaces.keys().next().copied();
            self.window = self
                .event_win
                .and_then(|eid| self.surfaces.get(&eid).map(|s| s.window.clone()));
        }
        if self.focus_win == Some(id) {
            self.focus_win = self.event_win;
            if let Some(eid) = self.focus_win {
                if let Some(key) = self.surfaces.get(&eid).map(|s| s.key) {
                    if let Some(tid) = self.surface_focus_tab(key) {
                        self.session.select_tab(tid);
                    }
                }
            }
        }
        self.prune_empty_surfaces(event_loop);
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

    fn select_tab_index(&mut self, index: usize) {
        if self.session.select_tab_index(index) {
            if let Some(tab) = self.session.active_tab() {
                let id = tab.id;
                let surface = tab.surface;
                self.set_surface_focus(surface, id);
            }
            self.warp_focused = true;
            self.terminal_focused = false;
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
        if self
            .workspace_ui
            .docked_pane
            .is_some_and(|id| finished.contains(&id))
        {
            self.workspace_ui.close();
        }
        for id in finished {
            self.runtimes.remove(id);
        }
        if self.session.is_empty() {
            event_loop.exit();
            return;
        }
        self.prune_empty_surfaces(event_loop);
        if let Some(tid) = self.session.active_tab().map(|t| t.id) {
            if let Some(surface) = self.session.surface_of_tab(tid) {
                self.set_surface_focus(surface, tid);
            }
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

    /// While a pane is on the alt screen (vim, grok, etc.), route keys to PTY,
    /// clear the warp draft, and expand the cell grid (no path/warp strip).
    /// When it leaves, restore command-line focus and resize back.
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
            // Don't leave shell draft visible under the TUI strip (if any).
            if let Some(p) = self.session.panes.get_mut(&id) {
                p.draft.clear();
            }
        } else if self.terminal_focused {
            // Left alt screen — back to command line.
            self.terminal_focused = false;
            self.warp_focused = true;
        }
        // Alt enter/leave changes cells height — keep grid/PTY in sync.
        self.sync_grids_to_panes();
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

    /// 1-based viewport cell under the pointer (xterm mouse protocol).
    /// Falls back to (1, 1) when the cursor is outside the well.
    fn term_screen_cell_1based(&self) -> (u16, u16) {
        let layout = self.current_layout();
        let focus = self.session.focus_pane_id();
        let Some(pl) = layout.panes.iter().find(|p| p.pane_id == focus) else {
            return (1, 1);
        };
        let Some(pane) = self.session.panes.get(&focus) else {
            return (1, 1);
        };
        let cells = pl.cells;
        let grid = &pane.grid;
        let cell = self.cell_metrics();
        let col = ((self.cursor.x - cells.x) / cell.w.max(1.0)).floor() as i32;
        let row = ((self.cursor.y - cells.y) / cell.h.max(1.0)).floor() as i32;
        let col = col.clamp(0, grid.cols().saturating_sub(1) as i32) as u16;
        let row = row.clamp(0, grid.rows().saturating_sub(1) as i32) as u16;
        (col + 1, row + 1)
    }

    /// Forward a wheel step to an alt-screen TUI (SGR 64/65 or arrow keys).
    fn forward_alt_wheel(&mut self, pane_id: u64, steps: i32) {
        let (col, row) = self.term_screen_cell_1based();
        let Some(rt) = self.runtimes.get_mut(&pane_id) else {
            return;
        };
        let Some(pty) = rt.pty.as_mut() else {
            return;
        };
        let bytes = encode_mouse_wheel(
            col,
            row,
            steps,
            rt.ansi.mouse_tracking,
            rt.ansi.mouse_sgr,
            rt.ansi.app_cursor,
        );
        if !bytes.is_empty() {
            let _ = pty.write_all(&bytes);
        }
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
        let cell = self.cell_metrics();
        let col = ((x - cells.x) / cell.w.max(1.0)).floor() as i32;
        let row = ((y - cells.y) / cell.h.max(1.0)).floor() as i32;
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
            let r = crate::commands::HelpLayout::modal_rect(win_w, win_h);
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
        if self.workspace_ui.is_modal()
            && self.workspace_ui.card_rect(win_w, win_h).contains(x, y)
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
        } else if self.overlay_open()
            || self.notes.visible()
            || self.workspace_ui.is_modal()
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
                } else if self.settings.open {
                    let layout = self.current_layout();
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    let _ = self
                        .settings
                        .try_click(self.cursor.x, self.cursor.y, win_w, win_h);
                } else if self.palette.open {
                    self.try_palette_click(event_loop);
                } else if self.notes.open {
                    let layout = self.current_layout();
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    self.notes
                        .try_click(self.cursor.x, self.cursor.y, win_w, win_h);
                } else if self.workspace_ui.is_modal() {
                    let layout = self.current_layout();
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    self.sync_workspace_host();
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
            HitTarget::Close => {
                if let Some(id) = self.event_win {
                    self.close_surface(event_loop, id);
                } else {
                    self.request_quit(event_loop);
                }
            }
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
                let surface = self.event_surface_key();
                if let Some(id) = self.session.tabs_on_surface(surface).get(i).copied() {
                    self.set_surface_focus(surface, id);
                    self.warp_focused = true;
                    self.terminal_focused = false;
                }
            }
            HitTarget::TabClose(i) => {
                let surface = self.event_surface_key();
                if let Some(tab) = self.session.tabs_on_surface(surface).get(i).copied() {
                    self.close_tab_by_id(event_loop, tab);
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
            HitTarget::Sash(_) => {}
            HitTarget::PaneChrome(pane_id) => {
                self.session.set_focus_pane(pane_id);
                if self.session.pane_kind(pane_id).is_workspace() {
                    self.sync_workspace_host();
                    self.warp_focused = false;
                    self.terminal_focused = false;
                } else {
                    self.warp_focused = true;
                    self.terminal_focused = false;
                }
            }
            HitTarget::WarpBar(pane_id) => {
                self.session.set_focus_pane(pane_id);
                if self.session.pane_kind(pane_id).is_workspace() {
                    self.sync_workspace_host();
                    let layout = self.current_layout();
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    let _ = self
                        .workspace_ui
                        .try_click(self.cursor.x, self.cursor.y, win_w, win_h);
                    self.warp_focused = false;
                    self.terminal_focused = false;
                } else {
                    self.warp_focused = true;
                    self.terminal_focused = false;
                }
            }
            HitTarget::Terminal(pane_id) => {
                self.session.set_focus_pane(pane_id);
                if self.session.pane_kind(pane_id).is_workspace() {
                    self.sync_workspace_host();
                    let layout = self.current_layout();
                    let win_w = layout.title.w;
                    let win_h = layout.workspace.y + layout.workspace.h + self.metrics.edge();
                    let _ = self
                        .workspace_ui
                        .try_click(self.cursor.x, self.cursor.y, win_w, win_h);
                    self.warp_focused = false;
                    self.terminal_focused = false;
                } else {
                    // Alt-screen TUIs own the keyboard; otherwise click focuses warp.
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
            }
            // Scrollbar clicks are handled on press/drag; release is a no-op.
            HitTarget::ScrollBar(pane_id) => {
                self.session.set_focus_pane(pane_id);
            }
            HitTarget::None => {}
        }
        self.remember_event_surface_tab();

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
            if self.pane_drag.is_some() {
                self.pane_drag = None;
                self.hide_drag_float();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            // Workspace: Esc cancels new-channel compose. Docked pane stays open (⌘W).
            if self.workspace_captures_input()
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
                || self.workspace_ui.is_modal()
                || self.transfer.visible()
                || self.rename.visible()
            {
                self.close_all_overlays();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            // Clear a drag-selection, then fall through so TUIs still get Esc.
            if !self.term_selection.is_empty() || self.selecting_term {
                self.term_selection.clear();
                self.selecting_term = false;
                self.last_term_click = None;
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            // Never quit on Escape — fullscreen TUIs (vim, less, Grok, …) use it
            // constantly. Forward CSI Esc to the live PTY.
            let id = self.session.focus_pane_id();
            if let Some(rt) = self.runtimes.get_mut(&id) {
                if let Some(pty) = &mut rt.pty {
                    let _ = pty.write_all(b"\x1b");
                    self.drain_all_ptys();
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
            }
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
            if self.workspace_captures_input() {
                match &event.logical_key {
                    Key::Named(NamedKey::Backspace) => {
                        self.workspace_ui.backspace();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::Enter) => {
                        // Completes @mention when picker open; otherwise posts.
                        self.workspace_ui.send();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    Key::Named(NamedKey::Tab) => {
                        // @mention picker cycles when open; else channel tabs.
                        self.workspace_ui.tab(shift);
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
            // Workspace: Ctrl+R refresh, Ctrl+Shift+A cycle presence, Ctrl+U path attach, Ctrl+Shift+U picker, Ctrl+D×2 delete channel.
            if self.workspace_captures_input() {
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
                        "u" | "U" if shift => {
                            self.workspace_ui.pick_and_attach();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        "d" | "D" if !shift => {
                            self.workspace_ui.request_delete_channel();
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
                    // Product: ctrl+k and ctrl+p both open the command palette.
                    "k" | "K" | "p" | "P" if !shift => {
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
                    // Product: ⇧⌘T new tab; also accept ⌘T.
                    "t" | "T" => {
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
                    // Prev / next tab: ⇧⌘[ ] and ⇧⌘← →
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
                    // ⌘1–9 jump to tab (product).
                    "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9" if !shift && !alt => {
                        if let Some(d) = ch.chars().next().and_then(|c| c.to_digit(10)) {
                            self.select_tab_index((d as usize).saturating_sub(1));
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                    }
                    // Zoom: ⌘+ / ⌘= / ⌘- / ⌘0
                    "+" | "=" if !shift || ch == "+" => {
                        self.nudge_zoom(0.1);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "-" | "_" => {
                        self.nudge_zoom(-0.1);
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    "0" if !shift => {
                        self.reset_zoom();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    // Clear warp draft: ⌘U (product clear-to-start).
                    "u" | "U" if !shift && self.warp_focused => {
                        self.session.draft_mut().clear();
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
                    // Product: ⇧⌘C copy; also ⌘C when selection / transfer ticket.
                    "c" | "C" if shift || !self.warp_focused || !self.term_selection.is_empty() => {
                        if self.transfer.open || self.transfer.visible() {
                            if self.copy_transfer_ticket_if_any() {
                                if let Some(w) = &self.window {
                                    w.request_redraw();
                                }
                                return;
                            }
                        }
                        if !self.term_selection.is_empty() {
                            self.copy_selection_if_any();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        // Warp focus + no selection: ⌘C clears draft (product clear/interrupt).
                        if self.warp_focused && !shift {
                            self.session.draft_mut().clear();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                    }
                    "c" | "C" if !shift && self.warp_focused => {
                        self.session.draft_mut().clear();
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                        return;
                    }
                    _ => {}
                }
            }
            // ⌘Tab / ⇧⌘Tab — next / prev tab
            if matches!(event.logical_key, Key::Named(NamedKey::Tab)) {
                if shift {
                    self.session.prev_tab();
                } else {
                    self.session.next_tab();
                }
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            // ⌘⌫ — clear warp line (product clear)
            if matches!(event.logical_key, Key::Named(NamedKey::Backspace))
                && self.warp_focused
                && !shift
            {
                self.session.draft_mut().clear();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
                return;
            }
            // ⌥⌘ arrows — pane focus; ⇧⌘ arrows — prev/next tab
            if let Key::Named(nk) = &event.logical_key {
                if shift && !alt {
                    match nk {
                        NamedKey::ArrowLeft | NamedKey::ArrowUp => {
                            self.session.prev_tab();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        NamedKey::ArrowRight | NamedKey::ArrowDown => {
                            self.session.next_tab();
                            if let Some(w) = &self.window {
                                w.request_redraw();
                            }
                            return;
                        }
                        _ => {}
                    }
                }
                if alt {
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
            let mut handled = false;
            match &event.logical_key {
                Key::Named(NamedKey::ArrowUp) => {
                    self.settings.move_selection(-1);
                    handled = true;
                }
                Key::Named(NamedKey::ArrowDown) | Key::Named(NamedKey::Tab) => {
                    self.settings.move_selection(1);
                    handled = true;
                }
                Key::Named(NamedKey::ArrowLeft) => {
                    handled = self.settings.nudge_selected(-1);
                }
                Key::Named(NamedKey::ArrowRight) => {
                    handled = self.settings.nudge_selected(1);
                }
                Key::Named(NamedKey::Enter) | Key::Named(NamedKey::Space) => {
                    handled = self.settings.activate_selected();
                }
                Key::Character(s) => {
                    // Shift+Tab → previous row (when we only get "\t" as char, rare).
                    if s.as_str() == "\t" {
                        self.settings
                            .move_selection(if self.modifiers.shift_key() { -1 } else { 1 });
                        handled = true;
                    } else {
                        handled = self.settings.handle_hotkey(s.as_str());
                    }
                }
                _ => {}
            }
            if handled {
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }
            return;
        }

        if self.help.open {
            return;
        }

        // Host scrollback navigation when not on alt-screen (product PageUp/Down).
        // On alt-screen, leave keys for the TUI / PTY path below.
        {
            let id = self.session.focus_pane_id();
            let alt = self
                .runtimes
                .get(&id)
                .map(|rt| rt.ansi.on_alt_screen())
                .unwrap_or(false);
            if !alt {
                let rows = self
                    .session
                    .active_grid()
                    .rows()
                    .max(1) as i32;
                let half = (rows / 2).max(1);
                let scrolled = match &event.logical_key {
                    Key::Named(NamedKey::PageUp) => {
                        self.session.active_grid_mut().scroll_view(half);
                        true
                    }
                    Key::Named(NamedKey::PageDown) => {
                        self.session.active_grid_mut().scroll_view(-half);
                        true
                    }
                    Key::Named(NamedKey::Home)
                        if self.modifiers.control_key() || self.modifiers.super_key() =>
                    {
                        let max = self.session.active_grid().max_view_offset() as i32;
                        self.session.active_grid_mut().scroll_view(max);
                        true
                    }
                    Key::Named(NamedKey::End)
                        if self.modifiers.control_key() || self.modifiers.super_key() =>
                    {
                        self.session.active_grid_mut().scroll_to_bottom();
                        true
                    }
                    _ => false,
                };
                if scrolled {
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                    return;
                }
            }
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
                    self.drain_all_ptys();
                    if let Some(w) = &self.window {
                        w.request_redraw();
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
        // Full-screen TUI owns the keyboard — don't inject warp commands.
        let id = self.session.focus_pane_id();
        if self
            .runtimes
            .get(&id)
            .map(|rt| rt.ansi.on_alt_screen())
            .unwrap_or(false)
        {
            return;
        }
        let line = self.session.draft().trim_end().to_string();
        if line.is_empty() {
            return;
        }
        self.session.draft_mut().clear();
        self.submit_line_text(&line);
    }

    /// Map pointer Y on the pane scrollbar track to `view_offset`.
    fn apply_scrollbar_drag(&mut self, pane_id: u64) {
        let layout = self.current_layout();
        let Some(pl) = layout.panes.iter().find(|p| p.pane_id == pane_id) else {
            return;
        };
        let track_h = pl.cells.h;
        if track_h < 8.0 {
            return;
        }
        let y_in = (self.cursor.y - pl.cells.y).clamp(0.0, track_h);
        if let Some(grid) = self.session.grid_mut(pane_id) {
            let frac = grid.scroll_fraction_from_track_y(y_in, track_h);
            grid.set_scroll_fraction(frac);
        }
    }

    /// Snapshot for MCP bridge: tabs + viewport/live lines + echo/blocks + PTY.
    ///
    /// Skips building the (expensive) snap unless the publisher is due — keeps
    /// selection / mouse frames snappy instead of rebuilding JSON every paint.
    fn publish_bridge_status(&mut self) {
        if !self.status_publisher.due() {
            return;
        }
        // While dragging a terminal selection, defer status I/O entirely so
        // press/drag/release paints aren't fighting disk writes.
        if self.selecting_term {
            return;
        }
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

        // Product applyBarSubmitToTab: commitLive → pushBlock → pin clear → arm echo.
        let alt = self
            .runtimes
            .get(&id)
            .map(|rt| rt.ansi.on_alt_screen())
            .unwrap_or(false);
        if let Some(rt) = self.runtimes.get_mut(&id) {
            rt.suppress_paint = false;
            if let Some(grid) = self.session.grid_mut(id) {
                let _ = rt.blocks.prepare_submit(&line, grid, &cwd_display, alt);
            }
            rt.echo.arm(&line);
        } else if let Some(grid) = self.session.grid_mut(id) {
            let mut log = CmdBlockLog::new();
            let _ = log.prepare_submit(&line, grid, &cwd_display, false);
        }

        // Product sendBarPayload: newlines → CR, trailing CR (not LF).
        let used_pty = if let Some(rt) = self.runtimes.get_mut(&id) {
            if let Some(pty) = &mut rt.pty {
                let buf = cmd_blocks::pty_submit_payload(&line);
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

    /// True when continuous frames are needed (rain, modal springs, scroll, etc.).
    fn needs_anim_frame(&self) -> bool {
        if self.settings.prefs.rain {
            return true;
        }
        if self.selecting_term || self.scroll_dragging.is_some() {
            return true;
        }
        if self.settings.visible()
            || self.palette.visible()
            || self.help.visible()
            || self.confirm.visible()
            || self.splash.visible()
            || self.notes.visible()
            || self.workspace_ui.visible()
            || self.transfer.visible()
            || self.rename.visible()
            || self.toast.visible()
        {
            return true;
        }
        for pane in self.session.panes.values() {
            if pane.grid.scroll_animating() {
                return true;
            }
        }
        // Caret blink while an input path is focused.
        if self.warp_focused
            || self.terminal_focused
            || self.workspace_captures_input()
            || self.pane_drag.as_ref().is_some_and(|d| d.active)
            || self.sash_drag.is_some()
            || self.session.tabs.iter().any(|t| t.solo_exit.is_some())
        {
            return true;
        }
        false
    }

    fn sync_window_fade(&self) {
        for s in self.surfaces.values() {
            let fade = self
                .surface_focus_tab(s.key)
                .and_then(|tid| self.session.tabs.iter().find(|t| t.id == tid))
                .and_then(|t| t.solo_exit.as_ref())
                .map(|a| a.opacity())
                .unwrap_or(1.0);
            #[cfg(target_os = "macos")]
            crate::macos_window::set_window_alpha(&s.window, fade as f64);
            let _ = fade;
        }
    }

}

impl ApplicationHandler for ChromeApp {
    fn resumed(&mut self, event_loop: &ActiveEventLoop) {
        if !self.surfaces.is_empty() {
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
        let wid = window.id();
        self.surfaces.insert(
            wid,
            Surface {
                key: 0,
                window: window.clone(),
                renderer,
                focus_tab: self.session.active_id,
            },
        );
        self.window = Some(window);
        self.event_win = Some(wid);
        self.focus_win = Some(wid);
        self.sync_grids_to_panes();

        if let Some(w) = &self.window {
            w.request_redraw();
        }
    }

    fn about_to_wait(&mut self, event_loop: &ActiveEventLoop) {
        // Lightweight wake path: drain PTY / mailboxes without a full GPU frame
        // so key-repeat and shell output stay responsive between paints.
        self.drain_all_ptys();
        for cmd in self.control_mailbox.poll() {
            self.run_action(event_loop, cmd.to_action());
            if matches!(cmd, crate::control_mailbox::ControlCommand::Quit) {
                return;
            }
        }
        self.apply_update_events();
        if let Some(line) = chrome_status::take_submit() {
            self.submit_line_text(&line);
            if let Some(w) = &self.window {
                w.request_redraw();
            }
        }

        // Schedule the next frame only when something is animating (composite
        // rain sample, springs, scroll ease, caret blink). Rain encode runs on
        // its own thread — this wake is just to blit the latest RT.
        let wake = if self.needs_anim_frame() {
            Duration::from_millis(16) // ~60 Hz while animating
        } else {
            Duration::from_millis(33) // PTY poll cadence when idle
        };
        event_loop.set_control_flow(ControlFlow::WaitUntil(Instant::now() + wake));
        if self.needs_anim_frame() {
            self.request_redraw_all();
        }
    }

    fn device_event(
        &mut self,
        _event_loop: &ActiveEventLoop,
        _device_id: winit::event::DeviceId,
        event: DeviceEvent,
    ) {
        if let DeviceEvent::MouseMotion { delta: (dx, dy) } = event {
            if self.pane_drag.as_ref().is_some_and(|d| d.active) {
                if !self.pointer_inside {
                    if let Some(g) = &self.drag_float {
                        if let Ok(pos) = g.window.outer_position() {
                            g.window.set_outer_position(
                                winit::dpi::PhysicalPosition::new(
                                    pos.x + dx as i32,
                                    pos.y + dy as i32,
                                ),
                            );
                            g.window.request_redraw();
                        }
                    }
                }
                return;
            }
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
        id: WindowId,
        event: WindowEvent,
    ) {
        if self.is_ghost_win(id) {
            if matches!(event, WindowEvent::RedrawRequested) {
                self.paint_drag_float();
            }
            return;
        }
        self.bind_win(id);
        match event {
            WindowEvent::CloseRequested => self.close_surface(event_loop, id),
            WindowEvent::Focused(true) => self.focus_surface_window(id),

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
                if let Some(pane_id) = self.scroll_dragging {
                    self.apply_scrollbar_drag(pane_id);
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                } else if let Some(sash) = self.sash_drag {
                    let t = match sash.axis {
                        crate::panes::SplitAxis::Vertical => {
                            (self.cursor.x - sash.parent.x) / sash.parent.w.max(1.0)
                        }
                        crate::panes::SplitAxis::Horizontal => {
                            (self.cursor.y - sash.parent.y) / sash.parent.h.max(1.0)
                        }
                    };
                    let _ = self.session.set_sash_ratio(sash.a_leaf, t);
                    self.sync_grids_to_panes();
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                } else if self.pane_drag.is_some() {
                    let (sx, sy) = {
                        let d = self.pane_drag.as_ref().expect("just checked");
                        (d.start.0, d.start.1)
                    };
                    let dx = self.cursor.x - sx;
                    let dy = self.cursor.y - sy;
                    let active = dx.hypot(dy) >= 8.0
                        || self.pane_drag.as_ref().is_some_and(|d| d.active);
                    if active {
                        let (drop, dest_surface) = match self.pointer_loc() {
                            PointerLoc::Outside => (Some(DropKind::TearOff), self.event_surface_key()),
                            PointerLoc::Surface { key, x, y } => {
                                let layout = self.layout_for_surface(key);
                                let drop = match self.pane_drag.as_ref().map(|d| &d.subject) {
                                    Some(DragSubject::Pane { pane_id, .. }) => classify_drop(
                                        &layout,
                                        &self.session,
                                        key,
                                        x,
                                        y,
                                        *pane_id,
                                    ),
                                    Some(DragSubject::Tab { tab_id, from_idx }) => {
                                        let from = self
                                            .session
                                            .surface_of_tab(*tab_id)
                                            .filter(|s| *s == key)
                                            .map(|_| *from_idx);
                                        classify_tab_drop(&layout, x, y, from)
                                    }
                                    None => None,
                                };
                                (drop, key)
                            }
                        };
                        if let Some(d) = self.pane_drag.as_mut() {
                            d.active = true;
                            d.drop = drop;
                            d.dest_surface = dest_surface;
                        }
                        self.ensure_drag_float(event_loop);
                    }
                    self.request_redraw_all();
                } else if self.selecting_term {
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
                let tear = self.pane_drag.as_ref().is_some_and(|d| d.active)
                    && matches!(self.pointer_loc(), PointerLoc::Outside);
                if tear {
                    if let Some(d) = self.pane_drag.as_mut() {
                        d.drop = Some(DropKind::TearOff);
                    }
                }
            }

            WindowEvent::MouseInput {
                state: ElementState::Pressed,
                button: MouseButton::Left,
                ..
            } => {
                self.focus_surface_window(id);
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
                if let HitTarget::PaneChrome(pane_id) = hit {
                    if !self.overlay_open() {
                        let source = self
                            .current_layout()
                            .panes
                            .iter()
                            .find(|p| p.pane_id == pane_id)
                            .map(|p| p.glass)
                            .unwrap_or_default();
                        self.pane_drag = Some(LayoutDrag {
                            subject: DragSubject::Pane { pane_id, source },
                            start: (self.cursor.x, self.cursor.y),
                            active: false,
                            drop: None,
                            dest_surface: self.event_surface_key(),
                            grab: (
                                (source.w * 0.55).clamp(120.0, 320.0) * 0.3,
                                12.0,
                            ),
                        });
                    }
                }
                if let HitTarget::Tab(i) = hit {
                    if !self.overlay_open() {
                        let surface = self.event_surface_key();
                        if let Some(tab_id) = self.session.tabs_on_surface(surface).get(i).copied() {
                            let chip = self
                                .current_layout()
                                .tab_chips
                                .get(i)
                                .copied()
                                .unwrap_or_default();
                            self.pane_drag = Some(LayoutDrag {
                                subject: DragSubject::Tab {
                                    tab_id,
                                    from_idx: i,
                                },
                                start: (self.cursor.x, self.cursor.y),
                                active: false,
                                drop: None,
                                dest_surface: surface,
                                grab: (
                                    (self.cursor.x - chip.x).clamp(8.0, chip.w.max(16.0) - 8.0),
                                    (self.cursor.y - chip.y).clamp(4.0, chip.h.max(8.0) - 4.0),
                                ),
                            });
                        }
                    }
                }
                if let HitTarget::Sash(a_leaf) = hit {
                    if !self.overlay_open() {
                        let layout = self.current_layout();
                        if let Some(s) = layout.sashes.iter().find(|s| s.a_leaf == a_leaf) {
                            self.sash_drag = Some(SashDrag {
                                a_leaf,
                                parent: s.parent,
                                axis: s.axis,
                            });
                        }
                    }
                }
                // Scrollbar track/thumb drag (right gutter of cell well).
                if let HitTarget::ScrollBar(pane_id) = hit {
                    if !self.overlay_open() {
                        self.scroll_dragging = Some(pane_id);
                        self.session.set_focus_pane(pane_id);
                        self.apply_scrollbar_drag(pane_id);
                        self.term_selection.clear();
                        self.selecting_term = false;
                        self.last_term_click = None;
                    }
                }
                // Terminal selection: single = cell drag, double = word, triple = line
                // (then drag keeps that mode via update_drag).
                else if matches!(hit, HitTarget::Terminal(_)) && !self.overlay_open() {
                    if let Some(pos) = self.term_cell_at_cursor() {
                        let clicks = self.term_click_count(pos);
                        self.apply_term_click_selection(pos, clicks);
                        // Start selection paint on this press — don't wait for
                        // the next continuous-redraw frame (felt ~½s late).
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                    }
                } else if !matches!(hit, HitTarget::Terminal(_) | HitTarget::ScrollBar(_)) {
                    if !self.term_selection.is_empty() || self.selecting_term {
                        self.term_selection.clear();
                        self.selecting_term = false;
                        self.last_term_click = None;
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                    } else {
                        self.term_selection.clear();
                        self.selecting_term = false;
                        self.last_term_click = None;
                    }
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
                let was_scroll = self.scroll_dragging.take().is_some();
                let was_sash = self.sash_drag.take().is_some();
                let pane_drag_done = self.finish_pane_drag(event_loop);
                if self.selecting_term {
                    self.term_selection.end();
                    self.selecting_term = false;
                    // End-drag paint immediately (selection stays, drag flag clears).
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
                // Activate only on release, and only if still over the same target.
                // Skip chrome activation if we were selecting terminal text or scrolling.
                if let Some(start) = self.press_hit.take() {
                    let end = self.hit_at_cursor();
                    if was_scroll || was_sash || pane_drag_done {
                        // already applied on drag
                    } else if start == end
                        && start != HitTarget::TitleDrag
                        && start != HitTarget::None
                        && !matches!(start, HitTarget::Terminal(_) | HitTarget::ScrollBar(_))
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
                        let idx = match start {
                            HitTarget::Tab(i) | HitTarget::TabClose(i) => Some(i),
                            _ => None,
                        };
                        if let Some(i) = idx {
                            let surface = self.event_surface_key();
                            if let Some(tab) = self.session.tabs_on_surface(surface).get(i).copied()
                            {
                                self.close_tab_by_id(event_loop, tab);
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
                            if let Some(r) = self.renderer_mut() {
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
                        if let Some(r) = self.renderer_mut() {
                            r.magnify_delta(step);
                        }
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                    }
                } else {
                    // LineDelta = discrete mouse wheel. PixelDelta = trackpad;
                    // macOS sends many sub-line events — accumulate or they vanish.
                    const PX_PER_LINE: f32 = 12.0;
                    let lines = match delta {
                        MouseScrollDelta::LineDelta(_, y) => {
                            self.wheel_accum = 0.0;
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
                            if !y.is_finite() {
                                0
                            } else {
                                self.wheel_accum += y;
                                let n = (self.wheel_accum / PX_PER_LINE).trunc() as i32;
                                self.wheel_accum -= n as f32 * PX_PER_LINE;
                                n
                            }
                        }
                    };
                    if lines != 0 {
                        let step = lines.clamp(-24, 24);
                        // Workspace chat owns the wheel over its pane (or modal).
                        if self.pointer_over_workspace() {
                            if step > 0 {
                                self.workspace_ui.scroll_up(step as usize);
                            } else {
                                self.workspace_ui.scroll_down((-step) as usize);
                            }
                        } else {
                            let id = self.session.focus_pane_id();
                            let alt = self
                                .runtimes
                                .get(&id)
                                .map(|rt| rt.ansi.on_alt_screen())
                                .unwrap_or(false);
                            if alt {
                                // Grok / vim / less: host history is suppressed.
                                // Forward wheel as SGR (if the app asked) or arrows.
                                self.forward_alt_wheel(id, step);
                            } else {
                                self.session.active_grid_mut().scroll_view(step);
                            }
                        }
                        if let Some(w) = &self.window {
                            w.request_redraw();
                        }
                    }
                }
            }

            WindowEvent::KeyboardInput { event, .. } => {
                self.focus_surface_window(id);
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
                let scale = self.window.as_ref().map(|w| w.scale_factor() as f32);
                if let (Some(r), Some(scale)) = (self.renderer_mut(), scale) {
                    r.resize(size, scale);
                }
                self.sync_grids_to_panes();
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }

            WindowEvent::ScaleFactorChanged { scale_factor, .. } => {
                let size = self.window.as_ref().map(|w| w.inner_size());
                if let (Some(r), Some(size)) = (self.renderer_mut(), size) {
                    r.resize(size, scale_factor as f32);
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
                self.apply_update_events();
                // MCP / host warp submit (chrome_submit mailbox).
                if let Some(line) = chrome_status::take_submit() {
                    self.submit_line_text(&line);
                }
                // Publish rich status for Go bridge proxy (`chrome_status.json`).
                self.publish_bridge_status();
                let now = Instant::now();
                let should_tick =
                    now.duration_since(self.last_world_tick) >= Duration::from_millis(8);
                let dt = 1.0 / 60.0;
                if should_tick {
                    self.last_world_tick = now;
                    for pane in self.session.panes.values_mut() {
                        let _ = pane.grid.tick_scroll(dt);
                    }
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
                    self.chip_ui.tick(dt);
                    self.sync_window_fade();
                }
                if self.session.is_empty() {
                    chrome_status::clear_status();
                    event_loop.exit();
                    return;
                }
                // Apply mono font only when prefs change (not every paint).
                let want_font = self.settings.prefs.font.clone();
                if self.applied_font != want_font {
                    for s in self.surfaces.values_mut() {
                        s.renderer.set_mono_font_id(&want_font);
                    }
                    self.applied_font = want_font;
                }

                // Cheap when cols/rows unchanged; needed during split animations.
                self.sync_grids_to_panes();
                self.update_chip_hover();

                let pty_on = self.any_pty_alive();
                let term_cursor = self.terminal_cursor_visible();
                let caret_alpha = self.input_caret_alpha();
                self.sync_workspace_host();
                let paint_key = self
                    .surfaces
                    .get(&id)
                    .map(|s| s.key)
                    .unwrap_or_else(|| self.event_surface_key());
                let layout = self.layout_for_surface(paint_key);
                let prev_active = self.session.active_id;
                if let Some(tid) = self.surface_focus_tab(paint_key) {
                    self.session.active_id = tid;
                }
                let loc = self.pointer_loc();
                let pointer = match loc {
                    PointerLoc::Surface { key, x, y } if key == paint_key => Some((x, y)),
                    _ => None,
                };
                let (ghost_x, ghost_y) = match loc {
                    PointerLoc::Surface { key, x, y } if key == paint_key => (x, y),
                    _ => (self.cursor.x, self.cursor.y),
                };
                let show_drag = self.pane_drag.as_ref().is_some_and(|d| d.active)
                    && matches!(loc, PointerLoc::Surface { key, .. } if key == paint_key)
                    || matches!(
                        self.pane_drag.as_ref().map(|d| d.drop),
                        Some(Some(DropKind::TearOff))
                    ) && self.focus_win == Some(id);

                let exit_blur = self
                    .surface_focus_tab(paint_key)
                    .and_then(|tid| self.session.tabs.iter().find(|t| t.id == tid))
                    .and_then(|t| t.solo_exit.as_ref())
                    .map(|a| a.blur_px())
                    .unwrap_or(0.0);
                if let Some(r) = self.surfaces.get_mut(&id).map(|s| &mut s.renderer) {
                    r.window_exit_blur = exit_blur;
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
                        self.pane_drag
                            .as_ref()
                            .filter(|d| d.active && show_drag)
                            .and_then(|d| d.drop),
                        self.pane_drag
                            .as_ref()
                            .filter(|d| d.active && show_drag && self.drag_float.is_none())
                            .and_then(|d| {
                            match &d.subject {
                                DragSubject::Pane { source, .. } => {
                                    let w = (source.w * 0.72).max(48.0);
                                    let h = (source.h * 0.72).max(36.0);
                                    Some(crate::layout::Rect::new(
                                        ghost_x - w * 0.3,
                                        ghost_y - 12.0,
                                        w,
                                        h,
                                    ))
                                }
                                DragSubject::Tab { .. } => {
                                    Some(crate::layout::Rect::new(
                                        ghost_x - 48.0,
                                        ghost_y - 16.0,
                                        96.0,
                                        32.0,
                                    ))
                                }
                            }
                        }),
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
                self.session.active_id = prev_active;
                // Do NOT always chain request_redraw — that + heavy frames
                // starved keyboard repeat. Continuous frames are scheduled
                // from about_to_wait via needs_anim_frame().
            }

            _ => {}
        }
    }
}
