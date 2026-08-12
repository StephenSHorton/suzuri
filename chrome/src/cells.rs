//! Terminal cell grid — character cells with optional per-cell colors.
//!
//! Default VT pen: **inkstone** (bg near #050a07, fg #e8f5ee, jade #00e676).
//! Named chrome themes (nord, dracula, …) live in [`crate::theme`]; the
//! renderer should paint glass / selection / rain from
//! `settings.prefs.theme_colors()` (or `theme::colors(&prefs.theme)`), not
//! only these static cell defaults.

/// Inkstone palette as linear-ish RGB floats in 0..=1 (sRGB channel / 255).
///
/// Kept as **const** defaults for ANSI / new cells so VT tests stay stable.
/// For the active chrome theme, use [`crate::theme::colors`].
pub mod theme {
    use crate::theme::INKSTONE;

    /// Near #050a07
    pub const BG: [f32; 3] = INKSTONE.bg;
    /// #e8f5ee
    pub const FG: [f32; 3] = INKSTONE.fg;
    /// #00e676
    pub const JADE: [f32; 3] = INKSTONE.jade;
    /// Dim / secondary text (~#6b7c72)
    pub const DIM: [f32; 3] = INKSTONE.muted;
    /// Error red (~#ff5252)
    pub const ERR: [f32; 3] = INKSTONE.err;
}

/// One terminal cell.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Cell {
    pub ch: char,
    pub fg: [f32; 3],
    /// When `None`, renderer should use the panel / theme background.
    pub bg: Option<[f32; 3]>,
}

impl Cell {
    pub fn blank() -> Self {
        Self {
            ch: ' ',
            fg: theme::FG,
            bg: None,
        }
    }

    pub fn with_char(ch: char) -> Self {
        Self {
            ch,
            fg: theme::FG,
            bg: None,
        }
    }

    pub fn colored(ch: char, fg: [f32; 3]) -> Self {
        Self {
            ch,
            fg,
            bg: None,
        }
    }
}

impl Default for Cell {
    fn default() -> Self {
        Self::blank()
    }
}

/// Cursor position in the grid (column, row), 0-based.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Cursor {
    pub col: u16,
    pub row: u16,
}

/// Max scrollback rows retained when the viewport scrolls.
const MAX_SCROLLBACK: usize = 2000;

/// Logical-px scrollbar geometry for the terminal cell well (product-style).
#[derive(Clone, Copy, Debug, Default)]
pub struct ScrollbarGeom {
    pub visible: bool,
    /// Thumb top relative to track top (0 = top of track).
    pub thumb_y: f32,
    pub thumb_h: f32,
    pub track_h: f32,
}

/// Fixed-size cell buffer with a logical cursor + scrollback.
#[derive(Clone, Debug)]
pub struct CellGrid {
    cols: u16,
    rows: u16,
    cells: Vec<Cell>,
    cursor: Cursor,
    /// Active pen color for subsequent `put_*` calls.
    fg: [f32; 3],
    bg: Option<[f32; 3]>,
    /// Rows scrolled off the top (oldest first). Each row is `cols` cells.
    scrollback: Vec<Vec<Cell>>,
    /// When true (alt screen), rows scrolled off the top are discarded — not
    /// pushed into scrollback. Full-screen TUIs must not pollute history.
    pub suppress_scrollback: bool,

    /// Integer scroll target (rows above stick-bottom). Wheel/keys update this.
    view_offset: usize,
    /// Smoothed offset for paint (product `scrollback.visual` / `tickSmooth`).
    visual_offset: f32,
    /// After `clear`/`cls`, stick-bottom floors here (scroll-up still reaches pre-pin).
    /// Absolute scrollback index; `0` = no pin.
    scrollback_pin: usize,
}

/// Exponential ease rate for [`CellGrid::tick_scroll`] (~product k=16).
const SCROLL_EASE_K: f32 = 16.0;

impl CellGrid {
    pub fn new(cols: u16, rows: u16) -> Self {
        let cols = cols.max(1);
        let rows = rows.max(1);
        let n = (cols as usize) * (rows as usize);
        Self {
            cols,
            rows,
            cells: vec![Cell::blank(); n],
            cursor: Cursor::default(),
            fg: theme::FG,
            bg: None,
            scrollback: Vec::new(),
            suppress_scrollback: false,
            view_offset: 0,
            visual_offset: 0.0,
            scrollback_pin: 0,
        }
    }

    pub fn cols(&self) -> u16 {
        self.cols
    }

    pub fn rows(&self) -> u16 {
        self.rows
    }

    pub fn cursor(&self) -> Cursor {
        self.cursor
    }

    /// Integer scroll target (rows above stick-bottom).
    pub fn view_offset(&self) -> usize {
        self.view_offset
    }

    /// Smoothed scroll offset used for paint / scrollbar (may lag the target).
    pub fn visual_offset(&self) -> f32 {
        self.visual_offset
    }

    /// True while the smooth scroll offset is still catching up to the target.
    #[inline]
    pub fn scroll_animating(&self) -> bool {
        (self.visual_offset - self.view_offset as f32).abs() > 0.02
    }

    /// Ease `visual_offset` toward `view_offset`. Returns true if still moving.
    pub fn tick_scroll(&mut self, dt: f32) -> bool {
        let target = self.view_offset as f32;
        let dt = dt.clamp(0.0, 0.05);
        if dt <= 0.0 {
            return self.scroll_animating();
        }
        let prev = self.visual_offset;
        let alpha = 1.0 - (-SCROLL_EASE_K * dt).exp();
        self.visual_offset += (target - self.visual_offset) * alpha;
        if (target - self.visual_offset).abs() < 0.02 {
            self.visual_offset = target;
        }
        // Clamp to legal range if document shrank.
        let max = self.max_view_offset() as f32;
        if self.visual_offset > max {
            self.visual_offset = max;
        }
        if self.visual_offset < 0.0 {
            self.visual_offset = 0.0;
        }
        (self.visual_offset - prev).abs() > 0.001
    }

    /// Snap visual to the integer target (drag scrub / stick-bottom).
    pub fn snap_scroll_visual(&mut self) {
        self.visual_offset = self.view_offset as f32;
    }

    /// Number of rows retained in scrollback (oldest first).
    pub fn scrollback_len(&self) -> usize {
        self.scrollback.len()
    }

    /// Total absolute document lines = scrollback + live viewport rows.
    pub fn abs_line_count(&self) -> usize {
        self.scrollback.len() + self.rows as usize
    }

    /// Absolute document row for viewport row 0 (pin-aware stick-bottom).
    ///
    /// Uses smoothed [`visual_offset`] for paint. Stick-bottom composes post-pin
    /// scrollback + live extent so command blocks stay visible above shell
    /// output (product `viewWindow`). Scrolling up reveals pre-pin history.
    pub fn view_top_abs(&self) -> usize {
        let vis = self.visual_offset.max(0.0) as usize;
        self.stick_bottom_top().saturating_sub(vis)
    }

    /// Top absolute row when fully stuck to the bottom (offset 0).
    pub fn stick_bottom_top(&self) -> usize {
        let vh = self.rows as usize;
        let hist = self.scrollback.len();
        let pin = self.scrollback_pin.min(hist);
        // Product liveExtent: trailing blank PTY rows don't push history off-screen.
        let live_ext = self.live_extent();
        let post = hist.saturating_sub(pin) + live_ext;
        if post <= vh {
            // Short post-pin content (e.g. after clear): top-align at pin.
            pin
        } else {
            pin + (post - vh)
        }
    }

    /// Max `view_offset` (scroll until absolute row 0 is at the top).
    pub fn max_view_offset(&self) -> usize {
        self.stick_bottom_top()
    }

    /// Scrollbar thumb geometry for a track of height `track_h` (logical px).
    /// Product `scrollback.Scrollbar`: at bottom → thumb near bottom; oldest → top.
    pub fn scrollbar(&self, track_h: f32) -> ScrollbarGeom {
        let track_h = track_h.max(8.0);
        let max_off = self.max_view_offset();
        let vh = self.rows as usize;
        let hist = self.scrollback.len();
        let live = self.live_extent().max(1);
        let doc = hist + live;
        let visible = max_off >= 1 || (self.scrollback_pin > 0 && hist > 0);
        if !visible || doc <= 1 {
            return ScrollbarGeom {
                visible: false,
                thumb_y: 0.0,
                thumb_h: track_h,
                track_h,
            };
        }
        let den = doc.max(vh + 1);
        let mut ratio = vh as f32 / den as f32;
        if ratio > 1.0 {
            ratio = 1.0;
        }
        let thumb_h = (track_h * ratio).max(18.0).min(track_h);
        let travel = (track_h - thumb_h).max(0.0);
        let t = if max_off > 0 {
            (self.visual_offset / max_off as f32).clamp(0.0, 1.0)
        } else {
            0.0
        };
        // offset 0 = bottom → thumb at bottom; offset max = top → thumb at top.
        let thumb_y = travel * (1.0 - t);
        ScrollbarGeom {
            visible: true,
            thumb_y,
            thumb_h,
            track_h,
        }
    }

    /// Set scroll position from a 0..=1 fraction (0 = stick bottom, 1 = oldest).
    /// Snaps visual for responsive scrollbar scrubbing.
    pub fn set_scroll_fraction(&mut self, t: f32) {
        let max = self.max_view_offset();
        if max == 0 {
            self.view_offset = 0;
            self.visual_offset = 0.0;
            return;
        }
        let t = t.clamp(0.0, 1.0);
        self.view_offset = (t * max as f32).round() as usize;
        if self.view_offset > max {
            self.view_offset = max;
        }
        self.snap_scroll_visual();
    }

    /// Map a Y position within a track (0 at top) to scroll fraction.
    pub fn scroll_fraction_from_track_y(&self, y_in_track: f32, track_h: f32) -> f32 {
        let sb = self.scrollbar(track_h);
        if !sb.visible || sb.track_h <= 0.0 {
            return 0.0;
        }
        let travel = (sb.track_h - sb.thumb_h).max(1.0);
        // Thumb center targets y; product inverts so top of track = oldest.
        let y = y_in_track - sb.thumb_h * 0.5;
        let t_ui = (y / travel).clamp(0.0, 1.0);
        // UI y=0 (top) → fraction 1 (oldest); y=bottom → 0.
        1.0 - t_ui
    }

    /// Map a visible viewport row (0..rows) to an absolute document row.
    ///
    /// Absolute row 0 is the oldest scrollback line. Stick-bottom composes
    /// history + live; see [`view_top_abs`].
    pub fn viewport_to_abs(&self, row: u16) -> usize {
        self.view_top_abs() + row as usize
    }

    /// Map absolute document row → visible viewport row, if currently on-screen.
    pub fn abs_to_viewport(&self, abs_row: usize) -> Option<u16> {
        let top = self.view_top_abs();
        let bottom = top + self.rows as usize;
        if abs_row >= top && abs_row < bottom {
            Some((abs_row - top) as u16)
        } else {
            None
        }
    }

    /// Absolute document row of the cell-grid cursor (live region).
    pub fn cursor_abs_row(&self) -> usize {
        self.scrollback.len() + self.cursor.row as usize
    }

    /// Characters for an absolute document row (full width, no trim).
    /// Out-of-range rows yield a blank line of spaces.
    pub fn line_text_abs(&self, abs_row: usize) -> String {
        let cols = self.cols as usize;
        if abs_row < self.scrollback.len() {
            let row = &self.scrollback[abs_row];
            let mut s: String = row.iter().map(|c| c.ch).collect();
            while s.chars().count() < cols {
                s.push(' ');
            }
            // If scrollback row was wider (shouldn't happen), truncate.
            if s.chars().count() > cols {
                s = s.chars().take(cols).collect();
            }
            s
        } else {
            let live = abs_row - self.scrollback.len();
            if live < self.rows as usize {
                self.row_cells(live as u16).iter().map(|c| c.ch).collect()
            } else {
                " ".repeat(cols)
            }
        }
    }

    /// Cell at absolute document coordinates, if in range.
    pub fn cell_at_abs(&self, col: u16, abs_row: usize) -> Option<Cell> {
        if col >= self.cols {
            return None;
        }
        if abs_row < self.scrollback.len() {
            let row = &self.scrollback[abs_row];
            row.get(col as usize).copied()
        } else {
            let live = abs_row - self.scrollback.len();
            if live < self.rows as usize {
                self.cell_at(col, live as u16).copied()
            } else {
                None
            }
        }
    }

    /// Scroll the view into history (positive = up into scrollback).
    /// Updates the integer target; paint eases via [`tick_scroll`].
    pub fn scroll_view(&mut self, delta_rows: i32) {
        if delta_rows == 0 {
            return;
        }
        let max = self.max_view_offset();
        if delta_rows > 0 {
            self.view_offset = (self.view_offset + delta_rows as usize).min(max);
        } else {
            let down = (-delta_rows) as usize;
            self.view_offset = self.view_offset.saturating_sub(down);
        }
    }

    /// Jump back to the live bottom of the viewport (snaps visual).
    pub fn scroll_to_bottom(&mut self) {
        self.view_offset = 0;
        self.visual_offset = 0.0;
    }

    /// Stick-bottom pin after clear (product `pinHere`).
    pub fn scrollback_pin(&self) -> usize {
        self.scrollback_pin
    }

    /// Floor stick-bottom at current scrollback length (pre-pin history via scroll-up).
    pub fn pin_here(&mut self) {
        self.scrollback_pin = self.scrollback.len();
        self.view_offset = 0;
        self.visual_offset = 0.0;
    }

    /// How many leading live rows have content (trailing blank PTY rows omitted).
    /// Product `liveExtent` — used by [`commit_live`].
    pub fn live_extent(&self) -> usize {
        let rows = self.rows as usize;
        if rows == 0 {
            return 0;
        }
        let mut last = self.cursor.row as isize;
        for r in 0..rows {
            let row = self.row_cells(r as u16);
            if row.iter().any(|c| c.ch != ' ' && c.ch != '\0') {
                last = last.max(r as isize);
            }
        }
        if last < 0 {
            0
        } else {
            (last as usize) + 1
        }
    }

    /// Fold non-blank live rows into scrollback and blank the live grid (product `commitLive`).
    ///
    /// Skips when `on_alt_screen` (fullscreen TUI owns the grid). Returns committed
    /// line texts (trimmed) for host history meta. Does not write to the real PTY.
    pub fn commit_live(&mut self, on_alt_screen: bool) -> Vec<String> {
        if on_alt_screen {
            return Vec::new();
        }
        let extent = self.live_extent();
        let mut out = Vec::new();
        for r in 0..extent {
            let line: String = self
                .row_cells(r as u16)
                .iter()
                .map(|c| if c.ch == '\0' { ' ' } else { c.ch })
                .collect();
            let t = line.trim_end_matches([' ', '\t']).to_string();
            if t.trim().is_empty() {
                continue;
            }
            out.push(t.clone());
            self.push_scrollback_text(&t, None);
        }
        // Host-side clear live region only (shell stream will repaint).
        for cell in &mut self.cells {
            *cell = Cell::blank();
        }
        self.cursor = Cursor::default();
        self.reset_pen();
        self.view_offset = 0;
        self.visual_offset = 0.0;
        out
    }

    /// Cells for a visible row accounting for pin-aware stick-bottom + scroll.
    pub fn visible_row_cells(&self, row: u16) -> Vec<Cell> {
        let cols = self.cols as usize;
        let blank = vec![Cell::blank(); cols];
        if row >= self.rows {
            return blank;
        }
        let abs = self.viewport_to_abs(row);
        if abs < self.scrollback.len() {
            let mut r = self.scrollback[abs].clone();
            r.resize(cols, Cell::blank());
            r
        } else {
            let live = abs - self.scrollback.len();
            if live < self.rows as usize {
                self.row_cells(live as u16).to_vec()
            } else {
                blank
            }
        }
    }

    /// Append a finished row to scrollback (host command blocks, VT scroll).
    /// No-op when [`Self::suppress_scrollback`] (alt screen).
    pub(crate) fn push_scrollback_row(&mut self, row: Vec<Cell>) {
        if self.suppress_scrollback {
            return;
        }
        self.scrollback.push(row);
        if self.scrollback.len() > MAX_SCROLLBACK {
            let drop_n = self.scrollback.len() - MAX_SCROLLBACK;
            self.scrollback.drain(0..drop_n);
            self.view_offset = self.view_offset.saturating_sub(drop_n);
            self.scrollback_pin = self.scrollback_pin.saturating_sub(drop_n);
        }
        // Keep offset valid as document grows/shrinks (stick-bottom top changes).
        let max = self.max_view_offset();
        if self.view_offset > max {
            self.view_offset = max;
        }
        if self.visual_offset > max as f32 {
            self.visual_offset = max as f32;
        }
    }

    /// Live-grid row only (no scrollback composition). Used for alt-screen paint.
    pub fn live_row_cells(&self, row: u16) -> Vec<Cell> {
        let cols = self.cols as usize;
        if row >= self.rows {
            return vec![Cell::blank(); cols];
        }
        self.row_cells(row).to_vec()
    }

    /// Append a plain-text row to scrollback (pads/truncates to `cols`).
    pub fn push_scrollback_text(&mut self, text: &str, fg: Option<[f32; 3]>) {
        let cols = self.cols as usize;
        let fg = fg.unwrap_or(theme::FG);
        let mut row = Vec::with_capacity(cols);
        for ch in text.chars().take(cols) {
            row.push(Cell::colored(ch, fg));
        }
        while row.len() < cols {
            row.push(Cell::colored(' ', fg));
        }
        self.push_scrollback_row(row);
    }

    pub fn set_cursor(&mut self, col: u16, row: u16) {
        self.cursor.col = col.min(self.cols.saturating_sub(1));
        self.cursor.row = row.min(self.rows.saturating_sub(1));
    }

    pub fn set_fg(&mut self, fg: [f32; 3]) {
        self.fg = fg;
    }

    pub fn set_bg(&mut self, bg: Option<[f32; 3]>) {
        self.bg = bg;
    }

    pub fn fg(&self) -> [f32; 3] {
        self.fg
    }

    pub fn bg(&self) -> Option<[f32; 3]> {
        self.bg
    }

    pub fn reset_pen(&mut self) {
        self.fg = theme::FG;
        self.bg = None;
    }

    /// Swap active pen fg/bg (for SGR reverse video).
    pub fn swap_pen_fg_bg(&mut self) {
        let prev_fg = self.fg;
        self.fg = self.bg.unwrap_or(theme::BG);
        self.bg = Some(prev_fg);
    }

    /// Flat cell buffer, row-major (`row * cols + col`).
    pub fn cells(&self) -> &[Cell] {
        &self.cells
    }

    pub fn cells_mut(&mut self) -> &mut [Cell] {
        &mut self.cells
    }

    pub fn cell_at(&self, col: u16, row: u16) -> Option<&Cell> {
        self.index(col, row).map(|i| &self.cells[i])
    }

    /// Slice of one row’s cells.
    pub fn row_cells(&self, row: u16) -> &[Cell] {
        if row >= self.rows {
            return &[];
        }
        let start = (row as usize) * (self.cols as usize);
        let end = start + self.cols as usize;
        &self.cells[start..end]
    }

    /// Resize, preserving overlapping content from the top-left origin.
    /// Scrollback is cleared when columns change (no reflow yet).
    pub fn resize(&mut self, cols: u16, rows: u16) {
        let cols = cols.max(1);
        let rows = rows.max(1);
        if cols == self.cols && rows == self.rows {
            return;
        }
        if cols != self.cols {
            self.scrollback.clear();
            self.view_offset = 0;
            self.visual_offset = 0.0;
            self.scrollback_pin = 0;
        }
        let mut next = vec![Cell::blank(); (cols as usize) * (rows as usize)];
        let copy_cols = (self.cols.min(cols)) as usize;
        let copy_rows = (self.rows.min(rows)) as usize;
        for r in 0..copy_rows {
            for c in 0..copy_cols {
                let src = r * (self.cols as usize) + c;
                let dst = r * (cols as usize) + c;
                next[dst] = self.cells[src];
            }
        }
        self.cols = cols;
        self.rows = rows;
        self.cells = next;
        self.cursor.col = self.cursor.col.min(cols.saturating_sub(1));
        self.cursor.row = self.cursor.row.min(rows.saturating_sub(1));
    }

    /// Fill all cells with blanks; cursor to origin; reset pen. Keeps scrollback.
    pub fn clear(&mut self) {
        for cell in &mut self.cells {
            *cell = Cell::blank();
        }
        self.cursor = Cursor::default();
        self.reset_pen();
        self.view_offset = 0;
        self.visual_offset = 0.0;
    }

    /// Scroll the buffer up by `n` rows (content moves up; blank lines at bottom).
    /// Rows leaving the top are appended to scrollback.
    pub fn scroll(&mut self, n: usize) {
        if n == 0 || self.rows == 0 {
            return;
        }
        let n = n.min(self.rows as usize);
        let cols = self.cols as usize;
        let total = self.cells.len();
        for r in 0..n {
            let start = r * cols;
            let row = self.cells[start..start + cols].to_vec();
            self.push_scrollback_row(row);
        }
        if self.view_offset > 0 {
            // Keep relative position when history grows while scrolled up.
            self.view_offset = (self.view_offset + n).min(self.max_view_offset());
        }
        if n >= self.rows as usize {
            self.cells.fill(Cell::blank());
        } else {
            self.cells.copy_within(n * cols..total, 0);
            let blank_start = total - n * cols;
            for cell in &mut self.cells[blank_start..] {
                *cell = Cell::blank();
            }
        }
        let row = self.cursor.row as i32 - n as i32;
        self.cursor.row = row.max(0) as u16;
    }

    /// Scroll rows `top..=bottom` (0-based inclusive) up by `n`. Cursor unchanged.
    /// When the region starts at row 0, rows leaving the top enter scrollback.
    pub fn scroll_region_up(&mut self, top: u16, bottom: u16, n: usize) {
        if n == 0 || self.rows == 0 || self.cols == 0 {
            return;
        }
        let top = top.min(self.rows.saturating_sub(1)) as usize;
        let bottom = bottom.min(self.rows.saturating_sub(1)) as usize;
        if top > bottom {
            return;
        }
        let height = bottom - top + 1;
        let n = n.min(height);
        let cols = self.cols as usize;
        if top == 0 {
            for r in 0..n.min(height) {
                let start = r * cols;
                let row = self.cells[start..start + cols].to_vec();
                self.push_scrollback_row(row);
            }
            if self.view_offset > 0 {
                self.view_offset = (self.view_offset + n).min(self.max_view_offset());
            }
        }
        if n >= height {
            for r in top..=bottom {
                let start = r * cols;
                self.cells[start..start + cols].fill(Cell::blank());
            }
            return;
        }
        let src_start = (top + n) * cols;
        let src_end = (bottom + 1) * cols;
        let dst_start = top * cols;
        self.cells.copy_within(src_start..src_end, dst_start);
        for r in (bottom + 1 - n)..=bottom {
            let start = r * cols;
            self.cells[start..start + cols].fill(Cell::blank());
        }
    }

    /// Scroll rows `top..=bottom` (0-based inclusive) down by `n`. Cursor unchanged.
    pub fn scroll_region_down(&mut self, top: u16, bottom: u16, n: usize) {
        if n == 0 || self.rows == 0 || self.cols == 0 {
            return;
        }
        let top = top.min(self.rows.saturating_sub(1)) as usize;
        let bottom = bottom.min(self.rows.saturating_sub(1)) as usize;
        if top > bottom {
            return;
        }
        let height = bottom - top + 1;
        let n = n.min(height);
        let cols = self.cols as usize;
        if n >= height {
            for r in top..=bottom {
                let start = r * cols;
                self.cells[start..start + cols].fill(Cell::blank());
            }
            return;
        }
        // Copy bottom-up to avoid overlap clobber.
        for r in (top..=(bottom - n)).rev() {
            let src = r * cols;
            let dst = (r + n) * cols;
            self.cells.copy_within(src..src + cols, dst);
        }
        for r in top..(top + n) {
            let start = r * cols;
            self.cells[start..start + cols].fill(Cell::blank());
        }
    }

    /// Blank cells in half-open rectangle `[col_start, col_end) × [row_start, row_end)`.
    /// Cursor and pen are left unchanged.
    pub fn erase_rect(
        &mut self,
        col_start: u16,
        col_end: u16,
        row_start: u16,
        row_end: u16,
    ) {
        let col_start = col_start.min(self.cols) as usize;
        let col_end = col_end.min(self.cols) as usize;
        let row_start = row_start.min(self.rows) as usize;
        let row_end = row_end.min(self.rows) as usize;
        if col_start >= col_end || row_start >= row_end {
            return;
        }
        let cols = self.cols as usize;
        for r in row_start..row_end {
            let start = r * cols + col_start;
            let end = r * cols + col_end;
            self.cells[start..end].fill(Cell::blank());
        }
    }

    /// ED — erase in display. `mode`: 0 cursor→end, 1 start→cursor, 2/3 whole screen.
    /// Cursor and pen unchanged.
    pub fn erase_in_display(&mut self, mode: u16) {
        let c = self.cursor;
        match mode {
            0 => {
                self.erase_rect(c.col, self.cols, c.row, c.row.saturating_add(1));
                if c.row + 1 < self.rows {
                    self.erase_rect(0, self.cols, c.row + 1, self.rows);
                }
            }
            1 => {
                if c.row > 0 {
                    self.erase_rect(0, self.cols, 0, c.row);
                }
                self.erase_rect(0, c.col.saturating_add(1), c.row, c.row.saturating_add(1));
            }
            2 | 3 => {
                self.cells.fill(Cell::blank());
            }
            _ => {}
        }
    }

    /// EL — erase in line. `mode`: 0 cursor→end, 1 start→cursor, 2 whole line.
    /// Cursor and pen unchanged.
    pub fn erase_in_line(&mut self, mode: u16) {
        let c = self.cursor;
        match mode {
            0 => self.erase_rect(c.col, self.cols, c.row, c.row.saturating_add(1)),
            1 => self.erase_rect(0, c.col.saturating_add(1), c.row, c.row.saturating_add(1)),
            2 => self.erase_rect(0, self.cols, c.row, c.row.saturating_add(1)),
            _ => {}
        }
    }

    /// Advance to the start of the next line (scrolls if at bottom).
    pub fn newline(&mut self) {
        self.cursor.col = 0;
        if self.cursor.row + 1 >= self.rows {
            self.scroll(1);
            self.cursor.row = self.rows.saturating_sub(1);
        } else {
            self.cursor.row += 1;
        }
    }

    /// Write a single character at the cursor (handles `\n` / `\r`).
    pub fn put_char(&mut self, ch: char) {
        match ch {
            '\n' => self.newline(),
            '\r' => self.cursor.col = 0,
            '\t' => {
                let next = ((self.cursor.col / 8) + 1) * 8;
                while self.cursor.col < next && self.cursor.col < self.cols {
                    self.put_visible(' ');
                }
            }
            c if c.is_control() => {}
            c => self.put_visible(c),
        }
    }

    /// Write a string at the cursor with the current pen (wraps).
    pub fn put_str(&mut self, s: &str) {
        for ch in s.chars() {
            self.put_char(ch);
        }
    }

    /// Write with an explicit foreground, restoring the previous pen afterward.
    pub fn put_str_colored(&mut self, s: &str, fg: [f32; 3]) {
        let prev = self.fg;
        self.fg = fg;
        self.put_str(s);
        self.fg = prev;
    }

    /// Write a line and advance to the next row.
    pub fn writeln(&mut self, s: &str) {
        self.put_str(s);
        self.newline();
    }

    pub fn writeln_colored(&mut self, s: &str, fg: [f32; 3]) {
        self.put_str_colored(s, fg);
        self.newline();
    }

    /// Snapshot: one `String` per row (trailing spaces trimmed).
    pub fn snapshot_strings(&self) -> Vec<String> {
        (0..self.rows)
            .map(|r| {
                let row = self.row_cells(r);
                let mut s: String = row.iter().map(|c| c.ch).collect();
                while s.ends_with(' ') {
                    s.pop();
                }
                s
            })
            .collect()
    }

    /// Snapshot: owned rows of cells (full width, not trimmed).
    pub fn snapshot_cells(&self) -> Vec<Vec<Cell>> {
        (0..self.rows)
            .map(|r| self.row_cells(r).to_vec())
            .collect()
    }

    fn put_visible(&mut self, ch: char) {
        use unicode_width::UnicodeWidthChar;
        // Display width: 0 = combining / control (skip), 1 = normal, 2 = wide.
        let w = match ch.width() {
            Some(0) => return,
            Some(n) => n.min(2) as u16,
            None => 1,
        };
        if self.cursor.col >= self.cols {
            self.newline();
        }
        // Wide glyph needs two cells: primary + spacer (TUI-safe mono grid).
        if w >= 2 && self.cursor.col + 1 >= self.cols {
            self.newline();
        }
        if let Some(i) = self.index(self.cursor.col, self.cursor.row) {
            self.cells[i] = Cell {
                ch,
                fg: self.fg,
                bg: self.bg,
            };
        }
        self.cursor.col = self.cursor.col.saturating_add(1);
        if w >= 2 {
            if let Some(i) = self.index(self.cursor.col, self.cursor.row) {
                // Continuation cell — blank with same colors so bg fills the span.
                self.cells[i] = Cell {
                    ch: ' ',
                    fg: self.fg,
                    bg: self.bg,
                };
            }
            self.cursor.col = self.cursor.col.saturating_add(1);
        }
        // Defer wrap until next glyph so a full line does not auto-scroll early.
    }

    fn index(&self, col: u16, row: u16) -> Option<usize> {
        if col >= self.cols || row >= self.rows {
            return None;
        }
        Some((row as usize) * (self.cols as usize) + (col as usize))
    }
}

impl Default for CellGrid {
    fn default() -> Self {
        Self::new(80, 24)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn writeln_and_snapshot() {
        let mut g = CellGrid::new(20, 5);
        g.writeln("hello");
        g.writeln("world");
        let snap = g.snapshot_strings();
        assert_eq!(snap[0], "hello");
        assert_eq!(snap[1], "world");
    }

    #[test]
    fn wide_char_takes_two_cells() {
        let mut g = CellGrid::new(8, 2);
        // Fullwidth digit １ (U+FF11) has display width 2.
        g.put_char('１');
        g.put_char('A');
        let row = g.row_cells(0);
        assert_eq!(row[0].ch, '１');
        assert_eq!(row[1].ch, ' '); // continuation
        assert_eq!(row[2].ch, 'A');
        assert_eq!(g.cursor().col, 3);
    }

    #[test]
    fn suppress_scrollback_discards_scrolled_rows() {
        let mut g = CellGrid::new(4, 2);
        g.suppress_scrollback = true;
        g.writeln("aa");
        g.writeln("bb");
        g.writeln("cc");
        // No history to scroll up into — stick-bottom top is live-only.
        assert_eq!(g.max_view_offset(), 0);
    }

    #[test]
    fn scroll_on_overflow() {
        let mut g = CellGrid::new(10, 3);
        g.writeln("a");
        g.writeln("b");
        g.writeln("c");
        g.writeln("d");
        let snap = g.snapshot_strings();
        // Each writeln ends with newline; after 4 lines on 3 rows the final
        // newline leaves a blank row at the bottom (a scrolled off, then b).
        assert_eq!(snap[0], "c");
        assert_eq!(snap[1], "d");
        assert_eq!(snap[2], "");
    }

    #[test]
    fn resize_preserves_top_left() {
        let mut g = CellGrid::new(4, 2);
        g.put_str("abcd");
        g.newline();
        g.put_str("ef");
        g.resize(3, 3);
        assert_eq!(g.snapshot_strings()[0], "abc");
        assert_eq!(g.snapshot_strings()[1], "ef");
    }

    #[test]
    fn viewport_abs_roundtrip_at_bottom() {
        let mut g = CellGrid::new(4, 3);
        g.writeln("a");
        g.writeln("b");
        g.writeln("c");
        g.writeln("d");
        assert_eq!(g.view_offset(), 0);
        let abs0 = g.viewport_to_abs(0);
        assert_eq!(g.abs_to_viewport(abs0), Some(0));
        // Stick-bottom composition may start in scrollback when live is short.
        let ch: String = g.line_text_abs(abs0).chars().take(1).collect();
        assert!(
            ch == "a" || ch == "b" || ch == "c" || ch == "d" || ch == " ",
            "unexpected first col {ch:?}"
        );
    }

    #[test]
    fn stick_bottom_shows_scrollback_blocks() {
        let mut g = CellGrid::new(20, 6);
        // Simulate host block + little live output.
        g.push_scrollback_text("────────", Some(theme::DIM));
        g.push_scrollback_text("❯ echo hi", Some(theme::JADE));
        g.set_cursor(0, 0);
        g.put_str("hi");
        assert_eq!(g.view_offset(), 0);
        // Viewport should include the command block line, not only live.
        let mut found_cmd = false;
        for r in 0..g.rows() {
            let line: String = g.visible_row_cells(r).iter().map(|c| c.ch).collect();
            if line.contains("echo hi") {
                found_cmd = true;
            }
        }
        assert!(found_cmd, "command block should be visible at stick-bottom");
    }

    #[test]
    fn scrollbar_visible_when_history() {
        let mut g = CellGrid::new(20, 6);
        for i in 0..30 {
            g.push_scrollback_text(&format!("line{i}"), None);
        }
        let sb = g.scrollbar(200.0);
        assert!(sb.visible);
        assert!(sb.thumb_h >= 18.0);
        assert!(sb.thumb_h <= 200.0);
        // At bottom, thumb near bottom of track.
        let travel = sb.track_h - sb.thumb_h;
        assert!(sb.thumb_y >= travel * 0.9 - 1.0);
        g.scroll_view(g.max_view_offset() as i32);
        g.snap_scroll_visual();
        let sb2 = g.scrollbar(200.0);
        assert!(sb2.thumb_y <= travel * 0.1 + 1.0);
    }

    #[test]
    fn scroll_fraction_roundtrip() {
        let mut g = CellGrid::new(20, 6);
        for i in 0..40 {
            g.push_scrollback_text(&format!("x{i}"), None);
        }
        g.set_scroll_fraction(0.0);
        assert_eq!(g.view_offset(), 0);
        g.set_scroll_fraction(1.0);
        assert_eq!(g.view_offset(), g.max_view_offset());
        g.set_scroll_fraction(0.5);
        let mid = g.max_view_offset() / 2;
        assert!((g.view_offset() as i32 - mid as i32).abs() <= 1);
        assert!((g.visual_offset() - g.view_offset() as f32).abs() < 0.01);
    }

    #[test]
    fn tick_scroll_eases_toward_target() {
        let mut g = CellGrid::new(20, 6);
        for i in 0..40 {
            g.push_scrollback_text(&format!("y{i}"), None);
        }
        g.scroll_view(20);
        assert_eq!(g.view_offset(), 20);
        // Visual lags until tick.
        assert!(g.visual_offset() < 5.0);
        for _ in 0..30 {
            g.tick_scroll(1.0 / 60.0);
        }
        assert!((g.visual_offset() - 20.0).abs() < 0.5);
    }

    #[test]
    fn pin_here_top_aligns_after_clear() {
        let mut g = CellGrid::new(20, 8);
        for i in 0..5 {
            g.push_scrollback_text(&format!("old{i}"), None);
        }
        g.pin_here();
        g.push_scrollback_text("────────", None);
        g.push_scrollback_text("❯ clear", Some(theme::JADE));
        // Empty live after clear (cursor row may still count as 1 for live_extent).
        assert!(g.live_extent() <= 1);
        assert_eq!(g.view_offset(), 0);
        // Stick-bottom top should be at pin (top-align short post-pin content).
        assert_eq!(g.view_top_abs(), g.scrollback_pin());
        // Scroll up can reach pre-pin history.
        g.scroll_view(g.max_view_offset() as i32);
        g.snap_scroll_visual(); // tests: settle ease immediately
        assert_eq!(g.view_top_abs(), 0);
        let line: String = g.visible_row_cells(0).iter().map(|c| c.ch).collect();
        assert!(line.contains("old0"), "got {line:?}");
    }

    #[test]
    fn line_text_abs_scrollback() {
        let mut g = CellGrid::new(5, 2);
        g.writeln("one");
        g.writeln("two");
        g.writeln("three");
        assert!(g.scrollback_len() >= 1);
        let t0 = g.line_text_abs(0);
        assert!(t0.starts_with("one") || t0.starts_with("two"), "got {t0:?}");
    }
}
