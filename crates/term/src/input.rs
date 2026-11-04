//! Input the UI hands the core besides keys: mouse reports for apps that
//! asked for them (xterm 1000/1002/1003, SGR 1006, UTF-8 1005), pasted text,
//! and URLs found in a row of text. Pure functions, so they can be tested
//! without a PTY. Go suzuri's equivalents: mouse_pty.go,
//! pty_keys_common.go, bracketed_paste.go and links.go.

use alacritty_terminal::term::TermMode;

/// A pointer event in cell coordinates (0-based) of the visible screen.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct MouseEvent {
    pub kind: MouseKind,
    /// 0 left, 1 middle, 2 right; ignored for wheel and plain motion.
    pub button: u8,
    pub col: u16,
    pub row: u16,
    pub shift: bool,
    pub alt: bool,
    pub ctrl: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MouseKind {
    Press,
    Release,
    /// Motion with `button` held (Some) or with no button down (None).
    Motion(Option<u8>),
    WheelUp,
    WheelDown,
}

/// The bytes that report `ev` to the app, or None when the app's mouse
/// modes don't ask for this event.
pub fn mouse_report(mode: TermMode, ev: MouseEvent) -> Option<Vec<u8>> {
    if !mode.intersects(TermMode::MOUSE_MODE) {
        return None;
    }
    let mut code: u32 = match ev.kind {
        MouseKind::Press | MouseKind::Release => ev.button.min(2) as u32,
        MouseKind::Motion(held) => {
            match held {
                // 1002 reports drags, 1003 every move.
                Some(b) if mode.intersects(TermMode::MOUSE_DRAG | TermMode::MOUSE_MOTION) => 32 + b.min(2) as u32,
                None if mode.contains(TermMode::MOUSE_MOTION) => 32 + 3,
                _ => return None,
            }
        }
        MouseKind::WheelUp => 64,
        MouseKind::WheelDown => 65,
    };
    if ev.shift {
        code += 4;
    }
    if ev.alt {
        code += 8;
    }
    if ev.ctrl {
        code += 16;
    }
    let (col, row) = (ev.col as u32 + 1, ev.row as u32 + 1);
    if mode.contains(TermMode::SGR_MOUSE) {
        let end = if ev.kind == MouseKind::Release { 'm' } else { 'M' };
        return Some(format!("\x1b[<{code};{col};{row}{end}").into_bytes());
    }
    // Normal (X10) encoding: a release has no button, only "3".
    if ev.kind == MouseKind::Release {
        code = 3 + (code & !3);
    }
    let mut out = b"\x1b[M".to_vec();
    out.push((32 + code) as u8);
    if mode.contains(TermMode::UTF8_MOUSE) {
        for v in [col, row] {
            let c = char::from_u32(32 + v.min(2015)).unwrap_or(' ');
            let mut buf = [0u8; 4];
            out.extend_from_slice(c.encode_utf8(&mut buf).as_bytes());
        }
    } else {
        // One byte each: positions past 223 can't be expressed.
        out.push((32 + col.min(223)) as u8);
        out.push((32 + row.min(223)) as u8);
    }
    Some(out)
}

/// Pasted text as the bytes to write: newlines become CR as host terminals
/// send them, and with bracketed paste (DECSET 2004) the text is framed.
/// An end marker inside the text is dropped so a paste can't break out of
/// its brackets and run as typed input.
pub fn paste_bytes(text: &str, bracketed: bool) -> Vec<u8> {
    let body = text.replace("\r\n", "\r").replace('\n', "\r");
    if !bracketed {
        return body.into_bytes();
    }
    let body = body.replace("\x1b[201~", "").replace("\x1b[200~", "");
    format!("\x1b[200~{body}\x1b[201~").into_bytes()
}

/// A URL found in a row: columns x0..x1 (end exclusive).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct UrlSpan {
    pub x0: usize,
    pub x1: usize,
    pub url: String,
}

/// '\0' marks the right half of a wide character: part of the URL, but
/// not a char of it.
fn url_char(c: char) -> bool {
    c == '\0' || !(c.is_whitespace() || c.is_control() || "<>\"'{}|\\^`[]()".contains(c))
}

fn trail_punct(c: char) -> bool {
    matches!(c, '.' | ',' | ';' | ':' | '!' | '?' | ']' | '}' | '\'' | '"' | '»' | '›') || c.is_whitespace()
}

/// http(s):// and www. URLs in `line` (one char per cell, '\0' for the
/// right half of a wide char), the way Go
/// suzuri's findLinksInGrid finds them: trailing prose punctuation and an
/// unbalanced closing paren are left off, www. gets https:// in front.
pub fn find_urls(line: &[char]) -> Vec<UrlSpan> {
    let lower: Vec<char> = line.iter().map(|c| c.to_ascii_lowercase()).collect();
    let starts_at = |i: usize, pat: &str| pat.chars().enumerate().all(|(k, p)| lower.get(i + k) == Some(&p));
    let mut out = Vec::new();
    let mut i = 0;
    while i < line.len() {
        // \b: the scheme can't continue a word.
        let boundary = i == 0 || !(line[i - 1].is_alphanumeric() || line[i - 1] == '_');
        let prefix = if !boundary {
            0
        } else if starts_at(i, "https://") {
            8
        } else if starts_at(i, "http://") {
            7
        } else if starts_at(i, "www.") {
            4
        } else {
            0
        };
        if prefix == 0 {
            i += 1;
            continue;
        }
        let mut j = i + prefix;
        // Parens are excluded in Go's pattern, so a URL stops at one.
        while j < line.len() && url_char(line[j]) {
            j += 1;
        }
        let mut end = j;
        while end > i && trail_punct(line[end - 1]) {
            end -= 1;
        }
        let raw: String = line[i..end].iter().filter(|&&c| c != '\0').collect();
        let rest = &raw[if prefix == 4 { 0 } else { prefix }..];
        let ok = end - i > prefix && !rest.starts_with('/') && !rest.is_empty();
        if ok {
            let url = if prefix == 4 { format!("https://{raw}") } else { raw };
            out.push(UrlSpan { x0: i, x1: end, url });
        }
        i = j.max(i + 1);
    }
    out
}

/// Schemes a click may open: web pages and mail. Anything else an app puts
/// in an OSC 8 link (file:, custom handlers) is not launched.
pub fn openable(url: &str) -> bool {
    let l = url.to_ascii_lowercase();
    (l.starts_with("https://") || l.starts_with("http://")) && url.len() > 8 || l.starts_with("mailto:") && url.len() > 7
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ev(kind: MouseKind, button: u8, col: u16, row: u16) -> MouseEvent {
        MouseEvent { kind, button, col, row, shift: false, alt: false, ctrl: false }
    }

    #[test]
    fn no_report_without_mouse_mode() {
        assert_eq!(mouse_report(TermMode::SGR_MOUSE, ev(MouseKind::Press, 0, 1, 1)), None);
    }

    #[test]
    fn sgr_press_release_wheel() {
        let m = TermMode::MOUSE_REPORT_CLICK | TermMode::SGR_MOUSE;
        assert_eq!(mouse_report(m, ev(MouseKind::Press, 0, 4, 9)).unwrap(), b"\x1b[<0;5;10M");
        assert_eq!(mouse_report(m, ev(MouseKind::Release, 0, 4, 9)).unwrap(), b"\x1b[<0;5;10m");
        assert_eq!(mouse_report(m, ev(MouseKind::Press, 2, 0, 0)).unwrap(), b"\x1b[<2;1;1M");
        assert_eq!(mouse_report(m, ev(MouseKind::WheelUp, 0, 0, 0)).unwrap(), b"\x1b[<64;1;1M");
        assert_eq!(mouse_report(m, ev(MouseKind::WheelDown, 0, 0, 0)).unwrap(), b"\x1b[<65;1;1M");
        let mut e = ev(MouseKind::Press, 0, 0, 0);
        e.ctrl = true;
        e.shift = true;
        assert_eq!(mouse_report(m, e).unwrap(), b"\x1b[<20;1;1M");
    }

    #[test]
    fn motion_follows_1000_1002_1003() {
        let click = TermMode::MOUSE_REPORT_CLICK | TermMode::SGR_MOUSE;
        assert_eq!(mouse_report(click, ev(MouseKind::Motion(Some(0)), 0, 1, 1)), None);
        let drag = click | TermMode::MOUSE_DRAG;
        assert_eq!(mouse_report(drag, ev(MouseKind::Motion(Some(0)), 0, 1, 1)).unwrap(), b"\x1b[<32;2;2M");
        assert_eq!(mouse_report(drag, ev(MouseKind::Motion(None), 0, 1, 1)), None);
        let any = click | TermMode::MOUSE_MOTION;
        assert_eq!(mouse_report(any, ev(MouseKind::Motion(None), 0, 1, 1)).unwrap(), b"\x1b[<35;2;2M");
    }

    #[test]
    fn x10_and_utf8_encodings() {
        let m = TermMode::MOUSE_REPORT_CLICK;
        assert_eq!(mouse_report(m, ev(MouseKind::Press, 0, 0, 0)).unwrap(), b"\x1b[M !!");
        // Release has no button in X10: 3.
        assert_eq!(mouse_report(m, ev(MouseKind::Release, 0, 0, 0)).unwrap(), b"\x1b[M#!!");
        // Past 223 columns X10 clamps; UTF-8 mode goes further.
        assert_eq!(mouse_report(m, ev(MouseKind::Press, 0, 300, 0)).unwrap(), [0x1b, b'[', b'M', 32, 255, 33]);
        let u = mouse_report(m | TermMode::UTF8_MOUSE, ev(MouseKind::Press, 0, 300, 0)).unwrap();
        assert_eq!(&u[..4], b"\x1b[M ");
        assert_eq!(std::str::from_utf8(&u[4..]).unwrap(), "\u{14d}!");
    }

    #[test]
    fn paste_framing() {
        assert_eq!(paste_bytes("a\nb\r\nc", false), b"a\rb\rc");
        assert_eq!(paste_bytes("a\nb", true), b"\x1b[200~a\rb\x1b[201~");
        // An embedded end marker can't end the paste early.
        assert_eq!(paste_bytes("x\x1b[201~rm -rf\n", true), b"\x1b[200~xrm -rf\r\x1b[201~");
    }

    fn urls(s: &str) -> Vec<(usize, usize, String)> {
        let v: Vec<char> = s.chars().collect();
        find_urls(&v).into_iter().map(|u| (u.x0, u.x1, u.url)).collect()
    }

    #[test]
    fn finds_urls_like_go_suzuri() {
        assert_eq!(urls("see https://example.com/a?b=1."), vec![(4, 29, "https://example.com/a?b=1".into())]);
        assert_eq!(urls("(see https://x.com/y)"), vec![(5, 20, "https://x.com/y".into())]);
        assert_eq!(urls("go to www.rust-lang.org, now"), vec![(6, 23, "https://www.rust-lang.org".into())]);
        assert_eq!(urls("HTTP://A.B and http:// and xhttps://no"), vec![(0, 10, "HTTP://A.B".into())]);
        assert_eq!(urls("硯\0 https://例\0え\0.jp/パ\0ス\0 end"), vec![(3, 23, "https://例え.jp/パス".into())]);
        assert_eq!(urls("two: http://a.io http://b.io"), vec![(5, 16, "http://a.io".into()), (17, 28, "http://b.io".into())]);
        assert!(urls("https:///nohost").is_empty());
    }

    #[test]
    fn only_web_and_mail_open() {
        assert!(openable("https://a.b"));
        assert!(openable("mailto:x@y.z"));
        assert!(!openable("file:///etc/passwd"));
        assert!(!openable("javascript:alert(1)"));
        assert!(!openable("https://"));
    }
}
