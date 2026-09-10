//! Notes bank — list screen, then full editor (product parity).
//!
//! Two screens, never a list|editor split: the list is a full browser; Enter
//! opens the editor; Esc returns to the list (Esc again closes). Persists
//! product-compatible `notes.json` under the suzuri config dir. Pure bank ops
//! live in [`crate::notes_ops`].

use std::fs;
use std::path::{Path, PathBuf};

use crate::layout::Rect;
use crate::notes_ops::{
    self, bank_active_index, bank_create_note, bank_delete_note, normalize_bank,
    note_display_title, NotesBank, NOTES_MAX_RUNES,
};

pub use crate::notes_ops::NoteDoc;
// Re-export for callers / docs.
#[allow(unused_imports)]
pub use crate::notes_ops::NOTES_MAX_BANK;

/// Which screen / field owns keyboard input.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub enum NotesFocus {
    /// Full-card note browser (Esc from the editor lands here).
    List,
    Title,
    #[default]
    Body,
}

/// Hit-test geometry for the list **or** editor screen (never both).
#[derive(Clone, Debug, Default)]
pub struct NotesLayout {
    pub modal: Rect,
    pub list: Rect,
    /// Visible list rows (bank index = `list_start + i`).
    pub list_rows: Vec<Rect>,
    pub list_start: usize,
    pub new_row: Rect,
    pub title: Rect,
    pub body: Rect,
    pub delete_row: Rect,
    pub footer: Rect,
    pub list_mode: bool,
}

impl NotesLayout {
    pub fn clip(r: Rect) -> [f32; 4] {
        [r.x, r.y, r.w.max(0.0), r.h.max(0.0)]
    }
}

/// Layout constants — keep in sync with renderer notes glass panels.
pub const NOTES_PAD: f32 = 14.0;
pub const NOTES_ROW_H: f32 = 32.0;
pub const NOTES_TITLE_H: f32 = 36.0;
pub const NOTES_TITLE_BODY_GAP: f32 = 8.0;
pub const NOTES_FOOTER_H: f32 = 22.0;
pub const NOTES_HEADER_H: f32 = 28.0;
pub const NOTES_BODY_LINE_H: f32 = 18.0;
pub const NOTES_BODY_INSET: f32 = 14.0;
pub const NOTES_TITLE_INSET: f32 = 12.0;
pub const NOTES_BODY_CHAR_W: f32 = 7.5;
pub const NOTES_TITLE_CHAR_W: f32 = 8.0;

/// Max undo depth for the body editor (product uses 200; chrome keeps it light).
pub const BODY_HIST_LIMIT: usize = 50;

/// One restorable body editor state (text + caret). Mirrors `textedit.Snapshot`.
#[derive(Clone, Debug, PartialEq, Eq)]
struct BodySnapshot {
    text: String,
    cursor: usize,
}

/// Linear undo/redo stack for the notes body (product `textedit.History` subset).
#[derive(Clone, Debug, Default)]
struct BodyHistory {
    past: Vec<BodySnapshot>,
    future: Vec<BodySnapshot>,
}

impl BodyHistory {
    fn clear(&mut self) {
        self.past.clear();
        self.future.clear();
    }

    fn can_undo(&self) -> bool {
        !self.past.is_empty()
    }

    fn can_redo(&self) -> bool {
        !self.future.is_empty()
    }

    /// Record before-state so the next mutation can be undone.
    /// Consecutive identical snapshots are coalesced; new branch drops redo.
    fn push(&mut self, snap: BodySnapshot) {
        if self.past.last().is_some_and(|p| p == &snap) {
            return;
        }
        self.past.push(snap);
        if self.past.len() > BODY_HIST_LIMIT {
            let drop = self.past.len() - BODY_HIST_LIMIT;
            self.past.drain(0..drop);
        }
        self.future.clear();
    }

    fn undo(&mut self, current: BodySnapshot) -> Option<BodySnapshot> {
        let prev = self.past.pop()?;
        self.future.push(current);
        Some(prev)
    }

    fn redo(&mut self, current: BodySnapshot) -> Option<BodySnapshot> {
        let next = self.future.pop()?;
        self.past.push(current);
        Some(next)
    }
}

/// Notes overlay state + persistence.
pub struct NotesState {
    pub open: bool,
    present: f32,
    present_vel: f32,
    overlay: f32,
    /// Body of the active note (edited live).
    pub body: String,
    pub title: String,
    pub cursor: usize,
    dirty: bool,
    bank: Vec<NoteDoc>,
    active: usize,
    path: PathBuf,
    pub focus: NotesFocus,
    /// Last layout for hit-testing (filled by [`Self::refresh_layout`] / try_click).
    pub list_hit: Vec<Rect>,
    pub body_rect: Rect,
    pub title_rect: Rect,
    /// Body-only undo/redo (cleared when switching notes).
    body_hist: BodyHistory,
    body_scroll: usize,
    list_scroll: usize,
}

impl NotesState {
    pub fn new() -> Self {
        Self::with_path(notes_path())
    }

    /// Construct with an injectable path (unit tests / alternate stores).
    pub fn with_path(path: impl Into<PathBuf>) -> Self {
        let path = path.into();
        let bank = load_bank(&path);
        let active = bank_active_index(&bank);
        let (title, body) = bank
            .notes
            .get(active)
            .map(|n| (n.title.clone(), n.body.clone()))
            .unwrap_or_else(|| (String::new(), String::new()));
        let cursor = body.chars().count();
        Self {
            open: false,
            present: 0.0,
            present_vel: 0.0,
            overlay: 0.0,
            body,
            title,
            cursor,
            dirty: false,
            bank: bank.notes,
            active,
            path,
            focus: NotesFocus::Body,
            list_hit: Vec::new(),
            body_rect: Rect::default(),
            title_rect: Rect::default(),
            body_hist: BodyHistory::default(),
            body_scroll: 0,
            list_scroll: 0,
        }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn bank(&self) -> &[NoteDoc] {
        &self.bank
    }

    pub fn active_index(&self) -> usize {
        self.active
    }

    pub fn active_id(&self) -> Option<&str> {
        self.bank.get(self.active).map(|n| n.id.as_str())
    }

    pub fn is_dirty(&self) -> bool {
        self.dirty
    }

    pub fn dirty(&self) -> bool {
        self.dirty
    }

    /// Display label for list rows (title, else first body line, else “Untitled”).
    pub fn display_title_for(&self, index: usize) -> String {
        self.bank
            .get(index)
            .map(note_display_title)
            .unwrap_or_else(|| "Untitled".into())
    }

    /// List row: `Title  ·  first line` (preview omitted when it repeats the title).
    pub fn list_row_label(&self, index: usize) -> String {
        let title = self.display_title_for(index);
        let preview = self
            .bank
            .get(index)
            .map(|n| {
                let p = n.body.lines().next().unwrap_or("").trim();
                if p.is_empty() || p == title {
                    String::new()
                } else {
                    p.to_string()
                }
            })
            .unwrap_or_default();
        if preview.is_empty() {
            title
        } else {
            format!("{title}  ·  {preview}")
        }
    }

    pub fn footer_text(&self) -> String {
        let status = if self.dirty { "unsaved" } else { "saved" };
        let n = self.bank.len();
        let chars = self.body.chars().count();
        match self.focus {
            NotesFocus::List => {
                format!("{n} notes  ·  ↑↓ move  ·  enter open  ·  n new  ·  d delete  ·  esc close")
            }
            NotesFocus::Title => "enter  body    esc  editor".into(),
            NotesFocus::Body => {
                format!("{n} notes · {chars} chars · {status} · esc list")
            }
        }
    }

    pub fn active_display_title(&self) -> String {
        let t = self.title.trim();
        if !t.is_empty() {
            return t.to_string();
        }
        note_display_title(&NoteDoc {
            id: String::new(),
            title: self.title.clone(),
            body: self.body.clone(),
            updated: 0,
        })
    }

    pub fn select(&mut self, index: usize) {
        if index >= self.bank.len() {
            return;
        }
        if index != self.active {
            self.flush_active();
            self.active = index;
            let n = &self.bank[index];
            self.title = n.title.clone();
            self.body = n.body.clone();
            self.cursor = self.body.chars().count();
            self.body_hist.clear();
            self.body_scroll = 0;
            self.dirty = true;
        }
        self.ensure_list_visible();
    }

    /// Open the editor screen on the active note (blank → title, else body).
    pub fn open_editor(&mut self) {
        if self.active_is_blank() {
            self.set_focus(NotesFocus::Title);
        } else {
            self.set_focus(NotesFocus::Body);
        }
        self.ensure_body_visible();
    }

    pub fn is_list(&self) -> bool {
        self.focus == NotesFocus::List
    }

    pub fn new_note(&mut self) {
        self.flush_active();
        let bank = NotesBank {
            active_id: self
                .bank
                .get(self.active)
                .map(|n| n.id.clone())
                .unwrap_or_default(),
            notes: self.bank.clone(),
        };
        let Ok((bank, n)) = bank_create_note(bank, "", "") else {
            return; // bank full
        };
        self.bank = bank.notes;
        self.active = self
            .bank
            .iter()
            .position(|x| x.id == n.id)
            .unwrap_or(self.bank.len().saturating_sub(1));
        self.title.clear();
        self.body.clear();
        self.cursor = 0;
        self.focus = NotesFocus::Title;
        self.body_hist.clear();
        self.body_scroll = 0;
        self.dirty = true;
    }

    /// Delete the active note (last note is cleared, bank never empty).
    pub fn delete_active(&mut self) {
        self.flush_active();
        let id = match self.bank.get(self.active) {
            Some(n) => n.id.clone(),
            None => return,
        };
        let bank = NotesBank {
            active_id: id.clone(),
            notes: self.bank.clone(),
        };
        let Ok((bank, _)) = bank_delete_note(bank, &id) else {
            return;
        };
        self.bank = bank.notes;
        self.active = bank_active_index(&NotesBank {
            active_id: bank.active_id,
            notes: self.bank.clone(),
        });
        if let Some(n) = self.bank.get(self.active) {
            self.title = n.title.clone();
            self.body = n.body.clone();
        } else {
            self.title.clear();
            self.body.clear();
        }
        self.cursor = self.body.chars().count();
        if self.focus != NotesFocus::List {
            self.focus = NotesFocus::Body;
        }
        self.body_hist.clear();
        self.body_scroll = 0;
        self.dirty = true;
        self.ensure_list_visible();
    }

    /// Alias used by some call sites / hooks.
    pub fn delete_note(&mut self) {
        self.delete_active();
    }

    /// Move keyboard ownership; caret lands at field end (list keeps caret).
    pub fn set_focus(&mut self, focus: NotesFocus) {
        self.focus = focus;
        match focus {
            NotesFocus::List => {
                self.flush_active();
                self.ensure_list_visible();
            }
            NotesFocus::Title => {
                self.cursor = self.title.chars().count();
            }
            NotesFocus::Body => {
                self.cursor = self.body.chars().count();
                self.ensure_body_visible();
            }
        }
    }

    /// Tab / Shift-Tab: list → editor; editor toggles Title ↔ Body.
    pub fn cycle_focus(&mut self, _reverse: bool) {
        self.set_focus(match self.focus {
            NotesFocus::List => NotesFocus::Body,
            NotesFocus::Title => NotesFocus::Body,
            NotesFocus::Body => NotesFocus::Title,
        });
    }

    pub fn can_undo(&self) -> bool {
        self.body_hist.can_undo()
    }

    pub fn can_redo(&self) -> bool {
        self.body_hist.can_redo()
    }

    /// Undo last body edit. No-op when stack empty or focus was title-only (still restores body).
    pub fn undo(&mut self) -> bool {
        let current = self.body_snapshot();
        let Some(prev) = self.body_hist.undo(current) else {
            return false;
        };
        self.apply_body_snapshot(prev);
        true
    }

    /// Redo previously undone body edit.
    pub fn redo(&mut self) -> bool {
        let current = self.body_snapshot();
        let Some(next) = self.body_hist.redo(current) else {
            return false;
        };
        self.apply_body_snapshot(next);
        true
    }

    fn body_snapshot(&self) -> BodySnapshot {
        BodySnapshot {
            text: self.body.clone(),
            cursor: self.cursor.min(self.body.chars().count()),
        }
    }

    fn apply_body_snapshot(&mut self, snap: BodySnapshot) {
        self.body = snap.text;
        let n = self.body.chars().count();
        self.cursor = snap.cursor.min(n);
        self.focus = NotesFocus::Body;
        self.dirty = true;
    }

    fn push_body_undo(&mut self) {
        let snap = self.body_snapshot();
        self.body_hist.push(snap);
    }

    /// Compute list-or-editor layout for hit-testing and rendering.
    pub fn layout(&self, win_w: f32, win_h: f32) -> NotesLayout {
        let modal = self.animated_modal_rect(win_w, win_h);
        notes_layout_in_modal(
            modal,
            self.bank.len(),
            self.focus == NotesFocus::List,
            self.list_scroll,
        )
    }

    /// Refresh cached hit rects (`list_hit`, `title_rect`, `body_rect`).
    pub fn refresh_layout(&mut self, win_w: f32, win_h: f32) {
        let lay = self.layout(win_w, win_h);
        self.list_hit = lay.list_rows.clone();
        self.title_rect = lay.title;
        self.body_rect = lay.body;
        self.ensure_body_visible();
        self.ensure_list_visible();
    }

    /// Click inside notes modal: list open, + New, delete, title/body caret.
    pub fn try_click(&mut self, x: f32, y: f32, win_w: f32, win_h: f32) {
        let lay = self.layout(win_w, win_h);
        self.list_hit = lay.list_rows.clone();
        self.title_rect = lay.title;
        self.body_rect = lay.body;

        if lay.list_mode {
            for (i, r) in lay.list_rows.iter().enumerate() {
                if r.contains(x, y) {
                    self.select(lay.list_start + i);
                    self.open_editor();
                    return;
                }
            }
            if lay.new_row.contains(x, y) {
                self.new_note();
                return;
            }
            if lay.delete_row.contains(x, y) {
                self.delete_active();
            }
            return;
        }

        if lay.title.contains(x, y) {
            self.focus = NotesFocus::Title;
            let rel = (x - (lay.title.x + NOTES_TITLE_INSET)).max(0.0);
            let col = (rel / NOTES_TITLE_CHAR_W).floor() as usize;
            self.cursor = col.min(self.title.chars().count());
            return;
        }
        if lay.body.contains(x, y) {
            self.focus = NotesFocus::Body;
            let cols = notes_wrap_cols(lay.body.w);
            let lines = notes_wrap_lines(&self.body, cols);
            let rel_y = (y - (lay.body.y + 12.0)).max(0.0);
            let row = (rel_y / NOTES_BODY_LINE_H).floor() as usize + self.body_scroll;
            let rel_x = (x - (lay.body.x + NOTES_BODY_INSET)).max(0.0);
            let col = (rel_x / NOTES_BODY_CHAR_W).floor() as usize;
            self.cursor = notes_index_at(&lines, row, col, self.body.chars().count());
            self.ensure_body_visible();
        }
    }

    /// Esc: editor → list, title → editor, list → close (caller closes).
    /// Returns true when the overlay should close.
    pub fn handle_escape(&mut self) -> bool {
        match self.focus {
            NotesFocus::List => true,
            NotesFocus::Title => {
                self.focus = NotesFocus::Body;
                self.cursor = self.body.chars().count();
                self.ensure_body_visible();
                false
            }
            NotesFocus::Body => {
                self.flush_active();
                self.focus = NotesFocus::List;
                self.ensure_list_visible();
                false
            }
        }
    }

    pub fn on_enter(&mut self) {
        match self.focus {
            NotesFocus::List => self.open_editor(),
            NotesFocus::Title => self.set_focus(NotesFocus::Body),
            NotesFocus::Body => self.insert_char('\n'),
        }
    }

    pub fn on_tab(&mut self, reverse: bool) {
        self.cycle_focus(reverse);
    }

    pub fn on_f2(&mut self) {
        self.set_focus(NotesFocus::Title);
    }

    pub fn on_delete_key(&mut self) {
        if self.focus == NotesFocus::List {
            self.delete_active();
        }
    }

    pub fn on_up(&mut self) {
        match self.focus {
            NotesFocus::List => {
                if self.active > 0 {
                    self.select(self.active - 1);
                }
            }
            NotesFocus::Title => {}
            NotesFocus::Body => {
                if self.body_cursor_row() == 0 {
                    self.focus = NotesFocus::Title;
                    self.cursor = self.title.chars().count();
                } else {
                    self.move_vert(-1);
                }
            }
        }
    }

    pub fn on_down(&mut self) {
        match self.focus {
            NotesFocus::List => {
                if self.active + 1 < self.bank.len() {
                    self.select(self.active + 1);
                }
            }
            NotesFocus::Title => {
                self.focus = NotesFocus::Body;
                self.cursor = self.cursor.min(self.body.chars().count());
                self.ensure_body_visible();
            }
            NotesFocus::Body => self.move_vert(1),
        }
    }

    pub fn on_left(&mut self) {
        if self.focus == NotesFocus::List {
            return;
        }
        self.move_cursor(-1);
    }

    pub fn on_right(&mut self) {
        if self.focus == NotesFocus::List {
            self.open_editor();
            return;
        }
        self.move_cursor(1);
    }

    /// List: n new, d delete, other printable opens editor then inserts.
    pub fn type_char(&mut self, ch: char) {
        if self.focus == NotesFocus::List {
            match ch {
                'n' | 'N' => {
                    self.new_note();
                    return;
                }
                'd' | 'D' => {
                    self.delete_active();
                    return;
                }
                _ => self.open_editor(),
            }
        }
        if ch == '\n' || !ch.is_control() {
            self.insert_char(ch);
        }
    }

    pub fn visible(&self) -> bool {
        self.open || self.present > 0.01
    }

    pub fn present(&self) -> f32 {
        self.present.clamp(0.0, 1.0)
    }

    pub fn open(&mut self) {
        self.open = true;
        // Blank active note: name first (product openNotes).
        if self.active_is_blank() {
            self.focus = NotesFocus::Title;
            self.cursor = self.title.chars().count();
        } else {
            self.focus = NotesFocus::Body;
            self.cursor = self.body.chars().count();
        }
    }

    pub fn close(&mut self) {
        self.flush_active();
        let _ = self.save();
        self.open = false;
    }

    pub fn toggle(&mut self) {
        if self.open {
            self.close();
        } else {
            self.open();
        }
    }

    pub fn tick(&mut self, dt: f32) {
        let dt = dt.clamp(0.0, 1.0 / 20.0);
        let target = if self.open { 1.0 } else { 0.0 };
        const K: f32 = 150.0;
        const C: f32 = 25.0;
        let force = -K * (self.present - target) - C * self.present_vel;
        self.present_vel += force * dt;
        self.present += self.present_vel * dt;
        if self.present > 1.05 {
            self.present = 1.05;
            self.present_vel *= -0.3;
        }
        if self.present < 0.0 {
            self.present = 0.0;
            self.present_vel = 0.0;
        }
        let ov_t = if self.open { 0.5 } else { 0.0 };
        let k = 1.0 - (-dt * 18.0).exp();
        self.overlay += (ov_t - self.overlay) * k;
    }

    pub fn content_ease(&self) -> f32 {
        self.present.clamp(0.0, 1.0)
    }

    pub fn scrim_alpha(&self) -> f32 {
        self.overlay.clamp(0.0, 0.5)
    }

    pub fn animated_modal_rect(&self, win_w: f32, win_h: f32) -> Rect {
        let t = self.content_ease();
        // Wide document card
        let base_w = 640.0_f32.min(win_w * 0.9).max(360.0);
        let base_h = 380.0_f32.min(win_h * 0.72).max(240.0);
        let sx = 0.88 + 0.12 * t;
        let sy = 0.90 + 0.10 * t;
        let w = base_w * sx;
        let h = base_h * sy;
        let x = (win_w - w) * 0.5;
        let y = (win_h - h) * 0.45 + (1.0 - t) * -20.0;
        Rect::new(x, y, w, h)
    }

    fn current_wrap_cols(&self) -> usize {
        if self.body_rect.w > 8.0 {
            notes_wrap_cols(self.body_rect.w)
        } else {
            64
        }
    }

    fn current_body_rows(&self) -> usize {
        if self.body_rect.h > 8.0 {
            notes_body_visible_rows(self.body_rect.h)
        } else {
            12
        }
    }

    fn current_list_rows(&self) -> usize {
        if self.list_hit.is_empty() {
            8
        } else {
            self.list_hit.len().max(1)
        }
    }

    fn body_cursor_row(&self) -> usize {
        let lines = notes_wrap_lines(&self.body, self.current_wrap_cols());
        notes_cursor_row_col(&lines, self.cursor).0
    }

    fn move_vert(&mut self, dir: isize) {
        let cols = self.current_wrap_cols();
        let lines = notes_wrap_lines(&self.body, cols);
        let (row, col) = notes_cursor_row_col(&lines, self.cursor);
        let n = self.body.chars().count();
        let new_row = row as isize + dir;
        if new_row < 0 {
            self.cursor = 0;
        } else if new_row >= lines.len() as isize {
            self.cursor = n;
        } else {
            self.cursor = notes_index_at(&lines, new_row as usize, col, n);
        }
        self.ensure_body_visible();
    }

    fn ensure_body_visible(&mut self) {
        let vis = self.current_body_rows();
        let lines = notes_wrap_lines(&self.body, self.current_wrap_cols());
        let row = notes_cursor_row_col(&lines, self.cursor).0;
        if row < self.body_scroll {
            self.body_scroll = row;
        }
        if row >= self.body_scroll + vis {
            self.body_scroll = row + 1 - vis;
        }
    }

    fn ensure_list_visible(&mut self) {
        let vis = self.current_list_rows();
        if self.active < self.list_scroll {
            self.list_scroll = self.active;
        }
        if self.active >= self.list_scroll + vis {
            self.list_scroll = self.active + 1 - vis;
        }
    }

    pub fn body_scroll(&self) -> usize {
        self.body_scroll
    }

    pub fn insert_char(&mut self, ch: char) {
        match self.focus {
            NotesFocus::List => {}
            NotesFocus::Title => {
                if ch == '\n' {
                    self.set_focus(NotesFocus::Body);
                    return;
                }
                let mut chars: Vec<char> = self.title.chars().collect();
                let i = self.cursor.min(chars.len());
                chars.insert(i, ch);
                self.cursor = i + 1;
                self.title = chars.into_iter().collect();
                self.dirty = true;
            }
            NotesFocus::Body => {
                let mut chars: Vec<char> = self.body.chars().collect();
                if chars.len() >= NOTES_MAX_RUNES {
                    return;
                }
                self.push_body_undo();
                let i = self.cursor.min(chars.len());
                chars.insert(i, ch);
                self.cursor = i + 1;
                self.body = chars.into_iter().collect();
                self.dirty = true;
                self.ensure_body_visible();
            }
        }
    }

    pub fn backspace(&mut self) {
        if self.cursor == 0 {
            return;
        }
        match self.focus {
            NotesFocus::List => {}
            NotesFocus::Title => {
                let mut chars: Vec<char> = self.title.chars().collect();
                let i = self.cursor.min(chars.len());
                if i > 0 {
                    chars.remove(i - 1);
                    self.cursor = i - 1;
                    self.title = chars.into_iter().collect();
                    self.dirty = true;
                }
            }
            NotesFocus::Body => {
                let mut chars: Vec<char> = self.body.chars().collect();
                let i = self.cursor.min(chars.len());
                if i > 0 {
                    self.push_body_undo();
                    chars.remove(i - 1);
                    self.cursor = i - 1;
                    self.body = chars.into_iter().collect();
                    self.dirty = true;
                    self.ensure_body_visible();
                }
            }
        }
    }

    pub fn move_cursor(&mut self, delta: isize) {
        let n = match self.focus {
            NotesFocus::List => return,
            NotesFocus::Title => self.title.chars().count() as isize,
            NotesFocus::Body => self.body.chars().count() as isize,
        };
        let c = (self.cursor as isize + delta).clamp(0, n) as usize;
        self.cursor = c;
    }

    pub fn display_lines(&self) -> Vec<String> {
        let mut lines = Vec::new();
        let title = self.active_display_title();
        lines.push(format!("Notes · {title}"));
        lines.push(String::new());
        if self.body.is_empty() {
            lines.push("  (type to write — ⌘⇧M / Esc to put away)".into());
        } else {
            for line in self.body.lines() {
                lines.push(format!("  {line}"));
            }
        }
        lines.push(String::new());
        let status = if self.dirty {
            "unsaved · auto-save on close"
        } else {
            "saved"
        };
        lines.push(format!(
            "  {status} · {} chars · {} notes",
            self.body.chars().count(),
            self.bank.len()
        ));
        lines
    }

    fn active_is_blank(&self) -> bool {
        self.title.trim().is_empty() && self.body.trim().is_empty()
    }

    fn flush_active(&mut self) {
        if let Some(n) = self.bank.get_mut(self.active) {
            let title = self.title.trim().to_string();
            if n.body != self.body || n.title != title {
                n.body = self.body.clone();
                n.title = title;
                n.updated = notes_ops::now_unix();
                self.dirty = true;
            } else {
                // Keep title trimmed in buffer.
                self.title = title;
            }
        }
    }

    /// Snapshot bank for external save / inspection (flushes editor first).
    pub fn snapshot(&mut self) -> NotesBank {
        self.flush_active();
        let active_id = self
            .bank
            .get(self.active)
            .map(|n| n.id.clone())
            .unwrap_or_default();
        NotesBank {
            active_id,
            notes: self.bank.clone(),
        }
    }

    /// Force a disk write (also used by close).
    pub fn save(&mut self) -> Result<(), String> {
        self.flush_active();
        let bank = NotesBank {
            active_id: self
                .bank
                .get(self.active)
                .map(|n| n.id.clone())
                .unwrap_or_default(),
            notes: self.bank.clone(),
        };
        save_bank(&self.path, &bank)?;
        self.dirty = false;
        Ok(())
    }
}

impl Default for NotesState {
    fn default() -> Self {
        Self::new()
    }
}

/// Layout geometry inside a modal rect — list **or** editor, never both.
pub fn notes_layout_in_modal(
    modal: Rect,
    bank_len: usize,
    list_mode: bool,
    list_scroll: usize,
) -> NotesLayout {
    let pad = NOTES_PAD;
    let inner_x = modal.x + pad;
    let inner_y = modal.y + pad;
    let inner_w = (modal.w - pad * 2.0).max(40.0);
    let inner_h = (modal.h - pad * 2.0 - NOTES_FOOTER_H).max(80.0);
    let footer = Rect::new(
        inner_x,
        modal.y + modal.h - pad - NOTES_FOOTER_H,
        inner_w,
        NOTES_FOOTER_H,
    );

    if list_mode {
        let list = Rect::new(inner_x, inner_y, inner_w, inner_h);
        let rows_top = list.y + NOTES_HEADER_H;
        let actions_h = NOTES_ROW_H * 2.0;
        let rows_bot = (list.y + list.h - actions_h).max(rows_top + NOTES_ROW_H);
        let vis = ((rows_bot - rows_top) / NOTES_ROW_H).floor().max(1.0) as usize;
        let max_start = bank_len.saturating_sub(vis);
        let start = list_scroll.min(max_start);
        let mut list_rows = Vec::new();
        let mut y = rows_top;
        let count = vis.min(bank_len.saturating_sub(start));
        for _ in 0..count {
            list_rows.push(Rect::new(list.x, y, list.w, NOTES_ROW_H));
            y += NOTES_ROW_H;
        }
        let delete_row = Rect::new(list.x, list.y + list.h - NOTES_ROW_H, list.w, NOTES_ROW_H);
        let new_row = Rect::new(list.x, delete_row.y - NOTES_ROW_H, list.w, NOTES_ROW_H);
        return NotesLayout {
            modal,
            list,
            list_rows,
            list_start: start,
            new_row,
            title: Rect::default(),
            body: Rect::default(),
            delete_row,
            footer,
            list_mode: true,
        };
    }

    let title = Rect::new(inner_x, inner_y, inner_w, NOTES_TITLE_H);
    let body_y = title.y + title.h + NOTES_TITLE_BODY_GAP;
    let body_h = (inner_y + inner_h - body_y).max(80.0);
    let body = Rect::new(inner_x, body_y, inner_w, body_h);
    NotesLayout {
        modal,
        list: Rect::default(),
        list_rows: Vec::new(),
        list_start: 0,
        new_row: Rect::default(),
        delete_row: Rect::default(),
        title,
        body,
        footer,
        list_mode: false,
    }
}

/// One soft-wrapped body line (`start`/`end` are char indices).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct NotesWrapLine {
    pub start: usize,
    pub end: usize,
    pub hard: bool,
}

pub fn notes_wrap_cols(body_w: f32) -> usize {
    let inner = (body_w - NOTES_BODY_INSET * 2.0).max(8.0);
    (inner / NOTES_BODY_CHAR_W).floor().max(8.0) as usize
}

pub fn notes_body_visible_rows(body_h: f32) -> usize {
    let inner = (body_h - 12.0 - 8.0).max(NOTES_BODY_LINE_H);
    (inner / NOTES_BODY_LINE_H).floor().max(1.0) as usize
}

pub fn notes_wrap_lines(text: &str, width: usize) -> Vec<NotesWrapLine> {
    let width = width.max(4);
    let chars: Vec<char> = text.chars().collect();
    if chars.is_empty() {
        return vec![NotesWrapLine {
            start: 0,
            end: 0,
            hard: false,
        }];
    }
    let mut out = Vec::new();
    let mut start = 0;
    let mut col = 0;
    for (i, ch) in chars.iter().enumerate() {
        if *ch == '\n' {
            out.push(NotesWrapLine {
                start,
                end: i,
                hard: true,
            });
            start = i + 1;
            col = 0;
            continue;
        }
        if col > 0 && col + 1 > width {
            out.push(NotesWrapLine {
                start,
                end: i,
                hard: false,
            });
            start = i;
            col = 0;
        }
        col += 1;
    }
    out.push(NotesWrapLine {
        start,
        end: chars.len(),
        hard: false,
    });
    out
}

pub fn notes_cursor_row_col(lines: &[NotesWrapLine], cursor: usize) -> (usize, usize) {
    if lines.is_empty() {
        return (0, 0);
    }
    for (i, ln) in lines.iter().enumerate() {
        if cursor >= ln.start && cursor <= ln.end {
            if cursor == ln.end && !ln.hard && i + 1 < lines.len() && lines[i + 1].start == ln.end {
                continue;
            }
            return (i, cursor.saturating_sub(ln.start));
        }
    }
    let last = lines.len() - 1;
    (last, lines[last].end.saturating_sub(lines[last].start))
}

pub fn notes_index_at(lines: &[NotesWrapLine], row: usize, col: usize, text_len: usize) -> usize {
    if lines.is_empty() {
        return 0;
    }
    if row >= lines.len() {
        return text_len;
    }
    let ln = lines[row];
    (ln.start + col).min(ln.end).min(text_len)
}

pub fn notes_line_text(text: &str, ln: NotesWrapLine) -> String {
    text.chars()
        .skip(ln.start)
        .take(ln.end.saturating_sub(ln.start))
        .collect()
}

fn json_string(s: &str) -> String {
    let mut o = String::from("\"");
    for c in s.chars() {
        match c {
            '"' => o.push_str("\\\""),
            '\\' => o.push_str("\\\\"),
            '\n' => o.push_str("\\n"),
            '\r' => o.push_str("\\r"),
            '\t' => o.push_str("\\t"),
            c if c < ' ' => o.push_str(&format!("\\u{:04x}", c as u32)),
            c => o.push(c),
        }
    }
    o.push('"');
    o
}

fn notes_path() -> PathBuf {
    #[cfg(target_os = "macos")]
    {
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join("Library/Application Support/suzuri/notes.json");
        }
    }
    #[cfg(target_os = "windows")]
    {
        if let Some(base) = std::env::var_os("LOCALAPPDATA") {
            return PathBuf::from(base).join("suzuri").join("notes.json");
        }
    }
    if let Some(home) = std::env::var_os("HOME") {
        return PathBuf::from(home).join(".config/suzuri/notes.json");
    }
    PathBuf::from("notes.json")
}

fn load_bank(path: &Path) -> NotesBank {
    let Ok(raw) = fs::read_to_string(path) else {
        return NotesBank::default_scratch();
    };
    parse_notes_json(&raw).unwrap_or_else(|_| NotesBank::default_scratch())
}

/// Parse product-shaped notes.json (`active_id` + `notes`).
pub fn parse_notes_json(raw: &str) -> Result<NotesBank, String> {
    let active_id = extract_string_field(raw, "active_id")
        .or_else(|| extract_string_field(raw, "activeId"))
        .unwrap_or_default();

    // Find the notes array.
    let notes_key = raw.find("\"notes\"").ok_or("missing notes")?;
    let after = &raw[notes_key + "\"notes\"".len()..];
    let bracket = after.find('[').ok_or("missing notes array")?;
    let arr = &after[bracket + 1..];

    let mut notes = Vec::new();
    // Split on objects by `"id"` keys inside the array region (until matching `]`
    // at depth 0 is hard without a full parser — walk objects instead).
    let mut rest = arr;
    while let Some(obj_start) = rest.find('{') {
        // Stop if we hit array end before this object (crude: check if `]` appears first).
        if let Some(end_arr) = rest.find(']') {
            if end_arr < obj_start {
                break;
            }
        }
        let obj_region = &rest[obj_start..];
        let obj_end = find_matching_brace(obj_region).ok_or("unclosed note object")?;
        let chunk = &obj_region[..=obj_end];
        if let Some(id) = extract_string_field(chunk, "id") {
            if !id.is_empty() {
                let title = extract_string_field(chunk, "title").unwrap_or_default();
                let body = extract_string_field(chunk, "body").unwrap_or_default();
                let updated = extract_string_field(chunk, "updated")
                    .and_then(|s| parse_rfc3339_approx(&s))
                    .unwrap_or(0);
                notes.push(NoteDoc {
                    id,
                    title,
                    body,
                    updated,
                });
            }
        }
        rest = &obj_region[obj_end + 1..];
    }

    if notes.is_empty() {
        return Ok(NotesBank::default_scratch());
    }
    Ok(normalize_bank(NotesBank { active_id, notes }))
}

fn find_matching_brace(s: &str) -> Option<usize> {
    let mut depth = 0i32;
    let mut in_str = false;
    let mut escape = false;
    for (i, c) in s.char_indices() {
        if in_str {
            if escape {
                escape = false;
                continue;
            }
            match c {
                '\\' => escape = true,
                '"' => in_str = false,
                _ => {}
            }
            continue;
        }
        match c {
            '"' => in_str = true,
            '{' => depth += 1,
            '}' => {
                depth -= 1;
                if depth == 0 {
                    return Some(i);
                }
            }
            _ => {}
        }
    }
    None
}

fn parse_rfc3339_approx(s: &str) -> Option<u64> {
    // Accept "YYYY-MM-DDTHH:MM:SS..." — only need non-zero for normalize.
    // Full parse is unnecessary; store 0 if unknown.
    if s.len() < 19 {
        return None;
    }
    let year: u64 = s.get(0..4)?.parse().ok()?;
    let month: u64 = s.get(5..7)?.parse().ok()?;
    let day: u64 = s.get(8..10)?.parse().ok()?;
    let hour: u64 = s.get(11..13)?.parse().ok()?;
    let min: u64 = s.get(14..16)?.parse().ok()?;
    let sec: u64 = s.get(17..19)?.parse().ok()?;
    // Rough seconds since epoch (not calendar-perfect; enough for dirty stamps).
    let days = year.saturating_sub(1970) * 365 + (month.saturating_sub(1)) * 30 + day;
    Some(days * 86400 + hour * 3600 + min * 60 + sec)
}

/// Write product-compatible notes.json (atomic replace when possible).
pub fn save_bank(path: &Path, bank: &NotesBank) -> Result<(), String> {
    let bank = normalize_bank(bank.clone());
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    let mut out = String::from("{\n  \"active_id\": ");
    out.push_str(&json_string(&bank.active_id));
    out.push_str(",\n  \"notes\": [\n");
    for (i, n) in bank.notes.iter().enumerate() {
        if i > 0 {
            out.push_str(",\n");
        }
        out.push_str("    {\n      \"id\": ");
        out.push_str(&json_string(&n.id));
        out.push_str(",\n      \"title\": ");
        out.push_str(&json_string(&n.title));
        out.push_str(",\n      \"body\": ");
        out.push_str(&json_string(&n.body));
        if n.updated > 0 {
            out.push_str(",\n      \"updated\": ");
            out.push_str(&json_string(&format_rfc3339(n.updated)));
        }
        out.push_str("\n    }");
    }
    out.push_str("\n  ]\n}\n");

    let tmp = path.with_extension("json.tmp");
    fs::write(&tmp, &out).map_err(|e| e.to_string())?;
    if let Err(e) = fs::rename(&tmp, path) {
        // Windows may need remove-first; try fallback write.
        let _ = fs::remove_file(path);
        if let Err(e2) = fs::rename(&tmp, path) {
            let _ = fs::remove_file(&tmp);
            // Last resort: direct write.
            fs::write(path, out).map_err(|e3| format!("{e}; {e2}; {e3}"))?;
        }
    }
    Ok(())
}

fn format_rfc3339(unix: u64) -> String {
    // UTC rough formatter (good enough for product round-trip / display).
    let secs = unix;
    let sec = secs % 60;
    let mins = secs / 60;
    let min = mins % 60;
    let hours = mins / 60;
    let hour = hours % 24;
    let days = hours / 24;
    // Civil date from days since 1970-01-01 (Howard Hinnant algorithm, simplified).
    let (y, m, d) = civil_from_days(days as i64);
    format!("{y:04}-{m:02}-{d:02}T{hour:02}:{min:02}:{sec:02}Z")
}

fn civil_from_days(days: i64) -> (i32, u32, u32) {
    // Algorithm from http://howardhinnant.github.io/date_algorithms.html
    let z = days + 719_468;
    let era = if z >= 0 { z } else { z - 146_096 } / 146_097;
    let doe = (z - era * 146_097) as u64;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146_096) / 365;
    let y = yoe as i64 + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = if m <= 2 { y + 1 } else { y };
    (y as i32, m as u32, d as u32)
}

fn extract_string_field(s: &str, key: &str) -> Option<String> {
    let pat = format!("\"{key}\"");
    let i = s.find(&pat)?;
    let rest = &s[i + pat.len()..];
    extract_quoted_after_colon(rest)
}

fn extract_quoted_after_colon(s: &str) -> Option<String> {
    let colon = s.find(':')?;
    let rest = s[colon + 1..].trim_start();
    if !rest.starts_with('"') {
        return None;
    }
    let mut out = String::new();
    let mut chars = rest[1..].chars();
    while let Some(c) = chars.next() {
        match c {
            '"' => return Some(out),
            '\\' => match chars.next()? {
                'n' => out.push('\n'),
                'r' => out.push('\r'),
                't' => out.push('\t'),
                '"' => out.push('"'),
                '\\' => out.push('\\'),
                'u' => {
                    let h: String = chars.by_ref().take(4).collect();
                    if let Ok(v) = u32::from_str_radix(&h, 16) {
                        if let Some(ch) = char::from_u32(v) {
                            out.push(ch);
                        }
                    }
                }
                other => out.push(other),
            },
            c => out.push(c),
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_notes_path(label: &str) -> PathBuf {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or(0);
        let mut p = std::env::temp_dir();
        p.push(format!("suzuri-notes-test-{label}-{nanos}.json"));
        p
    }

    #[test]
    fn round_trip_persist() {
        let path = temp_notes_path("rt");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        s.open();
        s.set_focus(NotesFocus::Title);
        for ch in "Hello".chars() {
            s.insert_char(ch);
        }
        s.set_focus(NotesFocus::Body);
        for ch in "world\nline2".chars() {
            s.insert_char(ch);
        }
        s.close();
        assert!(path.exists());

        let s2 = NotesState::with_path(&path);
        assert_eq!(s2.title, "Hello");
        assert_eq!(s2.body, "world\nline2");
        assert_eq!(s2.bank().len(), 1);
        let raw = fs::read_to_string(&path).unwrap();
        assert!(raw.contains("\"active_id\""));
        assert!(raw.contains("Hello"));
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn multi_note_select_create_delete() {
        let path = temp_notes_path("bank");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        assert_eq!(s.bank().len(), 1);

        s.set_focus(NotesFocus::Body);
        s.insert_char('a');
        s.new_note();
        assert_eq!(s.bank().len(), 2);
        assert_eq!(s.focus, NotesFocus::Title);
        s.insert_char('B');
        s.set_focus(NotesFocus::Body);
        s.insert_char('2');

        s.select(0);
        assert_eq!(s.body, "a");
        s.select(1);
        assert_eq!(s.body, "2");
        assert_eq!(s.title, "B");

        s.delete_active();
        assert_eq!(s.bank().len(), 1);
        assert_eq!(s.body, "a");

        // Last note clears.
        s.delete_active();
        assert_eq!(s.bank().len(), 1);
        assert!(s.body.is_empty());
        assert!(s.title.is_empty());

        s.close();
        let raw = fs::read_to_string(&path).unwrap();
        assert!(raw.contains("\"active_id\""));
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn layout_two_screens_no_sidebar() {
        let path = temp_notes_path("lay");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        s.open = true;
        s.present = 1.0;

        s.set_focus(NotesFocus::List);
        let list = s.layout(800.0, 600.0);
        assert!(list.list_mode);
        assert!(
            list.list.w > 200.0,
            "list uses the full card, not a sidebar"
        );
        assert!(list.list.w > list.modal.w * 0.7);
        assert_eq!(list.title.w, 0.0);
        assert_eq!(list.body.w, 0.0);
        assert!(!list.list_rows.is_empty());
        // List frost and editor frost must not coexist.
        assert!(list.title.w < 1.0 || list.list.w < 1.0);

        let r = list.list_rows[0];
        s.try_click(r.x + 4.0, r.y + 4.0, 800.0, 600.0);
        assert_ne!(s.focus, NotesFocus::List);

        let ed = s.layout(800.0, 600.0);
        assert!(!ed.list_mode);
        assert_eq!(ed.list.w, 0.0);
        assert!(ed.list_rows.is_empty());
        assert!(ed.title.w > 200.0);
        assert!(ed.body.w > 200.0);
        assert!(ed.body.h >= 80.0);
        assert!(
            (ed.title.x - ed.body.x).abs() < 0.5,
            "title and body share the full width"
        );

        s.try_click(ed.title.x + 8.0, ed.title.y + 8.0, 800.0, 600.0);
        assert_eq!(s.focus, NotesFocus::Title);
        s.try_click(ed.body.x + 8.0, ed.body.y + 8.0, 800.0, 600.0);
        assert_eq!(s.focus, NotesFocus::Body);

        let _ = fs::remove_file(&path);
    }

    #[test]
    fn wrap_long_line_stays_in_cols() {
        let text = "Need to try Gemma training and usage out. Can it do basket ball in the net recognition? Can it run?";
        let lines = notes_wrap_lines(text, 24);
        assert!(lines.len() > 1);
        for ln in &lines {
            assert!(ln.end.saturating_sub(ln.start) <= 24);
        }
        let (row, col) = notes_cursor_row_col(&lines, text.chars().count());
        assert_eq!(row, lines.len() - 1);
        assert!(col <= 24);
    }

    #[test]
    fn escape_editor_to_list_then_close() {
        let path = temp_notes_path("esc");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        s.open();
        assert_eq!(s.focus, NotesFocus::Title); // blank scratch
        assert!(!s.handle_escape());
        assert_eq!(s.focus, NotesFocus::Body);
        assert!(!s.handle_escape());
        assert_eq!(s.focus, NotesFocus::List);
        assert!(s.handle_escape());
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn list_keys_new_and_open() {
        let path = temp_notes_path("keys");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        s.set_focus(NotesFocus::List);
        s.type_char('n');
        assert_eq!(s.focus, NotesFocus::Title);
        assert_eq!(s.bank().len(), 2);
        s.handle_escape(); // title → body
        s.handle_escape(); // body → list
        assert_eq!(s.focus, NotesFocus::List);
        s.on_enter();
        assert_ne!(s.focus, NotesFocus::List);
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn parse_product_sample_shape() {
        let raw = r#"{
  "active_id": "abc",
  "notes": [
    { "id": "abc", "title": "T", "body": "line1\nline2" },
    { "id": "def", "title": "", "body": "other" }
  ]
}
"#;
        let bank = parse_notes_json(raw).unwrap();
        assert_eq!(bank.active_id, "abc");
        assert_eq!(bank.notes.len(), 2);
        assert_eq!(bank.notes[0].body, "line1\nline2");
    }

    #[test]
    fn open_close_toggle_api() {
        let path = temp_notes_path("api");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        assert!(!s.open);
        s.open();
        assert!(s.open);
        s.toggle();
        assert!(!s.open);
        s.toggle();
        assert!(s.open);
        s.close();
        assert!(!s.open);
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn body_undo_redo_stack() {
        let path = temp_notes_path("undo");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        s.set_focus(NotesFocus::Body);
        assert!(!s.can_undo());
        assert!(!s.can_redo());

        s.insert_char('a');
        s.insert_char('b');
        s.insert_char('c');
        assert_eq!(s.body, "abc");
        assert!(s.can_undo());
        assert!(!s.can_redo());

        assert!(s.undo());
        assert_eq!(s.body, "ab");
        assert!(s.can_redo());
        assert!(s.undo());
        assert_eq!(s.body, "a");
        assert!(s.undo());
        assert_eq!(s.body, "");
        assert!(!s.can_undo());

        assert!(s.redo());
        assert_eq!(s.body, "a");
        assert!(s.redo());
        assert_eq!(s.body, "ab");
        assert!(s.redo());
        assert_eq!(s.body, "abc");
        assert!(!s.can_redo());

        // New edit after undo drops redo branch.
        assert!(s.undo());
        assert_eq!(s.body, "ab");
        s.insert_char('X');
        assert_eq!(s.body, "abX");
        assert!(!s.can_redo());
        assert!(s.undo());
        assert_eq!(s.body, "ab");

        // Title edits do not push body history.
        s.set_focus(NotesFocus::Title);
        s.insert_char('T');
        assert_eq!(s.body, "ab");
        // Still can undo body.
        assert!(s.can_undo());
        assert!(s.undo());
        assert_eq!(s.focus, NotesFocus::Body);

        // Switching notes clears history.
        s.insert_char('z');
        assert!(s.can_undo());
        s.new_note();
        assert!(!s.can_undo());
        assert!(!s.can_redo());

        let _ = fs::remove_file(&path);
    }

    #[test]
    fn title_body_focus_tab_and_click() {
        let path = temp_notes_path("focus");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        s.open = true;
        s.present = 1.0;
        s.set_focus(NotesFocus::Title);
        s.insert_char('H');
        // Typing goes to title.
        assert_eq!(s.title, "H");
        assert!(s.body.is_empty());

        s.cycle_focus(false);
        assert_eq!(s.focus, NotesFocus::Body);
        s.insert_char('b');
        assert_eq!(s.body, "b");
        assert_eq!(s.title, "H");

        s.cycle_focus(false);
        assert_eq!(s.focus, NotesFocus::Title);

        // Enter in title commits to body.
        s.insert_char('\n');
        assert_eq!(s.focus, NotesFocus::Body);

        let lay = s.layout(800.0, 600.0);
        s.try_click(lay.title.x + 4.0, lay.title.y + 4.0, 800.0, 600.0);
        assert_eq!(s.focus, NotesFocus::Title);
        s.try_click(lay.body.x + 4.0, lay.body.y + 4.0, 800.0, 600.0);
        assert_eq!(s.focus, NotesFocus::Body);

        let _ = fs::remove_file(&path);
    }

    #[test]
    fn body_hist_limit_trims_oldest() {
        let path = temp_notes_path("lim");
        let _ = fs::remove_file(&path);
        let mut s = NotesState::with_path(&path);
        s.set_focus(NotesFocus::Body);
        for i in 0..(BODY_HIST_LIMIT + 10) {
            s.insert_char(char::from(b'a' + (i % 26) as u8));
        }
        assert_eq!(s.body_hist.past.len(), BODY_HIST_LIMIT);
        assert!(s.undo());
        let _ = fs::remove_file(&path);
    }
}
