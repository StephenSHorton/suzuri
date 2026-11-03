//! Hides the shell's echo of a line submitted from the input bar (#296):
//! the pane shows its own command block instead (Go suzuri's
//! internal/ui/echo_filter.go).
//!
//! A streaming matcher between the shell-OSC filter and the VT engine. Armed
//! with the command, it drops the command's characters as the shell echoes
//! them (skipping the colour codes PowerShell wraps around its echo), then
//! everything up to and including the line's newline. Unlike Go's, bytes of
//! a partial match are held back and released if the text turns out not to
//! be the echo, so real output is never lost.

/// Bytes looked at while armed before giving up and passing everything on.
const GIVE_UP: usize = 4096;

#[derive(Default)]
pub struct EchoFilter {
    want: Vec<char>,
    pos: usize,
    /// Matched the command; dropping through the newline.
    to_newline: bool,
    armed: bool,
    seen: usize,
    /// Bytes of a partial match (and escapes inside it), released on a
    /// mismatch.
    held: Vec<u8>,
    esc: Esc,
    /// An incomplete UTF-8 sequence carried to the next feed.
    utf8: Vec<u8>,
}

#[derive(Default, Clone, Copy, PartialEq)]
enum Esc {
    #[default]
    None,
    Esc,
    Csi,
    Osc,
    OscEsc,
}

impl EchoFilter {
    /// Expect the echo of `cmd`. Only its first line is matched (a
    /// continuation prompt is the shell's own business). Blank disarms.
    pub fn arm(&mut self, cmd: &str) {
        *self = EchoFilter::default();
        let first = cmd.replace("\r\n", "\n").replace('\r', "\n");
        let first = first.split('\n').next().unwrap_or("").trim_end();
        if first.trim().is_empty() {
            return;
        }
        self.want = first.chars().collect();
        self.armed = true;
    }

    pub fn armed(&self) -> bool {
        self.armed
    }

    /// Armed, or holding bytes still to give back.
    pub fn active(&self) -> bool {
        self.armed || !self.held.is_empty()
    }

    pub fn disarm(&mut self) {
        let held = std::mem::take(&mut self.held);
        *self = EchoFilter { held, ..EchoFilter::default() };
    }

    /// Filter `input` into `out`.
    pub fn feed(&mut self, input: &[u8], out: &mut Vec<u8>) {
        if !self.armed {
            out.extend_from_slice(&self.held);
            self.held.clear();
            out.extend_from_slice(input);
            return;
        }
        let mut i = 0;
        while i < input.len() {
            if !self.armed {
                out.extend_from_slice(&input[i..]);
                return;
            }
            self.seen += 1;
            if self.seen > GIVE_UP {
                self.release(out);
                self.disarm();
                out.extend_from_slice(&input[i..]);
                return;
            }
            let b = input[i];
            // Escapes: before the match starts they pass (a `clear` wipe
            // must reach the screen); inside it they are held with it.
            if self.esc != Esc::None || b == 0x1b {
                self.step_esc(b);
                if self.pos == 0 && !self.to_newline {
                    out.push(b);
                } else if !self.to_newline {
                    self.held.push(b);
                }
                i += 1;
                continue;
            }
            if self.to_newline {
                if b == b'\n' {
                    self.held.clear();
                    self.armed = false;
                }
                i += 1;
                continue;
            }
            // CR, LF and other controls end a partial match.
            if b < 0x20 {
                if self.pos > 0 {
                    self.release(out);
                }
                out.push(b);
                i += 1;
                continue;
            }
            // One UTF-8 character.
            let need = utf8_len(b);
            if !self.utf8.is_empty() || i + need > input.len() {
                self.utf8.push(b);
                i += 1;
                if self.utf8.len() < utf8_len(self.utf8[0]) {
                    continue;
                }
                let bytes = std::mem::take(&mut self.utf8);
                self.char_bytes(&bytes, out);
                continue;
            }
            let bytes = input[i..i + need].to_vec();
            i += need;
            self.char_bytes(&bytes, out);
        }
    }

    fn char_bytes(&mut self, bytes: &[u8], out: &mut Vec<u8>) {
        let c = std::str::from_utf8(bytes).ok().and_then(|s| s.chars().next());
        if c.is_some() && c == self.want.get(self.pos).copied() {
            self.pos += 1;
            self.held.extend_from_slice(bytes);
            if self.pos == self.want.len() {
                self.to_newline = true;
                self.pos = 0;
            }
            return;
        }
        if self.pos > 0 {
            self.release(out);
            // The mismatching character may start the echo itself.
            if c.is_some() && c == self.want.first().copied() {
                self.pos = 1;
                self.held.extend_from_slice(bytes);
                return;
            }
        }
        out.extend_from_slice(bytes);
    }

    /// A partial match wasn't the echo: give its bytes back.
    fn release(&mut self, out: &mut Vec<u8>) {
        out.append(&mut self.held);
        self.pos = 0;
    }

    fn step_esc(&mut self, b: u8) {
        self.esc = match (self.esc, b) {
            (Esc::None, 0x1b) => Esc::Esc,
            (Esc::Esc, b'[') => Esc::Csi,
            (Esc::Esc, b']') => Esc::Osc,
            (Esc::Esc, _) => Esc::None,
            (Esc::Csi, 0x40..=0x7e) => Esc::None,
            (Esc::Csi, _) => Esc::Csi,
            (Esc::Osc, 0x07) => Esc::None,
            (Esc::Osc, 0x1b) => Esc::OscEsc,
            (Esc::Osc, _) => Esc::Osc,
            (Esc::OscEsc, _) => Esc::None,
            (Esc::None, _) => Esc::None,
        };
    }
}

fn utf8_len(b: u8) -> usize {
    match b {
        0xf0..=0xf7 => 4,
        0xe0..=0xef => 3,
        0xc0..=0xdf => 2,
        _ => 1,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn run(cmd: &str, parts: &[&[u8]]) -> (Vec<u8>, bool) {
        let mut f = EchoFilter::default();
        f.arm(cmd);
        let mut out = Vec::new();
        for p in parts {
            f.feed(p, &mut out);
        }
        (out, f.armed())
    }

    fn has(h: &[u8], n: &[u8]) -> bool {
        h.windows(n.len()).any(|w| w == n)
    }

    #[test]
    fn suppresses_highlighted_powershell_echo() {
        // ConPTY + Windows PowerShell -NoProfile (Go's capture).
        let (out, armed) = run("Write-Output hello", &[b"\x1b[?25l\x1b[93mWrite-Output \x1b[37mhello\r\n\x1b[?25h\x1b[mhello\r\n "]);
        assert!(!has(&out, b"Write-Output"), "{:?}", String::from_utf8_lossy(&out));
        assert!(has(&out, b"hello"));
        assert!(!armed);
    }

    #[test]
    fn zsh_echo_then_output() {
        let (out, _) = run("echo hi", &[b"echo hi\r\n", b"hi\r\n"]);
        assert_eq!(out, b"hi\r\n");
    }

    #[test]
    fn chunked_anywhere() {
        let whole: &[u8] = b"\x1b[?25l\x1b[93mecho \x1b[37mhi\r\n\x1b[?25h\x1b[mhi\r\n ";
        for cut in 0..whole.len() {
            let (out, _) = run("echo hi", &[&whole[..cut], &whole[cut..]]);
            assert!(!has(&out, b"echo"), "cut {cut}: {:?}", String::from_utf8_lossy(&out));
            assert!(has(&out, b"hi\r\n "), "cut {cut}: {:?}", String::from_utf8_lossy(&out));
        }
    }

    #[test]
    fn leading_output_passes() {
        let (out, _) = run("Get-Date", &[b"NOTICE\r\n\x1b[93mGet-Date\r\n\x1b[mthedate\r\n"]);
        assert!(has(&out, b"NOTICE"));
        assert!(!has(&out, b"Get-Date"));
        assert!(has(&out, b"thedate"));
    }

    #[test]
    fn blank_arm_passes_all() {
        let (out, armed) = run("   ", &[b"hello\r\n"]);
        assert_eq!(out, b"hello\r\n");
        assert!(!armed);
    }

    #[test]
    fn clear_wipe_before_echo_reaches_the_screen() {
        let (out, _) = run("clear", &[b"\x1b[H\x1b[2J\x1b[3Jclear\r\n"]);
        assert!(has(&out, b"\x1b[2J"));
        assert!(!has(&out, b"clear"));
    }

    #[test]
    fn a_near_miss_is_given_back() {
        // Go lost "ab" here; the held bytes come back.
        let (out, armed) = run("abc", &[b"abX\r\n", b"\x1b[93mabc\r\nOUT\r\n"]);
        assert!(has(&out, b"abX\r\n"), "{:?}", String::from_utf8_lossy(&out));
        assert!(!has(&out[5..], b"abc"));
        assert!(has(&out, b"OUT"));
        assert!(!armed);
    }

    #[test]
    fn unicode_command_split_mid_character() {
        let line = "echo 硯".as_bytes();
        let mut whole = line.to_vec();
        whole.extend_from_slice(b"\r\n\xe7\xa1\xaf\r\n");
        for cut in 0..whole.len() {
            let (out, _) = run("echo 硯", &[&whole[..cut], &whole[cut..]]);
            assert_eq!(out, "硯\r\n".as_bytes(), "cut {cut}");
        }
    }

    #[test]
    fn multi_line_matches_first_line_only() {
        let (out, _) = run("echo a\necho b", &[b"echo a\r\n> echo b\r\na\r\nb\r\n"]);
        assert!(!has(&out, b"echo a"));
        assert!(has(&out, b"> echo b"));
    }

    #[test]
    fn gives_up_after_a_budget() {
        let noise = vec![b'x'; GIVE_UP + 10];
        let (out, armed) = run("never", &[&noise]);
        assert_eq!(out.len(), noise.len());
        assert!(!armed);
    }
}
