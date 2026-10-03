//! Boundedness checks for the chrome host (scrollback, PTY queue, VT hold).
//!
//! These catch the class of leak that OOM'd macOS and froze Windows: a fast
//! child writing faster than the UI can paint, with nowhere for the bytes to
//! stop. They run as `cargo test -p suzuri-chrome --lib mem_bound`.

use crate::ansi::AnsiDecoder;
use crate::cells::{Cell, CellGrid, MAX_SCROLLBACK};
use crate::pty::PTY_QUEUE_CHUNKS;

/// Feed `n_lines` of 80-column output through the VT decoder into `grid`.
/// Returns the number of live + scrollback cells retained.
pub fn feed_line_storm(grid: &mut CellGrid, n_lines: usize) -> usize {
    let mut dec = AnsiDecoder::new();
    let mut line = Vec::with_capacity(84);
    for i in 0..n_lines {
        line.clear();
        line.extend_from_slice(b"storm-");
        line.extend_from_slice(itoa_bytes(i).as_slice());
        line.resize(80, b'x');
        line.push(b'\r');
        line.push(b'\n');
        dec.feed(grid, &line);
    }
    grid.scrollback_len() * grid.cols() as usize + grid.cells().len()
}

fn itoa_bytes(mut n: usize) -> Vec<u8> {
    if n == 0 {
        return vec![b'0'];
    }
    let mut tmp = [0u8; 24];
    let mut i = tmp.len();
    while n > 0 {
        i -= 1;
        tmp[i] = b'0' + (n % 10) as u8;
        n /= 10;
    }
    tmp[i..].to_vec()
}

/// Upper bound on cell-grid bytes after any amount of ordinary line output.
pub fn max_grid_bytes(cols: u16, rows: u16) -> usize {
    let cells = MAX_SCROLLBACK * cols as usize + cols as usize * rows as usize;
    cells * std::mem::size_of::<Cell>()
}

/// Upper bound on unread PTY bytes queued for one pane.
pub fn max_pty_queue_bytes() -> usize {
    PTY_QUEUE_CHUNKS * 8192
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::pty::{
        drain_chunks_capped, resize_debounce_elapsed, should_defer_conpty_resize, CONPTY_IO_QUIET,
        PTY_DRAIN_BYTES, RESIZE_DEBOUNCE,
    };
    use crate::sync_hold::{SyncHold, MAX_HOLD};
    use std::time::{Duration, Instant};

    #[test]
    fn line_storm_keeps_grid_under_cap() {
        let mut grid = CellGrid::new(80, 24);
        let cells = feed_line_storm(&mut grid, 80_000);
        assert!(
            grid.scrollback_len() <= MAX_SCROLLBACK,
            "scrollback {}",
            grid.scrollback_len()
        );
        let bytes = cells * std::mem::size_of::<Cell>();
        assert!(
            bytes <= max_grid_bytes(80, 24),
            "retained {bytes} bytes after 80k lines"
        );
    }

    #[test]
    fn pty_queue_and_drain_caps_are_small() {
        assert!(max_pty_queue_bytes() <= 2 * 1024 * 1024);
        assert!(PTY_DRAIN_BYTES <= 512 * 1024);
        assert!(PTY_QUEUE_CHUNKS >= 8);
    }

    #[test]
    fn drain_helper_does_not_eat_the_rest() {
        let mut q: Vec<Vec<u8>> = (0..8).map(|i| vec![i; 100]).collect();
        let mut out = Vec::new();
        drain_chunks_capped(&mut q, &mut out, 250);
        assert_eq!(out.len(), 300);
        assert_eq!(q.len(), 5);
        let mut out2 = Vec::new();
        drain_chunks_capped(&mut q, &mut out2, PTY_DRAIN_BYTES);
        assert_eq!(out2.len(), 500);
        assert!(q.is_empty());
    }

    #[test]
    fn windows_hot_io_defers_native_resize() {
        let now = Instant::now();
        assert!(should_defer_conpty_resize(
            true,
            Some(now - Duration::from_millis(5)),
            now
        ));
        assert!(!should_defer_conpty_resize(
            true,
            Some(now - CONPTY_IO_QUIET - Duration::from_millis(5)),
            now
        ));
        assert!(!should_defer_conpty_resize(
            false,
            Some(now - Duration::from_millis(5)),
            now
        ));
        assert!(!resize_debounce_elapsed(now, now));
        assert!(resize_debounce_elapsed(
            now,
            now + RESIZE_DEBOUNCE + Duration::from_millis(1)
        ));
    }

    #[test]
    fn sync_hold_cannot_buffer_more_than_max() {
        let mut hold = SyncHold::new();
        hold.set_coalesce(true);
        let t0 = Instant::now();
        let chunk = vec![b'Z'; 100 * 1024];
        for _ in 0..40 {
            hold.feed_at(&chunk, t0);
            assert!(hold.pending_len() <= MAX_HOLD);
        }
    }

    #[test]
    fn alt_screen_storm_does_not_grow_scrollback() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(80, 24);
        dec.feed(&mut grid, b"\x1b[?1049h");
        assert!(grid.suppress_scrollback);
        let cells = feed_line_storm(&mut grid, 20_000);
        assert_eq!(grid.scrollback_len(), 0);
        assert!(cells <= 80 * 24);
    }
}
