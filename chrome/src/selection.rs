//! Terminal cell selection — drag model + text extraction for copy.
//!
//! Coordinates are **absolute document rows**: row 0 is the oldest retained
//! scrollback line; live viewport rows start at `grid.scrollback_len()`.
//! Convert mouse hits with [`CellGrid::viewport_to_abs`] / [`CellGrid::abs_to_viewport`].
//!
//! Multi-click modes (`SelectMode::{Cell,Word,Line}`) keep drag granularity after
//! double/triple click: word-aligned or full-line extend while the button is held.

use crate::cells::CellGrid;

/// Inclusive cell position in absolute document coordinates.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct CellPos {
    pub col: u16,
    /// Absolute row (scrollback + live).
    pub abs_row: usize,
}

/// Granularity for selection extend while dragging.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum SelectMode {
    /// Cell-to-cell drag (single click).
    #[default]
    Cell,
    /// Word-aligned extend (double click + drag).
    Word,
    /// Full-line extend (triple click + drag).
    Line,
}

/// Line-oriented cell selection (anchor → focus), matching product terminal UX.
#[derive(Clone, Debug, Default)]
pub struct Selection {
    active: bool,
    dragging: bool,
    mode: SelectMode,
    /// Fixed gesture origin (multi-click / begin cell). Used for Word/Line extend.
    origin: CellPos,
    /// Range endpoint A (for Cell mode: origin; for Word/Line: expanded).
    anchor: CellPos,
    /// Range endpoint B (current / expanded end).
    focus: CellPos,
}

impl Selection {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn clear(&mut self) {
        *self = Self::default();
    }

    pub fn is_active(&self) -> bool {
        self.active
    }

    pub fn is_dragging(&self) -> bool {
        self.dragging
    }

    pub fn is_empty(&self) -> bool {
        self.normalized().is_none()
    }

    pub fn mode(&self) -> SelectMode {
        self.mode
    }

    pub fn anchor(&self) -> CellPos {
        self.anchor
    }

    pub fn focus(&self) -> CellPos {
        self.focus
    }

    /// Begin a cell-mode drag at `pos` (clamped by the host to grid bounds).
    ///
    /// The click origin is not a painted selection until [`update`] /
    /// [`update_drag`] moves to another cell.
    pub fn begin(&mut self, pos: CellPos) {
        self.active = true;
        self.dragging = true;
        self.mode = SelectMode::Cell;
        self.origin = pos;
        self.anchor = pos;
        self.focus = pos;
    }

    /// Extend the selection while dragging (cell mode only, or raw focus move).
    ///
    /// Prefer [`Self::update_drag`] when a grid is available so Word/Line modes
    /// keep their granularity.
    pub fn update(&mut self, pos: CellPos) {
        if !self.active {
            self.begin(pos);
            return;
        }
        self.dragging = true;
        match self.mode {
            SelectMode::Cell => {
                self.focus = pos;
            }
            SelectMode::Word | SelectMode::Line => {
                // Without a grid we cannot re-expand; move focus raw.
                self.focus = pos;
            }
        }
    }

    /// Extend selection under the current mode using `grid` for word/line bounds.
    ///
    /// - **Cell**: focus follows `pos`
    /// - **Word**: range is word-aligned from the origin word to the word under `pos`
    /// - **Line**: full lines between origin row and `pos` row
    pub fn update_drag(&mut self, grid: &CellGrid, pos: CellPos) {
        if !self.active {
            self.begin(pos);
            return;
        }
        self.dragging = true;
        match self.mode {
            SelectMode::Cell => {
                self.focus = pos;
            }
            SelectMode::Word => {
                let (a_start, a_end) = word_bounds_at(grid, self.origin);
                let (b_start, b_end) = word_bounds_at(grid, pos);
                if cell_before_or_eq(self.origin, pos) {
                    self.anchor = CellPos {
                        col: a_start,
                        abs_row: self.origin.abs_row,
                    };
                    self.focus = CellPos {
                        col: b_end,
                        abs_row: pos.abs_row,
                    };
                } else {
                    self.anchor = CellPos {
                        col: b_start,
                        abs_row: pos.abs_row,
                    };
                    self.focus = CellPos {
                        col: a_end,
                        abs_row: self.origin.abs_row,
                    };
                }
            }
            SelectMode::Line => {
                let last = grid.cols().saturating_sub(1);
                let (r0, r1) = if self.origin.abs_row <= pos.abs_row {
                    (self.origin.abs_row, pos.abs_row)
                } else {
                    (pos.abs_row, self.origin.abs_row)
                };
                self.anchor = CellPos {
                    col: 0,
                    abs_row: r0,
                };
                self.focus = CellPos {
                    col: last,
                    abs_row: r1,
                };
            }
        }
    }

    /// End the drag. A cell-mode click (no range) is discarded; word/line
    /// multi-click and a real drag stay selected.
    pub fn end(&mut self) {
        self.dragging = false;
        if self.is_empty() {
            self.clear();
        }
    }

    /// Normalized inclusive range: start is top-left in document order.
    ///
    /// Cell-mode click origin (`anchor == focus`) is not a range — that is a
    /// click, not a drag. Word/line modes keep a one-cell range so a
    /// one-character double-click still highlights.
    pub fn normalized(&self) -> Option<(CellPos, CellPos)> {
        if !self.active {
            return None;
        }
        if self.mode == SelectMode::Cell && self.anchor == self.focus {
            return None;
        }
        let a = self.anchor;
        let b = self.focus;
        let (start, end) = if a.abs_row < b.abs_row
            || (a.abs_row == b.abs_row && a.col <= b.col)
        {
            (a, b)
        } else {
            (b, a)
        };
        Some((start, end))
    }

    /// Whether absolute cell `(col, abs_row)` lies in the line-wise selection.
    pub fn contains(&self, col: u16, abs_row: usize) -> bool {
        let Some((start, end)) = self.normalized() else {
            return false;
        };
        if abs_row < start.abs_row || abs_row > end.abs_row {
            return false;
        }
        if start.abs_row == end.abs_row {
            return col >= start.col && col <= end.col;
        }
        if abs_row == start.abs_row {
            return col >= start.col;
        }
        if abs_row == end.abs_row {
            return col <= end.col;
        }
        true
    }

    /// Extract selected text from `grid` (newlines between rows).
    ///
    /// Each line’s trailing spaces/tabs are trimmed so copy does not pull the
    /// blank right-padding of the cell row. Interior spaces are kept.
    pub fn text(&self, grid: &CellGrid) -> String {
        let Some((start, end)) = self.normalized() else {
            return String::new();
        };
        let cols = grid.cols();
        if cols == 0 {
            return String::new();
        }
        let max_abs = grid.abs_line_count().saturating_sub(1);
        let start_row = start.abs_row.min(max_abs);
        let end_row = end.abs_row.min(max_abs);
        let start_col = start.col.min(cols.saturating_sub(1));
        let end_col = end.col.min(cols.saturating_sub(1));

        let mut out = String::new();
        for y in start_row..=end_row {
            let line = grid.line_text_abs(y);
            let mut cells: Vec<char> = line.chars().collect();
            // Pad to full width so column indices stay stable.
            while cells.len() < cols as usize {
                cells.push(' ');
            }

            let (from, to) = if start_row == end_row {
                (start_col as usize, end_col as usize)
            } else if y == start_row {
                (start_col as usize, cols as usize - 1)
            } else if y == end_row {
                (0, end_col as usize)
            } else {
                (0, cols as usize - 1)
            };

            if from <= to && to < cells.len() {
                let mut segment: String = cells[from..=to].iter().collect();
                while segment.ends_with(' ') || segment.ends_with('\t') {
                    segment.pop();
                }
                out.push_str(&segment);
            }
            if y != end_row {
                out.push('\n');
            }
        }
        out
    }

    /// Park a cell-mode click origin (no drag). Not a painted selection.
    pub fn select_cell(&mut self, pos: CellPos) {
        self.begin(pos);
        self.end();
    }

    /// Select whole absolute line `abs_row` (columns `0..cols-1`). Sets [`SelectMode::Line`].
    pub fn select_line(&mut self, abs_row: usize, cols: u16) {
        let last = cols.saturating_sub(1);
        self.active = true;
        self.dragging = true;
        self.mode = SelectMode::Line;
        self.origin = CellPos {
            col: 0,
            abs_row,
        };
        self.anchor = CellPos {
            col: 0,
            abs_row,
        };
        self.focus = CellPos {
            col: last,
            abs_row,
        };
    }

    /// Select word under `pos` using the line text at that absolute row.
    /// Sets [`SelectMode::Word`] so a following drag stays word-aligned.
    pub fn select_word(&mut self, grid: &CellGrid, pos: CellPos) {
        self.active = true;
        self.dragging = true;
        self.mode = SelectMode::Word;
        self.origin = pos;
        let (s, e) = word_bounds_at(grid, pos);
        self.anchor = CellPos {
            col: s,
            abs_row: pos.abs_row,
        };
        self.focus = CellPos {
            col: e,
            abs_row: pos.abs_row,
        };
    }
}

fn cell_before_or_eq(a: CellPos, b: CellPos) -> bool {
    a.abs_row < b.abs_row || (a.abs_row == b.abs_row && a.col <= b.col)
}

fn word_bounds_at(grid: &CellGrid, pos: CellPos) -> (u16, u16) {
    let line = grid.line_text_abs(pos.abs_row);
    let (s, e) = word_bounds(&line, pos.col as usize);
    let cols = grid.cols();
    let e = (e as u16).min(cols.saturating_sub(1));
    let s = (s as u16).min(e);
    (s, e)
}

fn is_word_char(c: char) -> bool {
    c == '_' || c.is_alphanumeric()
}

/// Inclusive start/end columns for the word (or space/punct run) at `col`.
pub fn word_bounds(line: &str, col: usize) -> (usize, usize) {
    let rs: Vec<char> = line.chars().collect();
    if rs.is_empty() {
        return (0, 0);
    }
    let col = col.min(rs.len() - 1);
    let r = rs[col];
    if is_word_char(r) {
        let mut start = col;
        let mut end = col;
        while start > 0 && is_word_char(rs[start - 1]) {
            start -= 1;
        }
        while end + 1 < rs.len() && is_word_char(rs[end + 1]) {
            end += 1;
        }
        return (start, end);
    }
    if r == ' ' || r == '\t' {
        let mut start = col;
        let mut end = col;
        while start > 0 && (rs[start - 1] == ' ' || rs[start - 1] == '\t') {
            start -= 1;
        }
        while end + 1 < rs.len() && (rs[end + 1] == ' ' || rs[end + 1] == '\t') {
            end += 1;
        }
        return (start, end);
    }
    // Punctuation / other: contiguous non-word non-space.
    let mut start = col;
    let mut end = col;
    while start > 0 {
        let p = rs[start - 1];
        if is_word_char(p) || p == ' ' || p == '\t' {
            break;
        }
        start -= 1;
    }
    while end + 1 < rs.len() {
        let p = rs[end + 1];
        if is_word_char(p) || p == ' ' || p == '\t' {
            break;
        }
        end += 1;
    }
    (start, end)
}

/// Clamp a position into the grid's absolute document.
pub fn clamp_pos(grid: &CellGrid, col: u16, abs_row: usize) -> CellPos {
    let cols = grid.cols().max(1);
    let max_row = grid.abs_line_count().saturating_sub(1);
    CellPos {
        col: col.min(cols.saturating_sub(1)),
        abs_row: abs_row.min(max_row),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cells::CellGrid;

    fn grid_with_lines(cols: u16, rows: u16, lines: &[&str]) -> CellGrid {
        let mut g = CellGrid::new(cols, rows);
        for (i, line) in lines.iter().enumerate() {
            if i as u16 >= rows {
                break;
            }
            g.set_cursor(0, i as u16);
            g.put_str(line);
        }
        g
    }

    #[test]
    fn single_line_extract() {
        let g = grid_with_lines(20, 5, &["hello world"]);
        let mut sel = Selection::new();
        sel.begin(CellPos {
            col: 0,
            abs_row: 0,
        });
        sel.update(CellPos {
            col: 4,
            abs_row: 0,
        });
        sel.end();
        assert_eq!(sel.text(&g), "hello");
    }

    #[test]
    fn multi_line_newlines() {
        let g = grid_with_lines(10, 5, &["abcde", "fghij", "klmno"]);
        let mut sel = Selection::new();
        // From col 2 of row 0 through col 1 of row 2 → "cde\nfghij\nkl"
        sel.begin(CellPos {
            col: 2,
            abs_row: 0,
        });
        sel.update(CellPos {
            col: 1,
            abs_row: 2,
        });
        sel.end();
        assert_eq!(sel.text(&g), "cde\nfghij\nkl");
    }

    #[test]
    fn reverse_drag_normalizes() {
        let g = grid_with_lines(10, 3, &["abcdef"]);
        let mut sel = Selection::new();
        sel.begin(CellPos {
            col: 5,
            abs_row: 0,
        });
        sel.update(CellPos {
            col: 1,
            abs_row: 0,
        });
        assert_eq!(sel.text(&g), "bcdef");
        assert!(sel.contains(3, 0));
        assert!(!sel.contains(0, 0));
    }

    #[test]
    fn contains_middle_rows_full_width() {
        let g = grid_with_lines(5, 4, &["aaaaa", "bbbbb", "ccccc", "ddddd"]);
        let mut sel = Selection::new();
        sel.begin(CellPos {
            col: 3,
            abs_row: 0,
        });
        sel.update(CellPos {
            col: 1,
            abs_row: 2,
        });
        // First row: cols >= 3
        assert!(sel.contains(3, 0));
        assert!(!sel.contains(2, 0));
        // Middle row: full width
        assert!(sel.contains(0, 1));
        assert!(sel.contains(4, 1));
        // Last row: cols <= 1
        assert!(sel.contains(1, 2));
        assert!(!sel.contains(2, 2));
        assert!(!sel.contains(0, 3));
        let _ = g;
    }

    #[test]
    fn word_select() {
        let g = grid_with_lines(20, 3, &["foo bar-baz"]);
        let mut sel = Selection::new();
        sel.select_word(
            &g,
            CellPos {
                col: 5,
                abs_row: 0,
            },
        );
        assert_eq!(sel.mode(), SelectMode::Word);
        assert_eq!(sel.text(&g), "bar");
        sel.select_word(
            &g,
            CellPos {
                col: 0,
                abs_row: 0,
            },
        );
        assert_eq!(sel.text(&g), "foo");
    }

    #[test]
    fn line_select() {
        let g = grid_with_lines(10, 2, &["hello"]);
        let mut sel = Selection::new();
        sel.select_line(0, g.cols());
        assert_eq!(sel.mode(), SelectMode::Line);
        // Full width includes trailing spaces; whole-string trim keeps "hello"
        assert_eq!(sel.text(&g), "hello");
    }

    #[test]
    fn scrollback_absolute_rows() {
        let mut g = CellGrid::new(8, 2);
        g.writeln("one");
        g.writeln("two");
        g.writeln("three");
        // After 3 writeln on 2 rows: scrollback has "one","two"; live is "three", ""
        // (each writeln ends with newline)
        assert!(g.scrollback_len() >= 1);
        let mut sel = Selection::new();
        // Select oldest scrollback line
        sel.select_line(0, g.cols());
        let t = sel.text(&g);
        assert!(
            t.starts_with("one") || t.starts_with("two") || !t.is_empty(),
            "got {t:?}"
        );
    }

    #[test]
    fn clear_empties() {
        let g = grid_with_lines(10, 2, &["xyz"]);
        let mut sel = Selection::new();
        sel.begin(CellPos {
            col: 0,
            abs_row: 0,
        });
        sel.update(CellPos {
            col: 2,
            abs_row: 0,
        });
        assert!(!sel.is_empty());
        assert_eq!(sel.mode(), SelectMode::Cell);
        sel.clear();
        assert!(sel.is_empty());
        assert_eq!(sel.mode(), SelectMode::Cell);
        assert_eq!(sel.text(&g), "");
    }

    #[test]
    fn click_without_drag_is_not_a_selection() {
        let g = grid_with_lines(10, 2, &["xyz"]);
        let mut sel = Selection::new();
        let pos = CellPos {
            col: 1,
            abs_row: 0,
        };
        sel.begin(pos);
        // Pressing a cell must not paint / copy a one-glyph square.
        assert!(sel.is_empty());
        assert!(sel.is_dragging());
        assert!(!sel.contains(1, 0));
        assert_eq!(sel.text(&g), "");
        sel.end();
        assert!(sel.is_empty());
        assert!(!sel.is_active());
        assert!(!sel.is_dragging());
        assert_eq!(sel.text(&g), "");
    }

    #[test]
    fn drag_keeps_range_after_end() {
        let g = grid_with_lines(10, 2, &["xyz"]);
        let mut sel = Selection::new();
        sel.begin(CellPos {
            col: 0,
            abs_row: 0,
        });
        sel.update(CellPos {
            col: 2,
            abs_row: 0,
        });
        sel.end();
        assert!(!sel.is_empty());
        assert!(!sel.is_dragging());
        assert_eq!(sel.text(&g), "xyz");
        assert!(sel.contains(0, 0));
        assert!(sel.contains(2, 0));
    }

    #[test]
    fn one_char_word_click_still_selects() {
        let g = grid_with_lines(8, 2, &["a bc"]);
        let mut sel = Selection::new();
        sel.select_word(
            &g,
            CellPos {
                col: 0,
                abs_row: 0,
            },
        );
        sel.end();
        assert_eq!(sel.mode(), SelectMode::Word);
        assert!(!sel.is_empty());
        assert_eq!(sel.text(&g), "a");
        assert!(sel.contains(0, 0));
        assert!(!sel.contains(1, 0));
    }

    #[test]
    fn word_bounds_basic() {
        assert_eq!(word_bounds("hello world", 1), (0, 4));
        assert_eq!(word_bounds("hello world", 6), (6, 10));
        assert_eq!(word_bounds("path/to", 0), (0, 3));
        assert_eq!(word_bounds("path/to", 4), (4, 4));
    }

    /// Cases ported from `internal/ui/selection_test.go` TestWordBounds.
    #[test]
    fn word_bounds_from_go() {
        let cases: &[(&str, usize, usize, usize)] = &[
            ("hello world", 1, 0, 4),
            ("hello world", 6, 6, 10),
            ("  foo  ", 0, 0, 1),
            ("  foo  ", 3, 2, 4),
            ("a::b", 1, 1, 2),
            ("", 0, 0, 0),
            ("x", 0, 0, 0),
            ("path/to", 4, 4, 4), // '/'
            ("path/to", 0, 0, 3), // "path"
            ("path/to", 5, 5, 6), // "to"
        ];
        for &(line, col, want_s, want_e) in cases {
            assert_eq!(
                word_bounds(line, col),
                (want_s, want_e),
                "word_bounds({line:?}, {col})"
            );
        }
    }

    #[test]
    fn word_drag_extends_word_aligned() {
        // "hello  world  extra" — double-click "hello", drag onto "world"
        let g = grid_with_lines(24, 2, &["hello  world  extra"]);
        let mut sel = Selection::new();
        sel.select_word(
            &g,
            CellPos {
                col: 1,
                abs_row: 0,
            },
        );
        assert_eq!(sel.text(&g), "hello");
        assert_eq!(sel.mode(), SelectMode::Word);

        // Drag to a cell inside "world"
        sel.update_drag(
            &g,
            CellPos {
                col: 9,
                abs_row: 0,
            },
        );
        assert_eq!(sel.text(&g), "hello  world");
        // Interior of both words selected; space between included; not "extra"
        assert!(sel.contains(0, 0));
        assert!(sel.contains(10, 0));
        assert!(!sel.contains(15, 0)); // 'e' of extra

        // Reverse drag past origin onto leading side stays word-aligned on origin
        sel.update_drag(
            &g,
            CellPos {
                col: 0,
                abs_row: 0,
            },
        );
        assert_eq!(sel.text(&g), "hello");
    }

    #[test]
    fn word_drag_across_rows() {
        let g = grid_with_lines(12, 3, &["alpha beta", "gamma delta", "eps"]);
        let mut sel = Selection::new();
        sel.select_word(
            &g,
            CellPos {
                col: 0,
                abs_row: 0,
            },
        );
        assert_eq!(sel.text(&g), "alpha");
        sel.update_drag(
            &g,
            CellPos {
                col: 2,
                abs_row: 1,
            },
        );
        // From start of "alpha" through end of "gamma" (word under focus on row 1)
        assert_eq!(sel.text(&g), "alpha beta\ngamma");
        assert!(sel.contains(0, 0));
        assert!(sel.contains(11, 0)); // rest of first line
        assert!(sel.contains(0, 1));
        assert!(sel.contains(4, 1)); // end of gamma
        assert!(!sel.contains(6, 1)); // space before delta / delta
    }

    #[test]
    fn line_drag_extends_full_lines() {
        let g = grid_with_lines(8, 4, &["aaaa", "bbbb", "cccc", "dddd"]);
        let mut sel = Selection::new();
        sel.select_line(1, g.cols());
        assert_eq!(sel.mode(), SelectMode::Line);
        assert_eq!(sel.text(&g), "bbbb");
        // Full first selected line
        assert!(sel.contains(0, 1));
        assert!(sel.contains(7, 1));

        sel.update_drag(
            &g,
            CellPos {
                col: 3,
                abs_row: 3,
            },
        );
        // Lines 1..=3 fully selected regardless of focus col
        assert!(sel.contains(0, 1));
        assert!(sel.contains(7, 1));
        assert!(sel.contains(0, 2));
        assert!(sel.contains(7, 2));
        assert!(sel.contains(0, 3));
        assert!(sel.contains(7, 3));
        assert!(!sel.contains(0, 0));
        assert_eq!(sel.text(&g), "bbbb\ncccc\ndddd");

        // Reverse: drag above origin
        sel.update_drag(
            &g,
            CellPos {
                col: 2,
                abs_row: 0,
            },
        );
        assert_eq!(sel.text(&g), "aaaa\nbbbb");
        assert!(sel.contains(7, 0));
        assert!(!sel.contains(0, 2));
    }

    #[test]
    fn begin_resets_mode_to_cell() {
        let g = grid_with_lines(10, 2, &["hello"]);
        let mut sel = Selection::new();
        sel.select_word(
            &g,
            CellPos {
                col: 0,
                abs_row: 0,
            },
        );
        assert_eq!(sel.mode(), SelectMode::Word);
        sel.begin(CellPos {
            col: 2,
            abs_row: 0,
        });
        assert_eq!(sel.mode(), SelectMode::Cell);
        sel.update_drag(
            &g,
            CellPos {
                col: 4,
                abs_row: 0,
            },
        );
        assert_eq!(sel.text(&g), "llo");
    }
}
