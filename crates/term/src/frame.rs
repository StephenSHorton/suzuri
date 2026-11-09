//! The binary frame a pane sends to the UI: a header plus the dirty rows,
//! each as runs of cells sharing colours and attributes. Little endian.
//! The UI decodes it in ui/view/frame.js; keep the two in step.
//!
//! header (32 bytes):
//!   u8 'F', u8 flags (1 = full), u16 cols, u16 rows, u16 cursor_x,
//!   u16 cursor_y, u8 cursor_shape (0 hidden, 1 block, 2 underline, 3 beam,
//!   4 hollow), u8 version (FORMAT_VERSION), u32 modes, u32 display_offset, u32 history, u32 seq,
//!   u16 nrows, u16 nclusters
//! row: u16 y, u16 nruns, u16 x0, u16 x1, then runs. Columns [x0, x1) are
//!      the row's dirty range: the UI clears just those and the runs cover
//!      only them (a full row is 0..cols).
//! run: u16 col, u16 n, u32 fg, u32 bg, u16 attrs, u16 0, then n × u32
//!      codepoints (0 = right half of a wide glyph)
//!
//! cluster table: only when nclusters > 0, right after the last row. One
//! entry per cell whose grapheme has 2+ codepoints (👍🏽, 👨‍👩‍👧, 🇯🇵, e +
//! U+0301), in (y, col) order: u16 y, u16 col, u8 w (1 or 2 columns), u8 n
//! (2..=64), then n × u32 codepoints, base first. Unaligned. The cell's run
//! still carries the base codepoint, so a decoder that ignores the table
//! draws the base glyph. To find it, walk past the rows.

pub const MAGIC: u8 = b'F';
/// The wire format's version, header byte 11. Bump it on any change to the
/// layout below, together with FORMAT_VERSION in ui/view: a decoder in
/// another app refuses a version it doesn't know instead of drawing garbage.
/// An optional section after the rows, announced in header bytes that were
/// reserved (written 0), is additive and needs no bump: the cluster table.
pub const FORMAT_VERSION: u8 = 1;
pub const HEADER_LEN: usize = 32;
/// The most codepoints a cluster entry carries (ghostty's grapheme limit).
pub const CLUSTER_MAX_CPS: usize = 64;
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
    out.push(FORMAT_VERSION);
    out.extend_from_slice(&h.modes.to_le_bytes());
    out.extend_from_slice(&h.display_offset.to_le_bytes());
    out.extend_from_slice(&h.history.to_le_bytes());
    out.extend_from_slice(&h.seq.to_le_bytes());
    out.extend_from_slice(&h.nrows.to_le_bytes());
    out.extend_from_slice(&0u16.to_le_bytes());
}

pub fn decode_header(b: &[u8]) -> Option<FrameHeader> {
    if b.len() < HEADER_LEN || b[0] != MAGIC || b[11] != FORMAT_VERSION {
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

/// Add a cluster entry to `table` (cut to CLUSTER_MAX_CPS codepoints).
pub fn encode_cluster(table: &mut Vec<u8>, y: u16, col: u16, w: u8, cps: &[u32]) {
    let cps = &cps[..cps.len().min(CLUSTER_MAX_CPS)];
    table.extend_from_slice(&y.to_le_bytes());
    table.extend_from_slice(&col.to_le_bytes());
    table.push(w);
    table.push(cps.len() as u8);
    for cp in cps {
        table.extend_from_slice(&cp.to_le_bytes());
    }
}

/// End the frame that starts at `out[frame_start]`: with entries, write
/// their count into the header and append the table. With none the frame
/// is unchanged.
pub fn finish_clusters(out: &mut Vec<u8>, frame_start: usize, count: u16, table: &[u8]) {
    if count == 0 {
        return;
    }
    out[frame_start + 30..frame_start + 32].copy_from_slice(&count.to_le_bytes());
    out.extend_from_slice(table);
}

/// A frame's cluster table as (y, col, w, codepoints), for tests and
/// embedders. Empty when there is none or the frame is cut short.
pub fn decode_clusters(b: &[u8]) -> Vec<(u16, u16, u8, Vec<u32>)> {
    let mut v = Vec::new();
    if b.len() < HEADER_LEN {
        return v;
    }
    let u16_at = |i: usize| b.get(i..i + 2).map(|s| u16::from_le_bytes([s[0], s[1]]) as usize);
    let (Some(nrows), Some(count)) = (u16_at(28), u16_at(30)) else { return v };
    let mut p = HEADER_LEN;
    for _ in 0..nrows {
        let Some(nruns) = u16_at(p + 2) else { return v };
        p += 8;
        for _ in 0..nruns {
            let Some(n) = u16_at(p + 2) else { return v };
            p += 16 + 4 * n;
        }
    }
    for _ in 0..count {
        let (Some(y), Some(col), Some(&w), Some(&n)) = (u16_at(p), u16_at(p + 2), b.get(p + 4), b.get(p + 5)) else { return v };
        let Some(cps) = b.get(p + 6..p + 6 + 4 * n as usize) else { return v };
        v.push((y as u16, col as u16, w, cps.chunks(4).map(|c| u32::from_le_bytes([c[0], c[1], c[2], c[3]])).collect()));
        p += 6 + 4 * n as usize;
    }
    v
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
        assert_eq!(out[11], FORMAT_VERSION);
        assert_eq!(decode_header(&out), Some(h));
        out[11] = FORMAT_VERSION + 1;
        assert_eq!(decode_header(&out), None, "another version is refused");
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
    fn cluster_table_roundtrip() {
        let h = FrameHeader { full: true, cols: 4, rows: 2, cursor_x: 0, cursor_y: 0, cursor_shape: 1, modes: 0, display_offset: 0, history: 0, seq: 1, nrows: 2 };
        let c = |cp: u32| CellOut { cp, fg: 1, bg: DEFAULT_BG, attrs: 0 };
        let mut out = Vec::new();
        encode_header(&mut out, &h);
        encode_row(&mut out, 0, 0, &[c(0x1F44D), c(0), c('x' as u32), c(' ' as u32)]);
        encode_row(&mut out, 1, 0, &[c('e' as u32), c(' ' as u32), c(' ' as u32), c(' ' as u32)]);
        let plain = out.clone();
        // No entries: the frame is unchanged and bytes 30-31 stay 0.
        finish_clusters(&mut out, 0, 0, &[]);
        assert_eq!(out, plain);
        assert_eq!(&out[30..32], &[0, 0]);
        assert!(decode_clusters(&out).is_empty());
        let mut table = Vec::new();
        encode_cluster(&mut table, 0, 0, 2, &[0x1F44D, 0x1F3FD]);
        encode_cluster(&mut table, 1, 0, 1, &['e' as u32, 0x301]);
        finish_clusters(&mut out, 0, 2, &table);
        assert_eq!(decode_header(&out), Some(h));
        assert_eq!(decode_clusters(&out), vec![(0, 0, 2, vec![0x1F44D, 0x1F3FD]), (1, 0, 1, vec!['e' as u32, 0x301])]);
        // A long cluster is cut to the limit.
        let mut t = Vec::new();
        encode_cluster(&mut t, 0, 0, 1, &[0x301; 70]);
        assert_eq!((t[5] as usize, t.len()), (CLUSTER_MAX_CPS, 6 + 4 * CLUSTER_MAX_CPS));
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
