//! Corpus diff between the two VT engines (card #290): every recorded PTY
//! stream in testdata/pty goes through alacritty_terminal and libghostty-vt
//! in its recorded read chunks, resizes where recorded, and after every
//! chunk the frames each would send are decoded into the screen the UI
//! would show. Reported per stream:
//!
//! - damage: an engine's screen built from its partial frames differs from
//!   its own full frame (a damage bug in that engine);
//! - steps: chunks after which the two engines' screens differ, by kind
//!   (text, colours, attributes, cursor, modes, history);
//! - the first and the last differing step, row by row.
//!
//!   cargo run -p rsz-term --example engine_diff [dir] [--all] [--stream name]
//!
//! --all prints every differing step, not only the first and the last.
use rsz_term::frame::{self, CellOut, FrameHeader};
use rsz_term::{Alacritty, Engine, Ghostty};

struct Stream {
    name: String,
    bytes: Vec<u8>,
    cols: u16,
    rows: u16,
    chunks: Vec<usize>,
    resizes: Vec<(usize, u16, u16)>,
    /// The final screen Go suzuri's vt10x drew (the .golden file).
    golden: Option<Vec<String>>,
}

fn load(dir: &str) -> Vec<Stream> {
    let mut names: Vec<_> = std::fs::read_dir(dir).expect("corpus dir").filter_map(|e| e.ok()).map(|e| e.path()).filter(|p| p.extension().is_some_and(|x| x == "bin")).collect();
    names.sort();
    names
        .into_iter()
        .map(|bin| {
            let meta: serde_json::Value = serde_json::from_slice(&std::fs::read(bin.with_extension("json")).unwrap()).unwrap();
            let chunks = meta["chunks"].as_array().map(|a| a.iter().map(|v| v.as_u64().unwrap() as usize).collect()).unwrap_or_default();
            let resizes = meta["resizes"]
                .as_array()
                .map(|a| a.iter().map(|r| (r["chunk"].as_u64().unwrap() as usize, r["cols"].as_u64().unwrap() as u16, r["rows"].as_u64().unwrap() as u16)).collect())
                .unwrap_or_default();
            Stream {
                name: bin.file_stem().unwrap().to_string_lossy().into(),
                bytes: std::fs::read(&bin).unwrap(),
                cols: meta["cols"].as_u64().unwrap() as u16,
                rows: meta["rows"].as_u64().unwrap() as u16,
                chunks,
                resizes,
                golden: std::fs::read_to_string(bin.with_extension("golden"))
                    .ok()
                    .map(|g| g.lines().skip(1).take_while(|l| !l.starts_with("== scrollback")).map(|l| l.trim_end().to_string()).collect()),
            }
        })
        .collect()
}

const BLANK: CellOut = CellOut { cp: b' ' as u32, fg: 0, bg: frame::DEFAULT_BG, attrs: 0 };

/// The screen as the UI keeps it: frames applied one after another.
#[derive(Clone, PartialEq)]
struct Screen {
    h: Option<FrameHeader>,
    cells: Vec<Vec<CellOut>>,
}

impl Screen {
    fn new() -> Self {
        Screen { h: None, cells: Vec::new() }
    }

    fn apply(&mut self, b: &[u8]) {
        let h = frame::decode_header(b).expect("frame header");
        let (cols, rows) = (h.cols as usize, h.rows as usize);
        if self.cells.len() != rows || self.cells.first().map(|r| r.len()) != Some(cols) || h.full {
            // A size change or a full frame: the UI starts from blank.
            self.cells = vec![vec![BLANK; cols]; rows];
        }
        let u16_at = |i: usize| u16::from_le_bytes([b[i], b[i + 1]]) as usize;
        let u32_at = |i: usize| u32::from_le_bytes([b[i], b[i + 1], b[i + 2], b[i + 3]]);
        let mut p = frame::HEADER_LEN;
        for _ in 0..h.nrows {
            let (y, nruns, x0, x1) = (u16_at(p), u16_at(p + 2), u16_at(p + 4), u16_at(p + 6));
            p += 8;
            let row = &mut self.cells[y];
            for c in &mut row[x0..x1.min(cols)] {
                *c = BLANK;
            }
            for _ in 0..nruns {
                let (col, n) = (u16_at(p), u16_at(p + 2));
                let (fg, bg, attrs) = (u32_at(p + 4), u32_at(p + 8), u16_at(p + 12) as u16);
                p += 16;
                for i in 0..n {
                    if col + i < cols {
                        row[col + i] = CellOut { cp: u32_at(p), fg, bg, attrs };
                    }
                    p += 4;
                }
            }
        }
        self.h = Some(h);
    }

    fn text(&self, y: usize) -> String {
        self.cells[y].iter().filter(|c| c.cp != 0).map(|c| char::from_u32(c.cp).unwrap_or('?')).collect::<String>().trim_end().to_string()
    }
}

/// Blank cells compare equal whatever ink they carry: the UI draws nothing
/// for a blank on the default background.
fn same_cell(a: &CellOut, b: &CellOut) -> bool {
    let blank = |c: &CellOut| (c.cp == b' ' as u32 || c.cp == 0) && c.bg == frame::DEFAULT_BG && c.attrs & (frame::A_UNDERLINE | frame::A_STRIKE | frame::A_SELECTED) == 0;
    if blank(a) && blank(b) {
        return a.cp == b.cp;
    }
    a == b
}

#[derive(Default)]
struct Kinds {
    text: usize,
    fg: usize,
    bg: usize,
    attrs: usize,
    cursor: usize,
    shape: usize,
    modes: usize,
    history: usize,
    size: usize,
}

/// What differs between two screens: a bitset of kinds and the rows.
fn diff(a: &Screen, b: &Screen) -> (Vec<&'static str>, Vec<usize>) {
    let (ha, hb) = (a.h.unwrap(), b.h.unwrap());
    let mut kinds = Vec::new();
    let mut rows = Vec::new();
    if (ha.cols, ha.rows) != (hb.cols, hb.rows) {
        return (vec!["size"], rows);
    }
    let (mut t, mut f, mut g, mut at) = (false, false, false, false);
    for y in 0..ha.rows as usize {
        let mut row_differs = false;
        for x in 0..ha.cols as usize {
            let (p, q) = (&a.cells[y][x], &b.cells[y][x]);
            if same_cell(p, q) {
                continue;
            }
            row_differs = true;
            t |= p.cp != q.cp;
            f |= p.fg != q.fg && p.cp != b' ' as u32;
            g |= p.bg != q.bg;
            at |= p.attrs != q.attrs;
        }
        if row_differs {
            rows.push(y);
        }
    }
    for (on, k) in [(t, "text"), (f, "fg"), (g, "bg"), (at, "attrs")] {
        if on {
            kinds.push(k);
        }
    }
    if (ha.cursor_x, ha.cursor_y) != (hb.cursor_x, hb.cursor_y) {
        kinds.push("cursor");
    }
    if ha.cursor_shape != hb.cursor_shape {
        kinds.push("shape");
    }
    if ha.modes != hb.modes {
        kinds.push("modes");
    }
    if ha.history != hb.history {
        kinds.push("history");
    }
    (kinds, rows)
}

fn show(step: usize, a: &Screen, b: &Screen, kinds: &[&str], rows: &[usize]) {
    let (ha, hb) = (a.h.unwrap(), b.h.unwrap());
    println!("  step {step}: {}", kinds.join(" "));
    if kinds.contains(&"size") {
        println!("    size A {}x{}  G {}x{}", ha.cols, ha.rows, hb.cols, hb.rows);
        return;
    }
    println!(
        "    cursor A ({},{}) shape {}  G ({},{}) shape {}; modes A {:#x} G {:#x}; history A {} G {}",
        ha.cursor_x, ha.cursor_y, ha.cursor_shape, hb.cursor_x, hb.cursor_y, hb.cursor_shape, ha.modes, hb.modes, ha.history, hb.history
    );
    for &y in rows.iter().take(12) {
        println!("    {y:>3} A: {}", a.text(y));
        println!("    {y:>3} G: {}", b.text(y));
        // The first differing cell, with its colours and attributes.
        if let Some(x) = (0..ha.cols as usize).find(|&x| !same_cell(&a.cells[y][x], &b.cells[y][x])) {
            let (p, q) = (a.cells[y][x], b.cells[y][x]);
            println!("        x {x}: A cp {:#x} fg {:06x} bg {:07x} at {:#x} | G cp {:#x} fg {:06x} bg {:07x} at {:#x}", p.cp, p.fg, p.bg, p.attrs, q.cp, q.fg, q.bg, q.attrs);
        }
    }
    if rows.len() > 12 {
        println!("    ... {} more rows", rows.len() - 12);
    }
}

/// One engine fed two ways: one snapshots partial frames (as the pane
/// does), the other a full frame after every chunk (the truth).
struct Pair {
    partial: Box<dyn Engine>,
    full: Box<dyn Engine>,
    ps: Screen,
    fs: Screen,
}

impl Pair {
    fn new(mk: &dyn Fn() -> Box<dyn Engine>) -> Self {
        Pair { partial: mk(), full: mk(), ps: Screen::new(), fs: Screen::new() }
    }

    fn step(&mut self, bytes: &[u8], resize: Option<(u16, u16)>, seq: u32, last: bool, buf: &mut Vec<u8>) {
        let mut ev = Vec::new();
        for e in [&mut self.partial, &mut self.full] {
            if let Some((c, r)) = resize {
                e.resize(c, r);
            }
            e.feed(bytes, &mut ev);
            if last && e.sync_deadline().is_some() {
                e.stop_sync(&mut ev);
            }
        }
        if self.partial.snapshot(buf, seq, false) {
            self.ps.apply(buf);
        }
        if self.full.snapshot(buf, seq, true) {
            self.fs.apply(buf);
        }
    }
}

/// A differing step: its index, both screens, the kinds and the rows.
type Step = (usize, Screen, Screen, Vec<&'static str>, Vec<usize>);

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let all = args.iter().any(|a| a == "--all");
    let only = args.iter().position(|a| a == "--stream").and_then(|i| args.get(i + 1)).cloned();
    let dir = args.iter().find(|a| !a.starts_with("--") && Some(*a) != only.as_ref()).cloned().unwrap_or_else(|| concat!(env!("CARGO_MANIFEST_DIR"), "/../../testdata/pty").into());
    for s in load(&dir) {
        if only.as_ref().is_some_and(|o| *o != s.name) {
            continue;
        }
        let (cols, rows) = (s.cols, s.rows);
        let mut a = Pair::new(&|| Box::new(Alacritty::new(cols, rows, 10_000)));
        let mut g = Pair::new(&|| Box::new(Ghostty::new(cols, rows, 10_000)));
        let mut buf = Vec::new();
        let mut k = Kinds::default();
        let (mut steps, mut damage_a, mut damage_g) = (0usize, 0usize, 0usize);
        let mut first: Option<Step> = None;
        let mut last_diff: Option<Step> = None;
        let mut off = 0;
        // The chunks, then whatever the recording didn't cut into chunks.
        let mut chunks = s.chunks.clone();
        let total: usize = chunks.iter().sum();
        if total < s.bytes.len() {
            chunks.push(s.bytes.len() - total);
        }
        let n = chunks.len();
        println!("== {} ({} bytes, {} chunks, {}x{})", s.name, s.bytes.len(), n, cols, rows);
        for (i, &len) in chunks.iter().enumerate() {
            let resize = s.resizes.iter().rev().find(|r| r.0 == i).map(|r| (r.1, r.2));
            let end = (off + len).min(s.bytes.len());
            let bytes = &s.bytes[off..end];
            off = end;
            let last = i + 1 == n;
            a.step(bytes, resize, i as u32, last, &mut buf);
            g.step(bytes, resize, i as u32, last, &mut buf);
            if a.ps.h.is_some() && a.ps != a.fs && !diff(&a.ps, &a.fs).0.is_empty() {
                if damage_a == 0 {
                    println!("  damage (alacritty) first at step {i}");
                    let (kd, rd) = diff(&a.ps, &a.fs);
                    show(i, &a.ps, &a.fs, &kd, &rd);
                }
                damage_a += 1;
                a.ps = a.fs.clone();
            }
            if g.ps.h.is_some() && g.ps != g.fs && !diff(&g.ps, &g.fs).0.is_empty() {
                if damage_g == 0 {
                    println!("  damage (ghostty) first at step {i}");
                    let (kd, rd) = diff(&g.ps, &g.fs);
                    show(i, &g.ps, &g.fs, &kd, &rd);
                }
                damage_g += 1;
                g.ps = g.fs.clone();
            }
            let (kinds, rows_d) = diff(&a.fs, &g.fs);
            if kinds.is_empty() {
                continue;
            }
            steps += 1;
            for kd in &kinds {
                match *kd {
                    "text" => k.text += 1,
                    "fg" => k.fg += 1,
                    "bg" => k.bg += 1,
                    "attrs" => k.attrs += 1,
                    "cursor" => k.cursor += 1,
                    "shape" => k.shape += 1,
                    "modes" => k.modes += 1,
                    "history" => k.history += 1,
                    _ => k.size += 1,
                }
            }
            if all {
                show(i, &a.fs, &g.fs, &kinds, &rows_d);
            }
            let d = (i, a.fs.clone(), g.fs.clone(), kinds, rows_d);
            if first.is_none() {
                first = Some(d);
            } else {
                last_diff = Some(d);
            }
        }
        println!(
            "  differing steps {steps}/{n}: text {} fg {} bg {} attrs {} cursor {} shape {} modes {} history {} size {}; damage bugs: alacritty {damage_a} ghostty {damage_g}",
            k.text, k.fg, k.bg, k.attrs, k.cursor, k.shape, k.modes, k.history, k.size
        );
        if !all {
            if let Some((i, x, y, kd, rd)) = &first {
                println!("  first:");
                show(*i, x, y, kd, rd);
            }
            if let Some((i, x, y, kd, rd)) = &last_diff {
                println!("  last:");
                show(*i, x, y, kd, rd);
            }
        }
        let final_same = diff(&a.fs, &g.fs).0.is_empty();
        println!("  final screen: {}", if final_same { "same" } else { "DIFFERS" });
        // Against Go suzuri's final screen: rows that match, per engine.
        if let Some(gold) = &s.golden {
            let score = |e: &dyn Engine| {
                // Claude Code writes U+00A0 where the goldens show a space.
                let t = e.screen_text().replace('\u{a0}', " ");
                let ours: Vec<&str> = t.lines().collect();
                let n = gold.len().max(ours.len());
                let same = (0..n).filter(|&i| ours.get(i).copied().unwrap_or("") == gold.get(i).map(|s| s.as_str()).unwrap_or("")).count();
                format!("{same}/{n}")
            };
            println!("  golden rows: alacritty {} ghostty {}", score(&*a.full), score(&*g.full));
        }
    }
}
