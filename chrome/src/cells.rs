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
    /// How many rows above the live viewport the user is viewing (0 = live).
    view_offset: usize,
}

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
            view_offset: 0,
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

    /// Rows the user has scrolled up from the live bottom.
    pub fn view_offset(&self) -> usize {
        self.view_offset
    }

    /// Number of rows retained in scrollback (oldest first).
    pub fn scrollback_len(&self) -> usize {
        self.scrollback.len()
    }

    /// Total absolute document lines = scrollback + live viewport rows.
    pub fn abs_line_count(&self) -> usize {
        self.scrollback.len() + self.rows as usize
    }

    /// Map a visible viewport row (0..rows) to an absolute document row.
    ///
    /// Absolute row 0 is the oldest scrollback line. When `view_offset == 0`,
    /// viewport row 0 maps to the first live row (`scrollback_len()`).
    pub fn viewport_to_abs(&self, row: u16) -> usize {
        let top = self.scrollback.len().saturating_sub(self.view_offset);
        top + row as usize
    }

    /// Map absolute document row → visible viewport row, if currently on-screen.
    pub fn abs_to_viewport(&self, abs_row: usize) -> Option<u16> {
        let top = self.scrollback.len().saturating_sub(self.view_offset);
        let bottom = top + self.rows as usize;
        if abs_row >= top && abs_row < bottom {
            Some((abs_row - top) as u16)
        } else {
            None
        }
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
    pub fn scroll_view(&mut self, delta_rows: i32) {
        if delta_rows == 0 {
            return;
        }
        let max = self.scrollback.len();
        if delta_rows > 0 {
            self.view_offset = (self.view_offset + delta_rows as usize).min(max);
        } else {
            let down = (-delta_rows) as usize;
            self.view_offset = self.view_offset.saturating_sub(down);
        }
    }

    /// Jump back to the live bottom of the viewport.
    pub fn scroll_to_bottom(&mut self) {
        self.view_offset = 0;
    }

    /// Cells for a visible row accounting for `view_offset` (scrollback + live).
    pub fn visible_row_cells(&self, row: u16) -> Vec<Cell> {
        let cols = self.cols as usize;
        let blank = vec![Cell::blank(); cols];
        if row >= self.rows {
            return blank;
        }
        let top = self.scrollback.len().saturating_sub(self.view_offset);
        let abs = top + row as usize;
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

    fn push_scrollback_row(&mut self, row: Vec<Cell>) {
        self.scrollback.push(row);
        if self.scrollback.len() > MAX_SCROLLBACK {
            let drop_n = self.scrollback.len() - MAX_SCROLLBACK;
            self.scrollback.drain(0..drop_n);
            self.view_offset = self.view_offset.saturating_sub(drop_n);
        }
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
            self.view_offset = (self.view_offset + n).min(self.scrollback.len());
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
                self.view_offset = (self.view_offset + n).min(self.scrollback.len());
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
        if self.cursor.col >= self.cols {
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
        // view_offset 0: viewport row 0 → first live row
        assert_eq!(g.view_offset(), 0);
        let abs0 = g.viewport_to_abs(0);
        assert_eq!(g.abs_to_viewport(abs0), Some(0));
        assert_eq!(g.line_text_abs(abs0).chars().take(1).collect::<String>(), "c");
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
