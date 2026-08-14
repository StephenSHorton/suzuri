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

/// Left=0, middle=1, right=2. Press = SGR `M`, release = `m`.
pub fn encode_mouse_button(
    col: u16,
    row: u16,
    btn: u16,
    press: bool,
    tracking: bool,
    sgr: bool,
) -> Vec<u8> {
    if !tracking {
        return Vec::new();
    }
    let col = col.max(1);
    let row = row.max(1);
    if sgr {
        let end = if press { 'M' } else { 'm' };
        return format!("\x1b[<{btn};{col};{row}{end}").into_bytes();
    }
    if !press {
        return Vec::new();
    }
    let cb = 32u16.saturating_add(btn).min(255) as u8;
    let cx = 32u16.saturating_add(col).min(255) as u8;
    let cy = 32u16.saturating_add(row).min(255) as u8;
    vec![0x1b, b'[', b'M', cb, cx, cy]
}

/// Hover (1003, button 35) or drag (1002, button 32) SGR motion.
///
/// Press-only tracking (1000) returns empty. 1002 only reports while `left_down`.
pub fn encode_mouse_motion(
    col: u16,
    row: u16,
    left_down: bool,
    tracking: bool,
    any: bool,
    drag: bool,
    sgr: bool,
) -> Vec<u8> {
    if !tracking || (!any && !drag) {
        return Vec::new();
    }
    if !any && !left_down {
        return Vec::new();
    }
    let col = col.max(1);
    let row = row.max(1);
    // Motion bit 32 + button: left=0 → 32, none=3 → 35.
    let btn = if left_down { 32 } else { 35 };
    if sgr {
        return format!("\x1b[<{btn};{col};{row}M").into_bytes();
    }
    let col = col.min(223);
    let row = row.min(223);
    vec![
        0x1b,
        b'[',
        b'M',
        btn as u8,
        (32 + col) as u8,
        (32 + row) as u8,
    ]
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

    #[test]
    fn button_nil_without_tracking() {
        assert!(encode_mouse_button(5, 10, 0, true, false, true).is_empty());
    }

    #[test]
    fn button_sgr_press_release() {
        let press = encode_mouse_button(5, 10, 0, true, true, true);
        assert_eq!(press, b"\x1b[<0;5;10M");
        let rel = encode_mouse_button(5, 10, 0, false, true, true);
        assert_eq!(rel, b"\x1b[<0;5;10m");
    }

    #[test]
    fn motion_1000_silent() {
        assert!(encode_mouse_motion(3, 4, false, true, false, false, true).is_empty());
    }

    #[test]
    fn motion_1003_hover() {
        let got = encode_mouse_motion(3, 4, false, true, true, false, true);
        assert_eq!(got, b"\x1b[<35;3;4M");
        let drag = encode_mouse_motion(3, 4, true, true, true, false, true);
        assert_eq!(drag, b"\x1b[<32;3;4M");
    }

    #[test]
    fn motion_1002_drag_only() {
        assert!(encode_mouse_motion(1, 1, false, true, false, true, true).is_empty());
        let got = encode_mouse_motion(1, 1, true, true, false, true, true);
        assert_eq!(got, b"\x1b[<32;1;1M");
    }
}
