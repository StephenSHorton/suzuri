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
    /// The escape sequence being read.
    esc_buf: Vec<u8>,
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
                // An escape half read when we give up goes on too: a long
                // OSC (an OSC 1337 image, #288) is exactly what crosses
                // GIVE_UP, and losing its start printed it as text.
                out.append(&mut self.esc_buf);
                self.esc = Esc::None;
                self.disarm();
                out.extend_from_slice(&input[i..]);
                return;
            }
            let b = input[i];
            // Escapes: read whole, then decide. Before the match starts they
            // pass (a `clear` wipe must reach the screen); inside it they are
            // held with it, and ConPTY's cursor-forward (CSI n C), which it
            // sends for runs of blanks, counts as that many spaces.
            if self.esc != Esc::None || b == 0x1b {
                self.step_esc(b);
                self.esc_buf.push(b);
                i += 1;
                if self.esc == Esc::None {
                    let seq = std::mem::take(&mut self.esc_buf);
                    self.escape_done(seq, out);
                }
                continue;
            }
            if self.to_newline {
                // The echo's text goes; its line break stays, so the cursor
                // moves down as the shell (and ConPTY) believe it did.
                if b == b'\n' {
                    self.held.clear();
                    self.armed = false;
                    out.extend_from_slice(b"\r\n");
                }
                i += 1;
                continue;
            }
            // Backspace inside a match takes back a character (zsh echoes
            // the first one, backs over it, then writes the line).
            if b == 0x08 && self.pos > 0 {
                self.pos -= 1;
                self.held.push(b);
                i += 1;
                continue;
            }
            // CR, LF and other controls end a partial match.
            if b < 0x20 {
                self.release(out);
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
        if self.pos > 0 || !self.held.is_empty() {
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

    fn escape_done(&mut self, seq: Vec<u8>, out: &mut Vec<u8>) {
        if self.to_newline {
            // ConPTY may end the echo's line with a cursor move instead of a
            // newline: the move ends it, and goes on, so nothing after it is
            // lost.
            if seq.starts_with(b"\x1b[") && matches!(seq.last(), Some(b'H' | b'f' | b'B' | b'E' | b'd')) {
                self.held.clear();
                self.armed = false;
                out.extend_from_slice(&seq);
                return;
            }
            // Colours of the echo go; a mode change (zsh's bracketed-paste
            // off, a cursor show) must still reach the engine.
            // OSCs other than a title (images and notices, #288) pass.
            let title = [b"\x1b]0;".as_slice(), b"\x1b]1;", b"\x1b]2;"].iter().any(|p| seq.starts_with(p));
            if !seq.ends_with(b"m") && !title {
                out.extend_from_slice(&seq);
            }
            return;
        }
        if let Some(n) = cursor_forward(&seq) {
            let end = self.pos + n;
            if end <= self.want.len() && self.want[self.pos..end].iter().all(|&c| c == ' ') && (self.pos > 0 || n < self.want.len()) {
                if self.pos == 0 && self.want[0] != ' ' {
                    out.append(&mut self.held);
                    out.extend_from_slice(&seq);
                    return;
                }
                self.pos = end;
                self.held.extend_from_slice(&seq);
                if self.pos == self.want.len() {
                    self.to_newline = true;
                    self.pos = 0;
                }
                return;
            }
        }
        if self.pos == 0 && !seq.ends_with(b"m") {
            // Not ours: anything held before it goes first, in order.
            out.append(&mut self.held);
            out.extend_from_slice(&seq);
        } else {
            // Inside a match, or a colour that may open the echo (its reset
            // would go with the echo's tail): held.
            self.held.extend_from_slice(&seq);
        }
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

/// `ESC [ n C`: how many columns it moves right.
fn cursor_forward(seq: &[u8]) -> Option<usize> {
    let body = seq.strip_prefix(b"\x1b[")?.strip_suffix(b"C")?;
    if body.is_empty() {
        return Some(1);
    }
    std::str::from_utf8(body).ok()?.parse().ok()
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
    fn conpty_cursor_forward_counts_as_spaces() {
        // ConPTY sends the blank in "echo hi" as CSI C between colour runs.
        let (out, armed) = run("echo hi", &[b"\x1b[93mecho\x1b[C\x1b[37mhi\x1b[m\r\nhi\r\n"]);
        assert_eq!(out, b"\r\nhi\r\n", "{:?}", String::from_utf8_lossy(&out));
        assert!(!armed);
        let (out, _) = run("a   b", &[b"a\x1b[3Cb\r\nout\r\n"]);
        assert_eq!(out, b"\r\nout\r\n");
        // Before a match, a cursor move is just passed on.
        let (out, _) = run("x", &[b"\x1b[5Cy\r\n"]);
        assert_eq!(out, b"\x1b[5Cy\r\n");
    }

    #[test]
    fn a_cursor_move_ends_the_echo() {
        // No LF after the echo: the move ends it and the output after stays.
        let (out, armed) = run("echo hi", &[b"echo hi\x1b[2;1Hhi\x1b[3;1Hnext"]);
        assert_eq!(out, b"\x1b[2;1Hhi\x1b[3;1Hnext", "{:?}", String::from_utf8_lossy(&out));
        assert!(!armed);
    }

    #[test]
    fn conpty_repaint_from_home() {
        // kestrel's PC trace (#296): the echo inside a repaint from CSI H.
        let (out, armed) = run("echo hi", &[b"\x1b[?25l\x1b[H \x1b[93mecho \x1b[37mhi\x1b[K\x1b[m\r\nhi\x1b[K\r\n"]);
        assert!(!has(&out, b"echo"), "{:?}", String::from_utf8_lossy(&out));
        assert!(has(&out, b"\x1b[H"));
        assert!(has(&out, b"hi\x1b[K\r\n"));
        assert!(!armed);
    }

    #[test]
    fn zsh_echo_then_output() {
        let (out, _) = run("echo hi", &[b"echo hi\r\n", b"hi\r\n"]);
        assert_eq!(out, b"\r\nhi\r\n");
        // zle: the first character, a backspace over it, the line, then
        // bracketed paste off, which must still reach the engine.
        let (out, armed) = run("echo one", &[b"e\x08ec", b"ho one\x1b[?2004l\r\r\n", b"one\r\n"]);
        assert_eq!(out, b"\x1b[?2004l\r\none\r\n", "{:?}", String::from_utf8_lossy(&out));
        assert!(!armed);
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
            assert_eq!(out, "\r\n硯\r\n".as_bytes(), "cut {cut}");
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

/// #288: a long OSC (an inline image) reaches the engine whole whether it
/// comes before, with or after the echo, and with or without a newline.
#[cfg(test)]
mod osc288 {
    use super::*;
    fn osc(n: usize) -> Vec<u8> {
        let mut v = b"\x1b]1337;File=inline=1:".to_vec();
        v.extend(std::iter::repeat(b'A').take(n));
        v.push(0x07);
        v
    }
    fn run(chunks: &[Vec<u8>]) -> Vec<u8> {
        let mut f = EchoFilter::default();
        f.arm("py -3 show.py inline");
        let mut out = Vec::new();
        for c in chunks {
            if f.active() { f.feed(c, &mut out) } else { out.extend_from_slice(c) }
        }
        out
    }
    fn has(out: &[u8], big: &[u8]) -> bool { out.windows(big.len()).any(|w| w == big) }
    #[test]
    fn after_echo() {
        let big = osc(30000);
        let out = run(&[b"py -3 show.py inline\r\nlabel:\r\n".to_vec(), big.clone(), b"\r\n".to_vec()]);
        assert!(has(&out, &big));
    }
    #[test]
    fn same_chunk() {
        let big = osc(30000);
        let mut c = b"py -3 show.py inline\r\nlabel:\r\n".to_vec();
        c.extend(&big);
        let out = run(&[c]);
        assert!(has(&out, &big));
    }
    #[test]
    fn before_echo_seen() {
        let big = osc(30000);
        let mut c = b"\x1b[?25l".to_vec();
        c.extend(&big);
        let out = run(&[c, b"py -3 show.py inline\r\n".to_vec()]);
        assert!(has(&out, &big));
    }
    #[test]
    fn echo_without_lf() {
        let big = osc(30000);
        let mut c = b"py -3 show.py inline\x1b[2;1Hlabel:\x1b[3;1H".to_vec();
        c.extend(&big);
        let out = run(&[c]);
        assert!(has(&out, &big));
    }
}
