//! The VT engine seam (orca's §2.3 shape from #252): feed bytes on the reader
//! thread, take a dirty-rows snapshot on the frame thread, and get a small
//! slice of events per feed. libghostty-vt is the engine (card #290,
//! [`crate::ghostty::Ghostty`]); alacritty_terminal was until then.

use std::time::Instant;

use crate::input::MouseEvent;
use crate::palette;
#[cfg(test)]
use crate::frame;

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

    /// Draw #296's command block: `above` goes just above the cursor's
    /// line (what is on screen scrolls up into the history to make room) and
    /// `on_line` replaces the cursor's line, so the cursor stays on the row
    /// the shell, and ConPTY, which repaints by absolute row, expect. With
    /// `keep_cursor_line`, the cursor's line moves up with the rest instead
    /// of being replaced.
    fn insert_block(&mut self, _above: &[(String, palette::Rgb)], _on_line: &(String, palette::Rgb), _keep_cursor_line: bool) {}

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
    /// Growing taller adds blank rows under the screen and leaves the
    /// cursor where it is, instead of bringing history lines back down.
    /// Windows' ConPTY grows that way and then draws by absolute row, so
    /// our rows must stay where its rows are (#296).
    fn set_grow_at_bottom(&mut self, _on: bool) {}

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


#[cfg(test)]
mod tests {

    /// The engine tests. They ran on alacritty_terminal too until card
    /// #290 removed it; `GHOSTTY` keeps the asserts that showed where the two
    /// engines differed.
    macro_rules! engine_tests {
        ($name:ident, $engine:ty, $ghostty:expr) => {
            mod $name {
                #[allow(unused_imports)]
                use super::super::*;
                type E = $engine;
                #[allow(dead_code)]
                const GHOSTTY: bool = $ghostty;

    #[test]
    fn feeds_text_and_answers_dsr() {
        let mut e = E::new(20, 4, 100);
        let mut ev = Vec::new();
        e.feed(b"hello\r\nw\xc3\xb6rld \xe7\xa1\xaf", &mut ev);
        assert_eq!(e.screen_text(), "hello\nwörld 硯\n");
        e.feed(b"\x1b[6n", &mut ev);
        assert_eq!(ev, vec![EngineEvent::PtyWrite(b"\x1b[2;9R".to_vec())]);
    }

    #[test]
    fn grow_at_bottom_keeps_the_cursor_row() {
        // #296 on Windows: the bar hides while a command runs and the pane
        // grows 42 -> 45; ConPTY keeps its cursor row, so ours must too.
        let w = (1u8, 1u8, 1u8);
        for at_bottom in [false, true] {
            let mut e = E::new(20, 4, 100);
            e.set_grow_at_bottom(at_bottom);
            let mut ev = Vec::new();
            e.feed(b"\x1b[H\x1b[2J ", &mut ev);
            e.insert_block(&[("rule".into(), w)], &("hdr".into(), w), false);
            e.resize(20, 7);
            e.feed(b"\x1b[6n", &mut ev);
            // Without it alacritty brings the rule (and the line above)
            // back down and the cursor with them. ghostty pulls history
            // down only when the cursor is on the bottom row, so here it
            // keeps the row either way.
            let want: &[u8] = if at_bottom || GHOSTTY { b"\x1b[1;2R" } else { b"\x1b[3;2R" };
            assert_eq!(ev, vec![EngineEvent::PtyWrite(want.to_vec())], "at_bottom {at_bottom}");
            if at_bottom {
                assert_eq!(e.screen_text().lines().next(), Some("hdr"));
                assert_eq!(e.history_tail(1), vec!["rule".to_string()]);
            }
            // Shrinking and growing back from the bottom row: ConPTY scrolls
            // on the shrink and adds blank rows on the grow.
            let mut e = E::new(20, 6, 100);
            e.set_grow_at_bottom(at_bottom);
            e.feed(b"a\r\nb\r\nc\r\nd\r\ne\r\nf", &mut ev);
            e.resize(20, 3);
            e.resize(20, 6);
            ev.clear();
            e.feed(b"\x1b[6n", &mut ev);
            let want: &[u8] = if at_bottom { b"\x1b[3;2R" } else { b"\x1b[6;2R" };
            assert_eq!(ev, vec![EngineEvent::PtyWrite(want.to_vec())], "at_bottom {at_bottom}");
        }
    }

    #[test]
    fn history_tail_is_oldest_first_and_capped() {
        let mut e = E::new(10, 2, 100);
        let mut ev = Vec::new();
        e.feed(b"one\r\ntwo\r\nthree\r\nfour  \r\nfive", &mut ev);
        assert_eq!(e.screen_text(), "four\nfive\n");
        assert_eq!(e.history_tail(2), vec!["two".to_string(), "three".to_string()]);
        assert_eq!(e.history_tail(50), vec!["one".to_string(), "two".to_string(), "three".to_string()]);
        assert!(E::new(10, 2, 100).history_tail(5).is_empty());
    }

    fn kitty_flags(e: &mut E) -> u32 {
        let mut out = Vec::new();
        e.snapshot(&mut out, 1, true);
        (frame::decode_header(&out).unwrap().modes >> frame::M_KITTY_SHIFT) & 31
    }

    #[test]
    fn kitty_keyboard_push_query_pop_per_screen() {
        let mut e = E::new(10, 2, 0);
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
        let mut e = E::new(10, 2, 0);
        let mut ev = Vec::new();
        e.feed(b"\xe7\xa1", &mut ev);
        e.feed(b"\xaf!", &mut ev);
        assert_eq!(e.screen_text(), "硯!\n");
        // A frame taken between the halves (the corpus's cat-large splits
        // "ü ─" so).
        let mut e = E::new(10, 2, 0);
        let mut out = Vec::new();
        e.feed(b"a \xc3", &mut ev);
        e.snapshot(&mut out, 1, true);
        e.feed(b"\xbc b", &mut ev);
        assert_eq!(e.screen_text(), "a ü b\n");
        e.snapshot(&mut out, 2, true);
        let n = frame::HEADER_LEN + 8 + 16;
        let cps: Vec<u32> = out[n..].chunks(4).take(5).map(|c| u32::from_le_bytes([c[0], c[1], c[2], c[3]])).collect();
        assert_eq!(cps, vec!['a' as u32, ' ' as u32, 'ü' as u32, ' ' as u32, 'b' as u32]);
        // ... and followed by a space and another non-ASCII character, as
        // in the corpus's cat-large ("ü ─" split after its first byte).
        // alacritty's vte 0.15 loses that space; ghostty keeps it.
        let mut e = E::new(10, 2, 0);
        e.feed(b"a \xc3", &mut ev);
        e.feed(b"\xbc \xe2\x94\x80", &mut ev);
        assert_eq!(e.screen_text(), if GHOSTTY { "a ü ─\n" } else { "a ü─\n" });
    }

    #[test]
    fn partial_damage_after_full() {
        let mut e = E::new(10, 5, 0);
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
        // Each row carries only its dirty columns, with one cell of slack
        // (alacritty). ghostty's damage is by row: the whole row goes.
        let mut p = frame::HEADER_LEN;
        for _ in 0..h.nrows {
            let u = |i: usize| u16::from_le_bytes([out[i], out[i + 1]]) as usize;
            let (y, nruns, x0, x1) = (u(p), u(p + 2), u(p + 4), u(p + 6));
            let most = if GHOSTTY { 10 } else { 3 };
            assert!(x1 - x0 <= most, "row {y}: range {x0}..{x1}");
            p += 8;
            for _ in 0..nruns {
                p += 16 + 4 * u(p + 2);
            }
        }
        assert_eq!(p, out.len());
    }

    #[test]
    fn sync_update_holds_until_end() {
        let mut e = E::new(10, 2, 0);
        let mut ev = Vec::new();
        let mut out = Vec::new();
        // The first frame is always full; take it before the update.
        e.snapshot(&mut out, 0, false);
        e.feed(b"\x1b[?2026h", &mut ev);
        assert!(e.sync_deadline().is_some());
        e.feed(b"abc", &mut ev);
        if GHOSTTY {
            // ghostty applies the bytes and holds the frame instead: no
            // frame goes out until the update ends.
            assert!(!e.snapshot(&mut out, 1, false));
        } else {
            // Bytes inside a sync block are buffered, not applied yet.
            assert_eq!(e.screen_text(), "\n");
        }
        e.feed(b"\x1b[?2026l", &mut ev);
        assert!(e.sync_deadline().is_none());
        assert_eq!(e.screen_text(), "abc\n");
        assert!(e.snapshot(&mut out, 2, false));
        assert!(frame::decode_header(&out).unwrap().nrows >= 1);
    }

    fn fed(cols: u16, rows: u16, sb: usize, bytes: &[u8]) -> E {
        let mut e = E::new(cols, rows, sb);
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
        e.select_extend(5, 0, true);
        // A space the drag took, with text after it on the line, is copied.
        assert_eq!(e.selection_text().as_deref(), Some("hello "));
        // Blanks past the end of a line's text are not.
        let mut t = fed(20, 3, 0, b"ab\r\ncd");
        t.select_start(0, 0, false, SelectKind::Cell);
        t.select_extend(1, 1, true);
        assert_eq!(t.selection_text().as_deref(), Some("ab\ncd"));
        t.select_start(0, 0, false, SelectKind::Cell);
        t.select_extend(15, 0, true);
        assert_eq!(t.selection_text().as_deref(), Some("ab"));
        // A drag that ends in the next line's indentation keeps the line
        // break and the indentation it took.
        let mut t = fed(20, 3, 0, b"ab\r\n  cd");
        t.select_start(0, 0, false, SelectKind::Cell);
        t.select_extend(1, 1, true);
        assert_eq!(t.selection_text().as_deref(), Some("ab\n  "));
        // ... and no break when the row only continues a wrapped line.
        let mut t = fed(4, 3, 0, b"abcd  ef");
        t.select_start(0, 0, false, SelectKind::Cell);
        t.select_extend(1, 1, true);
        assert_eq!(t.selection_text().as_deref(), Some("abcd  "));
        e.select_extend(4, 0, true);
        assert_eq!(e.selection_text().as_deref(), Some("hello"));
        let mut out = Vec::new();
        e.snapshot(&mut out, 1, false);
        // Row 0's first run (after the 8-byte row head) is "hello", marked selected.
        let p = HEADER_LEN_FOR_TEST;
        let attrs = u16::from_le_bytes([out[p + 8 + 12], out[p + 8 + 13]]);
        let n = u16::from_le_bytes([out[p + 8 + 2], out[p + 8 + 3]]);
        assert_eq!((n, attrs & frame::A_SELECTED), (5, frame::A_SELECTED));
        // Clearing it repaints the rows without the mark.
        e.select_clear();
        e.snapshot(&mut out, 2, false);
        assert!(frame::decode_header(&out).unwrap().full);
        let attrs = u16::from_le_bytes([out[p + 8 + 12], out[p + 8 + 13]]);
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
        // Tracking modes replace each other (xterm): 1003 then 1003l leaves
        // no mouse reporting, though 1000 was set first.
        e.feed(b"\x1b[?1000h\x1b[?1003h\x1b[?1003l", &mut Vec::new());
        let report = e.mouse_report(ev);
        assert_eq!(report, None, "got {report:?}");
    }

    #[test]
    fn tabs_show_as_blanks_and_copy() {
        // alacritty keeps the tab in the cell it started from (the frame
        // carries 0x9, which the UI draws as nothing); ghostty keeps blanks.
        let mut e = fed(20, 2, 0, b"a\tb");
        let mut out = Vec::new();
        e.snapshot(&mut out, 1, true);
        let n = frame::HEADER_LEN + 8 + 16;
        let cp1 = u32::from_le_bytes([out[n + 4], out[n + 5], out[n + 6], out[n + 7]]);
        assert_eq!(cp1, if GHOSTTY { 0x20 } else { 0x09 });
        e.select_start(0, 0, false, SelectKind::Line);
        let copied = e.selection_text().unwrap();
        assert_eq!(copied, if GHOSTTY { "a       b" } else { "a\tb" }, "copy");
    }

    /// A frame's run codepoints for row `y` (a full frame).
    fn row_cps(out: &[u8], y: u16) -> Vec<u32> {
        let h = frame::decode_header(out).unwrap();
        let u = |i: usize| u16::from_le_bytes([out[i], out[i + 1]]) as usize;
        let mut p = frame::HEADER_LEN;
        let mut cps = vec![0x20; h.cols as usize];
        for _ in 0..h.nrows {
            let (ry, nruns) = (u(p), u(p + 2));
            p += 8;
            for _ in 0..nruns {
                let (col, n) = (u(p), u(p + 2));
                for i in 0..n {
                    let c = u32::from_le_bytes(out[p + 16 + 4 * i..p + 20 + 4 * i].try_into().unwrap());
                    if ry == y as usize {
                        cps[col + i] = c;
                    }
                }
                p += 16 + 4 * n;
            }
        }
        cps
    }

    #[test]
    fn grapheme_clusters_one_cell() {
        let s = "👍🏽|👨‍👩‍👧|🇯🇵|a\u{301}|";
        let mut e = fed(20, 2, 0, s.as_bytes());
        assert_eq!(e.cursor(), (11, 0));
        assert_eq!(e.screen_text(), format!("{s}\n"));
        let mut out = Vec::new();
        e.snapshot(&mut out, 1, true);
        let cl = frame::decode_clusters(&out);
        let want: Vec<(u16, u16, u8, Vec<u32>)> = vec![
            (0, 0, 2, "👍🏽".chars().map(|c| c as u32).collect()),
            (0, 3, 2, "👨‍👩‍👧".chars().map(|c| c as u32).collect()),
            (0, 6, 2, "🇯🇵".chars().map(|c| c as u32).collect()),
            (0, 9, 1, vec!['a' as u32, 0x301]),
        ];
        assert_eq!(cl, want);
        let cps = row_cps(&out, 0);
        assert_eq!((cps[0], cps[1], cps[3], cps[4], cps[6], cps[7], cps[9]), (0x1F44D, 0, 0x1F468, 0, 0x1F1EF, 0, 'a' as u32));
        // VS16 makes ❤ an emoji two wide; alone it is one, with no entry.
        let mut e = fed(20, 2, 0, "❤\u{FE0F}".as_bytes());
        assert_eq!(e.cursor(), (2, 0));
        let mut e2 = fed(20, 2, 0, "❤".as_bytes());
        assert_eq!(e2.cursor(), (1, 0));
        e.snapshot(&mut out, 1, true);
        assert_eq!(frame::decode_clusters(&out), vec![(0, 0, 2, vec![0x2764, 0xFE0F])]);
        e2.snapshot(&mut out, 1, true);
        assert!(frame::decode_clusters(&out).is_empty());
    }

    #[test]
    fn long_cluster_not_blanked() {
        // Past the old 16-codepoint buffers: e + 25 combining acutes.
        let s = format!("e{}", "\u{301}".repeat(25));
        let mut e = fed(20, 2, 100, s.as_bytes());
        assert_eq!(e.screen_text(), format!("{s}\n"));
        let mut out = Vec::new();
        e.snapshot(&mut out, 1, true);
        let cl = frame::decode_clusters(&out);
        assert_eq!((cl.len(), cl[0].3.len()), (1, 26));
        // An image mark rewrites the cell with its whole text.
        e.feed(b"\x1b[1D", &mut Vec::new());
        e.mark_cursor(4);
        assert_eq!(e.screen_text(), format!("{s}\n"));
        assert_eq!(e.marks(0), vec![(4, 0, 0)]);
    }

    #[test]
    fn clusters_copy_link_and_block_lines() {
        // Copy goes through ghostty's formatter, which keeps clusters.
        let mut e = fed(20, 2, 0, "👨‍👩‍👧x".as_bytes());
        e.select_start(0, 0, false, SelectKind::Line);
        assert_eq!(e.selection_text().as_deref(), Some("👨‍👩‍👧x"));
        // URL columns stay one per cell after an emoji.
        let e = fed(30, 2, 0, "👍🏽 https://x.y".as_bytes());
        let l = e.link_at(5, 0).unwrap();
        assert_eq!((l.url.as_str(), l.x0, l.x1), ("https://x.y", 3, 14));
        // A command-block line keeps whole clusters and is cut by their
        // width: the family is 2 columns, not 6.
        let w = (1u8, 1u8, 1u8);
        let line = "✅ ok 👨‍👩‍👧 é";
        let mut e = fed(20, 3, 100, b"");
        e.insert_block(&[], &(line.into(), w), false);
        assert_eq!(e.screen_text().lines().next(), Some(line));
        let mut e = fed(9, 3, 100, b"");
        e.insert_block(&[], &(line.into(), w), false);
        assert_eq!(e.screen_text().lines().next(), Some("✅ ok 👨‍👩‍👧"));
    }

    #[test]
    fn ascii_frame_has_no_cluster_table() {
        let mut e = fed(20, 2, 0, "ab 漢 😀".as_bytes());
        let mut out = Vec::new();
        e.snapshot(&mut out, 1, true);
        assert_eq!(&out[30..32], &[0, 0]);
    }

    #[test]
    fn grapheme_mode_survives_ris() {
        let mut e = fed(20, 2, 0, b"");
        let mut ev = Vec::new();
        e.feed(b"\x1b[?2027$p", &mut ev);
        assert_eq!(ev, vec![EngineEvent::PtyWrite(b"\x1b[?2027;1$y".to_vec())], "DECRQM reports it set");
        e.feed("\x1bc🇯🇵".as_bytes(), &mut Vec::new());
        assert_eq!(e.cursor(), (2, 0), "a flag is one cell after RIS");
        // A program can still turn it off.
        e.feed("\x1b[?2027l\r\x1b[2K🇯🇵".as_bytes(), &mut Vec::new());
        assert_eq!(e.cursor(), (4, 0));
    }

    #[test]
    fn image_marks_move_and_clear_with_the_text() {
        // #288's anchors: a mark sits on the cell under the cursor, keeps
        // that cell's text, scrolls with it, and goes when the cell is
        // rewritten, erased or the terminal reset.
        let mut e = fed(10, 3, 100, b"ab\x1b[1D");
        e.mark_cursor(7);
        assert_eq!(e.cursor(), (1, 0));
        assert_eq!(e.screen_text(), "ab\n");
        assert_eq!(e.marks(0), vec![(7, 0, 1)]);
        // Output scrolls it up and into the history.
        e.feed(b"\r\n1\r\n2\r\n3", &mut Vec::new());
        assert_eq!(e.marks(0), vec![]);
        assert_eq!(e.marks(1), vec![(7, -1, 1)]);
        // Scrolling the view up shows it again, by its row in the view.
        e.scroll(1);
        assert_eq!(e.marks(0), vec![(7, 0, 1)]);
        e.scroll(i32::MIN);
        // Two marks; rewriting one's cell drops only that one.
        e.feed(b"\x1b[2;1H", &mut Vec::new());
        e.mark_cursor(8);
        e.feed(b"\x1b[3;1H", &mut Vec::new());
        e.mark_cursor(9);
        assert_eq!(e.marks(1), vec![(7, -1, 1), (8, 1, 0), (9, 2, 0)]);
        e.feed(b"\x1b[2;1Hx", &mut Vec::new());
        assert_eq!(e.marks(1), vec![(7, -1, 1), (9, 2, 0)]);
        // Erasing the line drops it too.
        e.feed(b"\x1b[3;1H\x1b[2K", &mut Vec::new());
        assert_eq!(e.marks(1), vec![(7, -1, 1)]);
        // The alternate screen has none of the main screen's; they are
        // back when it closes.
        e.feed(b"\x1b[?1049h", &mut Vec::new());
        assert_eq!(e.marks(5), vec![]);
        e.feed(b"\x1b[?1049l", &mut Vec::new());
        assert_eq!(e.marks(1), vec![(7, -1, 1)]);
        // A mark at the last column with a wrap pending neither wraps nor
        // scrolls the screen.
        let mut e = fed(4, 2, 100, b"\x1b[2;1Habcd");
        e.mark_cursor(3);
        assert_eq!(e.cursor(), (3, 1));
        assert_eq!(e.screen_text(), "\nabcd\n");
        assert_eq!(e.marks(0), vec![(3, 1, 3)]);
        e.feed(b"\r\n", &mut Vec::new());
        assert_eq!(e.marks(0), vec![(3, 0, 3)]);
        // A reset clears them.
        e.feed(b"\x1bc", &mut Vec::new());
        assert_eq!(e.marks(10), vec![]);
        // Insert mode, line drawing and origin mode on: the marked cell keeps
        // its text, the row doesn't shift, and the program's next character
        // lands where it would have.
        let mut e = fed(10, 3, 100, b"aqc\x1b[4h\x1b(0\x1b[1;2r\x1b[?6h\x1b[1;2H");
        assert_eq!(e.cursor(), (1, 0));
        e.mark_cursor(5);
        assert_eq!(e.cursor(), (1, 0));
        assert_eq!(e.screen_text(), "aqc\n");
        assert_eq!(e.marks(0), vec![(5, 0, 1)]);
        // Still inserting, still drawing lines, still addressing from the
        // region's top.
        e.feed(b"q\x1b[2;1Hz", &mut Vec::new());
        assert_eq!(e.screen_text(), "a─qc\n≥\n");
        // Known difference: ghostty's mark is written between DECSC and
        // DECRC, so it replaces the program's own saved cursor. A program
        // that saves the cursor, prints an image and restores comes back to
        // the image, not to where it saved (alacritty marks the cell
        // without touching the saved cursor).
        let mut e = fed(10, 4, 100, b"\x1b7\x1b[3;5H");
        e.mark_cursor(6);
        e.feed(b"\x1b8", &mut Vec::new());
        assert_eq!(e.cursor(), if GHOSTTY { (4, 2) } else { (0, 0) });
    }
            }
        };
    }

    engine_tests!(ghostty, crate::ghostty::Ghostty, true);
}
