//! Frames for the JS decoder's fixture test (ui/test/frame.test.mjs): the
//! two sides of the wire format are in different languages, and the view
//! package (ui/view) is also decoded by apps outside this repo, so the
//! encoder writes known frames into testdata/frames and node decodes them.
//!
//! The files are checked in. This test fails when the encoder's bytes no
//! longer match them; regenerate with `SUZURI_REGEN_FIXTURES=1 cargo test
//! -p rsz-term --test frame_fixtures` and update the JS test's expectations.

use std::path::PathBuf;

use rsz_term::frame::*;

const RED: u32 = 0xcc3333;
const BLUE: u32 = 0x3355cc;
const WHITE: u32 = 0xdddddd;

fn cell(cp: char, fg: u32, bg: u32, attrs: u16) -> CellOut {
    CellOut { cp: cp as u32, fg, bg, attrs }
}

fn text(s: &str, fg: u32, bg: u32, attrs: u16) -> Vec<CellOut> {
    s.chars().map(|c| cell(c, fg, bg, attrs)).collect()
}

fn header(full: bool, nrows: u16, seq: u32) -> FrameHeader {
    FrameHeader {
        full,
        cols: 10,
        rows: 3,
        cursor_x: 2,
        cursor_y: 1,
        cursor_shape: 3,
        modes: M_BRACKETED_PASTE,
        display_offset: 0,
        history: 4,
        seq,
        nrows,
    }
}

/// A full 10x3 frame: colours, a selection that starts mid-row, a wide
/// glyph, a box-drawing glyph, an underline and a dropped blank tail.
fn full() -> Vec<u8> {
    let mut out = Vec::new();
    encode_header(&mut out, &header(true, 3, 7));
    // Row 0: "ab" red, "cd" white on blue, then blanks (dropped).
    let mut r0 = text("ab", RED, DEFAULT_BG, 0);
    r0.extend(text("cd", WHITE, BLUE, 0));
    r0.extend(text("      ", WHITE, DEFAULT_BG, 0));
    encode_row(&mut out, 0, 0, &r0);
    // Row 1: "$ " then "sel" selected at columns 2..5, then blanks.
    let mut r1 = text("$ ", WHITE, DEFAULT_BG, 0);
    r1.extend(text("sel", WHITE, DEFAULT_BG, A_SELECTED));
    r1.extend(text("     ", WHITE, DEFAULT_BG, 0));
    encode_row(&mut out, 1, 0, &r1);
    // Row 2: a wide glyph and its right half (0), a box line, an
    // underlined "u", then blanks.
    let mut r2 = vec![cell('漢', WHITE, DEFAULT_BG, 0), CellOut { cp: 0, fg: WHITE, bg: DEFAULT_BG, attrs: 0 }];
    r2.push(cell('─', WHITE, DEFAULT_BG, 0));
    r2.push(cell('u', WHITE, DEFAULT_BG, A_UNDERLINE));
    r2.extend(text("      ", WHITE, DEFAULT_BG, 0));
    encode_row(&mut out, 2, 0, &r2);
    out
}

/// A partial frame: only row 1's columns 4..7 changed, to "xyz".
fn partial() -> Vec<u8> {
    let mut out = Vec::new();
    encode_header(&mut out, &header(false, 1, 8));
    encode_row(&mut out, 1, 4, &text("xyz", RED, DEFAULT_BG, A_ITALIC));
    out
}

/// A full frame with grapheme clusters on row 0: 👍🏽, e + U+0301, 🇯🇵 and
/// 👨‍👩‍👧, each base in its run and the whole cluster in the table.
fn clusters() -> Vec<u8> {
    let mut out = Vec::new();
    encode_header(&mut out, &header(true, 1, 9));
    let spacer = CellOut { cp: 0, fg: WHITE, bg: DEFAULT_BG, attrs: 0 };
    let groups = ["👍🏽", "e\u{301}", "🇯🇵", "👨‍👩‍👧"];
    let mut row = Vec::new();
    let mut table = Vec::new();
    for g in groups {
        let cps: Vec<u32> = g.chars().map(|c| c as u32).collect();
        let w = if cps[0] == 'e' as u32 { 1 } else { 2 };
        encode_cluster(&mut table, 0, row.len() as u16, w, &cps);
        row.push(CellOut { cp: cps[0], fg: WHITE, bg: DEFAULT_BG, attrs: 0 });
        if w == 2 {
            row.push(spacer);
        }
    }
    row.extend(text("   ", WHITE, DEFAULT_BG, 0));
    encode_row(&mut out, 0, 0, &row);
    finish_clusters(&mut out, 0, groups.len() as u16, &table);
    out
}

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../testdata/frames")
}

#[test]
fn fixtures_match_the_encoder() {
    let regen = std::env::var_os("SUZURI_REGEN_FIXTURES").is_some();
    for (name, bytes) in [("full.bin", full()), ("partial.bin", partial()), ("clusters.bin", clusters())] {
        let path = dir().join(name);
        if regen {
            std::fs::create_dir_all(dir()).unwrap();
            std::fs::write(&path, &bytes).unwrap();
            continue;
        }
        let have = std::fs::read(&path).unwrap_or_else(|e| panic!("{}: {e}; regenerate (see the top of this file)", path.display()));
        assert!(have == bytes, "{name} no longer matches the encoder; regenerate (see the top of this file) and update ui/test/frame.test.mjs");
    }
}
