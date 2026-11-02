//! fakeagent: a reproducible stand-in for a Claude Code pane, after marble's
//! #251 design. It never touches a network or a model.
//!
//! - A 6-line live region (spinner, input box, footer) redrawn every 80 ms
//!   with cursor-up plus CSI 2K, each frame inside DEC 2026.
//! - An OSC 0 spinner title every 5th frame.
//! - Bursts every 2-9 s: 20-1500 lines of diff/syntax-coloured output in
//!   synchronized chunks of 50-450 lines, with wide CJK and emoji.
//! - A full CSI 2J 3J H repaint on resize and about every 90 s.
//! - Raw-mode stdin drawn into the input box at once (input-first), so an
//!   echo latency probe sees its key in the next frame.
//!
//! fakeagent [--seed N] [--quiet] [--quiet-first <lockfile>]
//!   --quiet: no bursts, live region only (the latency probe's pane)
//!   --quiet-first: only the first instance to create <lockfile> is quiet,
//!     so one command line can serve every pane of a bench
//! Without --seed the seed comes from the pid.

use std::io::{Read, Write};
use std::sync::mpsc;
use std::time::{Duration, Instant};

const SPIN: [&str; 6] = ["·", "✢", "✳", "✶", "✻", "✽"];
const WORDS: [&str; 16] = [
    "fn", "let", "match", "impl", "pane", "frame", "resize", "grid", "cursor", "scroll", "damage", "actor", "reader", "→", "硯", "墨",
];
const EMOJI: [&str; 4] = ["✅", "🔧", "📦", "🧪"];

struct Rng(u64);
impl Rng {
    fn next(&mut self) -> u64 {
        self.0 ^= self.0 << 13;
        self.0 ^= self.0 >> 7;
        self.0 ^= self.0 << 17;
        self.0
    }
    fn range(&mut self, lo: u64, hi: u64) -> u64 {
        lo + self.next() % (hi - lo + 1)
    }
}

struct Agent {
    rng: Rng,
    out: std::io::Stdout,
    input: String,
    frame: u64,
    live_drawn: bool,
    cols: u16,
    rows: u16,
    lines_out: u64,
}

impl Agent {
    fn w(&mut self, s: &str) {
        let _ = self.out.write_all(s.as_bytes());
    }

    fn flush(&mut self) {
        let _ = self.out.flush();
    }

    /// The cursor sits in the input box (region line 2). Move to the
    /// region's top line and clear the region.
    fn erase_live(&mut self) {
        if self.live_drawn {
            self.w("\r\x1b[2A");
            for i in 0..5 {
                self.w("\x1b[2K");
                if i < 4 {
                    self.w("\x1b[1B");
                }
            }
            self.w("\r\x1b[4A");
            self.live_drawn = false;
        }
    }

    /// Draw the region from the cursor's line; leave the cursor in the input box.
    fn draw_live(&mut self) {
        let width = (self.cols as usize).saturating_sub(3).clamp(10, 200);
        let spin = SPIN[(self.frame % SPIN.len() as u64) as usize];
        let bar = "─".repeat(width);
        let mut input = self.input.clone();
        let max = width.saturating_sub(4);
        if input.chars().count() > max {
            input = input.chars().skip(input.chars().count() - max).collect();
        }
        let secs = self.frame * 80 / 1000;
        let lines = [
            {
                // Never wider than the pane: a wrapped line would break the
                // cursor-up arithmetic of the next redraw.
                let msg = format!("Synthesizing… ({secs}s · ↓ {} lines · esc to interrupt)", self.lines_out);
                let msg: String = msg.chars().take(width.saturating_sub(2)).collect();
                format!("\x1b[38;2;215;119;87m{spin}\x1b[0m {msg}")
            },
            format!("\x1b[2m╭{bar}╮\x1b[0m"),
            format!("\x1b[2m│\x1b[0m > {input}"),
            format!("\x1b[2m╰{bar}╯\x1b[0m"),
            "  \x1b[2m? for shortcuts\x1b[0m".chars().take(width + 10).collect(),
        ];
        for (i, l) in lines.iter().enumerate() {
            self.w("\r\x1b[2K");
            self.w(l);
            if i < 4 {
                self.w("\r\n");
            }
        }
        let col = 4 + input.chars().count();
        self.w(&format!("\r\x1b[2A\x1b[{col}C"));
        self.live_drawn = true;
    }

    fn live_frame(&mut self) {
        self.w("\x1b[?2026h");
        self.erase_live();
        self.draw_live();
        if self.frame.is_multiple_of(5) {
            let spin = SPIN[(self.frame % SPIN.len() as u64) as usize];
            self.w(&format!("\x1b]0;{spin} fakeagent\x07"));
        }
        self.w("\x1b[?2026l");
        self.flush();
    }

    fn content_line(&mut self) -> String {
        let kind = self.rng.range(0, 9);
        let n = self.rng.range(3, 14);
        let mut words = Vec::new();
        for _ in 0..n {
            let w = WORDS[self.rng.range(0, WORDS.len() as u64 - 1) as usize];
            words.push(w.to_string());
        }
        if self.rng.range(0, 30) == 0 {
            words.push(EMOJI[self.rng.range(0, 3) as usize].to_string());
        }
        let text = words.join(" ");
        let ln = self.lines_out % 9999;
        match kind {
            0 | 1 => format!("\x1b[48;2;34;60;40m\x1b[38;2;140;200;150m{ln:>5} +  {text}\x1b[K\x1b[0m"),
            2 => format!("\x1b[48;2;70;34;38m\x1b[38;2;220;150;150m{ln:>5} -  {text}\x1b[K\x1b[0m"),
            3 => format!("\x1b[1m\x1b[38;5;141m● Update\x1b[0m(\x1b[38;5;110msrc/{}.rs\x1b[0m)", WORDS[(ln % 12) as usize]),
            _ => format!("\x1b[2m{ln:>5}\x1b[0m    \x1b[38;5;{}m{text}\x1b[0m", 100 + (ln % 120)),
        }
    }

    fn burst(&mut self) {
        let total = self.rng.range(20, 1500);
        let mut left = total;
        while left > 0 {
            let chunk = self.rng.range(50, 450).min(left);
            left -= chunk;
            self.w("\x1b[?2026h");
            self.erase_live();
            for _ in 0..chunk {
                let l = self.content_line();
                self.w(&l);
                self.w("\r\n");
                self.lines_out += 1;
            }
            self.draw_live();
            self.w("\x1b[?2026l");
            self.flush();
            std::thread::sleep(Duration::from_millis(12));
        }
    }

    fn full_repaint(&mut self) {
        self.w("\x1b[?2026h\x1b[2J\x1b[3J\x1b[H");
        self.live_drawn = false;
        let rows = self.rows.saturating_sub(7) as usize;
        for _ in 0..rows.min(40) {
            let l = self.content_line();
            self.w(&l);
            self.w("\r\n");
        }
        self.draw_live();
        self.w("\x1b[?2026l");
        self.flush();
    }
}

fn main() {
    let mut seed = 0x9e37_79b9_7f4a_7c15u64 ^ (std::process::id() as u64).wrapping_mul(0x2545_f491_4f6c_dd1d);
    let mut quiet = false;
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "--seed" => seed ^= args.next().and_then(|s| s.parse::<u64>().ok()).unwrap_or(1).wrapping_mul(0x2545_f491_4f6c_dd1d),
            "--quiet" => quiet = true,
            "--quiet-first" => {
                if let Some(lock) = args.next() {
                    quiet |= std::fs::OpenOptions::new().write(true).create_new(true).open(lock).is_ok();
                }
            }
            _ => {}
        }
    }
    let _ = crossterm::terminal::enable_raw_mode();
    let (cols, rows) = crossterm::terminal::size().unwrap_or((80, 24));
    let mut a = Agent { rng: Rng(seed | 1), out: std::io::stdout(), input: String::new(), frame: 0, live_drawn: false, cols, rows, lines_out: 0 };

    let (tx, rx) = mpsc::channel::<Vec<u8>>();
    std::thread::spawn(move || {
        let mut stdin = std::io::stdin();
        let mut buf = [0u8; 256];
        while let Ok(n) = stdin.read(&mut buf) {
            if n == 0 || tx.send(buf[..n].to_vec()).is_err() {
                break;
            }
        }
    });

    a.full_repaint();
    let mut next_burst = Instant::now() + Duration::from_millis(a.rng.range(2000, 9000));
    let mut next_repaint = Instant::now() + Duration::from_secs(90);
    let mut next_frame = Instant::now();
    loop {
        let wait = next_frame.saturating_duration_since(Instant::now());
        match rx.recv_timeout(wait) {
            Ok(bytes) => {
                for b in bytes {
                    match b {
                        0x03 | 0x04 => {
                            let _ = crossterm::terminal::disable_raw_mode();
                            print!("\r\n");
                            return;
                        }
                        0x7f | 0x08 => {
                            a.input.pop();
                        }
                        b'\r' => a.input.clear(),
                        0x20..=0x7e => a.input.push(b as char),
                        _ => {}
                    }
                }
                // Input-first: echo now, don't wait for the 80 ms tick.
                a.live_frame();
                continue;
            }
            Err(mpsc::RecvTimeoutError::Disconnected) => return,
            Err(mpsc::RecvTimeoutError::Timeout) => {}
        }
        next_frame += Duration::from_millis(80);
        a.frame += 1;
        if let Ok((c, r)) = crossterm::terminal::size()
            && (c, r) != (a.cols, a.rows)
        {
            a.cols = c;
            a.rows = r;
            a.full_repaint();
            continue;
        }
        let now = Instant::now();
        if !quiet && now >= next_burst {
            a.burst();
            next_burst = Instant::now() + Duration::from_millis(a.rng.range(2000, 9000));
        } else if !quiet && now >= next_repaint {
            a.full_repaint();
            next_repaint = now + Duration::from_secs(90);
        } else {
            a.live_frame();
        }
    }
}
