//! The VT engine seam (orca's §2.3 shape from #252): feed bytes on the reader
//! thread, take a dirty-rows snapshot on the frame thread, and get a small
//! slice of events per feed. alacritty_terminal is the engine today; another
//! (libghostty-vt) can sit behind the same trait for the bench.

use std::sync::Arc;
use std::time::Instant;

use alacritty_terminal::event::{Event, EventListener};
use alacritty_terminal::grid::{Dimensions, Scroll};
use alacritty_terminal::index::{Column, Line, Point, Side};
use alacritty_terminal::selection::{Selection, SelectionRange, SelectionType};
use alacritty_terminal::term::cell::{Flags, Hyperlink};
use alacritty_terminal::term::{Config, Term, TermDamage, TermMode};
use alacritty_terminal::vte::ansi::{Color, CursorShape, CursorStyle, NamedColor, Processor, Rgb as ARgb, StdSyncHandler};
use parking_lot::Mutex;

use crate::frame::{self, CellOut, FrameHeader};
use crate::input::{self, MouseEvent};
use crate::palette;

/// How a click selects: by cell (drag), by word (double click) or by line
/// (triple click).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SelectKind {
    Cell,
    Word,
    Line,
}

/// A link under the pointer: an OSC 8 hyperlink or a URL found in the text,
/// on one row of the visible screen, columns x0..x1 (end exclusive).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Link {
    pub url: String,
    pub row: u16,
    pub x0: u16,
    pub x1: u16,
    /// True for an OSC 8 hyperlink the app wrote, false for a found URL.
    pub osc8: bool,
}

/// What a feed asks of the pane besides new cells.
#[derive(Debug, Clone, PartialEq)]
pub enum EngineEvent {
    /// Bytes the terminal must answer with (DA, DSR, cursor position for
    /// ConPTY's INHERIT_CURSOR handshake, colour queries).
    PtyWrite(Vec<u8>),
    Title(String),
    Bell,
    Clipboard(String),
}

pub trait Engine: Send {
    fn feed(&mut self, bytes: &[u8], events: &mut Vec<EngineEvent>);
    fn resize(&mut self, cols: u16, rows: u16);
    /// Encode the rows changed since the last snapshot into `out` (cleared
    /// first). Returns false when there is nothing to send.
    fn snapshot(&mut self, out: &mut Vec<u8>, seq: u32, force_full: bool) -> bool;
    fn scroll(&mut self, delta: i32);
    /// While a DEC 2026 synchronized update is open: when it times out.
    fn sync_deadline(&self) -> Option<Instant>;
    fn stop_sync(&mut self, events: &mut Vec<EngineEvent>);
    /// The visible screen as text, trailing blanks trimmed (goldens).
    fn screen_text(&self) -> String;
    /// The last `lines` scrollback lines above the screen, oldest first,
    /// trailing blanks trimmed (the AI control plane's `tail`).
    fn history_tail(&self, lines: usize) -> Vec<String>;
    fn size(&self) -> (u16, u16);

    /// Start a selection at a cell of the visible screen; `right` is true
    /// when the pointer is in the cell's right half. The anchor stays on its
    /// text when the view or the output scrolls.
    fn select_start(&mut self, col: u16, row: u16, right: bool, kind: SelectKind);
    /// Move the selection's free end (a drag).
    fn select_extend(&mut self, col: u16, row: u16, right: bool);
    fn select_clear(&mut self);
    /// The selected text, or None when nothing is selected.
    fn selection_text(&self) -> Option<String>;
    /// The link at a cell of the visible screen.
    fn link_at(&self, col: u16, row: u16) -> Option<Link>;
    /// The bytes reporting a mouse event to the app, when its modes ask.
    fn mouse_report(&self, ev: MouseEvent) -> Option<Vec<u8>>;
    /// Pasted text as bytes for the PTY (bracketed if the app enabled it).
    fn paste_bytes(&self, text: &str) -> Vec<u8>;
    /// A full-screen app is up (DECSET 1049).
    fn alt_screen(&self) -> bool;
    /// The cursor shape used until a program sets one (DECSCUSR): 1 block,
    /// 2 underline, 3 beam (the Settings cursor).
    fn set_default_cursor(&mut self, _shape: u8) {}

    // Image anchors (card #288). An engine without them shows no images.
    /// The cursor: (column, screen row).
    fn cursor(&self) -> (u16, u16) {
        (0, 0)
    }
    /// Tag the cell under the cursor with an image mark that moves with the
    /// text (scrolling, history, clears).
    fn mark_cursor(&mut self, _tag: u32) {}
    /// The marks on screen rows, and up to `above` rows above the view:
    /// (tag, row relative to the view's top, may be negative; column).
    fn marks(&self, _above: usize) -> Vec<(u32, i32, u16)> {
        Vec::new()
    }
}

/// The hyperlink URI an image mark uses ([`Engine::mark_cursor`]). Link
/// handling must skip it.
pub const MARK_URI: &str = "rsz-img:";

fn cursor_shape(shape: u8) -> CursorShape {
    match shape {
        2 => CursorShape::Underline,
        3 => CursorShape::Beam,
        _ => CursorShape::Block,
    }
}

/// What ends a double-click word: everything but letters, digits and "_",
/// as in Go suzuri's selection.go (isWordRune). ASCII punctuation, blanks
/// and the box-drawing bar alacritty adds.
const WORD_BREAKS: &str = " \t!\"#$%&'()*+,-./:;<=>?@[\\]^`{|}~│";

#[derive(Clone, Copy)]
struct Size {
    cols: usize,
    rows: usize,
}

impl Dimensions for Size {
    fn total_lines(&self) -> usize {
        self.rows
    }
    fn screen_lines(&self) -> usize {
        self.rows
    }
    fn columns(&self) -> usize {
        self.cols
    }
}

#[derive(Clone, Default)]
struct Listener(Arc<Mutex<Vec<Event>>>);

impl EventListener for Listener {
    fn send_event(&self, event: Event) {
        self.0.lock().push(event);
    }
}

pub struct Alacritty {
    term: Term<Listener>,
    parser: Processor<StdSyncHandler>,
    pending: Listener,
    size: Size,
    full_next: bool,
    row_scratch: Vec<CellOut>,
    config: Config,
    /// A selection was painted in the last frame (its rows need repainting
    /// once it goes, even if the engine dropped it on its own).
    sel_shown: bool,
    /// The theme colours resolve against, refreshed per feed and snapshot.
    pal: palette::Active,
}

impl Alacritty {
    pub fn new(cols: u16, rows: u16, scrollback: usize) -> Self {
        let size = Size { cols: cols.max(2) as usize, rows: rows.max(1) as usize };
        let pending = Listener::default();
        // Kitty keyboard on: the engine keeps the flag stacks (per screen) and
        // answers CSI ? u; the UI's encoder (ui/kitty.js) reads the flags
        // from the frame's mode word.
        let config = Config {
            scrolling_history: scrollback,
            kitty_keyboard: true,
            semantic_escape_chars: WORD_BREAKS.to_owned(),
            ..Config::default()
        };
        let term = Term::new(config.clone(), &size, pending.clone());
        Self { term, parser: Processor::new(), pending, size, full_next: true, row_scratch: Vec::new(), config, sel_shown: false, pal: palette::active() }
    }

    fn drain(&mut self, events: &mut Vec<EngineEvent>) {
        self.pal = palette::active();
        let mut q = self.pending.0.lock();
        for ev in q.drain(..) {
            match ev {
                Event::PtyWrite(s) => events.push(EngineEvent::PtyWrite(s.into_bytes())),
                Event::Title(t) => events.push(EngineEvent::Title(t)),
                Event::ResetTitle => events.push(EngineEvent::Title(String::new())),
                Event::Bell => events.push(EngineEvent::Bell),
                Event::ClipboardStore(_, s) => events.push(EngineEvent::Clipboard(s)),
                Event::ColorRequest(i, f) => {
                    let rgb = self.resolve_index(i);
                    events.push(EngineEvent::PtyWrite(f(ARgb { r: rgb.0, g: rgb.1, b: rgb.2 }).into_bytes()));
                }
                Event::TextAreaSizeRequest(f) => {
                    let ws = alacritty_terminal::event::WindowSize {
                        num_lines: self.size.rows as u16,
                        num_cols: self.size.cols as u16,
                        cell_width: 8,
                        cell_height: 16,
                    };
                    events.push(EngineEvent::PtyWrite(f(ws).into_bytes()));
                }
                _ => {}
            }
        }
    }

    fn resolve_index(&self, i: usize) -> palette::Rgb {
        if let Some(c) = self.term.colors()[i] {
            return (c.r, c.g, c.b);
        }
        match i {
            0..=255 => palette::xterm256(&self.pal.ansi16, i as u8),
            _ if i == NamedColor::Background as usize => self.pal.theme.chrome.void,
            _ => self.pal.theme.chrome.text,
        }
    }

    /// Resolve a cell colour to 0x00RRGGBB, or frame::DEFAULT_BG for the
    /// theme background (so the UI can leave its glass showing).
    fn rgb(&self, c: Color, bold: bool, is_bg: bool) -> u32 {
        let pack = |(r, g, b): palette::Rgb| (r as u32) << 16 | (g as u32) << 8 | b as u32;
        match c {
            Color::Spec(rgb) => pack((rgb.r, rgb.g, rgb.b)),
            Color::Indexed(i) => {
                let i = if bold && i < 8 { i + 8 } else { i };
                pack(self.resolve_index(i as usize))
            }
            Color::Named(n) => {
                let idx = n as usize;
                if idx < 16 {
                    let idx = if bold && idx < 8 { idx + 8 } else { idx };
                    return pack(self.resolve_index(idx));
                }
                match n {
                    NamedColor::Background => {
                        if is_bg && self.term.colors()[idx].is_none() {
                            frame::DEFAULT_BG
                        } else {
                            pack(self.resolve_index(idx))
                        }
                    }
                    NamedColor::Foreground | NamedColor::BrightForeground => pack(self.resolve_index(NamedColor::Foreground as usize)),
                    NamedColor::DimForeground => {
                        let (r, g, b) = self.resolve_index(NamedColor::Foreground as usize);
                        pack(((r as u32 * 2 / 3) as u8, (g as u32 * 2 / 3) as u8, (b as u32 * 2 / 3) as u8))
                    }
                    _ => {
                        // Dim ANSI colours sit right after DimForeground... map
                        // them to their normal slot; DIM is applied as an attr.
                        let dim_base = NamedColor::DimBlack as usize;
                        if idx >= dim_base && idx < dim_base + 8 {
                            pack(self.resolve_index(idx - dim_base))
                        } else {
                            pack(self.pal.theme.chrome.text)
                        }
                    }
                }
            }
        }
    }

    /// A cell of the visible screen as a grid point (history lines are
    /// negative), clamped to the screen.
    fn point(&self, col: u16, row: u16) -> Point {
        let offset = self.term.grid().display_offset() as i32;
        let col = (col as usize).min(self.size.cols - 1);
        let row = (row as usize).min(self.size.rows - 1) as i32;
        Point::new(Line(row - offset), Column(col))
    }

    fn encode_row(&mut self, y: usize, out: &mut Vec<u8>, sel: Option<&SelectionRange>) {
        let offset = self.term.grid().display_offset() as i32;
        let line = Line(y as i32 - offset);
        self.row_scratch.clear();
        let cols = self.size.cols;
        for x in 0..cols {
            let cell = &self.term.grid()[line][Column(x)];
            let flags = cell.flags;
            let bold = flags.contains(Flags::BOLD);
            // Go suzuri's paint rules (internal/ui/color.go glyphToCell).
            let pack = |(r, g, b): palette::Rgb| (r as u32) << 16 | (g as u32) << 8 | b as u32;
            let unpack = |v: u32| ((v >> 16) as u8, (v >> 8) as u8, v as u8);
            let c = &self.pal.theme.chrome;
            let term_bg = pack(palette::term_bg(c));
            let mut fg = self.rgb(cell.fg, bold, false);
            let mut bg = self.rgb(cell.bg, false, true);
            if bold {
                fg = pack(palette::brighten_bold(unpack(fg)));
            }
            if flags.contains(Flags::INVERSE) {
                let default_rev = matches!(cell.fg, Color::Named(NamedColor::Foreground)) && bg == frame::DEFAULT_BG;
                // Swap, then keep the field visible on a dark host: the
                // default pair becomes panel + text, a near-black or
                // low-contrast field becomes the panel.
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
            if flags.contains(Flags::ITALIC) {
                attrs |= frame::A_ITALIC;
            }
            if flags.intersects(Flags::ALL_UNDERLINES) {
                attrs |= frame::A_UNDERLINE;
            }
            if flags.contains(Flags::STRIKEOUT) {
                attrs |= frame::A_STRIKE;
            }
            if flags.contains(Flags::DIM) {
                attrs |= frame::A_DIM;
            }
            if flags.contains(Flags::HIDDEN) {
                attrs |= frame::A_HIDDEN;
            }
            if sel.is_some_and(|r| r.contains(Point::new(line, Column(x)))) {
                attrs |= frame::A_SELECTED;
            }
            let cp = if flags.intersects(Flags::WIDE_CHAR_SPACER | Flags::LEADING_WIDE_CHAR_SPACER) {
                0
            } else {
                cell.c as u32
            };
            self.row_scratch.push(CellOut { cp, fg, bg, attrs });
        }
        frame::encode_row(out, y as u16, &self.row_scratch);
    }
}

impl Engine for Alacritty {
    fn feed(&mut self, bytes: &[u8], events: &mut Vec<EngineEvent>) {
        self.parser.advance(&mut self.term, bytes);
        self.drain(events);
    }

    fn resize(&mut self, cols: u16, rows: u16) {
        let size = Size { cols: cols.max(2) as usize, rows: rows.max(1) as usize };
        if size.cols == self.size.cols && size.rows == self.size.rows {
            return;
        }
        self.size = size;
        self.term.resize(size);
        self.full_next = true;
    }

    fn snapshot(&mut self, out: &mut Vec<u8>, seq: u32, force_full: bool) -> bool {
        out.clear();
        let pal = palette::active();
        // A theme switch repaints every row in the new colours.
        if !std::ptr::eq(pal.theme, self.pal.theme) || pal.map != self.pal.map {
            self.full_next = true;
        }
        self.pal = pal;
        // While a selection shows, every frame is full: the engine moves
        // the selection with scrolling output without marking damage.
        let sel = self.term.selection.as_ref().and_then(|s| s.to_range(&self.term));
        let full = force_full || self.full_next || sel.is_some() || self.sel_shown;
        self.sel_shown = sel.is_some();
        let rows: Vec<usize> = if full {
            (0..self.size.rows).collect()
        } else {
            match self.term.damage() {
                TermDamage::Full => (0..self.size.rows).collect(),
                TermDamage::Partial(it) => it.map(|d| d.line).collect(),
            }
        };
        let full = full || rows.len() == self.size.rows;
        self.term.reset_damage();
        self.full_next = false;

        let mode = *self.term.mode();
        let grid = self.term.grid();
        let offset = grid.display_offset();
        let cur = grid.cursor.point;
        let style = self.term.cursor_style();
        let visible = mode.contains(TermMode::SHOW_CURSOR) && offset == 0;
        let shape = if !visible {
            0
        } else {
            match style.shape {
                CursorShape::Block => 1,
                CursorShape::Underline => 2,
                CursorShape::Beam => 3,
                CursorShape::HollowBlock => 4,
                CursorShape::Hidden => 0,
            }
        };
        let mut modes = 0u32;
        let map = [
            (TermMode::APP_CURSOR, frame::M_APP_CURSOR),
            (TermMode::BRACKETED_PASTE, frame::M_BRACKETED_PASTE),
            (TermMode::ALT_SCREEN, frame::M_ALT_SCREEN),
            (TermMode::FOCUS_IN_OUT, frame::M_FOCUS),
            (TermMode::MOUSE_REPORT_CLICK, frame::M_MOUSE_CLICK),
            (TermMode::MOUSE_DRAG, frame::M_MOUSE_DRAG),
            (TermMode::MOUSE_MOTION, frame::M_MOUSE_MOTION),
            (TermMode::SGR_MOUSE, frame::M_SGR_MOUSE),
            (TermMode::ALTERNATE_SCROLL, frame::M_ALT_SCROLL),
            (TermMode::APP_KEYPAD, frame::M_APP_KEYPAD),
            (TermMode::DISAMBIGUATE_ESC_CODES, frame::M_KITTY_DISAMBIGUATE),
            (TermMode::REPORT_EVENT_TYPES, frame::M_KITTY_EVENT_TYPES),
            (TermMode::REPORT_ALTERNATE_KEYS, frame::M_KITTY_ALTERNATE_KEYS),
            (TermMode::REPORT_ALL_KEYS_AS_ESC, frame::M_KITTY_ALL_KEYS),
            (TermMode::REPORT_ASSOCIATED_TEXT, frame::M_KITTY_ASSOCIATED_TEXT),
        ];
        for (m, bit) in map {
            if mode.contains(m) {
                modes |= bit;
            }
        }
        let header = FrameHeader {
            full,
            cols: self.size.cols as u16,
            rows: self.size.rows as u16,
            cursor_x: cur.column.0 as u16,
            cursor_y: cur.line.0.max(0) as u16,
            cursor_shape: shape,
            modes,
            display_offset: offset as u32,
            history: grid.history_size() as u32,
            seq,
            nrows: rows.len() as u16,
        };
        frame::encode_header(out, &header);
        for y in rows {
            self.encode_row(y, out, sel.as_ref());
        }
        true
    }

    fn scroll(&mut self, delta: i32) {
        let before = self.term.grid().display_offset();
        if delta == i32::MIN {
            self.term.scroll_display(Scroll::Bottom);
        } else {
            self.term.scroll_display(Scroll::Delta(delta));
        }
        if self.term.grid().display_offset() != before {
            self.full_next = true;
        }
    }

    fn sync_deadline(&self) -> Option<Instant> {
        self.parser.sync_timeout().sync_timeout()
    }

    fn stop_sync(&mut self, events: &mut Vec<EngineEvent>) {
        self.parser.stop_sync(&mut self.term);
        self.drain(events);
    }

    fn screen_text(&self) -> String {
        let grid = self.term.grid();
        let mut s = String::new();
        for y in 0..self.size.rows {
            let mut line = String::new();
            for x in 0..self.size.cols {
                let cell = &grid[Line(y as i32)][Column(x)];
                if cell.flags.intersects(Flags::WIDE_CHAR_SPACER | Flags::LEADING_WIDE_CHAR_SPACER) {
                    continue;
                }
                line.push(cell.c);
            }
            s.push_str(line.trim_end());
            s.push('\n');
        }
        while s.ends_with("\n\n") {
            s.pop();
        }
        s
    }

    fn history_tail(&self, lines: usize) -> Vec<String> {
        let grid = self.term.grid();
        let n = lines.min(grid.history_size());
        let mut out = Vec::with_capacity(n);
        for i in (1..=n).rev() {
            let row = &grid[Line(-(i as i32))];
            let mut line = String::new();
            for x in 0..self.size.cols {
                let cell = &row[Column(x)];
                if cell.flags.intersects(Flags::WIDE_CHAR_SPACER | Flags::LEADING_WIDE_CHAR_SPACER) {
                    continue;
                }
                line.push(cell.c);
            }
            out.push(line.trim_end().to_string());
        }
        out
    }

    fn size(&self) -> (u16, u16) {
        (self.size.cols as u16, self.size.rows as u16)
    }

    fn set_default_cursor(&mut self, shape: u8) {
        self.config.default_cursor_style = CursorStyle { shape: cursor_shape(shape), blinking: false };
        self.term.set_options(self.config.clone());
        self.full_next = true;
    }

    fn select_start(&mut self, col: u16, row: u16, right: bool, kind: SelectKind) {
        let ty = match kind {
            SelectKind::Cell => SelectionType::Simple,
            SelectKind::Word => SelectionType::Semantic,
            SelectKind::Line => SelectionType::Lines,
        };
        let side = if right { Side::Right } else { Side::Left };
        let mut sel = Selection::new(ty, self.point(col, row), side);
        // A word or line selection is the whole word or line straight away;
        // a cell selection starts empty and grows with the drag.
        if kind != SelectKind::Cell {
            sel.update(self.point(col, row), side);
        }
        self.term.selection = Some(sel);
        self.full_next = true;
    }

    fn select_extend(&mut self, col: u16, row: u16, right: bool) {
        let p = self.point(col, row);
        if let Some(sel) = self.term.selection.as_mut() {
            sel.update(p, if right { Side::Right } else { Side::Left });
            self.full_next = true;
        }
    }

    fn select_clear(&mut self) {
        if self.term.selection.take().is_some() {
            self.full_next = true;
        }
    }

    fn selection_text(&self) -> Option<String> {
        let mut s = self.term.selection_to_string()?;
        // A line selection ends in a newline; copy it like Go suzuri, without.
        while s.ends_with('\n') {
            s.pop();
        }
        (!s.is_empty()).then_some(s)
    }

    fn link_at(&self, col: u16, row: u16) -> Option<Link> {
        let p = self.point(col, row);
        let grid = self.term.grid();
        let cols = self.size.cols;
        let row = p.line.0 + grid.display_offset() as i32;
        let row = row as u16;
        // An OSC 8 hyperlink: the run of cells carrying the same link.
        // (An image mark, #288, is not a link.)
        if let Some(h) = grid[p].hyperlink().filter(|h| !h.uri().starts_with(MARK_URI)) {
            let same = |x: usize| grid[p.line][Column(x)].hyperlink().as_ref() == Some(&h);
            let mut x0 = p.column.0;
            while x0 > 0 && same(x0 - 1) {
                x0 -= 1;
            }
            let mut x1 = p.column.0 + 1;
            while x1 < cols && same(x1) {
                x1 += 1;
            }
            return Some(Link { url: h.uri().to_string(), row, x0: x0 as u16, x1: x1 as u16, osc8: true });
        }
        let line: Vec<char> = (0..cols)
            .map(|x| {
                let c = &grid[p.line][Column(x)];
                if c.flags.contains(Flags::WIDE_CHAR_SPACER) {
                    '\0'
                } else if c.flags.contains(Flags::LEADING_WIDE_CHAR_SPACER) {
                    ' '
                } else {
                    c.c
                }
            })
            .collect();
        input::find_urls(&line)
            .into_iter()
            .find(|u| (u.x0..u.x1).contains(&p.column.0))
            .map(|u| Link { url: u.url, row, x0: u.x0 as u16, x1: u.x1 as u16, osc8: false })
    }

    fn mouse_report(&self, ev: MouseEvent) -> Option<Vec<u8>> {
        input::mouse_report(*self.term.mode(), ev)
    }

    fn paste_bytes(&self, text: &str) -> Vec<u8> {
        input::paste_bytes(text, self.term.mode().contains(TermMode::BRACKETED_PASTE))
    }

    fn cursor(&self) -> (u16, u16) {
        let p = self.term.grid().cursor.point;
        (p.column.0 as u16, p.line.0.max(0) as u16)
    }

    fn mark_cursor(&mut self, tag: u32) {
        let p = self.term.grid().cursor.point;
        let link = Hyperlink::new(Some(format!("rsz-img-{tag}")), format!("{MARK_URI}{tag}"));
        self.term.grid_mut()[p.line][p.column].set_hyperlink(Some(link));
    }

    fn marks(&self, above: usize) -> Vec<(u32, i32, u16)> {
        let grid = self.term.grid();
        let offset = grid.display_offset() as i32;
        let top = -(above.min(grid.history_size().saturating_sub(offset as usize)) as i32);
        let mut out = Vec::new();
        for y in top..self.size.rows as i32 {
            let row = &grid[Line(y - offset)];
            for x in 0..self.size.cols {
                if let Some(tag) = row[Column(x)].hyperlink().and_then(|h| h.uri().strip_prefix(MARK_URI).and_then(|t| t.parse().ok())) {
                    out.push((tag, y, x as u16));
                }
            }
        }
        out
    }

    fn alt_screen(&self) -> bool {
        self.term.mode().contains(TermMode::ALT_SCREEN)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn feeds_text_and_answers_dsr() {
        let mut e = Alacritty::new(20, 4, 100);
        let mut ev = Vec::new();
        e.feed(b"hello\r\nw\xc3\xb6rld \xe7\xa1\xaf", &mut ev);
        assert_eq!(e.screen_text(), "hello\nwörld 硯\n");
        e.feed(b"\x1b[6n", &mut ev);
        assert_eq!(ev, vec![EngineEvent::PtyWrite(b"\x1b[2;9R".to_vec())]);
    }

    #[test]
    fn history_tail_is_oldest_first_and_capped() {
        let mut e = Alacritty::new(10, 2, 100);
        let mut ev = Vec::new();
        e.feed(b"one\r\ntwo\r\nthree\r\nfour  \r\nfive", &mut ev);
        assert_eq!(e.screen_text(), "four\nfive\n");
        assert_eq!(e.history_tail(2), vec!["two".to_string(), "three".to_string()]);
        assert_eq!(e.history_tail(50), vec!["one".to_string(), "two".to_string(), "three".to_string()]);
        assert!(Alacritty::new(10, 2, 100).history_tail(5).is_empty());
    }

    fn kitty_flags(e: &mut Alacritty) -> u32 {
        let mut out = Vec::new();
        e.snapshot(&mut out, 1, true);
        (frame::decode_header(&out).unwrap().modes >> frame::M_KITTY_SHIFT) & 31
    }

    #[test]
    fn kitty_keyboard_push_query_pop_per_screen() {
        let mut e = Alacritty::new(10, 2, 0);
        let mut ev = Vec::new();
        assert_eq!(kitty_flags(&mut e), 0);
        e.feed(b"\x1b[>1u\x1b[?u", &mut ev);
        assert_eq!(kitty_flags(&mut e), 1);
        assert_eq!(ev, vec![EngineEvent::PtyWrite(b"\x1b[?1u".to_vec())]);
        // The alt screen keeps its own stack.
        e.feed(b"\x1b[?1049h", &mut ev);
        assert_eq!(kitty_flags(&mut e), 0);
        e.feed(b"\x1b[>31u", &mut ev);
        assert_eq!(kitty_flags(&mut e), 31);
        e.feed(b"\x1b[?1049l", &mut ev);
        assert_eq!(kitty_flags(&mut e), 1);
        e.feed(b"\x1b[<u", &mut ev);
        assert_eq!(kitty_flags(&mut e), 0);
    }

    #[test]
    fn utf8_split_across_feeds() {
        let mut e = Alacritty::new(10, 2, 0);
        let mut ev = Vec::new();
        e.feed(b"\xe7\xa1", &mut ev);
        e.feed(b"\xaf!", &mut ev);
        assert_eq!(e.screen_text(), "硯!\n");
    }

    #[test]
    fn partial_damage_after_full() {
        let mut e = Alacritty::new(10, 5, 0);
        let mut out = Vec::new();
        let mut ev = Vec::new();
        assert!(e.snapshot(&mut out, 1, false));
        assert_eq!(frame::decode_header(&out).unwrap().nrows, 5);
        e.feed(b"\x1b[3;1Hx", &mut ev);
        e.snapshot(&mut out, 2, false);
        let h = frame::decode_header(&out).unwrap();
        assert!(!h.full);
        // Row 2 (the write) plus row 0 (where the cursor left from).
        assert!(h.nrows <= 3, "nrows {}", h.nrows);
    }

    #[test]
    fn sync_update_holds_until_end() {
        let mut e = Alacritty::new(10, 2, 0);
        let mut ev = Vec::new();
        e.feed(b"\x1b[?2026h", &mut ev);
        assert!(e.sync_deadline().is_some());
        e.feed(b"abc", &mut ev);
        // Bytes inside a sync block are buffered, not applied yet.
        assert_eq!(e.screen_text(), "\n");
        e.feed(b"\x1b[?2026l", &mut ev);
        assert!(e.sync_deadline().is_none());
        assert_eq!(e.screen_text(), "abc\n");
    }

    fn fed(cols: u16, rows: u16, sb: usize, bytes: &[u8]) -> Alacritty {
        let mut e = Alacritty::new(cols, rows, sb);
        e.feed(bytes, &mut Vec::new());
        e
    }

    #[test]
    fn word_and_line_selection() {
        let mut e = fed(30, 3, 0, b"ls /usr/local/bin --all\r\nnext");
        // Words are letters, digits and "_" (Go suzuri's isWordRune).
        e.select_start(6, 0, false, SelectKind::Word);
        assert_eq!(e.selection_text().as_deref(), Some("usr"));
        e.select_start(0, 0, false, SelectKind::Word);
        assert_eq!(e.selection_text().as_deref(), Some("ls"));
        e.select_start(1, 0, false, SelectKind::Line);
        assert_eq!(e.selection_text().as_deref(), Some("ls /usr/local/bin --all"));
        // A triple click then dragged down takes whole lines.
        e.select_extend(0, 1, false);
        assert_eq!(e.selection_text().as_deref(), Some("ls /usr/local/bin --all\nnext"));
        e.select_clear();
        assert_eq!(e.selection_text(), None);
    }

    #[test]
    fn drag_selection_and_its_cells_in_frames() {
        let mut e = fed(20, 2, 0, b"hello world");
        e.select_start(0, 0, false, SelectKind::Cell);
        // A press without a drag selects nothing.
        assert_eq!(e.selection_text(), None);
        e.select_extend(4, 0, true);
        assert_eq!(e.selection_text().as_deref(), Some("hello"));
        let mut out = Vec::new();
        e.snapshot(&mut out, 1, false);
        // Row 0's first run is "hello", marked selected.
        let p = HEADER_LEN_FOR_TEST;
        let attrs = u16::from_le_bytes([out[p + 4 + 12], out[p + 4 + 13]]);
        let n = u16::from_le_bytes([out[p + 4 + 2], out[p + 4 + 3]]);
        assert_eq!((n, attrs & frame::A_SELECTED), (5, frame::A_SELECTED));
        // Clearing it repaints the rows without the mark.
        e.select_clear();
        e.snapshot(&mut out, 2, false);
        assert!(frame::decode_header(&out).unwrap().full);
        let attrs = u16::from_le_bytes([out[p + 4 + 12], out[p + 4 + 13]]);
        assert_eq!(attrs & frame::A_SELECTED, 0);
    }
    const HEADER_LEN_FOR_TEST: usize = frame::HEADER_LEN;

    #[test]
    fn selection_reaches_into_scrollback() {
        let mut text = Vec::new();
        for i in 0..10 {
            text.extend_from_slice(format!("line {i}\r\n").as_bytes());
        }
        text.extend_from_slice(b"last");
        let mut e = fed(10, 3, 100, &text);
        // Screen: line 8, line 9, last. Anchor on "last", scroll up, drag.
        e.select_start(4, 2, false, SelectKind::Cell);
        e.scroll(5);
        e.select_extend(0, 0, false);
        assert_eq!(e.selection_text().as_deref(), Some("line 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nlast"));
        // The anchor stays on its text when output scrolls the screen.
        e.scroll(i32::MIN);
        e.select_start(0, 2, false, SelectKind::Word);
        e.feed(b"\r\nmore\r\n", &mut Vec::new());
        assert_eq!(e.selection_text().as_deref(), Some("last"));
    }

    #[test]
    fn links_osc8_and_found_urls() {
        let mut e = fed(40, 2, 0, b"see \x1b]8;;https://a.io/x\x1b\\docs\x1b]8;;\x1b\\ or https://b.io.");
        let l = e.link_at(5, 0).unwrap();
        assert_eq!((l.url.as_str(), l.x0, l.x1, l.osc8), ("https://a.io/x", 4, 8, true));
        let l = e.link_at(15, 0).unwrap();
        assert_eq!((l.url.as_str(), l.x0, l.x1, l.osc8), ("https://b.io", 12, 24, false));
        assert_eq!(e.link_at(2, 0), None);
        assert_eq!(e.link_at(24, 0), None);
        // A link in scrollback is found at its on-screen row.
        e.feed(b"\r\n\r\n", &mut Vec::new());
        assert_eq!(e.link_at(15, 0), None);
    }

    #[test]
    fn mouse_and_paste_follow_app_modes() {
        let mut e = fed(20, 5, 0, b"");
        let ev = MouseEvent { kind: crate::input::MouseKind::Press, button: 0, col: 2, row: 3, shift: false, alt: false, ctrl: false };
        assert_eq!(e.mouse_report(ev), None);
        assert_eq!(e.paste_bytes("a\nb"), b"a\rb");
        e.feed(b"\x1b[?1000h\x1b[?1006h\x1b[?2004h", &mut Vec::new());
        assert_eq!(e.mouse_report(ev).unwrap(), b"\x1b[<0;3;4M");
        assert_eq!(e.paste_bytes("a\nb"), b"\x1b[200~a\rb\x1b[201~");
        e.feed(b"\x1b[?1000l", &mut Vec::new());
        assert_eq!(e.mouse_report(ev), None);
    }
}
