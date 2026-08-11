//! Terminal cell grid — character cells with optional per-cell colors.
//! Default theme: inkstone (bg near #050a07, fg #e8f5ee, jade #00e676).

/// Inkstone palette as linear-ish RGB floats in 0..=1 (sRGB channel / 255).
pub mod theme {
    /// Near #050a07
    pub const BG: [f32; 3] = [0.019_607_843, 0.039_215_687, 0.027_450_981];
    /// #e8f5ee
    pub const FG: [f32; 3] = [0.909_803_9, 0.960_784_3, 0.933_333_34];
    /// #00e676
    pub const JADE: [f32; 3] = [0.0, 0.901_960_8, 0.462_745_1];
    /// Dim / secondary text (~#6b7c72)
    pub const DIM: [f32; 3] = [0.419_607_85, 0.486_274_5, 0.447_058_83];
    /// Error red (~#ff5252)
    pub const ERR: [f32; 3] = [1.0, 0.321_568_64, 0.321_568_64];
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

/// Fixed-size cell buffer with a logical cursor.
#[derive(Clone, Debug)]
pub struct CellGrid {
    cols: u16,
    rows: u16,
    cells: Vec<Cell>,
    cursor: Cursor,
    /// Active pen color for subsequent `put_*` calls.
    fg: [f32; 3],
    bg: Option<[f32; 3]>,
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
    pub fn resize(&mut self, cols: u16, rows: u16) {
        let cols = cols.max(1);
        let rows = rows.max(1);
        if cols == self.cols && rows == self.rows {
            return;
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

    /// Fill all cells with blanks; cursor to origin; reset pen.
    pub fn clear(&mut self) {
        for cell in &mut self.cells {
            *cell = Cell::blank();
        }
        self.cursor = Cursor::default();
        self.reset_pen();
    }

    /// Scroll the buffer up by `n` rows (content moves up; blank lines at bottom).
    pub fn scroll(&mut self, n: usize) {
        if n == 0 || self.rows == 0 {
            return;
        }
        let n = n.min(self.rows as usize);
        let cols = self.cols as usize;
        let total = self.cells.len();
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
}
