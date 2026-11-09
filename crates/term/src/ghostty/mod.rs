//! The VT engine on libghostty-vt (card #290): ghostty's terminal parses
//! the bytes, keeps the grid, scrollback, modes and selection, and its
//! render state hands us the dirty rows. Colours and paint rules are still
//! suzuri's (palette.rs, Go suzuri's glyphToCell), the same as the
//! alacritty engine applied.
//!
//! What libghostty-vt has no call for is done with VT sequences fed to the
//! terminal itself (never to the PTY): the command block (#296) writes its
//! lines inside a scroll region, and an image mark (#288) is an OSC 8
//! hyperlink on the cell under the cursor, found again through a tracked
//! grid reference.

pub mod sys;

use std::ffi::c_void;
use std::ptr;
use std::time::{Duration, Instant};

use crate::engine::{Engine, EngineEvent, Link, SelectKind, MARK_URI};
use crate::frame::{self, CellOut, FrameHeader};
use crate::input::{self, MouseEvent, MouseModes};
use crate::palette;
use sys::*;

/// How long a synchronized update (DEC 2026) may hold the screen, as
/// alacritty_terminal's default.
const SYNC_TIMEOUT: Duration = Duration::from_millis(150);

/// What ends a double-click word: everything but letters, digits and "_",
/// as in Go suzuri's selection.go (isWordRune), and the box-drawing bar.
const WORD_BREAKS: &str = " \t!\"#$%&'()*+,-./:;<=>?@[\\]^`{|}~│";

/// Pack an RGB triple as 0x00RRGGBB.
fn pack((r, g, b): palette::Rgb) -> u32 {
    (r as u32) << 16 | (g as u32) << 8 | b as u32
}
fn unpack(v: u32) -> palette::Rgb {
    ((v >> 16) as u8, (v >> 8) as u8, v as u8)
}

/// A zeroed sized struct with its `size` field (always the first) set.
unsafe fn sized<T>() -> T {
    unsafe {
        let mut v: T = std::mem::zeroed();
        *(&mut v as *mut T as *mut usize) = std::mem::size_of::<T>();
        v
    }
}

fn mode(value: u16, ansi: bool) -> GhosttyMode {
    (value & 0x7fff) | ((ansi as u16) << 15)
}

/// State the terminal's callbacks write into, boxed so its address stays put.
#[derive(Default)]
struct Shared {
    events: Vec<EngineEvent>,
    cols: u16,
    rows: u16,
    /// A DEC 2026 hold started at this time.
    held_since: Option<Instant>,
    title_changed: bool,
}

unsafe fn shared<'a>(ud: *mut c_void) -> &'a mut Shared {
    unsafe { &mut *(ud as *mut Shared) }
}

unsafe extern "C" fn on_write_pty(_t: GhosttyTerminal, ud: *mut c_void, data: *const u8, len: usize) {
    let s = unsafe { shared(ud) };
    let bytes = unsafe { std::slice::from_raw_parts(data, len) };
    // Consecutive replies in one feed go out as one write.
    if let Some(EngineEvent::PtyWrite(v)) = s.events.last_mut() {
        v.extend_from_slice(bytes);
    } else {
        s.events.push(EngineEvent::PtyWrite(bytes.to_vec()));
    }
}

unsafe extern "C" fn on_bell(_t: GhosttyTerminal, ud: *mut c_void) {
    unsafe { shared(ud) }.events.push(EngineEvent::Bell);
}

unsafe extern "C" fn on_title(_t: GhosttyTerminal, ud: *mut c_void) {
    unsafe { shared(ud) }.title_changed = true;
}

unsafe extern "C" fn on_clipboard(_t: GhosttyTerminal, ud: *mut c_void, w: *const GhosttyClipboardWrite) {
    let s = unsafe { shared(ud) };
    let w = unsafe { &*w };
    let contents = if w.contents.is_null() { &[][..] } else { unsafe { std::slice::from_raw_parts(w.contents, w.contents_len) } };
    let text = contents.iter().find(|c| {
        let mime = unsafe { gstr(&c.mime) };
        mime.is_empty() || mime.starts_with(b"text/plain")
    });
    if let Some(c) = text {
        s.events.push(EngineEvent::Clipboard(String::from_utf8_lossy(unsafe { gstr(&c.data) }).into_owned()));
    }
    if let Some(reply) = w.reply {
        let mut r: GhosttyClipboardWriteReply = unsafe { sized() };
        r.result = GHOSTTY_CLIPBOARD_WRITE_RESULT_SUCCESS;
        unsafe { reply(w, &r) };
    }
}

unsafe extern "C" fn on_size(_t: GhosttyTerminal, ud: *mut c_void, out: *mut GhosttySizeReportSize) -> bool {
    let s = unsafe { shared(ud) };
    unsafe {
        (*out).rows = s.rows;
        (*out).columns = s.cols;
        (*out).cell_width = 8;
        (*out).cell_height = 16;
    }
    true
}

unsafe extern "C" fn on_device_attributes(_t: GhosttyTerminal, _ud: *mut c_void, out: *mut GhosttyDeviceAttributes) -> bool {
    // A VT220 with ANSI colour, as ghostty itself answers.
    let a = unsafe { &mut *out };
    a.primary.conformance_level = GHOSTTY_DA_CONFORMANCE_VT220 as u16;
    a.primary.features[0] = GHOSTTY_DA_FEATURE_ANSI_COLOR as u16;
    a.primary.num_features = 1;
    a.secondary.device_type = 1;
    a.secondary.firmware_version = 10;
    true
}

unsafe extern "C" fn on_render_hold(_t: GhosttyTerminal, ud: *mut c_void, held: bool) {
    let s = unsafe { shared(ud) };
    s.held_since = if held { Some(Instant::now()) } else { None };
}

unsafe fn gstr(s: &GhosttyString) -> &[u8] {
    if s.ptr.is_null() || s.len == 0 { &[] } else { unsafe { std::slice::from_raw_parts(s.ptr, s.len) } }
}

/// Where a cell selection starts: a cell of the screen (scrollback
/// included, y counted from the top of the scrollback) and its half.
#[derive(Clone, Copy)]
struct Anchor {
    kind: SelectKind,
    /// Tracked, so it stays on its text as output scrolls.
    at: GhosttyTrackedGridRef,
    right: bool,
}

pub struct Ghostty {
    t: GhosttyTerminal,
    rs: GhosttyRenderState,
    rows_it: GhosttyRenderStateRowIterator,
    cells: GhosttyRenderStateRowCells,
    shared: Box<Shared>,
    full_next: bool,
    row_scratch: Vec<CellOut>,
    sel_shown: bool,
    anchor: Option<Anchor>,
    pal: palette::Active,
    /// The theme's colours were given to the terminal for this theme.
    pal_set: Option<(usize, palette::AnsiMap)>,
    default_cursor: u8,
    /// Image marks (#288): the tag and a tracked reference to its cell. The
    /// reference follows the cell through scrolling, history and reflow;
    /// the cell's OSC 8 link says the mark is still there, since rewriting
    /// or erasing the cell drops the link, as alacritty's cell mark was.
    marks: Vec<(u32, GhosttyTrackedGridRef)>,
    /// The last frame's modes and (history, offset), sent again while a
    /// synchronized update holds the screen.
    last_modes: u32,
    last_hist: (usize, usize),
    /// ghostty's own mouse encoder and an event, to learn which tracking
    /// mode is in effect (see mouse_tracking).
    menc: GhosttyMouseEncoder,
    mev: GhosttyMouseEvent,
}

// The terminal is only ever used from one thread at a time (the pane holds
// it behind a lock); libghostty-vt has no thread affinity.
unsafe impl Send for Ghostty {}

impl Drop for Ghostty {
    fn drop(&mut self) {
        unsafe {
            if let Some(a) = self.anchor.take() {
                ghostty_tracked_grid_ref_free(a.at);
            }
            for (_, r) in self.marks.drain(..) {
                ghostty_tracked_grid_ref_free(r);
            }
            ghostty_mouse_event_free(self.mev);
            ghostty_mouse_encoder_free(self.menc);
            ghostty_render_state_row_cells_free(self.cells);
            ghostty_render_state_row_iterator_free(self.rows_it);
            ghostty_render_state_free(self.rs);
            ghostty_terminal_free(self.t);
        }
    }
}

macro_rules! ok {
    ($e:expr) => {{
        let r = $e;
        assert!(r == GHOSTTY_SUCCESS, "{} failed: {}", stringify!($e), r);
    }};
}

impl Ghostty {
    pub fn new(cols: u16, rows: u16, scrollback: usize) -> Self {
        let (cols, rows) = (cols.max(2), rows.max(1));
        let mut shared = Box::new(Shared { cols, rows, ..Default::default() });
        unsafe {
            let mut t: GhosttyTerminal = ptr::null_mut();
            ok!(ghostty_terminal_new(ptr::null(), &mut t, cols, rows));
            let ud = &mut *shared as *mut Shared as *mut c_void;
            let set = |opt: GhosttyTerminalOption, v: *const c_void| ok!(ghostty_terminal_set(t, opt, v));
            set(GHOSTTY_TERMINAL_OPT_USERDATA, ud);
            set(GHOSTTY_TERMINAL_OPT_WRITE_PTY, on_write_pty as *const c_void);
            set(GHOSTTY_TERMINAL_OPT_BELL, on_bell as *const c_void);
            set(GHOSTTY_TERMINAL_OPT_TITLE_CHANGED, on_title as *const c_void);
            set(GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE, on_clipboard as *const c_void);
            set(GHOSTTY_TERMINAL_OPT_SIZE, on_size as *const c_void);
            set(GHOSTTY_TERMINAL_OPT_DEVICE_ATTRIBUTES, on_device_attributes as *const c_void);
            set(GHOSTTY_TERMINAL_OPT_RENDER_HOLD, on_render_hold as *const c_void);
            // ghostty prunes whole pages (a few hundred lines) once past the
            // limit, so it can keep fewer lines than asked; a page more
            // keeps at least `scrollback` lines, as alacritty does.
            let lines = if scrollback == 0 { 0 } else { scrollback as u64 + 1024 };
            set(GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_LINES, &lines as *const u64 as *const c_void);
            // The line limit is the only limit, as alacritty's: ghostty's
            // default byte limit otherwise caps the history at a few hundred
            // lines of 100 columns.
            set(GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, ptr::null());
            // Kitty graphics stay on suzuri's path (protocols.rs takes the
            // APCs before the engine sees them); ghostty keeps none itself.
            let kitty_limit = 0u64;
            set(GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_STORAGE_LIMIT, &kitty_limit as *const u64 as *const c_void);
            ok!(ghostty_terminal_resize(t, cols, rows, 8, 16));

            let mut rs = ptr::null_mut();
            ok!(ghostty_render_state_new(ptr::null(), &mut rs));
            let mut it = ptr::null_mut();
            ok!(ghostty_render_state_row_iterator_new(ptr::null(), &mut it));
            let mut cells = ptr::null_mut();
            ok!(ghostty_render_state_row_cells_new(ptr::null(), &mut cells));
            let mut menc = ptr::null_mut();
            ok!(ghostty_mouse_encoder_new(ptr::null(), &mut menc));
            let mut mev = ptr::null_mut();
            ok!(ghostty_mouse_event_new(ptr::null(), &mut mev));
            // A 1x1 cell grid of 1px cells is all the probe needs; report
            // every motion, not only cell changes.
            let mut sz: GhosttyMouseEncoderSize = sized();
            (sz.screen_width, sz.screen_height, sz.cell_width, sz.cell_height) = (4, 4, 1, 1);
            ghostty_mouse_encoder_setopt(menc, GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &sz as *const _ as *const c_void);
            let track = false;
            ghostty_mouse_encoder_setopt(menc, GHOSTTY_MOUSE_ENCODER_OPT_TRACK_LAST_CELL, &track as *const bool as *const c_void);
            ghostty_mouse_event_set_position(mev, GhosttyMousePosition { x: 1.5, y: 1.5 });
            let mut g = Ghostty {
                t,
                rs,
                rows_it: it,
                cells,
                shared,
                full_next: true,
                row_scratch: Vec::new(),
                sel_shown: false,
                anchor: None,
                pal: palette::active(),
                pal_set: None,
                default_cursor: 1,
                marks: Vec::new(),
                last_modes: 0,
                last_hist: (0, 0),
                menc,
                mev,
            };
            g.apply_palette();
            // Grapheme clustering (DEC 2027): 👍🏽, 👨‍👩‍👧 and 🇯🇵 each take one
            // cell (two columns wide). As the default, so RIS keeps it;
            // programs can still turn it off. RSZ_GRAPHEME_CLUSTER=0 opts out.
            if std::env::var("RSZ_GRAPHEME_CLUSTER").as_deref() != Ok("0") {
                let m = GhosttyTerminalModeConfig { mode: mode(2027, false), value: true };
                g.set(GHOSTTY_TERMINAL_OPT_MODE_DEFAULT, &m as *const _ as *const c_void);
            }
            // Kitty keyboard flags are kept per screen by the terminal.
            g
        }
    }

    fn set(&mut self, opt: GhosttyTerminalOption, v: *const c_void) {
        unsafe { ok!(ghostty_terminal_set(self.t, opt, v)) }
    }

    fn get<T>(&self, data: GhosttyTerminalData, out: &mut T) -> bool {
        unsafe { ghostty_terminal_get(self.t, data, out as *mut T as *mut c_void) == GHOSTTY_SUCCESS }
    }

    fn mode(&self, value: u16) -> bool {
        let mut m = GhosttyTerminalModeConfig { mode: mode(value, false), value: false };
        self.get(GHOSTTY_TERMINAL_DATA_MODE, &mut m) && m.value
    }

    /// Give the terminal the theme's colours as its defaults (OSC 4/10/11
    /// queries answer with them; a program's overrides stay on top).
    fn apply_palette(&mut self) {
        let key = (self.pal.theme as *const _ as usize, self.pal.map);
        if self.pal_set == Some(key) {
            return;
        }
        self.pal_set = Some(key);
        let ansi = self.pal.ansi16;
        let mut p = [GhosttyColorRgb { r: 0, g: 0, b: 0 }; 256];
        for (i, c) in p.iter_mut().enumerate() {
            let (r, g, b) = palette::xterm256(&ansi, i as u8);
            *c = GhosttyColorRgb { r, g, b };
        }
        self.set(GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, p.as_ptr() as *const c_void);
        let c = &self.pal.theme.chrome;
        let fg = GhosttyColorRgb { r: c.text.0, g: c.text.1, b: c.text.2 };
        let bg = GhosttyColorRgb { r: c.void.0, g: c.void.1, b: c.void.2 };
        self.set(GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, &fg as *const _ as *const c_void);
        self.set(GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, &bg as *const _ as *const c_void);
    }

    fn drain(&mut self, events: &mut Vec<EngineEvent>) {
        if std::mem::take(&mut self.shared.title_changed) {
            let mut s = GhosttyString { ptr: ptr::null(), len: 0 };
            let title = if self.get(GHOSTTY_TERMINAL_DATA_TITLE, &mut s) { String::from_utf8_lossy(unsafe { gstr(&s) }).into_owned() } else { String::new() };
            // The title event goes where it happened relative to replies:
            // after them is close enough, nobody orders the two.
            self.shared.events.push(EngineEvent::Title(title));
        }
        events.append(&mut self.shared.events);
    }

    fn scrollbar(&self) -> GhosttyTerminalScrollbar {
        let mut sb = GhosttyTerminalScrollbar { total: 0, offset: 0, len: 0 };
        self.get(GHOSTTY_TERMINAL_DATA_SCROLLBAR, &mut sb);
        sb
    }

    /// Rows of history above the screen, and how far the view is scrolled
    /// up into it.
    fn history_and_offset(&self) -> (usize, usize) {
        let sb = self.scrollbar();
        let history = sb.total.saturating_sub(sb.len) as usize;
        (history, history.saturating_sub(sb.offset as usize))
    }

    fn grid_ref(&self, tag: GhosttyPointTag, x: u16, y: u32) -> Option<GhosttyGridRef> {
        unsafe {
            let mut r: GhosttyGridRef = sized();
            let p = GhosttyPoint { tag, value: GhosttyPointValue { coordinate: GhosttyPointCoordinate { x, y } } };
            (ghostty_terminal_grid_ref(self.t, p, &mut r) == GHOSTTY_SUCCESS).then_some(r)
        }
    }

    fn cell_wide(r: &GhosttyGridRef) -> GhosttyCellWide {
        unsafe {
            let mut c: GhosttyCell = 0;
            let mut w: GhosttyCellWide = GHOSTTY_CELL_WIDE_NARROW;
            if ghostty_grid_ref_cell(r, &mut c) == GHOSTTY_SUCCESS {
                ghostty_cell_get(c, GHOSTTY_CELL_DATA_WIDE, &mut w as *mut _ as *mut c_void);
            }
            w
        }
    }

    /// A cell's text: its base character, ' ' when blank, None for the
    /// right half of a wide character.
    fn cell_char(r: &GhosttyGridRef) -> Option<char> {
        unsafe {
            let mut buf = [0u32; 16];
            let mut n = 0usize;
            let res = ghostty_grid_ref_graphemes(r, buf.as_mut_ptr(), buf.len(), &mut n);
            if res == GHOSTTY_SUCCESS && n > 0 {
                return Some(char::from_u32(buf[0]).unwrap_or(' '));
            }
        }
        match Self::cell_wide(r) {
            GHOSTTY_CELL_WIDE_SPACER_TAIL => None,
            _ => Some(' '),
        }
    }

    fn row_text(&self, tag: GhosttyPointTag, y: u32, cols: u16) -> String {
        let mut line = String::new();
        for x in 0..cols {
            if let Some(c) = self.grid_ref(tag, x, y).and_then(|r| Self::cell_char(&r)) {
                line.push(c);
            }
        }
        line.trim_end().to_string()
    }

    fn hyperlink(r: &GhosttyGridRef) -> Option<String> {
        unsafe {
            let mut buf = [0u8; 2048];
            let mut n = 0usize;
            if ghostty_grid_ref_hyperlink_uri(r, buf.as_mut_ptr(), buf.len(), &mut n) == GHOSTTY_SUCCESS && n > 0 && n <= buf.len() {
                return Some(String::from_utf8_lossy(&buf[..n]).into_owned());
            }
        }
        None
    }

    fn feed_internal(&mut self, bytes: &[u8]) {
        unsafe { ghostty_terminal_vt_write(self.t, bytes.as_ptr(), bytes.len()) }
    }

    /// Resolve a palette index or the default ink through the terminal's
    /// colours (the theme plus a program's OSC overrides).
    fn colors(&self) -> GhosttyRenderStateColors {
        unsafe {
            let mut c: GhosttyRenderStateColors = sized();
            ghostty_render_state_get(self.rs, GHOSTTY_RENDER_STATE_DATA_COLORS, &mut c as *mut _ as *mut c_void);
            c
        }
    }

    /// A program set the background with OSC 11.
    fn bg_override(&self) -> Option<palette::Rgb> {
        let mut a = GhosttyColorRgb { r: 0, g: 0, b: 0 };
        let mut d = a;
        if self.get(GHOSTTY_TERMINAL_DATA_COLOR_BACKGROUND, &mut a) && self.get(GHOSTTY_TERMINAL_DATA_COLOR_BACKGROUND_DEFAULT, &mut d) && (a.r, a.g, a.b) != (d.r, d.g, d.b) {
            return Some((a.r, a.g, a.b));
        }
        None
    }

    fn encode_row(&mut self, y: u16, out: &mut Vec<u8>, colors: &GhosttyRenderStateColors, bg_over: Option<palette::Rgb>) {
        self.row_scratch.clear();
        let mut sel = unsafe { sized::<GhosttyRenderStateRowSelection>() };
        let has_sel = unsafe { ghostty_render_state_row_get(self.rows_it, GHOSTTY_RENDER_STATE_ROW_DATA_SELECTION, &mut sel as *mut _ as *mut c_void) } == GHOSTTY_SUCCESS;
        unsafe {
            ghostty_render_state_row_get(self.rows_it, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &mut self.cells as *mut _ as *mut c_void);
        }
        let c = &self.pal.theme.chrome;
        let term_bg = pack(palette::term_bg(c));
        let pal = |i: u8| {
            let p = colors.palette[i as usize];
            (p.r, p.g, p.b)
        };
        let default_fg = (colors.foreground.r, colors.foreground.g, colors.foreground.b);
        let mut x: u16 = 0;
        while unsafe { ghostty_render_state_row_cells_next(self.cells) } {
            let get = |d: GhosttyRenderStateRowCellsData, out: *mut c_void| unsafe { ghostty_render_state_row_cells_get(self.cells, d, out) };
            let mut n = 0u32;
            get(GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, &mut n as *mut _ as *mut c_void);
            let cp = if n > 0 {
                let mut buf = [0u32; 16];
                if n as usize <= buf.len() {
                    get(GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF, buf.as_mut_ptr() as *mut c_void);
                    buf[0]
                } else {
                    let mut big = vec![0u32; n as usize];
                    get(GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF, big.as_mut_ptr() as *mut c_void);
                    big[0]
                }
            } else {
                // A blank cell is a space; either spacer of a wide
                // character is 0, as the alacritty engine sent them.
                let mut raw: GhosttyCell = 0;
                let mut wide: GhosttyCellWide = GHOSTTY_CELL_WIDE_NARROW;
                if get(GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &mut raw as *mut _ as *mut c_void) == GHOSTTY_SUCCESS {
                    unsafe { ghostty_cell_get(raw, GHOSTTY_CELL_DATA_WIDE, &mut wide as *mut _ as *mut c_void) };
                }
                if wide == GHOSTTY_CELL_WIDE_SPACER_TAIL || wide == GHOSTTY_CELL_WIDE_SPACER_HEAD { 0 } else { ' ' as u32 }
            };
            let mut styled = false;
            get(GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_HAS_STYLING, &mut styled as *mut _ as *mut c_void);
            let mut st: GhosttyStyle = unsafe { sized() };
            if styled {
                get(GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &mut st as *mut _ as *mut c_void);
            }
            let bold = st.bold;
            let default_ink = st.fg_color.tag == GHOSTTY_STYLE_COLOR_NONE;
            let mut fg = match st.fg_color.tag {
                GHOSTTY_STYLE_COLOR_RGB => {
                    let v = unsafe { st.fg_color.value.rgb };
                    pack((v.r, v.g, v.b))
                }
                GHOSTTY_STYLE_COLOR_PALETTE => {
                    let i = unsafe { st.fg_color.value.palette };
                    pack(pal(if bold && i < 8 { i + 8 } else { i }))
                }
                _ => pack(default_fg),
            };
            let mut bgc = GhosttyColorRgb { r: 0, g: 0, b: 0 };
            let mut bg = if styled || n == 0 {
                if get(GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &mut bgc as *mut _ as *mut c_void) == GHOSTTY_SUCCESS {
                    pack((bgc.r, bgc.g, bgc.b))
                } else {
                    bg_over.map(pack).unwrap_or(frame::DEFAULT_BG)
                }
            } else {
                bg_over.map(pack).unwrap_or(frame::DEFAULT_BG)
            };
            // Go suzuri's paint rules (internal/ui/color.go glyphToCell);
            // see the alacritty engine for the measurements behind them.
            if st.faint {
                let d = palette::dim_faint(unpack(fg));
                fg = if default_ink && (d.0 as u32 + d.1 as u32 + d.2 as u32) < 80 { pack((140, 140, 140)) } else { pack(d) };
            }
            if bold {
                fg = pack(palette::brighten_bold(unpack(fg)));
            }
            if st.inverse {
                let default_rev = default_ink && bg == frame::DEFAULT_BG;
                let (mut f, mut b) = (if bg == frame::DEFAULT_BG { term_bg } else { bg }, fg);
                if default_rev {
                    (f, b) = (pack(c.text), pack(c.panel));
                } else if palette::near_black(unpack(b)) {
                    b = pack(c.panel);
                    if palette::near_black(unpack(f)) {
                        f = pack(c.text);
                    }
                } else if palette::low_contrast(unpack(f), unpack(b)) {
                    (f, b) = (pack(c.text), pack(c.panel));
                }
                (fg, bg) = (f, b);
            }
            if bg != frame::DEFAULT_BG && palette::bg_shows_through(unpack(bg), c) {
                bg = frame::DEFAULT_BG;
            }
            let mut attrs = 0u16;
            if bold {
                attrs |= frame::A_BOLD;
            }
            if st.italic {
                attrs |= frame::A_ITALIC;
            }
            if st.underline != 0 {
                attrs |= frame::A_UNDERLINE;
            }
            if st.strikethrough {
                attrs |= frame::A_STRIKE;
            }
            if st.faint {
                attrs |= frame::A_DIM;
            }
            if st.invisible {
                attrs |= frame::A_HIDDEN;
            }
            if has_sel && x >= sel.start_x && x <= sel.end_x {
                attrs |= frame::A_SELECTED;
            }
            self.row_scratch.push(CellOut { cp, fg, bg, attrs });
            x += 1;
        }
        frame::encode_row(out, y, 0, &self.row_scratch);
    }

    /// The selection's screen point for a cell of the view.
    fn view_point(&self, col: u16, row: u16) -> (u16, u32) {
        let (cols, rows) = (self.shared.cols, self.shared.rows);
        let sb = self.scrollbar();
        (col.min(cols - 1), sb.offset as u32 + row.min(rows - 1) as u32)
    }

    fn tracked_screen_point(r: GhosttyTrackedGridRef) -> Option<(u16, u32)> {
        unsafe {
            let mut p = GhosttyPointCoordinate { x: 0, y: 0 };
            (ghostty_tracked_grid_ref_point(r, GHOSTTY_POINT_TAG_SCREEN, &mut p) == GHOSTTY_SUCCESS).then_some((p.x, p.y))
        }
    }

    fn set_selection(&mut self, a: (u16, u32), b: (u16, u32)) -> bool {
        let (Some(s), Some(e)) = (self.grid_ref(GHOSTTY_POINT_TAG_SCREEN, a.0, a.1), self.grid_ref(GHOSTTY_POINT_TAG_SCREEN, b.0, b.1)) else {
            return false;
        };
        let mut sel: GhosttySelection = unsafe { sized() };
        sel.start = s;
        sel.end = e;
        self.set(GHOSTTY_TERMINAL_OPT_SELECTION, &sel as *const _ as *const c_void);
        true
    }

    fn clear_selection(&mut self) {
        self.set(GHOSTTY_TERMINAL_OPT_SELECTION, ptr::null());
    }

    fn current_selection(&self) -> Option<GhosttySelection> {
        let mut sel: GhosttySelection = unsafe { sized() };
        self.get(GHOSTTY_TERMINAL_DATA_SELECTION, &mut sel).then_some(sel)
    }

    fn selection_points(&self, sel: &GhosttySelection) -> Option<((u16, u32), (u16, u32))> {
        unsafe {
            let mut a = GhosttyPointCoordinate { x: 0, y: 0 };
            let mut b = a;
            if ghostty_terminal_point_from_grid_ref(self.t, &sel.start, GHOSTTY_POINT_TAG_SCREEN, &mut a) != GHOSTTY_SUCCESS || ghostty_terminal_point_from_grid_ref(self.t, &sel.end, GHOSTTY_POINT_TAG_SCREEN, &mut b) != GHOSTTY_SUCCESS {
                return None;
            }
            Some(((a.x, a.y), (b.x, b.y)))
        }
    }

    /// The word or line around a screen point, as a pair of points.
    fn unit_at(&self, kind: SelectKind, p: (u16, u32)) -> Option<((u16, u32), (u16, u32))> {
        let r = self.grid_ref(GHOSTTY_POINT_TAG_SCREEN, p.0, p.1)?;
        let mut out: GhosttySelection = unsafe { sized() };
        let res = unsafe {
            match kind {
                SelectKind::Word => {
                    let breaks: Vec<u32> = WORD_BREAKS.chars().map(|c| c as u32).collect();
                    let mut o: GhosttyTerminalSelectWordOptions = sized();
                    o.ref_ = r;
                    o.boundary_codepoints = breaks.as_ptr();
                    o.boundary_codepoints_len = breaks.len();
                    ghostty_terminal_select_word(self.t, &o, &mut out)
                }
                _ => {
                    let mut o: GhosttyTerminalSelectLineOptions = sized();
                    o.ref_ = r;
                    ghostty_terminal_select_line(self.t, &o, &mut out)
                }
            }
        };
        if res != GHOSTTY_SUCCESS {
            return None;
        }
        self.selection_points(&out)
    }

    /// The frame's mode bits.
    fn modes(&self) -> u32 {
        let mut modes = 0u32;
        let map = [
            (1, frame::M_APP_CURSOR),
            (2004, frame::M_BRACKETED_PASTE),
            (1004, frame::M_FOCUS),
            (1006, frame::M_SGR_MOUSE),
            (1007, frame::M_ALT_SCROLL),
            (66, frame::M_APP_KEYPAD),
        ];
        for (m, bit) in map {
            if self.mode(m) {
                modes |= bit;
            }
        }
        if self.alt_screen() {
            modes |= frame::M_ALT_SCREEN;
        }
        let (click, drag, motion) = self.mouse_tracking();
        for (on, bit) in [(click, frame::M_MOUSE_CLICK), (drag, frame::M_MOUSE_DRAG), (motion, frame::M_MOUSE_MOTION)] {
            if on {
                modes |= bit;
            }
        }
        let mut kitty: GhosttyKittyKeyFlags = 0;
        self.get(GHOSTTY_TERMINAL_DATA_KITTY_KEYBOARD_FLAGS, &mut kitty);
        modes |= ((kitty & 31) as u32) << frame::M_KITTY_SHIFT;
        modes
    }

    /// The mouse tracking mode in effect, as (click, drag, motion), at most
    /// one of them. The tracking modes replace each other (xterm,
    /// alacritty): after 1000h 1003h 1003l nothing is reported. ghostty
    /// keeps that state for its mouse encoder only (its mode bits stay set
    /// per mode), so ask the encoder: does it report a bare motion, a drag,
    /// or neither?
    fn mouse_tracking(&self) -> (bool, bool, bool) {
        let mut on = false;
        if !self.get(GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &mut on) || !on {
            return (false, false, false);
        }
        unsafe {
            ghostty_mouse_encoder_setopt_from_terminal(self.menc, self.t);
            let reports = |action: GhosttyMouseAction, pressed: bool| {
                ghostty_mouse_encoder_setopt(self.menc, GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED, &pressed as *const bool as *const c_void);
                ghostty_mouse_event_set_action(self.mev, action);
                if pressed {
                    ghostty_mouse_event_set_button(self.mev, GHOSTTY_MOUSE_BUTTON_LEFT);
                } else {
                    ghostty_mouse_event_clear_button(self.mev);
                }
                let mut buf = [0u8; 32];
                let mut n = 0usize;
                ghostty_mouse_encoder_encode(self.menc, self.mev, buf.as_mut_ptr() as *mut _, buf.len(), &mut n) == GHOSTTY_SUCCESS && n > 0
            };
            if reports(GHOSTTY_MOUSE_ACTION_MOTION, false) {
                (false, false, true)
            } else if reports(GHOSTTY_MOUSE_ACTION_MOTION, true) {
                (false, true, false)
            } else {
                // Normal or X10 tracking report presses; none, nothing.
                (reports(GHOSTTY_MOUSE_ACTION_PRESS, true), false, false)
            }
        }
    }

    /// The size the render state was last updated at.
    fn rs_size(&self) -> (u16, u16) {
        let (mut c, mut r) = (self.shared.cols, self.shared.rows);
        unsafe {
            ghostty_render_state_get(self.rs, GHOSTTY_RENDER_STATE_DATA_COLS, &mut c as *mut _ as *mut c_void);
            ghostty_render_state_get(self.rs, GHOSTTY_RENDER_STATE_DATA_ROWS, &mut r as *mut _ as *mut c_void);
        }
        (c, r)
    }

    fn mode_ansi(&self, value: u16) -> bool {
        let mut m = GhosttyTerminalModeConfig { mode: mode(value, true), value: false };
        self.get(GHOSTTY_TERMINAL_DATA_MODE, &mut m) && m.value
    }

    fn set_mode(&mut self, value: u16, on: bool) {
        let m = GhosttyTerminalModeConfig { mode: mode(value, true), value: on };
        self.set(GHOSTTY_TERMINAL_OPT_MODE, &m as *const _ as *const c_void);
    }

    /// The mark's cell still carries its link (a tracked reference that
    /// lost its cell, to a reset or the history's end, has none).
    fn mark_alive(r: GhosttyTrackedGridRef, tag: u32) -> bool {
        unsafe {
            if !ghostty_tracked_grid_ref_has_value(r) {
                return false;
            }
            let mut g: GhosttyGridRef = sized();
            if ghostty_tracked_grid_ref_snapshot(r, &mut g) != GHOSTTY_SUCCESS {
                return false;
            }
            Self::hyperlink(&g).and_then(|u| u.strip_prefix(MARK_URI).and_then(|t| t.parse::<u32>().ok())) == Some(tag)
        }
    }

    /// Forget marks whose cell is gone, so the list stays as long as the
    /// images on screen.
    fn prune_marks(&mut self) {
        self.marks.retain(|&(tag, r)| {
            let keep = Self::mark_alive(r, tag);
            if !keep {
                unsafe { ghostty_tracked_grid_ref_free(r) };
            }
            keep
        });
    }

    /// A screen row is the soft-wrapped continuation of the row above.
    fn wraps_from_above(&self, y: u32) -> bool {
        let Some(r) = self.grid_ref(GHOSTTY_POINT_TAG_SCREEN, 0, y) else { return false };
        unsafe {
            let mut row: GhosttyRow = 0;
            let mut cont = false;
            ghostty_grid_ref_row(&r, &mut row) == GHOSTTY_SUCCESS && ghostty_row_get(row, GHOSTTY_ROW_DATA_WRAP_CONTINUATION, &mut cont as *mut _ as *mut c_void) == GHOSTTY_SUCCESS && cont
        }
    }

    fn drop_anchor(&mut self) {
        if let Some(a) = self.anchor.take() {
            unsafe { ghostty_tracked_grid_ref_free(a.at) };
        }
    }

    /// Re-select from the anchor to `p` (screen coordinates).
    fn select_to(&mut self, p: (u16, u32), right: bool) {
        let Some(a) = self.anchor else { return };
        let Some(ap) = Self::tracked_screen_point(a.at) else {
            self.clear_selection();
            return;
        };
        let ord = |p: (u16, u32)| (p.1, p.0);
        match a.kind {
            SelectKind::Cell => {
                // alacritty's rule: the anchor's cell is in when the press
                // was in its left half, the free end's when the pointer is
                // in its right half.
                let ((s, s_right), (e, e_right)) = if ord(ap) <= ord(p) { ((ap, a.right), (p, right)) } else { ((p, right), (ap, a.right)) };
                let cols = self.shared.cols;
                let step = |q: (u16, u32), fwd: bool| -> Option<(u16, u32)> {
                    if fwd {
                        if q.0 + 1 < cols { Some((q.0 + 1, q.1)) } else { Some((0, q.1 + 1)) }
                    } else if q.0 > 0 {
                        Some((q.0 - 1, q.1))
                    } else if q.1 > 0 {
                        Some((cols - 1, q.1 - 1))
                    } else {
                        None
                    }
                };
                let start = if s_right { step(s, true) } else { Some(s) };
                let end = if e_right { Some(e) } else { step(e, false) };
                match (start, end) {
                    (Some(st), Some(en)) if ord(st) <= ord(en) => {
                        self.set_selection(st, en);
                    }
                    _ => self.clear_selection(),
                }
            }
            kind => {
                let (Some(u1), Some(u2)) = (self.unit_at(kind, ap), self.unit_at(kind, p)) else { return };
                let start = if ord(u1.0) <= ord(u2.0) { u1.0 } else { u2.0 };
                let end = if ord(u1.1) >= ord(u2.1) { u1.1 } else { u2.1 };
                self.set_selection(start, end);
            }
        }
        self.full_next = true;
    }

    /// Write a line of coloured text over the cursor's row (cursor moves).
    fn line_bytes(&self, (text, (r, g, b)): &(String, palette::Rgb), out: &mut Vec<u8>) {
        use unicode_width::UnicodeWidthChar;
        let cols = self.shared.cols as usize;
        out.extend_from_slice(b"\r\x1b[0m\x1b[2K");
        out.extend_from_slice(format!("\x1b[38;2;{r};{g};{b}m").as_bytes());
        let mut x = 0;
        for ch in text.chars() {
            let w = ch.width().unwrap_or(0);
            if w == 0 || ch.is_control() {
                continue;
            }
            // Stop short of the last column: a character there would arm the
            // wrap, and the line must not spill onto the next.
            if x + w > cols {
                break;
            }
            let mut buf = [0u8; 4];
            out.extend_from_slice(ch.encode_utf8(&mut buf).as_bytes());
            x += w;
        }
        out.extend_from_slice(b"\x1b[0m");
    }
}

impl Engine for Ghostty {
    fn feed(&mut self, bytes: &[u8], events: &mut Vec<EngineEvent>) {
        // A held frame (DEC 2026) is the render state's, which is the screen
        // at the last update. Update it just before a synchronized update
        // begins, so it holds the screen as it was there, as alacritty's
        // buffered bytes did.
        // Most updates end in the read they began in (Claude Code wraps every
        // frame in one), and then no frame can see the hold: only a BSU with
        // no ESU after it in this read needs the update.
        match (self.shared.held_since.is_none()).then(|| open_bsu(bytes)).flatten() {
            Some(i) => {
                self.feed_internal(&bytes[..i]);
                unsafe { ok!(ghostty_render_state_update(self.rs, self.t)) };
                self.feed_internal(&bytes[i..]);
            }
            None => self.feed_internal(bytes),
        }
        self.drain(events);
    }

    fn resize(&mut self, cols: u16, rows: u16) {
        let (cols, rows) = (cols.max(2), rows.max(1));
        if (cols, rows) == (self.shared.cols, self.shared.rows) {
            return;
        }
        self.shared.cols = cols;
        self.shared.rows = rows;
        // A resize ends a synchronized update: the program draws again for
        // the new size, and the held frame is the old size's.
        if self.shared.held_since.take().is_some() {
            let m = GhosttyTerminalModeConfig { mode: mode(2026, false), value: false };
            self.set(GHOSTTY_TERMINAL_OPT_MODE, &m as *const _ as *const c_void);
        }
        unsafe { ok!(ghostty_terminal_resize(self.t, cols, rows, 8, 16)) };
        self.full_next = true;
    }

    fn set_grow_at_bottom(&mut self, on: bool) {
        // Growing taller normally brings history lines back down; ConPTY
        // adds blank rows at the bottom instead (#296).
        let pull = !on;
        self.set(GHOSTTY_TERMINAL_OPT_RESIZE_PULL_SCROLLBACK, &pull as *const bool as *const c_void);
    }

    fn snapshot(&mut self, out: &mut Vec<u8>, seq: u32, force_full: bool) -> bool {
        out.clear();
        let pal = palette::active();
        if !std::ptr::eq(pal.theme, self.pal.theme) || pal.map != self.pal.map {
            self.full_next = true;
        }
        self.pal = pal;
        self.apply_palette();
        if !self.marks.is_empty() {
            self.prune_marks();
        }
        // While a synchronized update holds the screen, the render state
        // keeps the frame from before it (captured when the hold began).
        let held = self.shared.held_since.is_some();
        if !held {
            unsafe { ok!(ghostty_render_state_update(self.rs, self.t)) };
        }
        let has_sel = self.current_selection().is_some();
        let full = force_full || self.full_next || has_sel || self.sel_shown;
        self.sel_shown = has_sel;
        let mut dirty: GhosttyRenderStateDirty = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
        unsafe { ghostty_render_state_get(self.rs, GHOSTTY_RENDER_STATE_DATA_DIRTY, &mut dirty as *mut _ as *mut c_void) };
        if held && !full {
            return false;
        }
        let full = full || dirty == GHOSTTY_RENDER_STATE_DIRTY_FULL;
        self.full_next = false;

        let colors = self.colors();
        let bg_over = self.bg_override();
        // A held frame is the render state's: its size and cursor, and the
        // modes and history of the last frame before the hold.
        let (cols, rows) = if held { self.rs_size() } else { (self.shared.cols, self.shared.rows) };
        let (history, offset) = if held { self.last_hist } else { self.history_and_offset() };
        let mut cur = unsafe { sized::<GhosttyRenderStateCursor>() };
        unsafe { ghostty_render_state_get(self.rs, GHOSTTY_RENDER_STATE_DATA_CURSOR, &mut cur as *mut _ as *mut c_void) };
        let shape = if !cur.visible || offset != 0 {
            0
        } else {
            match cur.visual_style {
                GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BLOCK => 1,
                GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_UNDERLINE => 2,
                GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BAR => 3,
                GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BLOCK_HOLLOW => 4,
                _ => 1,
            }
        };
        let (cx, cy) = if cur.viewport_has_value && offset == 0 { (cur.viewport_x, cur.viewport_y) } else { self.cursor() };
        let modes = if held { self.last_modes } else { self.modes() };
        self.last_modes = modes;
        self.last_hist = (history, offset);

        // The rows to send: every row, or the dirty ones.
        let mut body = Vec::new();
        let mut nrows = 0u16;
        unsafe {
            ghostty_render_state_get(self.rs, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &mut self.rows_it as *mut _ as *mut c_void);
        }
        let mut y: u16 = 0;
        while unsafe { ghostty_render_state_row_iterator_next(self.rows_it) } {
            let mut row_dirty = false;
            unsafe { ghostty_render_state_row_get(self.rows_it, GHOSTTY_RENDER_STATE_ROW_DATA_DIRTY, &mut row_dirty as *mut _ as *mut c_void) };
            if full || (dirty != GHOSTTY_RENDER_STATE_DIRTY_FALSE && row_dirty) {
                self.encode_row(y, &mut body, &colors, bg_over);
                nrows += 1;
            }
            y += 1;
        }
        if !held {
            unsafe { ghostty_render_state_clean(self.rs) };
        }
        let full = full || nrows == rows;
        let header = FrameHeader {
            full,
            cols,
            rows,
            cursor_x: cx,
            cursor_y: cy,
            cursor_shape: shape,
            modes,
            display_offset: offset as u32,
            history: history as u32,
            seq,
            nrows,
        };
        frame::encode_header(out, &header);
        out.extend_from_slice(&body);
        true
    }

    fn scroll(&mut self, delta: i32) {
        let before = self.history_and_offset().1;
        let mut v: GhosttyTerminalScrollViewport = unsafe { std::mem::zeroed() };
        if delta == i32::MIN {
            v.tag = GHOSTTY_SCROLL_VIEWPORT_BOTTOM;
        } else {
            // Our delta is lines up into the history; ghostty's up is negative.
            v.tag = GHOSTTY_SCROLL_VIEWPORT_DELTA;
            v.value.delta = -(delta as isize);
        }
        unsafe { ghostty_terminal_scroll_viewport(self.t, v) };
        if self.history_and_offset().1 != before {
            self.full_next = true;
        }
    }

    fn sync_deadline(&self) -> Option<Instant> {
        self.shared.held_since.map(|t| t + SYNC_TIMEOUT)
    }

    fn stop_sync(&mut self, events: &mut Vec<EngineEvent>) {
        if self.shared.held_since.take().is_some() {
            let m = GhosttyTerminalModeConfig { mode: mode(2026, false), value: false };
            self.set(GHOSTTY_TERMINAL_OPT_MODE, &m as *const _ as *const c_void);
            self.full_next = true;
        }
        self.drain(events);
    }

    fn screen_text(&self) -> String {
        let (cols, rows) = (self.shared.cols, self.shared.rows);
        let mut s = String::new();
        for y in 0..rows {
            s.push_str(&self.row_text(GHOSTTY_POINT_TAG_ACTIVE, y as u32, cols));
            s.push('\n');
        }
        while s.ends_with("\n\n") {
            s.pop();
        }
        s
    }

    fn history_tail(&self, lines: usize) -> Vec<String> {
        let (history, _) = self.history_and_offset();
        let n = lines.min(history);
        (history - n..history).map(|y| self.row_text(GHOSTTY_POINT_TAG_HISTORY, y as u32, self.shared.cols)).collect()
    }

    fn size(&self) -> (u16, u16) {
        (self.shared.cols, self.shared.rows)
    }

    fn set_default_cursor(&mut self, shape: u8) {
        self.default_cursor = shape;
        let style: GhosttyTerminalCursorStyle = match shape {
            2 => GHOSTTY_TERMINAL_CURSOR_STYLE_UNDERLINE,
            3 => GHOSTTY_TERMINAL_CURSOR_STYLE_BAR,
            _ => GHOSTTY_TERMINAL_CURSOR_STYLE_BLOCK,
        };
        self.set(GHOSTTY_TERMINAL_OPT_DEFAULT_CURSOR_STYLE, &style as *const _ as *const c_void);
        let blink = false;
        self.set(GHOSTTY_TERMINAL_OPT_DEFAULT_CURSOR_BLINK, &blink as *const bool as *const c_void);
        self.full_next = true;
    }

    fn insert_block(&mut self, above: &[(String, palette::Rgb)], on_line: &(String, palette::Rgb), keep_cursor_line: bool) {
        self.scroll(i32::MIN);
        self.drop_anchor();
        self.clear_selection();
        // The alacritty engine scrolls the whole screen up by `shift` and
        // then writes the lines at the cursor's row and above it, some of
        // them in the history. A program can't write into the history, so
        // write the lines first, at the rows they will scroll up from, and
        // then scroll: line feeds on the bottom row, which push the top
        // rows into the history. The cursor comes back to its row number,
        // which is what the shell, and ConPTY, expect.
        let rows = self.shared.rows as usize;
        let (_, cy) = self.cursor();
        let cy = cy as usize;
        let keep = keep_cursor_line as usize;
        let k = above.len().min(rows.saturating_sub(1 + keep));
        let above = &above[above.len() - k..];
        let shift = k + keep;
        let mut b = Vec::new();
        let scroll = |b: &mut Vec<u8>, n: usize| {
            if n > 0 {
                b.extend_from_slice(format!("\x1b[{rows};1H").as_bytes());
                b.extend(std::iter::repeat_n(b'\n', n));
            }
        };
        // DECSC (DECRC brings back the pen, origin mode and charsets too);
        // absolute addressing; G0 is ASCII and shifted in, so the text is
        // written as it is. Insert mode is off while it is written.
        b.extend_from_slice(b"\x1b7\x1b[?6l\x1b(B\x0f");
        let insert = self.mode_ansi(4);
        if insert {
            self.set_mode(4, false);
        }
        // Room below for the lines: scroll part of the way first.
        let early = (cy + shift + 1).saturating_sub(rows);
        scroll(&mut b, early);
        let base = cy - early.min(cy);
        for (i, l) in above.iter().enumerate() {
            b.extend_from_slice(format!("\x1b[{};1H", base + keep + i + 1).as_bytes());
            self.line_bytes(l, &mut b);
        }
        b.extend_from_slice(format!("\x1b[{};1H", base + shift + 1).as_bytes());
        self.line_bytes(on_line, &mut b);
        scroll(&mut b, shift - early.min(shift));
        b.extend_from_slice(b"\x1b8");
        self.feed_internal(&b);
        if insert {
            self.set_mode(4, true);
        }
        self.full_next = true;
    }

    fn select_start(&mut self, col: u16, row: u16, right: bool, kind: SelectKind) {
        self.drop_anchor();
        let p = self.view_point(col, row);
        let mut at: GhosttyTrackedGridRef = ptr::null_mut();
        let pt = GhosttyPoint { tag: GHOSTTY_POINT_TAG_SCREEN, value: GhosttyPointValue { coordinate: GhosttyPointCoordinate { x: p.0, y: p.1 } } };
        if unsafe { ghostty_terminal_grid_ref_track(self.t, pt, &mut at) } != GHOSTTY_SUCCESS {
            return;
        }
        self.anchor = Some(Anchor { kind, at, right });
        // A word or line selection is the whole word or line straight away;
        // a cell selection starts empty and grows with the drag.
        if kind == SelectKind::Cell {
            self.clear_selection();
        } else {
            self.select_to(p, right);
        }
        self.full_next = true;
    }

    fn select_extend(&mut self, col: u16, row: u16, right: bool) {
        if self.anchor.is_some() {
            let p = self.view_point(col, row);
            self.select_to(p, right);
        }
    }

    fn select_clear(&mut self) {
        self.drop_anchor();
        if self.current_selection().is_some() {
            self.clear_selection();
            self.full_next = true;
        }
    }

    fn selection_text(&self) -> Option<String> {
        let sel = self.current_selection()?;
        let mut o: GhosttyTerminalSelectionFormatOptions = unsafe { sized() };
        o.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN;
        o.unwrap = true;
        o.trim = true;
        o.selection = &sel;
        let mut p: *mut u8 = ptr::null_mut();
        let mut n = 0usize;
        let mut s = unsafe {
            if ghostty_terminal_selection_format_alloc(self.t, ptr::null(), o, &mut p, &mut n) != GHOSTTY_SUCCESS || p.is_null() {
                return None;
            }
            let s = String::from_utf8_lossy(std::slice::from_raw_parts(p, n)).into_owned();
            ghostty_free(ptr::null(), p, n);
            s
        };
        while s.ends_with('\n') {
            s.pop();
        }
        // The trim drops trailing blanks, right for blanks past a line's
        // text, but a selection can also end on spaces with text after
        // them on its last row: those were selected, and alacritty copies
        // them. Put them back.
        if let Some((a, b)) = self.selection_points(&sel) {
            let end = if (a.1, a.0) <= (b.1, b.0) { b } else { a };
            let start_x = if (a.1, a.0) <= (b.1, b.0) { a } else { b };
            let x0 = if start_x.1 == end.1 { start_x.0 } else { 0 };
            let ch = |x: u16| self.grid_ref(GHOSTTY_POINT_TAG_SCREEN, x, end.1).and_then(|r| Self::cell_char(&r));
            let text_after = (end.0 + 1..self.shared.cols).any(|x| ch(x).is_some_and(|c| c != ' '));
            if text_after {
                let spaces = (x0..=end.0).rev().take_while(|&x| ch(x) == Some(' ')).count();
                // Only spaces taken on the last row: the trim emptied that
                // row and the pop above took its line break, unless the row
                // continues the one before it (unwrapped, no break).
                let all_blank = spaces == (end.0 - x0 + 1) as usize;
                if spaces > 0 && all_blank && start_x.1 != end.1 && !self.wraps_from_above(end.1) {
                    s.push('\n');
                }
                s.extend(std::iter::repeat_n(' ', spaces));
            }
        }
        (!s.is_empty()).then_some(s)
    }

    fn link_at(&self, col: u16, row: u16) -> Option<Link> {
        let (cols, rows) = (self.shared.cols, self.shared.rows);
        let (col, row) = (col.min(cols - 1), row.min(rows - 1));
        let at = |x: u16| self.grid_ref(GHOSTTY_POINT_TAG_VIEWPORT, x, row as u32);
        let r = at(col)?;
        // An OSC 8 hyperlink: the run of cells carrying the same link.
        // (An image mark, #288, is not a link.)
        if let Some(uri) = Self::hyperlink(&r).filter(|u| !u.starts_with(MARK_URI)) {
            let same = |x: u16| at(x).and_then(|r| Self::hyperlink(&r)).as_deref() == Some(uri.as_str());
            let mut x0 = col;
            while x0 > 0 && same(x0 - 1) {
                x0 -= 1;
            }
            let mut x1 = col + 1;
            while x1 < cols && same(x1) {
                x1 += 1;
            }
            return Some(Link { url: uri, row, x0, x1, osc8: true });
        }
        let line: Vec<char> = (0..cols)
            .map(|x| match at(x) {
                Some(r) => match Self::cell_wide(&r) {
                    GHOSTTY_CELL_WIDE_SPACER_TAIL => '\0',
                    GHOSTTY_CELL_WIDE_SPACER_HEAD => ' ',
                    _ => Self::cell_char(&r).unwrap_or(' '),
                },
                None => ' ',
            })
            .collect();
        input::find_urls(&line)
            .into_iter()
            .find(|u| (u.x0..u.x1).contains(&(col as usize)))
            .map(|u| Link { url: u.url, row, x0: u.x0 as u16, x1: u.x1 as u16, osc8: false })
    }

    fn mouse_report(&self, ev: MouseEvent) -> Option<Vec<u8>> {
        let (click, drag, motion) = self.mouse_tracking();
        let modes = MouseModes { click, drag, motion, sgr: self.mode(1006), utf8: self.mode(1005) };
        input::mouse_report(modes, ev)
    }

    fn paste_bytes(&self, text: &str) -> Vec<u8> {
        input::paste_bytes(text, self.mode(2004))
    }

    fn alt_screen(&self) -> bool {
        let mut s: GhosttyTerminalScreen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
        self.get(GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &mut s);
        s != GHOSTTY_TERMINAL_SCREEN_PRIMARY
    }

    fn cursor(&self) -> (u16, u16) {
        let (mut x, mut y) = (0u16, 0u16);
        self.get(GHOSTTY_TERMINAL_DATA_CURSOR_X, &mut x);
        self.get(GHOSTTY_TERMINAL_DATA_CURSOR_Y, &mut y);
        (x, y)
    }

    fn mark_cursor(&mut self, tag: u32) {
        // An OSC 8 link over the cell under the cursor, with the cell's own
        // text and style written again, so the screen doesn't change. DECSC
        // and DECRC put the cursor back with its pending wrap, pen, origin
        // mode and charsets. Insert mode is off meanwhile, so the write
        // replaces the cell instead of shifting the row.
        self.prune_marks();
        let (x, y) = self.cursor();
        let Some(r) = self.grid_ref(GHOSTTY_POINT_TAG_ACTIVE, x, y as u32) else { return };
        let wide = Self::cell_wide(&r);
        if wide == GHOSTTY_CELL_WIDE_SPACER_TAIL || wide == GHOSTTY_CELL_WIDE_SPACER_HEAD {
            // Half of a wide character, or the gap before one that wrapped:
            // writing there would split it.
            return;
        }
        let mut text = String::new();
        unsafe {
            let mut buf = [0u32; 16];
            let mut n = 0usize;
            if ghostty_grid_ref_graphemes(&r, buf.as_mut_ptr(), buf.len(), &mut n) == GHOSTTY_SUCCESS {
                text.extend(buf[..n.min(buf.len())].iter().filter_map(|&c| char::from_u32(c)));
            }
        }
        if text.is_empty() {
            text.push(' ');
        }
        let mut st: GhosttyStyle = unsafe { sized() };
        if unsafe { ghostty_grid_ref_style(&r, &mut st) } != GHOSTTY_SUCCESS {
            unsafe { ghostty_style_default(&mut st) };
        }
        let insert = self.mode_ansi(4);
        if insert {
            self.set_mode(4, false);
        }
        let mut b = Vec::with_capacity(96);
        // DECSC; absolute addressing; G0 is ASCII and shifted in.
        b.extend_from_slice(b"\x1b7\x1b[?6l\x1b(B\x0f");
        b.extend_from_slice(format!("\x1b[{};{}H", y + 1, x + 1).as_bytes());
        sgr_bytes(&st, &mut b);
        b.extend_from_slice(format!("\x1b]8;id=rsz-img-{tag};{MARK_URI}{tag}\x1b\\").as_bytes());
        b.extend_from_slice(text.as_bytes());
        b.extend_from_slice(b"\x1b]8;;\x1b\\\x1b8");
        self.feed_internal(&b);
        if insert {
            self.set_mode(4, true);
        }
        let pt = GhosttyPoint { tag: GHOSTTY_POINT_TAG_ACTIVE, value: GhosttyPointValue { coordinate: GhosttyPointCoordinate { x, y: y as u32 } } };
        let mut at: GhosttyTrackedGridRef = ptr::null_mut();
        if unsafe { ghostty_terminal_grid_ref_track(self.t, pt, &mut at) } == GHOSTTY_SUCCESS {
            self.marks.push((tag, at));
        }
    }

    fn marks(&self, above: usize) -> Vec<(u32, i32, u16)> {
        // Marks live on the main screen; the alternate screen shows none.
        if self.alt_screen() {
            return Vec::new();
        }
        let rows = self.shared.rows as i64;
        let offset = self.scrollbar().offset as i64;
        let mut out = Vec::new();
        for &(tag, r) in &self.marks {
            let Some((x, sy)) = Self::tracked_screen_point(r) else { continue };
            let row = sy as i64 - offset;
            if row < -(above as i64) || row >= rows || !Self::mark_alive(r, tag) {
                continue;
            }
            out.push((tag, row as i32, x));
        }
        // Screen order, as a scan of the grid finds them.
        out.sort_by_key(|m| (m.1, m.2));
        out
    }
}

/// SGR that sets a cell's style from scratch.
fn sgr_bytes(st: &GhosttyStyle, b: &mut Vec<u8>) {
    let mut p: Vec<String> = vec!["0".into()];
    for (on, code) in [(st.bold, "1"), (st.faint, "2"), (st.italic, "3"), (st.blink, "5"), (st.inverse, "7"), (st.invisible, "8"), (st.strikethrough, "9"), (st.overline, "53")] {
        if on {
            p.push(code.into());
        }
    }
    if st.underline != 0 {
        p.push(format!("4:{}", st.underline));
    }
    let color = |c: &GhosttyStyleColor, base: u8| -> Option<String> {
        match c.tag {
            GHOSTTY_STYLE_COLOR_PALETTE => Some(format!("{base}:5:{}", unsafe { c.value.palette })),
            GHOSTTY_STYLE_COLOR_RGB => {
                let v = unsafe { c.value.rgb };
                Some(format!("{base}:2::{}:{}:{}", v.r, v.g, v.b))
            }
            _ => None,
        }
    };
    p.extend(color(&st.fg_color, 38));
    p.extend(color(&st.bg_color, 48));
    p.extend(color(&st.underline_color, 58));
    b.extend_from_slice(format!("\x1b[{}m", p.join(";")).as_bytes());
}

/// Where the last DEC 2026 begin (CSI ? 2026 h) in `b` starts, when no end
/// (CSI ? 2026 l) follows it in `b`: the update is still open after `b`.
fn open_bsu(b: &[u8]) -> Option<usize> {
    const SEQ: &[u8] = b"\x1b[?2026";
    let mut open = None;
    let mut i = 0;
    while let Some(j) = b[i..].iter().position(|&c| c == 0x1b) {
        let at = i + j;
        let rest = &b[at..];
        if rest.len() > SEQ.len() && rest.starts_with(SEQ) {
            match rest[SEQ.len()] {
                b'h' => open = Some(at),
                b'l' => open = None,
                _ => {}
            }
        }
        i = at + 1;
    }
    open
}
