//! DEC synchronized output (`CSI ?2026 h/l`) hold + ConPTY burst coalesce.
//!
//! Grok wraps frames in CSI ?2026, but Windows ConPTY typically **strips**
//! those modes. The cell diff still arrives as many small reads. Feeding the
//! grid on every read paints a half-rewritten slash menu onto default-bg cells
//! (rain shows through the holes).
//!
//! 1. If 2026 survives, hold until ESU (or 64ms).
//! 2. On alt-screen (`set_coalesce`), also hold ordinary bursts until the PTY
//!    is quiet for [`BURST_QUIET`] (or [`BURST_MAX`]).

use std::time::{Duration, Instant};

/// Flush a 2026 hold if ESU never arrives (ConPTY drop).
pub const DEFAULT_TIMEOUT: Duration = Duration::from_millis(64);
/// After the last alt-screen byte, wait one vsync so ConPTY's intra-frame
/// gaps (often >8ms) do not flush a torn Grok menu.
pub const BURST_QUIET: Duration = Duration::from_millis(16);
/// Cap so a continuous Grok stream still updates (~2-3 frames).
pub const BURST_MAX: Duration = Duration::from_millis(48);
const MAX_HOLD: usize = 1 << 20;

/// Bytes that are safe to write into the VT grid.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct Release {
    pub bytes: Vec<u8>,
    /// True when this release closed a 2026 frame (or a stale/overflow flush).
    pub sync_frame: bool,
}

#[derive(Debug)]
pub struct SyncHold {
    pending: Vec<u8>,
    open: bool,
    open_at: Option<Instant>,
    timeout: Duration,
    /// Hold non-2026 bursts (alt-screen / ConPTY split reads).
    coalesce: bool,
    last_at: Option<Instant>,
    burst_at: Option<Instant>,
}

impl Default for SyncHold {
    fn default() -> Self {
        Self {
            pending: Vec::new(),
            open: false,
            open_at: None,
            timeout: DEFAULT_TIMEOUT,
            coalesce: false,
            last_at: None,
            burst_at: None,
        }
    }
}

impl SyncHold {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn holding(&self) -> bool {
        self.open
    }

    pub fn has_pending(&self) -> bool {
        !self.pending.is_empty()
    }

    pub fn set_coalesce(&mut self, on: bool) {
        self.coalesce = on;
    }

    pub fn feed(&mut self, p: &[u8]) -> Release {
        self.feed_at(p, Instant::now())
    }

    pub fn feed_at(&mut self, p: &[u8], now: Instant) -> Release {
        if !p.is_empty() {
            if self.pending.is_empty() {
                self.burst_at = Some(now);
            }
            self.pending.extend_from_slice(p);
            self.last_at = Some(now);
        }
        let force = self.should_force(now);
        self.drain(force)
    }

    pub fn flush_if_stale(&mut self, now: Instant) -> Release {
        if !self.should_force(now) {
            return Release::default();
        }
        self.drain(true)
    }

    /// Drop hold state. Held payload is discarded (alt-screen leave).
    pub fn reset(&mut self) {
        self.pending.clear();
        self.open = false;
        self.open_at = None;
        self.last_at = None;
        self.burst_at = None;
    }

    fn should_force(&self, now: Instant) -> bool {
        if self.open {
            return self.open_at
                .is_some_and(|t| now.saturating_duration_since(t) >= self.timeout);
        }
        if !self.coalesce || self.pending.is_empty() {
            return false;
        }
        if self
            .last_at
            .is_some_and(|t| now.saturating_duration_since(t) >= BURST_QUIET)
        {
            return true;
        }
        self.burst_at
            .is_some_and(|t| now.saturating_duration_since(t) >= BURST_MAX)
    }

    fn drain(&mut self, mut force: bool) -> Release {
        if self.open && self.pending.len() > MAX_HOLD {
            force = true;
        }
        let mut out = Vec::new();
        let mut sync_frame = false;
        loop {
            if !self.open && !self.coalesce {
                force = true;
            }
            if self.pending.is_empty() {
                break;
            }
            let found = find_sync_csi(&self.pending);
            if !self.open {
                match found {
                    SyncFind::Incomplete { idx } => {
                        if idx > 0 && force {
                            out.extend_from_slice(&self.pending[..idx]);
                            self.pending.drain(..idx);
                        }
                        break;
                    }
                    SyncFind::None => {
                        if !force {
                            break;
                        }
                        out.extend_from_slice(&self.pending);
                        self.pending.clear();
                        break;
                    }
                    SyncFind::Hit { idx, len, set } => {
                        if idx > 0 {
                            out.extend_from_slice(&self.pending[..idx]);
                        }
                        self.pending.drain(..idx + len);
                        if set {
                            self.open = true;
                            self.open_at = Some(Instant::now());
                            // Pass-through force must not dump this frame's payload.
                            force = false;
                            continue;
                        }
                        // Orphan ESU — drop.
                    }
                }
                continue;
            }
            // Inside a hold.
            match found {
                SyncFind::Incomplete { idx } => {
                    if force && idx > 0 {
                        out.extend_from_slice(&self.pending[..idx]);
                        self.pending.drain(..idx);
                        sync_frame = true;
                        self.open = false;
                        self.open_at = None;
                    }
                    break;
                }
                SyncFind::None => {
                    if force {
                        out.extend_from_slice(&self.pending);
                        self.pending.clear();
                        sync_frame = true;
                        self.open = false;
                        self.open_at = None;
                    }
                    break;
                }
                SyncFind::Hit { idx, len, set } => {
                    if set {
                        // Nested BSU: drop CSI, keep payload on either side.
                        self.pending.drain(idx..idx + len);
                        continue;
                    }
                    out.extend_from_slice(&self.pending[..idx]);
                    self.pending.drain(..idx + len);
                    self.open = false;
                    self.open_at = None;
                    sync_frame = true;
                }
            }
        }
        if self.pending.is_empty() {
            self.last_at = None;
            self.burst_at = None;
        }
        Release {
            bytes: out,
            sync_frame,
        }
    }
}

#[derive(Debug)]
enum SyncFind {
    None,
    Incomplete { idx: usize },
    Hit { idx: usize, len: usize, set: bool },
}

fn find_sync_csi(p: &[u8]) -> SyncFind {
    let mut i = 0;
    while i < p.len() {
        if p[i] != 0x1b {
            i += 1;
            continue;
        }
        match seq_len(&p[i..]) {
            SeqLen::Incomplete => return SyncFind::Incomplete { idx: i },
            SeqLen::Complete(n) => {
                if let Some(set) = dec_2026(&p[i..i + n]) {
                    return SyncFind::Hit {
                        idx: i,
                        len: n,
                        set,
                    };
                }
                i += n;
            }
        }
    }
    SyncFind::None
}

enum SeqLen {
    Incomplete,
    Complete(usize),
}

fn seq_len(p: &[u8]) -> SeqLen {
    if p.is_empty() {
        return SeqLen::Incomplete;
    }
    if p[0] != 0x1b {
        return SeqLen::Complete(1);
    }
    if p.len() < 2 {
        return SeqLen::Incomplete;
    }
    match p[1] {
        b'[' => {
            let mut i = 2;
            while i < p.len() {
                if (0x40..=0x7e).contains(&p[i]) {
                    return SeqLen::Complete(i + 1);
                }
                i += 1;
                if i > 256 {
                    return SeqLen::Complete(i);
                }
            }
            SeqLen::Incomplete
        }
        b']' => osc_or_string(p, false),
        b'P' | b'X' | b'^' | b'_' => osc_or_string(p, true),
        _ => SeqLen::Complete(2),
    }
}

fn osc_or_string(p: &[u8], _string: bool) -> SeqLen {
    let mut i = 2;
    while i < p.len() {
        if p[i] == 0x07 {
            return SeqLen::Complete(i + 1);
        }
        if p[i] == 0x1b {
            if i + 1 >= p.len() {
                return SeqLen::Incomplete;
            }
            if p[i + 1] == b'\\' {
                return SeqLen::Complete(i + 2);
            }
        }
        i += 1;
    }
    SeqLen::Incomplete
}

/// `Some(true)` = BSU (`h`), `Some(false)` = ESU (`l`).
fn dec_2026(seq: &[u8]) -> Option<bool> {
    if seq.len() < 5 || seq[0] != 0x1b || seq[1] != b'[' {
        return None;
    }
    let final_b = seq[seq.len() - 1];
    if final_b != b'h' && final_b != b'l' {
        return None;
    }
    let body = &seq[2..seq.len() - 1];
    if body.is_empty() || body[0] != b'?' {
        return None;
    }
    if !args_contain_2026(&body[1..]) {
        return None;
    }
    Some(final_b == b'h')
}

fn args_contain_2026(body: &[u8]) -> bool {
    let mut start = 0;
    for i in 0..=body.len() {
        if i < body.len() && body[i] != b';' {
            continue;
        }
        if i > start && atoi_exact(&body[start..i]) == 2026 {
            return true;
        }
        start = i + 1;
    }
    false
}

fn atoi_exact(b: &[u8]) -> i32 {
    if b.is_empty() {
        return -1;
    }
    let mut n = 0i32;
    for &c in b {
        if !c.is_ascii_digit() {
            return -1;
        }
        n = n.saturating_mul(10).saturating_add((c - b'0') as i32);
        if n > 99_999 {
            return -1;
        }
    }
    n
}

#[cfg(test)]
mod tests {
    use super::*;

    const BSU: &[u8] = b"\x1b[?2026h";
    const ESU: &[u8] = b"\x1b[?2026l";

    #[test]
    fn pass_through() {
        let mut s = SyncHold::new();
        let r = s.feed(b"hello \x1b[31mred\x1b[0m");
        assert!(!r.sync_frame && !s.holding());
        assert_eq!(r.bytes, b"hello \x1b[31mred\x1b[0m");
    }

    #[test]
    fn atomic_frame() {
        let mut s = SyncHold::new();
        let mut inb = b"pre".to_vec();
        inb.extend_from_slice(BSU);
        inb.extend_from_slice(b"MENU");
        inb.extend_from_slice(ESU);
        inb.extend_from_slice(b"post");
        let r = s.feed(&inb);
        assert!(r.sync_frame && !s.holding());
        assert_eq!(r.bytes, b"preMENUpost");
    }

    #[test]
    fn split_across_chunks() {
        let mut s = SyncHold::new();
        let mut first = b"aa".to_vec();
        first.extend_from_slice(&BSU[..3]);
        let r = s.feed(&first);
        assert_eq!(r.bytes, b"aa");
        assert!(!r.sync_frame && !s.holding());

        let mut mid = BSU[3..].to_vec();
        mid.extend_from_slice(b"XY");
        let r = s.feed(&mid);
        assert!(s.holding());
        assert!(!r.sync_frame);
        assert!(r.bytes.is_empty());

        let mut last = b"Z".to_vec();
        last.extend_from_slice(ESU);
        last.extend_from_slice(b"!");
        let r = s.feed(&last);
        assert!(r.sync_frame && !s.holding());
        assert_eq!(r.bytes, b"XYZ!");
    }

    #[test]
    fn split_csi_final() {
        let mut s = SyncHold::new();
        let mut first = BSU.to_vec();
        first.extend_from_slice(b"AB");
        s.feed(&first);
        assert!(s.holding());
        let r = s.feed(b"\x1b[?2026");
        assert!(r.bytes.is_empty() && !r.sync_frame);
        let mut last = b"l".to_vec();
        last.extend_from_slice(b"CD");
        let r = s.feed(&last);
        assert!(r.sync_frame);
        assert_eq!(r.bytes, b"ABCD");
    }

    #[test]
    fn flush_if_stale() {
        let mut s = SyncHold::new();
        s.timeout = Duration::from_millis(10);
        s.feed(b"\x1b[?2026hSTUCK");
        assert!(s.holding());
        let t0 = s.open_at.unwrap();
        let r = s.flush_if_stale(t0 + Duration::from_millis(1));
        assert!(r.bytes.is_empty() && !r.sync_frame);
        let r = s.flush_if_stale(t0 + Duration::from_millis(20));
        assert!(r.sync_frame && !s.holding());
        assert_eq!(r.bytes, b"STUCK");
    }

    #[test]
    fn extra_bsu_dropped() {
        let mut s = SyncHold::new();
        let mut inb = BSU.to_vec();
        inb.extend_from_slice(b"A");
        inb.extend_from_slice(BSU);
        inb.extend_from_slice(b"B");
        inb.extend_from_slice(ESU);
        let r = s.feed(&inb);
        assert!(r.sync_frame);
        assert_eq!(r.bytes, b"AB");
    }

    #[test]
    fn orphan_esu_dropped() {
        let mut s = SyncHold::new();
        let mut inb = b"x".to_vec();
        inb.extend_from_slice(ESU);
        inb.extend_from_slice(b"y");
        let r = s.feed(&inb);
        assert!(!r.sync_frame && !s.holding());
        assert_eq!(r.bytes, b"xy");
    }

    #[test]
    fn combined_modes() {
        let mut s = SyncHold::new();
        let r = s.feed(b"\x1b[?2026;25hPAY\x1b[?2026;25l");
        assert!(r.sync_frame);
        assert_eq!(r.bytes, b"PAY");
    }

    #[test]
    fn reset_discards() {
        let mut s = SyncHold::new();
        s.feed(b"\x1b[?2026hNOPE");
        s.reset();
        assert!(!s.holding());
        let r = s.feed(b"ok");
        assert!(!r.sync_frame);
        assert_eq!(r.bytes, b"ok");
    }

    #[test]
    fn keeps_other_csi() {
        let mut s = SyncHold::new();
        let inb = b"\x1b[2J\x1b[Hhi";
        let r = s.feed(inb);
        assert_eq!(r.bytes, inb);
    }

    #[test]
    fn coalesce_holds_until_quiet() {
        let mut s = SyncHold::new();
        s.set_coalesce(true);
        let t0 = Instant::now();
        let r = s.feed_at(b"MENU", t0);
        assert!(r.bytes.is_empty(), "burst must not paint mid-read");
        assert!(s.has_pending());
        let r = s.flush_if_stale(t0 + Duration::from_millis(1));
        assert!(r.bytes.is_empty());
        let r = s.flush_if_stale(t0 + BURST_QUIET);
        assert_eq!(r.bytes, b"MENU");
        assert!(!s.has_pending());
    }

    #[test]
    fn coalesce_max_flushes_long_burst() {
        let mut s = SyncHold::new();
        s.set_coalesce(true);
        let t0 = Instant::now();
        s.feed_at(b"A", t0);
        s.feed_at(b"B", t0 + Duration::from_millis(5));
        let r = s.flush_if_stale(t0 + BURST_MAX);
        assert_eq!(r.bytes, b"AB");
    }

    #[test]
    fn hold_hides_menu_until_esu() {
        use crate::ansi::AnsiDecoder;
        use crate::cells::CellGrid;

        let mut hold = SyncHold::new();
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 3);

        // Frame 1: paint MENU
        let mut f1 = BSU.to_vec();
        f1.extend_from_slice(b"\x1b[H\x1b[7mMENU\x1b[0m");
        f1.extend_from_slice(ESU);
        let r = hold.feed(&f1);
        dec.feed(&mut grid, &r.bytes);
        let snap = grid.snapshot_strings();
        assert!(snap[0].starts_with("MENU"), "{:?}", snap[0]);

        // Frame 2 split: BSU + clear + new text, ESU later.
        let mut f2a = BSU.to_vec();
        f2a.extend_from_slice(b"\x1b[H\x1b[2KOK");
        let r = hold.feed(&f2a);
        assert!(hold.holding());
        assert!(r.bytes.is_empty());
        let snap = grid.snapshot_strings();
        assert!(
            snap[0].starts_with("MENU"),
            "grid must stay on last complete frame, got {:?}",
            snap[0]
        );

        let r = hold.feed(ESU);
        assert!(r.sync_frame);
        dec.feed(&mut grid, &r.bytes);
        let snap = grid.snapshot_strings();
        assert!(snap[0].starts_with("OK"), "{:?}", snap[0]);
        assert!(!snap[0].contains("MENU"), "{:?}", snap[0]);
    }
}
