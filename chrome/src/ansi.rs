//! Minimal ANSI / VT stream → [`CellGrid`].
//!
//! Enough for interactive shells (prompt, colors, cursor, clear, alt screen).
//! Not a full VT100 / xterm.

use crate::cells::{theme, CellGrid};
use crate::kitty::KittyKeyboard;
use crate::kitty_gfx::GraphicsStore;

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
    /// DECSET 1 — application cursor keys (`ESC OA` vs `ESC [A`).
    pub app_cursor: bool,
    /// Any xterm mouse report mode (9 / 1000 / 1002 / 1003).
    pub mouse_tracking: bool,
    /// CSI ? 1002 h — motion while a button is held.
    pub mouse_drag: bool,
    /// CSI ? 1003 h — all motion (hover).
    pub mouse_any: bool,
    /// CSI ? 1006 h — SGR mouse encoding.
    pub mouse_sgr: bool,
    /// Kitty keyboard progressive-enhancement flags (`CSI ?/=/</> u`).
    kitty: KittyKeyboard,
    /// Kitty graphics protocol (APC `ESC _ G … ST`).
    gfx: GraphicsStore,
    /// APC payload (between ESC _ and ST). Graphics commands start with `G`.
    apc_buf: Vec<u8>,
    apc_overflow: bool,
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
    /// Application Program Command (`ESC _ … ST`) — Kitty graphics uses this
    /// (`ESC _ G a=… ST`). Must not print the payload.
    Apc,
    /// Privacy Message (`ESC ^ … ST`) — swallow like APC/DCS.
    Pm,
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
            app_cursor: false,
            mouse_tracking: false,
            mouse_drag: false,
            mouse_any: false,
            mouse_sgr: false,
            kitty: KittyKeyboard::new(),
            gfx: GraphicsStore::new(),
            apc_buf: Vec::new(),
            apc_overflow: false,
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

    /// Kitty disambiguate / all-keys-as-escapes is on (child pushed flags).
    pub fn kitty_active(&self) -> bool {
        self.kitty.active()
    }

    /// Kitty graphics store (images + placements) for this PTY.
    pub fn graphics(&self) -> &GraphicsStore {
        &self.gfx
    }

    /// Physical cell / pane metrics used by CSI 14/16/18 t and image placement.
    pub fn set_pixel_metrics(
        &mut self,
        cell_w: u32,
        cell_h: u32,
        area_w: u32,
        area_h: u32,
        cols: u32,
        rows: u32,
    ) {
        self.gfx
            .set_pixel_metrics(cell_w, cell_h, area_w, area_h, cols, rows);
    }

    fn finish_apc(&mut self, grid: &mut CellGrid) {
        let buf = std::mem::take(&mut self.apc_buf);
        let overflow = self.apc_overflow;
        self.apc_overflow = false;
        if overflow || buf.is_empty() {
            return;
        }
        if buf[0] != b'G' {
            return;
        }
        let replies = self.gfx.execute(&buf[1..], grid);
        for r in replies {
            self.reply(&r);
        }
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
                // APC — Kitty graphics: ESC _ G a=d,d=i,i=1,q=2 ST
                b'_' => {
                    self.apc_buf.clear();
                    self.apc_overflow = false;
                    self.esc = EscState::Apc;
                }
                // PM — privacy message (rare); same swallow rule.
                b'^' => self.esc = EscState::Pm,
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
                // ST alone (ESC \) — no-op if we got here without open string.
                b'\\' => self.esc = EscState::Ground,
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
            // DCS / PM: swallow body until BEL or ESC (then ST via `\`).
            EscState::Dcs | EscState::Pm => {
                if b == 0x07 {
                    self.esc = EscState::Ground;
                } else if b == 0x1b {
                    // Next `\` = ST terminator; any other sequence restarts Esc.
                    self.esc = EscState::Esc;
                }
                // else: discard payload byte (never put_char)
            }
            EscState::Apc => {
                if b == 0x07 {
                    self.finish_apc(grid);
                    self.esc = EscState::Ground;
                } else if b == 0x1b {
                    // ST is ESC \; finish now so DA that follows still sees the reply.
                    self.finish_apc(grid);
                    self.esc = EscState::Esc;
                } else if !self.apc_overflow {
                    if self.apc_buf.len() >= 8 * 1024 * 1024 {
                        self.apc_overflow = true;
                        self.apc_buf.clear();
                    } else {
                        self.apc_buf.push(b);
                    }
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
                    self.csi_num = self
                        .csi_num
                        .saturating_mul(10)
                        .saturating_add((b - b'0') as u16);
                }
                b';' => {
                    self.csi_params
                        .push(if self.csi_has_num { self.csi_num } else { 0 });
                    self.csi_num = 0;
                    self.csi_has_num = false;
                }
                // Final byte 0x40–0x7E (ECMA-48).
                0x40..=0x7e => {
                    self.csi_params
                        .push(if self.csi_has_num { self.csi_num } else { 0 });
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

    fn exec_priv_csi(&mut self, grid: &mut CellGrid, final_byte: u8, params: &[u16], prefix: u8) {
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
            // CSI ? u / CSI ? 0 u — Kitty keyboard progressive enhancement query.
            (b'?', b'u') => {
                self.reply(&self.kitty.query_reply());
            }
            // CSI > flags u — push
            (b'>', b'u') => {
                let flags = params.first().copied().unwrap_or(0);
                self.kitty.push(flags);
            }
            // CSI < n u — pop (n default 1; parser yields 0 when omitted)
            (b'<', b'u') => {
                let n = params.first().copied().unwrap_or(1);
                self.kitty.pop(if n == 0 { 1 } else { n });
            }
            // CSI = flags ; mode u — set / or / clear
            (b'=', b'u') => {
                let flags = params.first().copied().unwrap_or(0);
                let mode = params.get(1).copied().unwrap_or(1);
                self.kitty.apply(flags, mode);
            }
            // CSI ? … $ p — DECRQM. 1 = set, 2 = reset.
            (b'?', b'p') => {
                let mode = params.first().copied().unwrap_or(0);
                let val = if self.priv_mode_is_set(mode) { 1 } else { 2 };
                let s = format!("\x1b[?{mode};{val}$y");
                self.reply(s.as_bytes());
            }
            _ => {}
        }
    }

    fn priv_mode_is_set(&self, mode: u16) -> bool {
        match mode {
            1 => self.app_cursor,
            25 => self.cursor_visible,
            9 | 1000 => self.mouse_tracking,
            1002 => self.mouse_drag,
            1003 => self.mouse_any,
            1006 => self.mouse_sgr,
            47 | 1047 | 1049 => self.on_alt_screen(),
            _ => false,
        }
    }

    fn set_priv_mode(&mut self, grid: &mut CellGrid, mode: u16, set: bool) {
        match mode {
            1 => self.app_cursor = set,
            // Show / hide cursor
            25 => self.cursor_visible = set,
            // X10 / VT200 / drag / any-event mouse
            9 | 1000 => self.mouse_tracking = set,
            1002 => {
                self.mouse_drag = set;
                if set {
                    self.mouse_tracking = true;
                }
            }
            1003 => {
                self.mouse_any = set;
                if set {
                    self.mouse_tracking = true;
                }
            }
            1006 => self.mouse_sgr = set,
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
            self.gfx.clear_placements();
        }
    }

    fn leave_alt_screen(&mut self, grid: &mut CellGrid) {
        if let Some(primary) = self.primary_backup.take() {
            *grid = primary;
            // Restored primary keeps its own suppress flag (false).
        }
        self.scroll_region = None;
        self.gfx.clear_placements();
        // Don't leak SGR mouse reports into the shell if the TUI forgot ?1000l.
        self.mouse_tracking = false;
        self.mouse_drag = false;
        self.mouse_any = false;
        self.mouse_sgr = false;
        self.app_cursor = false;
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
            b't' => self.xtwinops(params, grid),
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

    /// xterm window ops. Reports used by terminal-browser for pane pixels.
    fn xtwinops(&mut self, params: &[u16], grid: &CellGrid) {
        let what = params.first().copied().unwrap_or(0);
        match what {
            14 => {
                // CSI 14 t → CSI 4 ; height ; width t  (physical px)
                let (w, h) = self.gfx.area_px;
                let s = format!("\x1b[4;{h};{w}t");
                self.reply(s.as_bytes());
            }
            16 => {
                // CSI 16 t → CSI 6 ; cellheight ; cellwidth t
                let (w, h) = self.gfx.cell_px;
                let s = format!("\x1b[6;{h};{w}t");
                self.reply(s.as_bytes());
            }
            18 => {
                // CSI 18 t → CSI 8 ; rows ; cols t
                let (cols, rows) = self.gfx.chars;
                let cols = if cols == 0 { grid.cols() as u32 } else { cols };
                let rows = if rows == 0 { grid.rows() as u32 } else { rows };
                let s = format!("\x1b[8;{rows};{cols}t");
                self.reply(s.as_bytes());
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
        if !host.is_empty() && !host.eq_ignore_ascii_case("localhost") && host != "127.0.0.1" {
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
        dec.feed(&mut grid, b"\x1b]7;file:///Users/stephen/projects\x07");
        assert_eq!(dec.take_cwd().as_deref(), Some("/Users/stephen/projects"));
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
    fn kitty_graphics_apc_not_printed() {
        // Grok (and product chrome) emit Kitty graphics: ESC _ G a=d,d=i,i=1,q=2 ST
        // Without APC handling this leaked as literal "Ga=d,d=i,i=1,q=2" into the TUI.
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(80, 5);
        dec.feed(&mut grid, b"hello\x1b_Ga=d,d=i,i=1,q=2\x1b\\world");
        let snap = grid.snapshot_strings();
        let row0 = &snap[0];
        assert!(
            !row0.contains("Ga=") && !row0.contains("q=2") && !row0.contains("d=i"),
            "kitty graphics payload leaked: {row0:?}"
        );
        assert!(row0.contains("hello"), "got {row0:?}");
        assert!(row0.contains("world"), "got {row0:?}");
    }

    #[test]
    fn kitty_graphics_query_replies_before_da() {
        // terminal-browser probe: APC query + CSI c. Needle is `Gi=4207;OK`.
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(40, 5);
        dec.feed(
            &mut grid,
            b"\x1b_Gi=4207,a=q,t=d,f=24,s=1,v=1;AAAA\x1b\\\x1b[c",
        );
        assert!(grid.snapshot_strings().iter().all(|s| s.is_empty()));
        let replies = dec.take_replies();
        let gfx = replies
            .iter()
            .find(|r| r.windows(8).any(|w| w == b"Gi=4207;"))
            .expect("graphics query reply");
        let s = String::from_utf8_lossy(gfx);
        let at = s.find("Gi=4207;").unwrap();
        assert!(s[at + 8..].starts_with("OK"), "{s}");
        assert!(
            replies.iter().any(|r| r == b"\x1b[?1;2c"),
            "DA after query: {replies:?}"
        );
        let gfx_i = replies
            .iter()
            .position(|r| r.windows(8).any(|w| w == b"Gi=4207;"))
            .unwrap();
        let da_i = replies.iter().position(|r| r == b"\x1b[?1;2c").unwrap();
        assert!(gfx_i < da_i, "query reply must beat DA");
    }

    #[test]
    fn xtwinops_reports_pixel_and_cell_size() {
        let mut dec = AnsiDecoder::new();
        dec.set_pixel_metrics(7, 14, 560, 336, 80, 24);
        let mut grid = CellGrid::new(80, 24);
        dec.feed(&mut grid, b"\x1b[14t\x1b[16t\x1b[18t");
        let replies = dec.take_replies();
        assert!(
            replies.iter().any(|r| r == b"\x1b[4;336;560t"),
            "CSI 14 t: {replies:?}"
        );
        assert!(
            replies.iter().any(|r| r == b"\x1b[6;14;7t"),
            "CSI 16 t: {replies:?}"
        );
        assert!(
            replies.iter().any(|r| r == b"\x1b[8;24;80t"),
            "CSI 18 t: {replies:?}"
        );
    }

    #[test]
    fn kitty_keyboard_query_silent() {
        // crossterm supports_keyboard_enhancement: CSI ? u then CSI c
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(40, 5);
        dec.feed(&mut grid, b"\x1b[?u\x1b[c");
        assert!(grid.snapshot_strings().iter().all(|s| s.is_empty()));
        // DA still replies
        let replies = dec.take_replies();
        assert!(replies.iter().any(|r| r == b"\x1b[?0u"));
        assert!(replies.iter().any(|r| r == b"\x1b[?1;2c"));
        assert!(!dec.kitty_active());
    }

    #[test]
    fn kitty_keyboard_push_query_pop() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(40, 5);
        // Grok: probe, then push disambiguate (CSI > 1 u).
        dec.feed(&mut grid, b"\x1b[?u\x1b[>1u");
        assert!(dec.kitty_active());
        dec.feed(&mut grid, b"\x1b[?u");
        let replies = dec.take_replies();
        assert!(
            replies.iter().any(|r| r == b"\x1b[?1u"),
            "query after push should report flags=1: {replies:?}"
        );
        dec.feed(&mut grid, b"\x1b[<u");
        assert!(!dec.kitty_active());
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
    fn mouse_modes_and_decrqm() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(20, 5);
        assert!(!dec.mouse_tracking);
        dec.feed(&mut grid, b"\x1b[?1000h\x1b[?1006h");
        assert!(dec.mouse_tracking);
        assert!(dec.mouse_sgr);
        dec.feed(&mut grid, b"\x1b[?1000$p");
        let replies = dec.take_replies();
        assert!(
            replies.iter().any(|r| r == b"\x1b[?1000;1$y"),
            "DECRQM should report set: {replies:?}"
        );
        dec.feed(&mut grid, b"\x1b[?1049h");
        assert!(dec.on_alt_screen());
        dec.feed(&mut grid, b"\x1b[?1049l");
        assert!(!dec.mouse_tracking);
        assert!(!dec.mouse_sgr);
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
    fn utf8_split_across_feeds_keeps_braille() {
        // ConPTY splits 3-byte runes. Must NOT from_utf8_lossy at the seam.
        let ch = '⣿';
        let mut enc = [0u8; 4];
        let n = ch.encode_utf8(&mut enc).len();
        assert!(n > 1);
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(10, 2);
        dec.feed(&mut grid, &enc[..1]);
        dec.feed(&mut grid, &enc[1..n]);
        assert_eq!(grid.snapshot_strings()[0], ch.to_string());
    }

    #[test]
    fn csi_2j_clears_screen() {
        let mut dec = AnsiDecoder::new();
        let mut grid = CellGrid::new(10, 3);
        dec.feed(&mut grid, b"hello\nworld");
        assert_eq!(grid.snapshot_strings()[0], "hello");
        dec.feed(&mut grid, b"\x1b[2J");
        let snap = grid.snapshot_strings();
        assert!(
            snap.iter().all(|s| s.is_empty()),
            "expected blank grid, got {snap:?}"
        );
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
