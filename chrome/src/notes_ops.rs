//! Pure notes-bank operations (product parity with `internal/chrome/notes_ops.go`).
//!
//! No I/O — callers load/save via [`crate::notes`] persistence helpers.

use std::time::{SystemTime, UNIX_EPOCH};

/// Soft cap matching product `notesMaxBank`.
pub const NOTES_MAX_BANK: usize = 48;
/// Soft cap matching product `notesMaxRunes`.
pub const NOTES_MAX_RUNES: usize = 64 * 1024;

/// One note document — product-compatible subset of `NoteDoc`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NoteDoc {
    pub id: String,
    pub title: String,
    pub body: String,
    /// Unix seconds; 0 means “unset” (product fills on normalize).
    pub updated: u64,
}

/// In-memory multi-note store — product `NotesBank`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NotesBank {
    pub active_id: String,
    pub notes: Vec<NoteDoc>,
}

impl NotesBank {
    pub fn default_scratch() -> Self {
        let n = new_note_doc("", "");
        Self {
            active_id: n.id.clone(),
            notes: vec![n],
        }
    }
}

/// Label for the bank strip / list (product `NoteDisplayTitle`).
pub fn note_display_title(n: &NoteDoc) -> String {
    let t = n.title.trim();
    if !t.is_empty() {
        return truncate_title(t, 28);
    }
    let body = n.body.trim();
    if body.is_empty() {
        return "Untitled".into();
    }
    let line = body.lines().next().unwrap_or("").trim();
    if line.is_empty() {
        return "Untitled".into();
    }
    truncate_title(line, 28)
}

fn truncate_title(s: &str, max: usize) -> String {
    let rs: Vec<char> = s.chars().collect();
    if rs.len() <= max {
        return s.to_string();
    }
    if max < 2 {
        return rs.into_iter().take(max).collect();
    }
    let mut out: String = rs.into_iter().take(max - 1).collect();
    out.push('…');
    out
}

pub fn new_note_id() -> String {
    // Prefer 16 hex chars like product (`crypto/rand` 8 bytes).
    let t = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    // Mix time bits for uniqueness within a process.
    let a = (t ^ (t >> 17) ^ (t << 7)) as u64;
    let b = a.wrapping_mul(0x9E37_79B9_7F4A_7C15).rotate_left(13);
    format!("{a:08x}{b:08x}")
}

pub fn now_unix() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

pub fn new_note_doc(title: &str, body: &str) -> NoteDoc {
    NoteDoc {
        id: new_note_id(),
        title: title.trim().to_string(),
        body: body.to_string(),
        updated: now_unix(),
    }
}

/// Cap body runes, repair empty ids, ensure active_id is valid.
pub fn normalize_bank(mut bank: NotesBank) -> NotesBank {
    if bank.notes.is_empty() {
        return NotesBank::default_scratch();
    }
    for n in &mut bank.notes {
        if n.id.trim().is_empty() {
            n.id = new_note_id();
        }
        if n.updated == 0 {
            n.updated = now_unix();
        }
        let rs: Vec<char> = n.body.chars().collect();
        if rs.len() > NOTES_MAX_RUNES {
            n.body = rs.into_iter().take(NOTES_MAX_RUNES).collect();
        }
    }
    if bank.active_id.is_empty()
        || !bank.notes.iter().any(|n| n.id == bank.active_id)
    {
        bank.active_id = bank.notes[0].id.clone();
    }
    bank
}

pub fn bank_find_note<'a>(bank: &'a NotesBank, id: &str) -> Option<&'a NoteDoc> {
    if bank.notes.is_empty() {
        return None;
    }
    let id = if id.trim().is_empty() {
        bank.active_id.as_str()
    } else {
        id
    };
    bank.notes.iter().find(|n| n.id == id)
}

pub fn bank_active_index(bank: &NotesBank) -> usize {
    bank.notes
        .iter()
        .position(|n| n.id == bank.active_id)
        .unwrap_or(0)
}

/// Append a note, make it active. Errors if bank is full.
pub fn bank_create_note(
    mut bank: NotesBank,
    title: &str,
    body: &str,
) -> Result<(NotesBank, NoteDoc), String> {
    bank = normalize_bank(bank);
    if bank.notes.len() >= NOTES_MAX_BANK {
        return Err(format!("notes bank full (max {NOTES_MAX_BANK})"));
    }
    let n = new_note_doc(title, body);
    bank.active_id = n.id.clone();
    bank.notes.push(n.clone());
    Ok((bank, n))
}

/// Patch title and/or body. `None` leaves field unchanged; `Some("")` clears title.
pub fn bank_update_note(
    mut bank: NotesBank,
    id: &str,
    title: Option<&str>,
    body: Option<&str>,
) -> Result<(NotesBank, NoteDoc), String> {
    bank = normalize_bank(bank);
    let id = if id.trim().is_empty() {
        bank.active_id.clone()
    } else {
        id.to_string()
    };
    for n in &mut bank.notes {
        if n.id != id {
            continue;
        }
        if let Some(t) = title {
            n.title = t.trim().to_string();
        }
        if let Some(b) = body {
            let rs: Vec<char> = b.chars().collect();
            n.body = if rs.len() > NOTES_MAX_RUNES {
                rs.into_iter().take(NOTES_MAX_RUNES).collect()
            } else {
                b.to_string()
            };
        }
        n.updated = now_unix();
        let out = n.clone();
        return Ok((bank, out));
    }
    Err(format!("note not found: {id}"))
}

/// Remove a note by id. Last note is cleared instead of emptying the bank.
/// Returns the removed (or cleared) doc.
pub fn bank_delete_note(
    mut bank: NotesBank,
    id: &str,
) -> Result<(NotesBank, NoteDoc), String> {
    bank = normalize_bank(bank);
    let id = if id.trim().is_empty() {
        bank.active_id.clone()
    } else {
        id.to_string()
    };
    let idx = bank
        .notes
        .iter()
        .position(|n| n.id == id)
        .ok_or_else(|| format!("note not found: {id}"))?;

    if bank.notes.len() == 1 {
        let mut n = bank.notes[0].clone();
        n.title.clear();
        n.body.clear();
        n.updated = now_unix();
        bank.notes[0] = n.clone();
        bank.active_id = n.id.clone();
        return Ok((bank, n));
    }

    let removed = bank.notes.remove(idx);
    if bank.active_id == id {
        let next = idx.min(bank.notes.len() - 1);
        bank.active_id = bank.notes[next].id.clone();
    }
    Ok((bank, removed))
}

pub fn bank_set_active(mut bank: NotesBank, id: &str) -> Result<NotesBank, String> {
    bank = normalize_bank(bank);
    if id.trim().is_empty() {
        return Ok(bank);
    }
    if bank.notes.iter().any(|n| n.id == id) {
        bank.active_id = id.to_string();
        Ok(bank)
    } else {
        Err(format!("note not found: {id}"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_update_delete() {
        let bank = NotesBank::default_scratch();
        assert_eq!(bank.notes.len(), 1);

        let (bank, n) = bank_create_note(bank, "Hello", "body one").unwrap();
        assert_eq!(n.title, "Hello");
        assert_eq!(n.body, "body one");
        assert_eq!(bank.active_id, n.id);
        assert_eq!(bank.notes.len(), 2);

        let (bank, n2) =
            bank_update_note(bank, &n.id, Some("Hello2"), Some("body two")).unwrap();
        assert_eq!(n2.title, "Hello2");
        assert_eq!(n2.body, "body two");

        let (bank, n3) = bank_update_note(bank, &n.id, None, Some("body three")).unwrap();
        assert_eq!(n3.title, "Hello2");
        assert_eq!(n3.body, "body three");

        let (bank, _) = bank_delete_note(bank, &n.id).unwrap();
        assert_eq!(bank.notes.len(), 1);

        let last_id = bank.notes[0].id.clone();
        let (bank, _) = bank_delete_note(bank, &last_id).unwrap();
        assert_eq!(bank.notes.len(), 1);
        assert!(bank.notes[0].body.is_empty());
        assert!(bank.notes[0].title.is_empty());
    }

    #[test]
    fn find_active_empty_id() {
        let bank = NotesBank::default_scratch();
        let (bank, created) = bank_create_note(bank, "A", "aaa").unwrap();
        let got = bank_find_note(&bank, "").unwrap();
        assert_eq!(got.id, created.id);
    }

    #[test]
    fn display_title_falls_back_to_body() {
        let n = NoteDoc {
            id: "x".into(),
            title: String::new(),
            body: "First line\nSecond".into(),
            updated: 0,
        };
        assert_eq!(note_display_title(&n), "First line");
        let empty = NoteDoc {
            id: "y".into(),
            title: String::new(),
            body: String::new(),
            updated: 0,
        };
        assert_eq!(note_display_title(&empty), "Untitled");
    }

    #[test]
    fn bank_full() {
        let mut bank = NotesBank::default_scratch();
        // default already has 1
        for i in 0..(NOTES_MAX_BANK - 1) {
            bank = bank_create_note(bank, &format!("n{i}"), "").unwrap().0;
        }
        assert_eq!(bank.notes.len(), NOTES_MAX_BANK);
        assert!(bank_create_note(bank, "over", "").is_err());
    }
}
