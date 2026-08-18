//! Warp-bar line editor: one line of text plus a Unicode-scalar caret.
//!
//! The input strip is not a native text field, so macOS word/line motion
//! (⌥←→, ⌘←→) and mid-line insert have to live here.

/// Local command draft for a pane's warp strip.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct DraftLine {
    text: String,
    /// Caret as a count of Unicode scalars from the start (not bytes).
    cursor: usize,
}

impl DraftLine {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn as_str(&self) -> &str {
        &self.text
    }

    pub fn is_empty(&self) -> bool {
        self.text.is_empty()
    }

    pub fn cursor(&self) -> usize {
        self.cursor.min(self.len_chars())
    }

    pub fn len_chars(&self) -> usize {
        self.text.chars().count()
    }

    fn chars_vec(&self) -> Vec<char> {
        self.text.chars().collect()
    }

    fn set_from_chars(&mut self, chars: Vec<char>, cursor: usize) {
        self.text = chars.into_iter().collect();
        self.cursor = cursor.min(self.len_chars());
    }

    pub fn clear(&mut self) {
        self.text.clear();
        self.cursor = 0;
    }

    /// Replace the line and park the caret at the end (history recall, paste-all).
    pub fn replace(&mut self, text: impl Into<String>) {
        self.text = text.into();
        self.cursor = self.len_chars();
    }

    pub fn set_cursor(&mut self, cursor: usize) {
        self.cursor = cursor.min(self.len_chars());
    }

    pub fn move_by(&mut self, delta: isize) {
        let n = self.len_chars() as isize;
        self.cursor = (self.cursor as isize + delta).clamp(0, n) as usize;
    }

    pub fn home(&mut self) {
        self.cursor = 0;
    }

    pub fn end(&mut self) {
        self.cursor = self.len_chars();
    }

    pub fn insert_char(&mut self, c: char) {
        if c.is_control() {
            return;
        }
        let mut chars = self.chars_vec();
        let i = self.cursor.min(chars.len());
        chars.insert(i, c);
        self.set_from_chars(chars, i + 1);
    }

    /// Insert printable text at the caret. Stops at the first newline.
    pub fn insert_str(&mut self, text: &str) {
        for c in text.chars() {
            if c == '\n' || c == '\r' {
                break;
            }
            self.insert_char(c);
        }
    }

    pub fn backspace(&mut self) {
        if self.cursor == 0 {
            return;
        }
        let mut chars = self.chars_vec();
        let i = self.cursor.min(chars.len());
        if i > 0 {
            chars.remove(i - 1);
            self.set_from_chars(chars, i - 1);
        }
    }

    pub fn delete(&mut self) {
        let mut chars = self.chars_vec();
        let i = self.cursor.min(chars.len());
        if i < chars.len() {
            chars.remove(i);
            self.set_from_chars(chars, i);
        }
    }

    pub fn word_left(&mut self) {
        let chars = self.chars_vec();
        self.cursor = word_left(&chars, self.cursor.min(chars.len()));
    }

    pub fn word_right(&mut self) {
        let chars = self.chars_vec();
        self.cursor = word_right(&chars, self.cursor.min(chars.len()));
    }

    pub fn delete_word_back(&mut self) {
        let chars = self.chars_vec();
        let end = self.cursor.min(chars.len());
        let start = word_left(&chars, end);
        if start >= end {
            return;
        }
        let mut next = chars;
        next.drain(start..end);
        self.set_from_chars(next, start);
    }

    pub fn delete_word_forward(&mut self) {
        let chars = self.chars_vec();
        let start = self.cursor.min(chars.len());
        let end = word_right(&chars, start);
        if start >= end {
            return;
        }
        let mut next = chars;
        next.drain(start..end);
        self.set_from_chars(next, start);
    }
}

fn is_word_char(c: char) -> bool {
    c == '_' || c.is_alphanumeric()
}

/// Start of the current / previous word (macOS `moveWordBackward`).
fn word_left(chars: &[char], cursor: usize) -> usize {
    if cursor == 0 {
        return 0;
    }
    let mut i = cursor;
    while i > 0 && chars[i - 1].is_whitespace() {
        i -= 1;
    }
    if i == 0 {
        return 0;
    }
    let word = is_word_char(chars[i - 1]);
    while i > 0 && !chars[i - 1].is_whitespace() && is_word_char(chars[i - 1]) == word {
        i -= 1;
    }
    i
}

/// Start of the next word, skipping the current run and following space.
fn word_right(chars: &[char], cursor: usize) -> usize {
    let n = chars.len();
    if cursor >= n {
        return n;
    }
    let mut i = cursor;
    if chars[i].is_whitespace() {
        while i < n && chars[i].is_whitespace() {
            i += 1;
        }
    } else {
        let word = is_word_char(chars[i]);
        while i < n && !chars[i].is_whitespace() && is_word_char(chars[i]) == word {
            i += 1;
        }
        while i < n && chars[i].is_whitespace() {
            i += 1;
        }
    }
    i
}

/// First draft scalar shown in a `❯ {draft}` strip of `max_cols` cells.
pub fn warp_scroll_start(draft: &str, cursor: usize, max_cols: usize) -> usize {
    const PREFIX_N: usize = 2;
    let max_cols = max_cols.max(PREFIX_N + 1);
    let n = draft.chars().count();
    let cur = cursor.min(n);
    let room = (max_cols - PREFIX_N).saturating_sub(1).max(1);
    if n <= room {
        0
    } else {
        cur.saturating_sub(room).min(n.saturating_sub(room))
    }
}

/// Visible `❯ {draft}` slice and caret column so the block caret stays on-screen.
pub fn warp_view(draft: &str, cursor: usize, max_cols: usize) -> (String, usize) {
    const PREFIX: &str = "❯ ";
    const PREFIX_N: usize = 2;
    let max_cols = max_cols.max(PREFIX_N + 1);
    let chars: Vec<char> = draft.chars().collect();
    let n = chars.len();
    let cur = cursor.min(n);
    let start = warp_scroll_start(draft, cur, max_cols);
    let room = (max_cols - PREFIX_N).saturating_sub(1).max(1);
    let vis: String = chars.iter().skip(start).take(room).collect();
    let text = format!("{PREFIX}{vis}");
    let caret_cols = (PREFIX_N + (cur - start)).min(max_cols.saturating_sub(1));
    (text, caret_cols)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn insert_and_backspace_at_end() {
        let mut d = DraftLine::new();
        d.insert_char('h');
        d.insert_char('i');
        assert_eq!(d.as_str(), "hi");
        assert_eq!(d.cursor(), 2);
        d.backspace();
        assert_eq!(d.as_str(), "h");
        assert_eq!(d.cursor(), 1);
    }

    #[test]
    fn arrows_move_and_insert_in_middle() {
        let mut d = DraftLine::new();
        d.replace("ac");
        d.set_cursor(1);
        d.insert_char('b');
        assert_eq!(d.as_str(), "abc");
        assert_eq!(d.cursor(), 2);
        d.move_by(-1);
        d.move_by(-1);
        assert_eq!(d.cursor(), 0);
        d.move_by(-1);
        assert_eq!(d.cursor(), 0);
        d.move_by(8);
        assert_eq!(d.cursor(), 3);
    }

    #[test]
    fn delete_forward_and_home_end() {
        let mut d = DraftLine::new();
        d.replace("abc");
        d.home();
        d.delete();
        assert_eq!(d.as_str(), "bc");
        d.end();
        d.delete();
        assert_eq!(d.as_str(), "bc");
        d.home();
        d.end();
        assert_eq!(d.cursor(), 2);
    }

    #[test]
    fn word_jumps_skip_runs_and_space() {
        let mut d = DraftLine::new();
        d.replace("git commit -m hi");
        d.word_left();
        assert_eq!(&d.as_str()[d.cursor()..], "hi");
        d.word_left();
        assert_eq!(&d.as_str()[d.cursor()..], "m hi");
        d.home();
        d.word_right();
        assert_eq!(&d.as_str()[d.cursor()..], "commit -m hi");
        d.word_right();
        assert_eq!(&d.as_str()[d.cursor()..], "-m hi");
    }

    #[test]
    fn word_left_from_trailing_space_lands_on_word() {
        let mut d = DraftLine::new();
        d.replace("hello world  ");
        d.word_left();
        assert_eq!(d.cursor(), 6);
        d.word_left();
        assert_eq!(d.cursor(), 0);
    }

    #[test]
    fn delete_word_back_and_forward() {
        let mut d = DraftLine::new();
        d.replace("foo bar baz");
        d.delete_word_back();
        assert_eq!(d.as_str(), "foo bar ");
        d.set_cursor(4);
        d.delete_word_forward();
        assert_eq!(d.as_str(), "foo ");
        assert_eq!(d.cursor(), 4);
    }

    #[test]
    fn paste_inserts_at_caret_and_stops_at_newline() {
        let mut d = DraftLine::new();
        d.replace("ac");
        d.set_cursor(1);
        d.insert_str("b\nzzz");
        assert_eq!(d.as_str(), "abc");
        assert_eq!(d.cursor(), 2);
    }

    #[test]
    fn warp_view_keeps_caret_in_band() {
        let (text, cols) = warp_view("", 0, 80);
        assert_eq!(text, "❯ ");
        assert_eq!(cols, 2);

        let (text, cols) = warp_view("hi", 2, 80);
        assert_eq!(text, "❯ hi");
        assert_eq!(cols, 4);

        let (text, cols) = warp_view("hi", 0, 80);
        assert_eq!(text, "❯ hi");
        assert_eq!(cols, 2);

        // Narrow strip: scroll so the caret stays visible.
        let draft = "abcdefghij";
        let (text, cols) = warp_view(draft, 10, 8);
        assert!(text.ends_with('j'), "{text}");
        assert!(cols < 8, "{cols}");
        let (text, cols) = warp_view(draft, 0, 8);
        assert!(text.starts_with("❯ a"), "{text}");
        assert_eq!(cols, 2);
    }

    #[test]
    fn unicode_scalar_cursor() {
        let mut d = DraftLine::new();
        d.insert_str("a🦀b");
        assert_eq!(d.cursor(), 3);
        d.move_by(-1);
        assert_eq!(d.cursor(), 2);
        d.backspace();
        assert_eq!(d.as_str(), "ab");
        assert_eq!(d.cursor(), 1);
    }
}
