//! The replay bench for libghostty-vt (ghostty 7b267cd, ReleaseFast), next to
//! examples/replay.rs for alacritty_terminal. Same corpus, same cases.
//!
//!   GHOSTTY_VT_LIB=<ghostty>/zig-out/lib \
//!     cargo run --release -p rsz-term --features ghostty --example replay_ghostty
//!
//! "snap" here is the render-state path a renderer would use: update, walk
//! the dirty rows, read each cell's graphemes and resolved fg/bg, then clean.
use std::ffi::c_void;
use std::time::{Duration, Instant};

type Terminal = *mut c_void;
type RenderState = *mut c_void;
type RowIter = *mut c_void;
type RowCells = *mut c_void;

#[repr(C)]
#[derive(Default, Clone, Copy)]
struct Rgb {
    r: u8,
    g: u8,
    b: u8,
}

const RS_DATA_ROW_ITERATOR: i32 = 4;
const ROW_DATA_CELLS: i32 = 3;
const CELLS_GRAPHEMES_LEN: i32 = 3;
const CELLS_GRAPHEMES_BUF: i32 = 4;
const CELLS_BG: i32 = 5;
const CELLS_FG: i32 = 6;

#[link(name = "ghostty-vt")]
unsafe extern "C" {
    fn ghostty_terminal_new(alloc: *const c_void, out: *mut Terminal, cols: u16, rows: u16) -> i32;
    fn ghostty_terminal_free(t: Terminal);
    fn ghostty_terminal_resize(t: Terminal, cols: u16, rows: u16, cw: u32, ch: u32) -> i32;
    fn ghostty_terminal_vt_write(t: Terminal, data: *const u8, len: usize);
    fn ghostty_render_state_new(alloc: *const c_void, out: *mut RenderState) -> i32;
    fn ghostty_render_state_free(s: RenderState);
    fn ghostty_render_state_update(s: RenderState, t: Terminal) -> i32;
    fn ghostty_render_state_clean(s: RenderState) -> i32;
    fn ghostty_render_state_get(s: RenderState, data: i32, out: *mut c_void) -> i32;
    fn ghostty_render_state_row_iterator_new(alloc: *const c_void, out: *mut RowIter) -> i32;
    fn ghostty_render_state_row_iterator_free(it: RowIter);
    fn ghostty_render_state_row_iterator_next_dirty(it: RowIter, y: *mut u16) -> bool;
    fn ghostty_render_state_row_get(it: RowIter, data: i32, out: *mut c_void) -> i32;
    fn ghostty_render_state_row_cells_new(alloc: *const c_void, out: *mut RowCells) -> i32;
    fn ghostty_render_state_row_cells_free(c: RowCells);
    fn ghostty_render_state_row_cells_next(c: RowCells) -> bool;
    fn ghostty_render_state_row_cells_get(c: RowCells, data: i32, out: *mut c_void) -> i32;
}

struct Ghostty {
    t: Terminal,
    rs: RenderState,
    it: RowIter,
    cells: RowCells,
    sum: u64,
}

impl Ghostty {
    fn new(cols: u16, rows: u16) -> Self {
        unsafe {
            let mut g = Ghostty { t: std::ptr::null_mut(), rs: std::ptr::null_mut(), it: std::ptr::null_mut(), cells: std::ptr::null_mut(), sum: 0 };
            assert_eq!(ghostty_terminal_new(std::ptr::null(), &mut g.t, cols, rows), 0);
            assert_eq!(ghostty_render_state_new(std::ptr::null(), &mut g.rs), 0);
            assert_eq!(ghostty_render_state_row_iterator_new(std::ptr::null(), &mut g.it), 0);
            assert_eq!(ghostty_render_state_row_cells_new(std::ptr::null(), &mut g.cells), 0);
            g
        }
    }
    fn feed(&mut self, b: &[u8]) {
        unsafe { ghostty_terminal_vt_write(self.t, b.as_ptr(), b.len()) }
    }
    fn resize(&mut self, cols: u16, rows: u16) {
        unsafe { ghostty_terminal_resize(self.t, cols, rows, 8, 16) };
    }
    fn snapshot(&mut self) {
        unsafe {
            ghostty_render_state_update(self.rs, self.t);
            ghostty_render_state_get(self.rs, RS_DATA_ROW_ITERATOR, &mut self.it as *mut _ as *mut c_void);
            let mut y = 0u16;
            let mut buf = [0u32; 16];
            while ghostty_render_state_row_iterator_next_dirty(self.it, &mut y) {
                ghostty_render_state_row_get(self.it, ROW_DATA_CELLS, &mut self.cells as *mut _ as *mut c_void);
                while ghostty_render_state_row_cells_next(self.cells) {
                    let mut n = 0u32;
                    ghostty_render_state_row_cells_get(self.cells, CELLS_GRAPHEMES_LEN, &mut n as *mut _ as *mut c_void);
                    if n > 0 && n <= 16 {
                        ghostty_render_state_row_cells_get(self.cells, CELLS_GRAPHEMES_BUF, buf.as_mut_ptr() as *mut c_void);
                        self.sum = self.sum.wrapping_add(buf[0] as u64);
                    }
                    let mut fg = Rgb::default();
                    let mut bg = Rgb::default();
                    ghostty_render_state_row_cells_get(self.cells, CELLS_FG, &mut fg as *mut _ as *mut c_void);
                    ghostty_render_state_row_cells_get(self.cells, CELLS_BG, &mut bg as *mut _ as *mut c_void);
                    self.sum = self.sum.wrapping_add((fg.r ^ bg.b) as u64 + y as u64);
                }
            }
            ghostty_render_state_clean(self.rs);
        }
    }
}

impl Drop for Ghostty {
    fn drop(&mut self) {
        unsafe {
            ghostty_render_state_row_cells_free(self.cells);
            ghostty_render_state_row_iterator_free(self.it);
            ghostty_render_state_free(self.rs);
            ghostty_terminal_free(self.t);
        }
    }
}

struct Stream {
    name: String,
    bytes: Vec<u8>,
    cols: u16,
    rows: u16,
    chunks: Vec<usize>,
    resizes: Vec<(usize, u16, u16)>,
}

fn load(dir: &str) -> Vec<Stream> {
    let mut names: Vec<_> = std::fs::read_dir(dir).unwrap().filter_map(|e| e.ok()).map(|e| e.path()).filter(|p| p.extension().is_some_and(|x| x == "bin")).collect();
    names.sort();
    names
        .into_iter()
        .map(|bin| {
            let m: serde_json::Value = serde_json::from_slice(&std::fs::read(bin.with_extension("json")).unwrap()).unwrap();
            Stream {
                name: bin.file_stem().unwrap().to_string_lossy().into(),
                bytes: std::fs::read(&bin).unwrap(),
                cols: m["cols"].as_u64().unwrap() as u16,
                rows: m["rows"].as_u64().unwrap() as u16,
                chunks: m["chunks"].as_array().map(|a| a.iter().map(|v| v.as_u64().unwrap() as usize).collect()).unwrap_or_default(),
                resizes: m["resizes"]
                    .as_array()
                    .map(|a| a.iter().map(|r| (r["chunk"].as_u64().unwrap() as usize, r["cols"].as_u64().unwrap() as u16, r["rows"].as_u64().unwrap() as u16)).collect())
                    .unwrap_or_default(),
            }
        })
        .collect()
}

fn recorded(s: &Stream, snap: bool) -> u64 {
    let mut g = Ghostty::new(s.cols, s.rows);
    let mut off = 0;
    for (i, &n) in s.chunks.iter().enumerate() {
        for &(at, c, r) in &s.resizes {
            if at == i {
                g.resize(c, r);
            }
        }
        let end = (off + n).min(s.bytes.len());
        g.feed(&s.bytes[off..end]);
        if snap {
            g.snapshot();
        }
        off = end;
    }
    if off < s.bytes.len() {
        g.feed(&s.bytes[off..]);
    }
    g.sum
}

fn fixed(s: &Stream, chunk: usize) {
    let mut g = Ghostty::new(s.cols, s.rows);
    for c in s.bytes.chunks(chunk) {
        g.feed(c);
    }
    std::hint::black_box(&g.sum);
}

fn mbps(bytes: usize, f: &mut dyn FnMut()) -> f64 {
    f();
    let start = Instant::now();
    let mut n = 0usize;
    while start.elapsed() < Duration::from_millis(400) || n < 3 {
        f();
        n += 1;
    }
    (bytes * n) as f64 / start.elapsed().as_secs_f64() / 1e6
}

fn main() {
    let dir = std::env::args().nth(1).unwrap_or_else(|| concat!(env!("CARGO_MANIFEST_DIR"), "/../../testdata/pty").into());
    println!("{:<18} {:>8} {:>9} {:>9} {:>8} {:>8} {:>8} {:>8}", "stream", "bytes", "rec", "rec+snap", "1B", "64B", "4K", "all");
    let (mut tb, mut tt, mut ts) = (0usize, 0f64, 0f64);
    for s in load(&dir) {
        let len = s.bytes.len();
        let rec = mbps(len, &mut || {
            std::hint::black_box(recorded(&s, false));
        });
        let recs = mbps(len, &mut || {
            std::hint::black_box(recorded(&s, true));
        });
        let fx: Vec<f64> = [1usize, 64, 4096, usize::MAX].iter().map(|&c| mbps(len, &mut || fixed(&s, c.min(len.max(1))))).collect();
        println!("{:<18} {:>8} {:>9.1} {:>9.1} {:>8.1} {:>8.1} {:>8.1} {:>8.1}", s.name, len, rec, recs, fx[0], fx[1], fx[2], fx[3]);
        tb += len;
        tt += len as f64 / rec;
        ts += len as f64 / recs;
    }
    println!("all streams, recorded chunks: {:.1} MB/s feed, {:.1} MB/s feed+snapshot", tb as f64 / tt, tb as f64 / ts);
}
