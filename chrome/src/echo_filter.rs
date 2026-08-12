//! Streaming echo suppressor — product port of `internal/ui/echo_filter.go`.
//!
//! After a warp-bar submit, the host injects a command block and arms this
//! filter so the shell's local echo of the same line does not double-print.

/// Max PTY bytes examined while armed before giving up.
pub const ECHO_FILTER_GIVE_UP: usize = 4096;

const PHASE_MATCH: i32 = 0;
const PHASE_NL: i32 = 1;

/// Strips local echo of a just-submitted command from the raw PTY stream.
#[derive(Clone, Debug, Default)]
pub struct EchoFilter {
    want: Vec<char>,
    pos: usize,
    phase: i32,
    armed: bool,
    seen: usize,
    /// 0 none, 1 saw ESC, 2 CSI params, 3 OSC, 4 OSC ST almost done
    esc_state: i32,
}

impl EchoFilter {
    pub fn new() -> Self {
        Self::default()
    }

    /// Prepare to suppress echo of `cmd`. Empty / whitespace disarms.
    pub fn arm(&mut self, cmd: &str) {
        *self = Self::default();
        let mut norm = cmd.replace("\r\n", "\n").replace('\r', "\n");
        if let Some(i) = norm.find('\n') {
            norm.truncate(i);
        }
        let norm = norm.trim_end_matches([' ', '\t']);
        if norm.is_empty() {
            return;
        }
        self.want = norm.chars().collect();
        self.armed = true;
        self.phase = PHASE_MATCH;
    }

    pub fn disarm(&mut self) {
        *self = Self::default();
    }

    /// `(armed, cmd, phase)` for MCP diag.
    pub fn status(&self) -> (bool, String, i32) {
        if !self.armed {
            return (false, String::new(), 0);
        }
        (true, self.want.iter().collect(), self.phase)
    }

    pub fn is_armed(&self) -> bool {
        self.armed
    }

    /// Consume PTY bytes; return bytes that should reach the VT decoder.
    pub fn feed(&mut self, input: &[u8]) -> Vec<u8> {
        if !self.armed || self.want.is_empty() {
            return input.to_vec();
        }
        if input.is_empty() {
            return Vec::new();
        }

        let mut out = Vec::with_capacity(input.len());
        let mut i = 0;
        while i < input.len() {
            if !self.armed {
                out.extend_from_slice(&input[i..]);
                break;
            }

            let b = input[i];
            self.seen += 1;
            if self.seen > ECHO_FILTER_GIVE_UP {
                out.extend_from_slice(&input[i..]);
                self.disarm();
                break;
            }

            if self.esc_state != 0 || b == 0x1b {
                let pass_through = self.pos == 0 && self.phase == PHASE_MATCH;
                if pass_through {
                    out.push(b);
                    let _ = self.consume_esc(b);
                    i += 1;
                    continue;
                }
                if self.consume_esc(b) {
                    i += 1;
                    continue;
                }
                // escape finished — drop final byte too
                i += 1;
                continue;
            }

            match self.phase {
                PHASE_MATCH => {
                    if b == b'\r' {
                        if self.pos == 0 {
                            out.push(b);
                        }
                        i += 1;
                        continue;
                    }
                    if b == b'\n' {
                        if self.pos == 0 {
                            out.push(b);
                            i += 1;
                            continue;
                        }
                        self.pos = 0;
                        i += 1;
                        continue;
                    }
                    if b < 0x20 {
                        if self.pos == 0 {
                            out.push(b);
                            i += 1;
                            continue;
                        }
                        self.pos = 0;
                        i += 1;
                        continue;
                    }

                    let want = self.want[self.pos];
                    let (r, n) = decode_rune_byte(input, i);
                    if n > 1 {
                        if r == want {
                            self.pos += 1;
                            i += n;
                            if self.pos >= self.want.len() {
                                self.phase = PHASE_NL;
                                self.pos = 0;
                            }
                            continue;
                        }
                        if self.pos == 0 {
                            out.extend_from_slice(&input[i..i + n]);
                            i += n;
                            continue;
                        }
                        self.pos = 0;
                        out.extend_from_slice(&input[i..i + n]);
                        i += n;
                        continue;
                    }

                    if char::from(b) == want {
                        self.pos += 1;
                        i += 1;
                        if self.pos >= self.want.len() {
                            self.phase = PHASE_NL;
                            self.pos = 0;
                        }
                        continue;
                    }
                    if self.pos == 0 {
                        out.push(b);
                        i += 1;
                        continue;
                    }
                    self.pos = 0;
                    out.push(b);
                    i += 1;
                }
                PHASE_NL => {
                    if b == b'\n' {
                        self.disarm();
                        i += 1;
                        continue;
                    }
                    i += 1;
                }
                _ => {
                    out.push(b);
                    i += 1;
                }
            }
        }
        out
    }

    /// Returns true if still inside an escape sequence.
    fn consume_esc(&mut self, b: u8) -> bool {
        match self.esc_state {
            0 => {
                if b == 0x1b {
                    self.esc_state = 1;
                    return true;
                }
                false
            }
            1 => match b {
                b'[' => {
                    self.esc_state = 2;
                    true
                }
                b']' => {
                    self.esc_state = 3;
                    true
                }
                _ => {
                    self.esc_state = 0;
                    false
                }
            },
            2 => {
                if (0x40..=0x7e).contains(&b) {
                    self.esc_state = 0;
                    false
                } else {
                    true
                }
            }
            3 => {
                if b == 0x07 {
                    self.esc_state = 0;
                    false
                } else if b == 0x1b {
                    self.esc_state = 4;
                    true
                } else {
                    true
                }
            }
            4 => {
                self.esc_state = 0;
                false
            }
            _ => {
                self.esc_state = 0;
                false
            }
        }
    }
}

fn decode_rune_byte(input: &[u8], i: usize) -> (char, usize) {
    if i >= input.len() {
        return ('\0', 0);
    }
    let b = input[i];
    if b < 0x80 {
        return (char::from(b), 1);
    }
    if b & 0xe0 == 0xc0 && i + 1 < input.len() {
        let cp = (u32::from(b & 0x1f) << 6) | u32::from(input[i + 1] & 0x3f);
        return (char::from_u32(cp).unwrap_or('\u{FFFD}'), 2);
    }
    if b & 0xf0 == 0xe0 && i + 2 < input.len() {
        let cp = (u32::from(b & 0x0f) << 12)
            | (u32::from(input[i + 1] & 0x3f) << 6)
            | u32::from(input[i + 2] & 0x3f);
        return (char::from_u32(cp).unwrap_or('\u{FFFD}'), 3);
    }
    (char::from(b), 1)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn suppresses_highlighted_ps_echo() {
        let raw = b"\x1b[?25l\x1b[93mWrite-Output \x1b[37mhello\r\n\x1b[?25h\x1b[mhello\r\n ";
        let mut f = EchoFilter::new();
        f.arm("Write-Output hello");
        let got = f.feed(raw);
        let s = String::from_utf8_lossy(&got);
        assert!(!s.contains("Write-Output"), "command leak: {s:?}");
        assert!(s.contains("hello"), "output missing: {s:?}");
        assert!(!f.is_armed());
    }

    #[test]
    fn chunked() {
        let mut f = EchoFilter::new();
        f.arm("echo hi");
        let parts: &[&[u8]] = &[
            b"\x1b[?25l",
            b"\x1b[93mecho \x1b[37mhi\r",
            b"\n\x1b[?25h\x1b[mhi\r\n ",
        ];
        let mut got = Vec::new();
        for p in parts {
            got.extend(f.feed(p));
        }
        let s = String::from_utf8_lossy(&got);
        assert!(!s.contains("echo"), "command leak: {s:?}");
        assert!(s.contains("hi"), "output missing: {s:?}");
    }

    #[test]
    fn leading_output_passes() {
        let mut f = EchoFilter::new();
        f.arm("Get-Date");
        let raw = b"NOTICE\r\n\x1b[93mGet-Date\r\n\x1b[mthedate\r\n";
        let got = f.feed(raw);
        let s = String::from_utf8_lossy(&got);
        assert!(s.contains("NOTICE"), "leading lost: {s:?}");
        assert!(!s.contains("Get-Date"), "command leak: {s:?}");
        assert!(s.contains("thedate"), "output missing: {s:?}");
    }

    #[test]
    fn empty_arm() {
        let mut f = EchoFilter::new();
        f.arm("   ");
        let raw = b"hello\r\n";
        assert_eq!(f.feed(raw), raw);
    }

    #[test]
    fn passes_clear_csi_before_echo() {
        let mut f = EchoFilter::new();
        f.arm("clear");
        let raw = b"\x1b[H\x1b[2J\x1b[3Jclear\r\n";
        let got = f.feed(raw);
        let s = String::from_utf8_lossy(&got);
        assert!(s.contains("\x1b[2J"), "clear CSI swallowed: {s:?}");
        assert!(!s.contains("clear"), "command leak: {s:?}");
    }

    #[test]
    fn status_reports_armed() {
        let mut f = EchoFilter::new();
        assert_eq!(f.status(), (false, String::new(), 0));
        f.arm("ls -la");
        let (armed, cmd, phase) = f.status();
        assert!(armed);
        assert_eq!(cmd, "ls -la");
        assert_eq!(phase, 0);
    }
}
