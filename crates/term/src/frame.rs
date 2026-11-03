//! The binary frame a pane sends to the UI: a header plus the dirty rows,
//! each as runs of cells sharing colours and attributes. Little endian.
//! The UI decodes it in ui/frame.js; keep the two in step.
//!
//! header (32 bytes):
//!   u8 'F', u8 flags (1 = full), u16 cols, u16 rows, u16 cursor_x,
//!   u16 cursor_y, u8 cursor_shape (0 hidden, 1 block, 2 underline, 3 beam,
//!   4 hollow), u8 0, u32 modes, u32 display_offset, u32 history, u32 seq,
//!   u16 nrows, u16 0
//! row: u16 y, u16 nruns, u16 x0, u16 x1, then runs. Columns [x0, x1) are
//!      the row's dirty range: the UI clears just those and the runs cover
//!      only them (a full row is 0..cols).
//! run: u16 col, u16 n, u32 fg, u32 bg, u16 attrs, u16 0, then n × u32
//!      codepoints (0 = right half of a wide glyph)

pub const MAGIC: u8 = b'F';
pub const HEADER_LEN: usize = 32;
/// bg value meaning "the theme background": the UI leaves it unpainted.
pub const DEFAULT_BG: u32 = 0x0100_0000;

pub const A_BOLD: u16 = 1;
pub const A_ITALIC: u16 = 2;
pub const A_UNDERLINE: u16 = 4;
pub const A_STRIKE: u16 = 8;
pub const A_DIM: u16 = 16;
pub const A_HIDDEN: u16 = 32;
/// The cell is inside the selection: the UI paints the selection colour.
pub const A_SELECTED: u16 = 64;

pub const M_APP_CURSOR: u32 = 1;
pub const M_BRACKETED_PASTE: u32 = 2;
pub const M_ALT_SCREEN: u32 = 4;
pub const M_FOCUS: u32 = 8;
pub const M_MOUSE_CLICK: u32 = 16;
pub const M_MOUSE_DRAG: u32 = 32;
pub const M_MOUSE_MOTION: u32 = 64;
pub const M_SGR_MOUSE: u32 = 128;
pub const M_ALT_SCROLL: u32 = 256;
pub const M_APP_KEYPAD: u32 = 512;
/// Kitty keyboard progressive-enhancement flags, in the protocol's bit
/// order (CSI > flags u): flags = (modes >> M_KITTY_SHIFT) & 31.
pub const M_KITTY_DISAMBIGUATE: u32 = 1024;
pub const M_KITTY_EVENT_TYPES: u32 = 2048;
pub const M_KITTY_ALTERNATE_KEYS: u32 = 4096;
pub const M_KITTY_ALL_KEYS: u32 = 8192;
pub const M_KITTY_ASSOCIATED_TEXT: u32 = 16384;
pub const M_KITTY_SHIFT: u32 = 10;

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct FrameHeader {
    pub full: bool,
    pub cols: u16,
    pub rows: u16,
    pub cursor_x: u16,
    pub cursor_y: u16,
    pub cursor_shape: u8,
    pub modes: u32,
    pub display_offset: u32,
    pub history: u32,
    pub seq: u32,
    pub nrows: u16,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct CellOut {
    pub cp: u32,
    pub fg: u32,
    pub bg: u32,
    pub attrs: u16,
}

pub fn encode_header(out: &mut Vec<u8>, h: &FrameHeader) {
    out.push(MAGIC);
    out.push(h.full as u8);
    out.extend_from_slice(&h.cols.to_le_bytes());
    out.extend_from_slice(&h.rows.to_le_bytes());
    out.extend_from_slice(&h.cursor_x.to_le_bytes());
    out.extend_from_slice(&h.cursor_y.to_le_bytes());
    out.push(h.cursor_shape);
    out.push(0);
    out.extend_from_slice(&h.modes.to_le_bytes());
    out.extend_from_slice(&h.display_offset.to_le_bytes());
    out.extend_from_slice(&h.history.to_le_bytes());
    out.extend_from_slice(&h.seq.to_le_bytes());
    out.extend_from_slice(&h.nrows.to_le_bytes());
    out.extend_from_slice(&0u16.to_le_bytes());
}

pub fn decode_header(b: &[u8]) -> Option<FrameHeader> {
    if b.len() < HEADER_LEN || b[0] != MAGIC {
        return None;
    }
    let u16_at = |i: usize| u16::from_le_bytes([b[i], b[i + 1]]);
    let u32_at = |i: usize| u32::from_le_bytes([b[i], b[i + 1], b[i + 2], b[i + 3]]);
    Some(FrameHeader {
        full: b[1] & 1 != 0,
        cols: u16_at(2),
        rows: u16_at(4),
        cursor_x: u16_at(6),
        cursor_y: u16_at(8),
        cursor_shape: b[10],
        modes: u32_at(12),
        display_offset: u32_at(16),
        history: u32_at(20),
        seq: u32_at(24),
        nrows: u16_at(28),
    })
}

/// Encode one row. `cells` are the row's columns [x0, x0 + cells.len()).
pub fn encode_row(out: &mut Vec<u8>, y: u16, x0: u16, cells: &[CellOut]) {
    out.extend_from_slice(&y.to_le_bytes());
    let nruns_at = out.len();
    out.extend_from_slice(&0u16.to_le_bytes());
    out.extend_from_slice(&x0.to_le_bytes());
    out.extend_from_slice(&(x0 + cells.len() as u16).to_le_bytes());
    let mut nruns = 0u16;
    let mut i = 0;
    while i < cells.len() {
        let c = cells[i];
        let mut j = i + 1;
        while j < cells.len() && cells[j].fg == c.fg && cells[j].bg == c.bg && cells[j].attrs == c.attrs {
            j += 1;
        }
        // Trailing blank run on the default background: the UI clears the
        // range first, so it need not be sent.
        let blank_tail = j == cells.len() && c.bg == DEFAULT_BG && c.attrs & (A_UNDERLINE | A_STRIKE) == 0 && cells[i..j].iter().all(|c| c.cp == b' ' as u32);
        if !blank_tail {
            out.extend_from_slice(&(x0 + i as u16).to_le_bytes());
            out.extend_from_slice(&((j - i) as u16).to_le_bytes());
            out.extend_from_slice(&c.fg.to_le_bytes());
            out.extend_from_slice(&c.bg.to_le_bytes());
            out.extend_from_slice(&c.attrs.to_le_bytes());
            out.extend_from_slice(&0u16.to_le_bytes());
            for cell in &cells[i..j] {
                out.extend_from_slice(&cell.cp.to_le_bytes());
            }
            nruns += 1;
        }
        i = j;
    }
    out[nruns_at..nruns_at + 2].copy_from_slice(&nruns.to_le_bytes());
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn header_roundtrip() {
        let h = FrameHeader {
            full: true,
            cols: 120,
            rows: 40,
            cursor_x: 3,
            cursor_y: 39,
            cursor_shape: 3,
            modes: M_APP_CURSOR | M_BRACKETED_PASTE,
            display_offset: 7,
            history: 900,
            seq: 42,
            nrows: 40,
        };
        let mut out = Vec::new();
        encode_header(&mut out, &h);
        assert_eq!(out.len(), HEADER_LEN);
        assert_eq!(decode_header(&out), Some(h));
    }

    #[test]
    fn runs_merge_and_drop_blank_tail() {
        let c = |cp: char, fg| CellOut { cp: cp as u32, fg, bg: DEFAULT_BG, attrs: 0 };
        let cells = [c('a', 1), c('b', 1), c('c', 2), c(' ', 2), c(' ', 2)];
        let mut out = Vec::new();
        encode_row(&mut out, 5, 0, &cells);
        // y, nruns=2 (ab, c+blanks is not all blank so it stays)
        assert_eq!(u16::from_le_bytes([out[2], out[3]]), 2);
        let blank = [c('x', 1), c(' ', 2), c(' ', 2)];
        let mut out = Vec::new();
        encode_row(&mut out, 0, 0, &blank);
        assert_eq!(u16::from_le_bytes([out[2], out[3]]), 1);
    }

    #[test]
    fn column_range() {
        let c = |cp: char| CellOut { cp: cp as u32, fg: 1, bg: DEFAULT_BG, attrs: 0 };
        let mut out = Vec::new();
        encode_row(&mut out, 3, 7, &[c('a'), c('b')]);
        let u = |i: usize| u16::from_le_bytes([out[i], out[i + 1]]);
        assert_eq!((u(0), u(2), u(4), u(6)), (3, 1, 7, 9));
        // The run starts at the range's first column.
        assert_eq!((u(8), u(10)), (7, 2));
    }
}
