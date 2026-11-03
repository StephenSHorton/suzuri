//! The VT engine seam (orca's §2.3 shape from #252): feed bytes on the reader
//! thread, take a dirty-rows snapshot on the frame thread, and get a small
//! slice of events per feed. alacritty_terminal is the engine today; another
//! (libghostty-vt) can sit behind the same trait for the bench.

use std::sync::Arc;
use std::time::Instant;

use alacritty_terminal::event::{Event, EventListener};
use alacritty_terminal::grid::{Dimensions, Scroll};
use alacritty_terminal::index::{Column, Line};
use alacritty_terminal::term::cell::Flags;
use alacritty_terminal::term::{Config, Term, TermDamage, TermMode};
use alacritty_terminal::vte::ansi::{Color, CursorShape, NamedColor, Processor, Rgb as ARgb, StdSyncHandler};
use parking_lot::Mutex;

use crate::frame::{self, CellOut, FrameHeader};
use crate::palette;

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
    fn size(&self) -> (u16, u16);
}

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
}

impl Alacritty {
    pub fn new(cols: u16, rows: u16, scrollback: usize) -> Self {
        let size = Size { cols: cols.max(2) as usize, rows: rows.max(1) as usize };
        let pending = Listener::default();
        // Kitty keyboard on: the engine keeps the flag stacks (per screen) and
        // answers CSI ? u; the UI's encoder (ui/kitty.js) reads the flags
        // from the frame's mode word.
        let config = Config { scrolling_history: scrollback, kitty_keyboard: true, ..Config::default() };
        let term = Term::new(config, &size, pending.clone());
        Self { term, parser: Processor::new(), pending, size, full_next: true, row_scratch: Vec::new() }
    }

    fn drain(&mut self, events: &mut Vec<EngineEvent>) {
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
            0..=255 => palette::xterm256(i as u8),
            _ if i == NamedColor::Background as usize => palette::VOID,
            _ => palette::TEXT,
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
                            pack(palette::TEXT)
                        }
                    }
                }
            }
        }
    }

    fn encode_row(&mut self, y: usize, out: &mut Vec<u8>) {
        let offset = self.term.grid().display_offset() as i32;
        let line = Line(y as i32 - offset);
        self.row_scratch.clear();
        let cols = self.size.cols;
        for x in 0..cols {
            let cell = &self.term.grid()[line][Column(x)];
            let flags = cell.flags;
            let bold = flags.contains(Flags::BOLD);
            let mut fg = self.rgb(cell.fg, bold, false);
            let mut bg = self.rgb(cell.bg, false, true);
            if flags.contains(Flags::INVERSE) {
                let pack = |(r, g, b): palette::Rgb| (r as u32) << 16 | (g as u32) << 8 | b as u32;
                let nbg = fg;
                fg = if bg == frame::DEFAULT_BG { pack(palette::VOID) } else { bg };
                bg = nbg;
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
        let full = force_full || self.full_next;
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
            self.encode_row(y, out);
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

    fn size(&self) -> (u16, u16) {
        (self.size.cols as u16, self.size.rows as u16)
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
}
