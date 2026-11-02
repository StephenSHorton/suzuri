//! Replay-corpus bench: feed each recorded PTY stream (testdata/pty, from
//! suzuri #249) through the VT engine and report throughput.
//!
//!   cargo run --release -p rsz-term --example replay [dir]
//!
//! Per stream:
//! - screen: does the final screen text match the vt10x golden's screen?
//! - rec: feed in the recorded read chunks, resizes applied where recorded;
//! - rec+snap: the same plus a dirty-rows snapshot after every chunk (a
//!   frame per read, the worst case for the frame thread);
//! - 1B / 64B / 4K / all: feed in fixed chunk sizes (resizes skipped).
//! Throughput is MB/s of PTY bytes; each case repeats until ~0.4 s.
use std::time::{Duration, Instant};

use rsz_term::{Alacritty, Engine};

struct Stream {
    name: String,
    bytes: Vec<u8>,
    cols: u16,
    rows: u16,
    chunks: Vec<usize>,
    resizes: Vec<(usize, u16, u16)>, // before chunk i
    golden: Option<String>,
}

fn load(dir: &str) -> Vec<Stream> {
    let mut out = Vec::new();
    let mut names: Vec<_> = std::fs::read_dir(dir)
        .expect("corpus dir")
        .filter_map(|e| e.ok())
        .map(|e| e.path())
        .filter(|p| p.extension().is_some_and(|x| x == "bin"))
        .collect();
    names.sort();
    for bin in names {
        let meta: serde_json::Value = serde_json::from_slice(&std::fs::read(bin.with_extension("json")).unwrap()).unwrap();
        let chunks = meta["chunks"].as_array().map(|a| a.iter().map(|v| v.as_u64().unwrap() as usize).collect()).unwrap_or_default();
        let resizes = meta["resizes"]
            .as_array()
            .map(|a| a.iter().map(|r| (r["chunk"].as_u64().unwrap() as usize, r["cols"].as_u64().unwrap() as u16, r["rows"].as_u64().unwrap() as u16)).collect())
            .unwrap_or_default();
        let golden = std::fs::read_to_string(bin.with_extension("golden")).ok().map(|g| {
            // "== screen WxH ... ==" then lines until "== scrollback".
            g.lines().skip(1).take_while(|l| !l.starts_with("== scrollback")).map(|l| l.trim_end()).collect::<Vec<_>>().join("\n")
        });
        out.push(Stream {
            name: bin.file_stem().unwrap().to_string_lossy().into(),
            bytes: std::fs::read(&bin).unwrap(),
            cols: meta["cols"].as_u64().unwrap() as u16,
            rows: meta["rows"].as_u64().unwrap() as u16,
            chunks,
            resizes,
            golden,
        });
    }
    out
}

fn feed_recorded(s: &Stream, snap: bool) -> Alacritty {
    let mut e = Alacritty::new(s.cols, s.rows, 10_000);
    let mut ev = Vec::new();
    let mut frame = Vec::with_capacity(64 * 1024);
    let mut off = 0;
    for (i, &n) in s.chunks.iter().enumerate() {
        for &(at, c, r) in &s.resizes {
            if at == i {
                e.resize(c, r);
            }
        }
        let end = (off + n).min(s.bytes.len());
        e.feed(&s.bytes[off..end], &mut ev);
        ev.clear();
        if snap {
            e.snapshot(&mut frame, i as u32, false);
        }
        off = end;
    }
    if off < s.bytes.len() {
        e.feed(&s.bytes[off..], &mut ev);
    }
    // Close any synchronized update the stream left open.
    if e.sync_deadline().is_some() {
        e.stop_sync(&mut ev);
    }
    e
}

fn feed_fixed(s: &Stream, chunk: usize) {
    let mut e = Alacritty::new(s.cols, s.rows, 10_000);
    let mut ev = Vec::new();
    for c in s.bytes.chunks(chunk) {
        e.feed(c, &mut ev);
        ev.clear();
    }
    std::hint::black_box(&e);
}

fn mbps(bytes: usize, f: &mut dyn FnMut()) -> f64 {
    f(); // warm
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
    let streams = load(&dir);
    println!("{:<18} {:>8} {:>7} {:>9} {:>9} {:>8} {:>8} {:>8} {:>8}", "stream", "bytes", "screen", "rec", "rec+snap", "1B", "64B", "4K", "all");
    let (mut tot_b, mut tot_t, mut tot_ts) = (0usize, 0f64, 0f64);
    for s in &streams {
        let e = feed_recorded(s, false);
        // Claude Code writes U+00A0 where the vt10x goldens show a space.
        let screen = e.screen_text().replace('\u{a0}', " ");
        let matches = match &s.golden {
            Some(g) => {
                let ours: Vec<&str> = screen.lines().collect();
                let gold: Vec<&str> = g.lines().collect();
                let n = gold.len().max(ours.len());
                let same = (0..n).filter(|&i| ours.get(i).copied().unwrap_or("") == gold.get(i).copied().unwrap_or("")).count();
                if same == n { "same".to_string() } else { format!("{same}/{n}") }
            }
            None => "-".into(),
        };
        let len = s.bytes.len();
        let rec = mbps(len, &mut || {
            std::hint::black_box(feed_recorded(s, false));
        });
        let recs = mbps(len, &mut || {
            std::hint::black_box(feed_recorded(s, true));
        });
        let fixed: Vec<f64> = [1usize, 64, 4096, usize::MAX].iter().map(|&c| mbps(len, &mut || feed_fixed(s, c.min(len.max(1))))).collect();
        println!(
            "{:<18} {:>8} {:>7} {:>9.1} {:>9.1} {:>8.1} {:>8.1} {:>8.1} {:>8.1}",
            s.name, len, matches, rec, recs, fixed[0], fixed[1], fixed[2], fixed[3]
        );
        tot_b += len;
        tot_t += len as f64 / rec;
        tot_ts += len as f64 / recs;
        if std::env::var_os("REPLAY_DIFF").is_some() && matches != "same" {
            if let Some(g) = &s.golden {
                for (i, (a, b)) in screen.lines().zip(g.lines()).enumerate() {
                    if a != b {
                        println!("  {i:>3} rsz : {a}\n  {i:>3} gold: {b}");
                    }
                }
            }
        }
    }
    println!("all streams, recorded chunks: {:.1} MB/s feed, {:.1} MB/s feed+snapshot", tot_b as f64 / tot_t, tot_b as f64 / tot_ts);
}
