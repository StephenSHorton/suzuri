//! Encode PTY mouse reports for alt-screen TUIs (Grok, vim, less, …).
//!
//! Product parity with classic `internal/ui/mouse_pty.go`:
//! - mouse tracking + SGR → xterm wheel buttons 64/65
//! - otherwise → arrow keys (alternate-scroll), app-cursor when DECSET 1

/// Wheel away from the user (older content / list up). Positive `steps`.
const WHEEL_UP: u16 = 64;
/// Wheel toward the user. Negative `steps`.
const WHEEL_DOWN: u16 = 65;

/// Build PTY bytes for a wheel gesture.
///
/// `col` / `row` are 1-based cell coordinates. `steps > 0` is wheel-away
/// (scroll up); `steps < 0` is toward (scroll down).
pub fn encode_mouse_wheel(
    col: u16,
    row: u16,
    steps: i32,
    tracking: bool,
    sgr: bool,
    app_cursor: bool,
) -> Vec<u8> {
    if steps == 0 {
        return Vec::new();
    }
    let col = col.max(1);
    let row = row.max(1);
    let n = steps.unsigned_abs().min(32) as usize;

    if tracking && sgr {
        let btn = if steps > 0 { WHEEL_UP } else { WHEEL_DOWN };
        let one = format!("\x1b[<{btn};{col};{row}M");
        return one.repeat(n).into_bytes();
    }

    let seq: &[u8] = if steps > 0 {
        if app_cursor {
            b"\x1bOA"
        } else {
            b"\x1b[A"
        }
    } else if app_cursor {
        b"\x1bOB"
    } else {
        b"\x1b[B"
    };
    seq.repeat(n)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn arrows_when_no_mouse() {
        let up = encode_mouse_wheel(1, 1, 2, false, false, false);
        assert_eq!(up, b"\x1b[A\x1b[A");
        let down = encode_mouse_wheel(1, 1, -3, false, false, false);
        assert_eq!(down, b"\x1b[B\x1b[B\x1b[B");
        assert!(encode_mouse_wheel(1, 1, 0, false, false, false).is_empty());
    }

    #[test]
    fn app_cursor_arrows() {
        let up = encode_mouse_wheel(1, 1, 1, false, false, true);
        assert_eq!(up, b"\x1bOA");
        let down = encode_mouse_wheel(1, 1, -1, false, false, true);
        assert_eq!(down, b"\x1bOB");
    }

    #[test]
    fn sgr_wheel_buttons() {
        let up = encode_mouse_wheel(10, 5, 1, true, true, false);
        assert_eq!(up, b"\x1b[<64;10;5M");
        let down = encode_mouse_wheel(3, 7, -1, true, true, false);
        assert_eq!(down, b"\x1b[<65;3;7M");
    }

    #[test]
    fn cap_fling() {
        let out = encode_mouse_wheel(1, 1, 100, false, false, false);
        assert_eq!(out.len(), 32 * b"\x1b[A".len());
    }
}
