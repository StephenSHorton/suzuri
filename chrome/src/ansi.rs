//! Minimal ANSI / VT stream → [`CellGrid`].
//!
//! Enough for interactive shells (prompt, colors, cursor, clear, alt screen).
//! Not a full VT100 / xterm.

use crate::cells::{theme, CellGrid};

/// Stateful decoder for a byte stream from a PTY.
#[derive(Debug)]
pub struct AnsiDecoder {
    /// Incomplete UTF-8 sequence held across reads.
    pending: Vec<u8>,
    /// CSI / ESC parser state.
    esc: EscState,
    /// SGR params while parsing CSI.
    csi_params: Vec<u16>,
    csi_num: u16,
    csi_has_num: bool,
    /// Private CSI (`?`) marker.
    csi_priv: bool,
    /// CSI private prefix: `?` `>` `=` `<` (0 when none).
    csi_prefix: u8,
    /// Whether the hardware/cursor should be drawn (`CSI ? 25 h/l`).
    pub cursor_visible: bool,
    /// Saved primary buffer while on the alternate screen.
    primary_backup: Option<CellGrid>,
    /// Scroll region as 1-based inclusive `(top, bottom)`. `None` = full screen.
    scroll_region: Option<(u16, u16)>,
    /// SGR reverse video active (`7` / `27`).
    reverse: bool,
    /// OSC payload buffer (between ESC ] and BEL/ST).
    osc_buf: Vec<u8>,
    /// Latest cwd from OSC 7 / 7878 (consumed by the host).
    pending_cwd: Option<String>,
    /// Latest window/icon title from OSC 0 / 2 (consumed by the host).
    pending_title: Option<String>,
    /// Bytes to write back to the PTY (DA / DECRQM answers). Host drains.
    pending_replies: Vec<Vec<u8>>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
enum EscState {
    #[default]
    Ground,
    Esc,
    Csi,
    /// Skip OSC until BEL or ST
    Osc,
    /// Device Control String — skip until ST (ESC \ ) or BEL.
    Dcs,
}

impl Default for AnsiDecoder {
    fn default() -> Self {
        Self {
            pending: Vec::new(),
            esc: EscState::Ground,
            csi_params: Vec::new(),
            csi_num: 0,
            csi_has_num: false,
            csi_priv: false,
            csi_prefix: 0,
            cursor_visible: true,
            primary_backup: None,
            scroll_region: None,
            reverse: false,
            osc_buf: Vec::new(),
            pending_cwd: None,
            pending_title: None,
            pending_replies: Vec::new(),
        }
    }
}

impl AnsiDecoder {
    pub fn new() -> Self {
        Self::default()
    }

    /// True while the alternate screen buffer is active (`CSI ? 1049 h` / `? 47 h`).
    pub fn on_alt_screen(&self) -> bool {
        self.primary_backup.is_some()
    }

    /// Take the latest OSC-reported cwd, if any.
    pub fn take_cwd(&mut self) -> Option<String> {
        self.pending_cwd.take()
    }

    /// Take the latest OSC 0 / 2 window title, if any.
    pub fn take_title(&mut self) -> Option<String> {
        self.pending_title.take()
    }

    /// Drain PTY write-back replies (device attributes, mode reports, …).
    pub fn take_replies(&mut self) -> Vec<Vec<u8>> {
        std::mem::take(&mut self.pending_replies)
    }

    fn reply(&mut self, bytes: &[u8]) {
        self.pending_replies.push(bytes.to_vec());
    }

    fn finish_osc(&mut self) {
        if let Some(path) = parse_cwd_osc_payload(&self.osc_buf) {
            self.pending_cwd = Some(path);
        }
        if let Some(title) = parse_title_osc_payload(&self.osc_buf) {
            self.pending_title = Some(title);
        }
        self.osc_buf.clear();
    }

    /// Feed raw PTY bytes into the grid.
    pub fn feed(&mut self, grid: &mut CellGrid, bytes: &[u8]) {
        self.pending.extend_from_slice(bytes);
        let data = std::mem::take(&mut self.pending);
        let mut i = 0;
        while i < data.len() {
            // UTF-8
            let b = data[i];
            if self.esc == EscState::Ground && b < 0x80 {
                self.byte(grid, b);
                i += 1;
                continue;
            }
            if self.esc != EscState::Ground {
                self.byte(grid, b);
                i += 1;
                continue;
            }
            // multi-byte UTF-8
            let width = utf8_width(b);
            if width == 0 {
                i += 1;
                continue;
            }
            if i + width > data.len() {
                self.pending = data[i..].to_vec();
                break;
            }
            if let Ok(s) = std::str::from_utf8(&data[i..i + width]) {
                for ch in s.chars() {
                    grid.put_char(ch);
                }
            }
            i += width;
        }
    }

    fn byte(&mut self, grid: &mut CellGrid, b: u8) {
        match self.esc {
            EscState::Ground => match b {
                0x1b => self.esc = EscState::Esc,
                0x08 => {
                    // backspace
                    let c = grid.cursor();
                    if c.col > 0 {
                        grid.set_cursor(c.col - 1, c.row);
                        grid.put_char(' ');
                        let c2 = grid.cursor();
                        grid.set_cursor(c2.col.saturating_sub(1), c2.row);
                    }
                }
                0x09 => grid.put_char('\t'),
                0x0a => self.linefeed(grid),
                0x0d => grid.put_char('\r'),
                0x07 => {} // bell
                c if c < 0x20 => {}
                c => grid.put_char(c as char),
            },
            EscState::Esc => match b {
                b'[' => {
                    self.esc = EscState::Csi;
                    self.csi_params.clear();
                    self.csi_num = 0;
                    self.csi_has_num = false;
                    self.csi_priv = false;
                    self.csi_prefix = 0;
                }
                b']' => {
                    self.osc_buf.clear();
                    self.esc = EscState::Osc;
                }
                // DCS — skip payload until ST so probes never print as text.
                b'P' => self.esc = EscState::Dcs,
                // IND — index (move down / scroll)
                b'D' => {
                    self.index_down(grid);
                    self.esc = EscState::Ground;
                }
                // RI — reverse index
                b'M' => {
                    self.index_up(grid);
                    self.esc = EscState::Ground;
                }
                _ => self.esc = EscState::Ground,
            },
            EscState::Osc => {
                // BEL or ST (ESC \) ends OSC — parse cwd (OSC 7 / 7878).
                if b == 0x07 {
                    self.finish_osc();
                    self.esc = EscState::Ground;
                } else if b == 0x1b {
                    // Possible ST: next byte should be `\`; finish on that path via Esc.
                    // If next is not `\`, we already left Osc — treat as cancelled ST.
                    self.finish_osc();
                    self.esc = EscState::Esc;
                } else if self.osc_buf.len() < 4096 {
                    self.osc_buf.push(b);
                }
            }
            EscState::Dcs => {
                // Swallow DCS body; BEL or ESC ends (ESC \ = ST).
                if b == 0x07 {
                    self.esc = EscState::Ground;
                } else if b == 0x1b {
                    self.esc = EscState::Esc; // next `\` completes ST; other → Esc handler
                }
            }
            EscState::Csi => match b {
                // Private / intermediate parameter bytes at sequence start.
                b'?' | b'>' | b'=' | b'<'
                    if !self.csi_has_num && self.csi_params.is_empty() && self.csi_prefix == 0 =>
                {
                    self.csi_prefix = b;
                    self.csi_priv = b == b'?' || b == b'>' || b == b'=' || b == b'<';
                }
                // Intermediate bytes (0x20–0x2F) — e.g. `$` in DECRQM. Stay in CSI.
                0x20..=0x2f => {}
                b'0'..=b'9' => {
                    self.csi_has_num = true;
                    self.csi_num = self.csi_num.saturating_mul(10).saturating_add((b - b'0') as u16);
                }
                b';' => {
                    self.csi_params.push(if self.csi_has_num { self.csi_num } else { 0 });
                    self.csi_num = 0;
                    self.csi_has_num = false;
                }
                // Final byte 0x40–0x7E (ECMA-48).
                0x40..=0x7e => {
                    self.csi_params.push(if self.csi_has_num { self.csi_num } else { 0 });
                    let params = std::mem::take(&mut self.csi_params);
                    let priv_ = self.csi_priv;
                    let prefix = self.csi_prefix;
                    self.csi_priv = false;
                    self.csi_prefix = 0;
                    self.esc = EscState::Ground;
                    if priv_ || prefix != 0 {
                        self.exec_priv_csi(grid, b, &params, prefix);
                    } else {
                        self.exec_csi(grid, b, &params);
                    }
                }
                // Cancel on C0 (except we already handled digits etc.).
                _ => {
                    self.esc = EscState::Ground;
                    self.csi_priv = false;
                    self.csi_prefix = 0;
                    self.csi_params.clear();
                    self.csi_num = 0;
                    self.csi_has_num = false;
                }
            },
        }
    }

    fn exec_priv_csi(
        &mut self,
        grid: &mut CellGrid,
        final_byte: u8,
        params: &[u16],
        prefix: u8,
    ) {
        match (prefix, final_byte) {
            // CSI ? … h/l — DEC private modes
            (b'?' | 0, b'h') => {
                for &p in params {
                    self.set_priv_mode(grid, p, true);
                }
            }
            (b'?' | 0, b'l') => {
                for &p in params {
                    self.set_priv_mode(grid, p, false);
                }
            }
            // CSI c / CSI 0 c — primary DA
            (0, b'c') => {
                // VT100-ish: ESC [ ? 1 ; 2 c
                self.reply(b"\x1b[?1;2c");
            }
            // CSI > c / CSI > 0 c — secondary DA
            (b'>', b'c') => {
                // xterm-like: ESC [ > 0 ; 100 ; 0 c
                self.reply(b"\x1b[>0;100;0c");
            }
            // CSI ? … n — DSR private (e.g. DECXCPR). Report none / ignore.
            (b'?', b'n') => {}
            // CSI ? … $ p — DECRQM: report mode as reset (2) so apps don't hang.
            // Final is `p` after intermediate `$` (already consumed).
            (b'?', b'p') => {
                let mode = params.first().copied().unwrap_or(0);
                // ESC [ ? mode ; 2 $ y  (2 = reset)
                let s = format!("\x1b[?{mode};2$y");
                self.reply(s.as_bytes());
            }
            _ => {}
        }
    }

    fn set_priv_mode(&mut self, grid: &mut CellGrid, mode: u16, set: bool) {
        match mode {
            // Show / hide cursor
            25 => self.cursor_visible = set,
            // Alternate screen (xterm 1049 saves cursor; 47 is classic)
            47 | 1047 | 1049 => {
                if set {
                    self.enter_alt_screen(grid);
                } else {
                    self.leave_alt_screen(grid);
                }
            }
            _ => {}
        }
    }

    fn enter_alt_screen(&mut self, grid: &mut CellGrid) {
        if self.primary_backup.is_none() {
            self.primary_backup = Some(grid.clone());
            // Fresh alt buffer (common for full-screen apps).
            let cols = grid.cols();
            let rows = grid.rows();
            *grid = CellGrid::new(cols, rows);
            grid.suppress_scrollback = true;
            self.scroll_region = None;
        }
    }

    fn leave_alt_screen(&mut self, grid: &mut CellGrid) {
        if let Some(primary) = self.primary_backup.take() {
            *grid = primary;
            // Restored primary keeps its own suppress flag (false).
        }
        self.scroll_region = None;
    }

    fn exec_csi(&mut self, grid: &mut CellGrid, final_byte: u8, params: &[u16]) {
        let p = |i: usize, default: u16| {
            params
                .get(i)
                .copied()
                .filter(|&v| v != 0)
                .unwrap_or(default)
        };
        match final_byte {
            b'c' => {
                // Primary Device Attributes (CSI c / CSI 0 c)
                self.reply(b"\x1b[?1;2c");
            }
            b'n' => {
                // DSR — status report. CSI 5 n → OK; CSI 6 n → cursor pos.
                let what = params.first().copied().unwrap_or(0);
                if what == 5 {
                    self.reply(b"\x1b[0n");
                } else if what == 6 {
                    let c = grid.cursor();
                    let s = format!("\x1b[{};{}R", c.row + 1, c.col + 1);
                    self.reply(s.as_bytes());
                }
            }
            b'm' => self.sgr(grid, params),
            b'H' | b'f' => {
                let row = p(0, 1).saturating_sub(1);
                let col = p(1, 1).saturating_sub(1);
                grid.set_cursor(col, row);
            }
            b'A' => {
                let n = p(0, 1);
                let c = grid.cursor();
                grid.set_cursor(c.col, c.row.saturating_sub(n));
            }
            b'B' => {
                let n = p(0, 1);
                let c = grid.cursor();
                grid.set_cursor(c.col, c.row.saturating_add(n));
            }
            b'C' => {
                let n = p(0, 1);
                let c = grid.cursor();
                grid.set_cursor(c.col.saturating_add(n), c.row);
            }
            b'D' => {
                let n = p(0, 1);
                let c = grid.cursor();
                grid.set_cursor(c.col.saturating_sub(n), c.row);
            }
            b'G' => {
                // CHA — cursor horizontal absolute (1-based)
                let col = p(0, 1).saturating_sub(1);
                let c = grid.cursor();
                grid.set_cursor(col, c.row);
            }
            b'd' => {
                // VPA — vertical position absolute (1-based)
                let row = p(0, 1).saturating_sub(1);
                let c = grid.cursor();
                grid.set_cursor(c.col, row);
            }
            b'J' => {
                // ED — erase in display
                let mode = params.first().copied().unwrap_or(0);
                if mode == 2 || mode == 3 {
                    // Full clear: blank cells but keep cursor at origin is common for 2J.
                    // xterm leaves cursor where it is for ED; many apps follow with CUP.
                    // Preserve prior behavior of CSI 2J resetting cursor via clear().
                    if mode == 2 {
                        grid.clear();
                    } else {
                        // 3J also clears scrollback (we have none) — blank only.
                        grid.erase_in_display(3);
                    }
                } else {
                    grid.erase_in_display(mode);
                }
            }
            b'K' => {
                // EL — erase in line
                let mode = params.first().copied().unwrap_or(0);
                grid.erase_in_line(mode);
            }
            b'r' => {
                // DECSTBM — set scroll region (1-based inclusive)
                let top = p(0, 1);
                let bottom = params
                    .get(1)
                    .copied()
                    .filter(|&v| v != 0)
                    .unwrap_or(grid.rows());
                if top < bottom && bottom <= grid.rows() {
                    // Full-screen region is stored as None.
                    if top == 1 && bottom == grid.rows() {
                        self.scroll_region = None;
                    } else {
                        self.scroll_region = Some((top, bottom));
                    }
                } else {
                    self.scroll_region = None;
                }
                // Cursor home on region set (VT100).
                grid.set_cursor(0, 0);
            }
            b'S' => {
                // SU — scroll up within region
                let n = p(0, 1) as usize;
                let (top, bottom) = self.region_0based(grid);
                grid.scroll_region_up(top, bottom, n);
            }
            b'T' => {
                // SD — scroll down within region
                let n = p(0, 1) as usize;
                let (top, bottom) = self.region_0based(grid);
                grid.scroll_region_down(top, bottom, n);
            }
            b'L' => {
                // IL — insert lines at cursor row (within region, simplified: scroll down from cursor)
                let n = p(0, 1) as usize;
                let c = grid.cursor();
                let (top, bottom) = self.region_0based(grid);
                if c.row >= top && c.row <= bottom {
                    grid.scroll_region_down(c.row, bottom, n);
                }
            }
            b'M' => {
                // DL — delete lines at cursor row
                let n = p(0, 1) as usize;
                let c = grid.cursor();
                let (top, bottom) = self.region_0based(grid);
                if c.row >= top && c.row <= bottom {
                    grid.scroll_region_up(c.row, bottom, n);
                }
            }
            _ => {}
        }
    }

    /// 0-based inclusive scroll region (full screen when unset).
    fn region_0based(&self, grid: &CellGrid) -> (u16, u16) {
        match self.scroll_region {
            Some((top, bottom)) => {
                let top = top.saturating_sub(1).min(grid.rows().saturating_sub(1));
                let bottom = bottom.saturating_sub(1).min(grid.rows().saturating_sub(1));
                if top <= bottom {
                    (top, bottom)
                } else {
                    (0, grid.rows().saturating_sub(1))
                }
            }
            None => (0, grid.rows().saturating_sub(1)),
        }
    }

    /// LF / index: move down; scroll region when at bottom. Column → 0 (matches prior `newline`).
    fn linefeed(&mut self, grid: &mut CellGrid) {
        let c = grid.cursor();
        let (top, bottom) = self.region_0based(grid);
        if c.row >= bottom {
            grid.scroll_region_up(top, bottom, 1);
            grid.set_cursor(0, bottom);
        } else {
            let next = (c.row + 1).min(grid.rows().saturating_sub(1));
            grid.set_cursor(0, next);
        }
    }

    fn index_down(&mut self, grid: &mut CellGrid) {
        let c = grid.cursor();
        let (top, bottom) = self.region_0based(grid);
        if c.row >= bottom {
            grid.scroll_region_up(top, bottom, 1);
        } else {
            grid.set_cursor(c.col, c.row + 1);
        }
    }

    fn index_up(&mut self, grid: &mut CellGrid) {
        let c = grid.cursor();
        let (top, bottom) = self.region_0based(grid);
        if c.row <= top {
            grid.scroll_region_down(top, bottom, 1);
        } else {
            grid.set_cursor(c.col, c.row.saturating_sub(1));
        }
    }

    fn sgr(&mut self, grid: &mut CellGrid, params: &[u16]) {
        if params.is_empty() || (params.len() == 1 && params[0] == 0) {
            if self.reverse {
                // Leave reverse without double-swapping: reset pen fully.
                self.reverse = false;
            }
            grid.reset_pen();
            return;
        }
        let mut i = 0;
        while i < params.len() {
            match params[i] {
                0 => {
                    self.reverse = false;
                    grid.reset_pen();
                }
                1 => {} // bold — ignore
                7 => {
                    if !self.reverse {
                        self.reverse = true;
                        grid.swap_pen_fg_bg();
                    }
                }
                27 => {
                    if self.reverse {
                        self.reverse = false;
                        grid.swap_pen_fg_bg();
                    }
                }
                // Foreground standard
                30 => self.set_fg(grid, [0.1, 0.1, 0.1]),
                31 => self.set_fg(grid, theme::ERR),
                32 => self.set_fg(grid, theme::JADE),
                33 => self.set_fg(grid, [1.0, 0.72, 0.3]),
                34 => self.set_fg(grid, [0.4, 0.7, 1.0]),
                35 => self.set_fg(grid, [0.85, 0.5, 0.9]),
                36 => self.set_fg(grid, [0.4, 0.9, 0.9]),
                37 | 39 => self.set_fg(grid, theme::FG),
                // Bright foreground
                90 => self.set_fg(grid, theme::DIM),
                91 => self.set_fg(grid, theme::ERR),
                92 => self.set_fg(grid, theme::JADE),
                93 => self.set_fg(grid, [1.0, 0.85, 0.4]),
                94 => self.set_fg(grid, [0.5, 0.75, 1.0]),
                95 => self.set_fg(grid, [0.9, 0.6, 0.95]),
                96 => self.set_fg(grid, [0.5, 0.95, 0.95]),
                97 => self.set_fg(grid, [1.0, 1.0, 1.0]),
                // Background standard
                40 => self.set_bg(grid, Some([0.05, 0.05, 0.05])),
                41 => self.set_bg(grid, Some(theme::ERR)),
                42 => self.set_bg(grid, Some(theme::JADE)),
                43 => self.set_bg(grid, Some([1.0, 0.72, 0.3])),
                44 => self.set_bg(grid, Some([0.4, 0.7, 1.0])),
                45 => self.set_bg(grid, Some([0.85, 0.5, 0.9])),
                46 => self.set_bg(grid, Some([0.4, 0.9, 0.9])),
                47 => self.set_bg(grid, Some(theme::FG)),
                49 => self.set_bg(grid, None),
                // Bright background
                100 => self.set_bg(grid, Some(theme::DIM)),
                101 => self.set_bg(grid, Some(theme::ERR)),
                102 => self.set_bg(grid, Some(theme::JADE)),
                103 => self.set_bg(grid, Some([1.0, 0.85, 0.4])),
                104 => self.set_bg(grid, Some([0.5, 0.75, 1.0])),
                105 => self.set_bg(grid, Some([0.9, 0.6, 0.95])),
                106 => self.set_bg(grid, Some([0.5, 0.95, 0.95])),
                107 => self.set_bg(grid, Some([1.0, 1.0, 1.0])),
                // 38 / 48 extended color
                38 if params.get(i + 1) == Some(&5) && params.len() > i + 2 => {
                    let idx = params[i + 2];
                    self.set_fg(grid, xterm256(idx));
                    i += 2;
                }
                38 if params.get(i + 1) == Some(&2) && params.len() > i + 4 => {
                    let r = params[i + 2] as f32 / 255.0;
                    let g = params[i + 3] as f32 / 255.0;
                    let b = params[i + 4] as f32 / 255.0;
                    self.set_fg(grid, [r, g, b]);
                    i += 4;
                }
                48 if params.get(i + 1) == Some(&5) && params.len() > i + 2 => {
                    let idx = params[i + 2];
                    self.set_bg(grid, Some(xterm256(idx)));
                    i += 2;
                }
                48 if params.get(i + 1) == Some(&2) && params.len() > i + 4 => {
                    let r = params[i + 2] as f32 / 255.0;
                    let g = params[i + 3] as f32 / 255.0;
                    let b = params[i + 4] as f32 / 255.0;
                    self.set_bg(grid, Some([r, g, b]));
                    i += 4;
                }
                _ => {}
            }
            i += 1;
        }
    }

    /// Apply foreground respecting reverse video.
    fn set_fg(&self, grid: &mut CellGrid, color: [f32; 3]) {
        if self.reverse {
            grid.set_bg(Some(color));
        } else {
            grid.set_fg(color);
        }
    }

    /// Apply background respecting reverse video.
    fn set_bg(&self, grid: &mut CellGrid, color: Option<[f32; 3]>) {
        if self.reverse {
            grid.set_fg(color.unwrap_or(theme::BG));
        } else {
            grid.set_bg(color);
        }
    }
}

fn utf8_width(b: u8) -> usize {
    if b < 0x80 {
        1
    } else if b & 0xe0 == 0xc0 {
        2
    } else if b & 0xf0 == 0xe0 {
        3
    } else if b & 0xf8 == 0xf0 {
        4
    } else {
        0
    }
}

/// Parse OSC payload for cwd: `7878;cwd=<path>` or `7;file://...`.
fn parse_cwd_osc_payload(payload: &[u8]) -> Option<String> {
    let s = std::str::from_utf8(payload).ok()?.trim();
    if let Some(rest) = s.strip_prefix("7878;cwd=") {
        let p = rest.trim();
        if !p.is_empty() {
            return Some(p.to_string());
        }
        return None;
    }
    if let Some(uri) = s.strip_prefix("7;") {
        return file_uri_path(uri.trim());
    }
    None
}

/// Parse OSC 0 / 2 window title: `0;<title>` or `2;<title>` (also accepts `1;`).
///
/// Empty titles are ignored so a reset sequence does not blank the pane label.
fn parse_title_osc_payload(payload: &[u8]) -> Option<String> {
    let s = std::str::from_utf8(payload).ok()?;
    let (code, rest) = s.split_once(';')?;
    match code {
        "0" | "1" | "2" => {
            let title = rest.trim_end_matches(['\u{7}', '\0']);
            // Keep internal whitespace; only reject fully empty.
            if title.is_empty() {
                None
            } else {
                Some(title.to_string())
            }
        }
        _ => None,
    }
}

fn file_uri_path(uri: &str) -> Option<String> {
    let uri = uri.trim();
    if !uri.starts_with("file:") {
        return None;
    }
    let mut rest = uri.trim_start_matches("file:");
    rest = rest.trim_start_matches("//");
    // Drop host if present (file://localhost/path or file:///path)
    if let Some(slash) = rest.find('/') {
        let host = &rest[..slash];
        let path = &rest[slash..];
        if !host.is_empty()
            && !host.eq_ignore_ascii_case("localhost")
            && host != "127.0.0.1"
        {
            // UNC-ish: skip for chrome macOS path display
            return Some(format!("//{host}{path}"));
        }
        rest = path;
    }
    if rest.is_empty() {
        return None;
    }
    // file:///Users/foo → /Users/foo
    if !rest.starts_with('/') {
        return Some(format!("/{rest}"));
    }
    Some(rest.to_string())
}

fn xterm256(idx: u16) -> [f32; 3] {
    match idx {
        0 => [0.0, 0.0, 0.0],
        1 | 9 => theme::ERR,
        2 | 10 => theme::JADE,
        7 | 15 => theme::FG,
        8 => theme::DIM,
        n if (16..=231).contains(&n) => {
            let n = n - 16;
            let r = n / 36;
            let g = (n % 36) / 6;
            let b = n % 6;
            let c = |v: u16| {
                if v == 0 {
                    0.0
                } else {
                    (v as f32 * 40.0 + 55.0) / 255.0
                }
            };
            [c(r), c(g), c(b)]
        }
        n if (232..=255).contains(&n) => {
            let v = ((n - 232) as f32 * 10.0 + 8.0) / 255.0;
            [v, v, v]
        }
        _ => theme::FG,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cells::theme;

    #[test]
    fn osc7_file_uri_sets_cwd() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        dec.feed(
            &mut grid,
            b"\x1b]7;file:///Users/stephen/projects\x07",
        );
        assert_eq!(
            dec.take_cwd().as_deref(),
            Some("/Users/stephen/projects")
        );
    }

    #[test]
    fn primary_da_replies_not_printed() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(40, 5);
        dec.feed(&mut grid, b"\x1b[0c");
        let replies = dec.take_replies();
        assert_eq!(replies.len(), 1);
        assert_eq!(replies[0], b"\x1b[?1;2c");
        // Must not dump into the cell grid
        assert!(grid.snapshot_strings().iter().all(|s| s.is_empty()));
    }

    #[test]
    fn csi_intermediate_does_not_print_final() {
        // Old bug: `$` dropped CSI to Ground, then `p` printed as text.
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(40, 5);
        dec.feed(&mut grid, b"\x1b[?2026$p");
        let snap = grid.snapshot_strings();
        assert!(!snap[0].contains('p'), "got {:?}", snap[0]);
        let replies = dec.take_replies();
        assert!(!replies.is_empty());
    }

    #[test]
    fn alt_screen_sets_suppress_scrollback() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        dec.feed(&mut grid, b"\x1b[?1049h");
        assert!(dec.on_alt_screen());
        assert!(grid.suppress_scrollback);
        dec.feed(&mut grid, b"\x1b[?1049l");
        assert!(!dec.on_alt_screen());
    }

    #[test]
    fn osc_7878_cwd() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        dec.feed(&mut grid, b"\x1b]7878;cwd=/tmp/demo\x07");
        assert_eq!(dec.take_cwd().as_deref(), Some("/tmp/demo"));
    }

    #[test]
    fn osc_0_and_2_set_title() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        dec.feed(&mut grid, b"\x1b]0;shell - main\x07");
        assert_eq!(dec.take_title().as_deref(), Some("shell - main"));
        assert!(dec.take_title().is_none());

        dec.feed(&mut grid, b"\x1b]2;nvim\x07");
        assert_eq!(dec.take_title().as_deref(), Some("nvim"));
    }

    #[test]
    fn osc_title_st_terminator() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        // OSC 2 ... ST (ESC \)
        dec.feed(&mut grid, b"\x1b]2;via-st\x1b\\");
        assert_eq!(dec.take_title().as_deref(), Some("via-st"));
    }

    #[test]
    fn osc_title_does_not_clobber_cwd() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        dec.feed(&mut grid, b"\x1b]2;title-only\x07");
        assert_eq!(dec.take_title().as_deref(), Some("title-only"));
        assert!(dec.take_cwd().is_none());
        dec.feed(&mut grid, b"\x1b]7;file:///tmp\x07");
        assert_eq!(dec.take_cwd().as_deref(), Some("/tmp"));
        assert!(dec.take_title().is_none());
    }

    #[test]
    fn sgr_bg_41_sets_bg_on_next_chars() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        // ESC[41m = red background, then "hi", then reset
        dec.feed(&mut grid, b"\x1b[41mhi\x1b[0m");
        let c0 = grid.cell_at(0, 0).unwrap();
        let c1 = grid.cell_at(1, 0).unwrap();
        assert_eq!(c0.ch, 'h');
        assert_eq!(c1.ch, 'i');
        assert_eq!(c0.bg, Some(theme::ERR));
        assert_eq!(c1.bg, Some(theme::ERR));
        // After reset, next char has no bg
        dec.feed(&mut grid, b"x");
        let cx = grid.cell_at(2, 0).unwrap();
        assert_eq!(cx.ch, 'x');
        assert_eq!(cx.bg, None);
    }

    #[test]
    fn csi_2j_clears_screen() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(10, 3);
        dec.feed(&mut grid, b"hello\nworld");
        assert_eq!(grid.snapshot_strings()[0], "hello");
        dec.feed(&mut grid, b"\x1b[2J");
        let snap = grid.snapshot_strings();
        assert!(snap.iter().all(|s| s.is_empty()), "expected blank grid, got {snap:?}");
        assert_eq!(grid.cursor().col, 0);
        assert_eq!(grid.cursor().row, 0);
    }

    #[test]
    fn alt_screen_enter_leave_restores_content() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        dec.feed(&mut grid, b"primary");
        assert_eq!(grid.snapshot_strings()[0], "primary");
        assert!(!dec.on_alt_screen());

        // Enter alt screen
        dec.feed(&mut grid, b"\x1b[?1049h");
        assert!(dec.on_alt_screen());
        // Alt starts clear
        assert!(grid.snapshot_strings().iter().all(|s| s.is_empty()));
        dec.feed(&mut grid, b"alt-buf");
        assert_eq!(grid.snapshot_strings()[0], "alt-buf");

        // Leave alt screen — primary restored
        dec.feed(&mut grid, b"\x1b[?1049l");
        assert!(!dec.on_alt_screen());
        assert_eq!(grid.snapshot_strings()[0], "primary");
    }

    #[test]
    fn cursor_visible_decset() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(10, 3);
        assert!(dec.cursor_visible);
        dec.feed(&mut grid, b"\x1b[?25l");
        assert!(!dec.cursor_visible);
        dec.feed(&mut grid, b"\x1b[?25h");
        assert!(dec.cursor_visible);
    }

    #[test]
    fn scroll_region_up_on_linefeed() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(10, 5);
        // Region rows 2-4 (1-based) → 0-based 1..=3
        dec.feed(&mut grid, b"\x1b[2;4r");
        // Write three lines that fill the region, then one more to scroll
        // After CSI r cursor is at 0,0 — move into region
        dec.feed(&mut grid, b"\x1b[2;1H");
        dec.feed(&mut grid, b"a\nb\nc\nd");
        let snap = grid.snapshot_strings();
        // Row 0 untouched (outside region)
        assert_eq!(snap[0], "");
        // Region scrolled: a gone, b,c,d in rows 1..=3
        assert_eq!(snap[1], "b");
        assert_eq!(snap[2], "c");
        assert_eq!(snap[3], "d");
    }
}

#[cfg(test)]
mod cell_region_tests {
    use crate::cells::CellGrid;

    #[test]
    fn scroll_region_up_blanks_bottom() {
        let mut g = CellGrid::new(4, 4);
        g.set_cursor(0, 0);
        g.put_str("aaaa");
        g.set_cursor(0, 1);
        g.put_str("bbbb");
        g.set_cursor(0, 2);
        g.put_str("cccc");
        g.set_cursor(0, 3);
        g.put_str("dddd");
        g.scroll_region_up(1, 2, 1);
        let snap = g.snapshot_strings();
        assert_eq!(snap[0], "aaaa");
        assert_eq!(snap[1], "cccc");
        assert_eq!(snap[2], "");
        assert_eq!(snap[3], "dddd");
    }
}
