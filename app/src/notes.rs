//! Notes (#294): Go suzuri's notes bank, the same notes.json in Go suzuri's
//! config dir, so both apps and `suzuri mcp` see one set of notes.
//!
//! The format is Go's NotesBank: {"active_id", "notes": [{id, title?, body,
//! updated}]}. Loading repairs it as Go's normalizeNotesBank does; saving
//! writes it atomically (temp file + rename). ui/notes.js is the panel.

use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

/// Go's notesMaxRunes: a body is capped at 64 Ki characters.
pub const MAX_RUNES: usize = 64 * 1024;

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Note {
    #[serde(default)]
    pub id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub title: String,
    #[serde(default)]
    pub body: String,
    /// RFC 3339, as Go's time.Time marshals it.
    #[serde(default)]
    pub updated: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Bank {
    #[serde(default)]
    pub active_id: String,
    #[serde(default)]
    pub notes: Vec<Note>,
}

pub fn path() -> PathBuf {
    crate::sidecar::config_dir().join("notes.json")
}

fn new_id() -> String {
    use std::collections::hash_map::RandomState;
    use std::hash::{BuildHasher, Hasher};
    let mut h = RandomState::new().build_hasher();
    h.write_u128(std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0));
    format!("{:016x}", h.finish())
}

fn blank_bank(now: &str) -> Bank {
    let n = Note { id: new_id(), updated: now.into(), ..Default::default() };
    Bank { active_id: n.id.clone(), notes: vec![n] }
}

/// Go's normalizeNotesBank: never empty, every note has an id and a time,
/// bodies capped, and active_id names a note that exists.
pub fn normalize(mut b: Bank, now: &str) -> Bank {
    if b.notes.is_empty() {
        return blank_bank(now);
    }
    for n in &mut b.notes {
        if n.id.trim().is_empty() {
            n.id = new_id();
        }
        if n.updated.is_empty() {
            n.updated = now.into();
        }
        if let Some((i, _)) = n.body.char_indices().nth(MAX_RUNES) {
            n.body.truncate(i);
        }
    }
    if !b.notes.iter().any(|n| n.id == b.active_id) {
        b.active_id = b.notes[0].id.clone();
    }
    b
}

pub fn load_from(p: &Path, now: &str) -> Result<Bank, String> {
    match std::fs::read_to_string(p) {
        Ok(s) => serde_json::from_str::<Bank>(&s).map(|b| normalize(b, now)).map_err(|e| format!("parse notes: {e}")),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(blank_bank(now)),
        Err(e) => Err(e.to_string()),
    }
}

pub fn save_to(p: &Path, bank: Bank, now: &str) -> Result<Bank, String> {
    let bank = normalize(bank, now);
    if let Some(dir) = p.parent() {
        std::fs::create_dir_all(dir).map_err(|e| e.to_string())?;
    }
    let mut raw = serde_json::to_string_pretty(&bank).map_err(|e| e.to_string())?;
    raw.push('\n');
    let tmp = p.with_extension("json.tmp");
    std::fs::write(&tmp, raw).map_err(|e| e.to_string())?;
    if std::fs::rename(&tmp, p).is_err() {
        // Windows can refuse to replace; Go removes and retries the same way.
        let _ = std::fs::remove_file(p);
        std::fs::rename(&tmp, p).map_err(|e| {
            let _ = std::fs::remove_file(&tmp);
            e.to_string()
        })?;
    }
    Ok(bank)
}

fn now() -> String {
    crate::bridge::rfc3339_now()
}

/// The bank from disk (a fresh blank note if there is no file yet). A file
/// Go suzuri can't parse either is reported, not overwritten.
#[tauri::command]
pub fn notes_load() -> Result<Bank, String> {
    load_from(&path(), &now())
}

/// Writes the bank and returns it as saved (normalized).
#[tauri::command]
pub fn notes_save(bank: Bank) -> Result<Bank, String> {
    save_to(&path(), bank, &now())
}

#[cfg(test)]
mod tests {
    use super::*;

    const NOW: &str = "2026-11-03T05:00:00Z";

    fn tmp(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("rsz-notes-{}-{name}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        d.join("notes.json")
    }

    #[test]
    fn missing_file_is_one_blank_note() {
        let b = load_from(&tmp("missing"), NOW).unwrap();
        assert_eq!(b.notes.len(), 1);
        assert_eq!(b.active_id, b.notes[0].id);
        assert!(b.notes[0].body.is_empty() && b.notes[0].title.is_empty());
        assert_eq!(b.notes[0].updated, NOW);
    }

    #[test]
    fn normalize_repairs_like_go() {
        let long = "é".repeat(MAX_RUNES + 5);
        let b = normalize(
            Bank {
                active_id: "gone".into(),
                notes: vec![Note { id: " ".into(), body: long, ..Default::default() }, Note { id: "b".into(), updated: "x".into(), ..Default::default() }],
            },
            NOW,
        );
        assert_eq!(b.notes[0].body.chars().count(), MAX_RUNES);
        assert!(!b.notes[0].id.trim().is_empty());
        assert_eq!(b.notes[0].updated, NOW);
        assert_eq!(b.notes[1].updated, "x");
        assert_eq!(b.active_id, b.notes[0].id);
        assert_eq!(normalize(Bank::default(), NOW).notes.len(), 1);
    }

    #[test]
    fn reads_go_suzuri_files_and_round_trips() {
        let p = tmp("go");
        std::fs::create_dir_all(p.parent().unwrap()).unwrap();
        // As Go writes it: title omitted when empty, nanosecond times.
        let go = r#"{
  "active_id": "b2",
  "notes": [
    {"id": "a1", "body": "first\nline", "updated": "2026-10-30T12:00:00.123456789-07:00"},
    {"id": "b2", "title": "Todo", "body": "x", "updated": "2026-10-31T01:02:03Z"}
  ]
}"#;
        std::fs::write(&p, go).unwrap();
        let b = load_from(&p, NOW).unwrap();
        assert_eq!(b.active_id, "b2");
        assert_eq!(b.notes[0].updated, "2026-10-30T12:00:00.123456789-07:00");
        assert_eq!(b.notes[1].title, "Todo");
        let saved = save_to(&p, b.clone(), NOW).unwrap();
        assert_eq!(saved, b);
        let raw = std::fs::read_to_string(&p).unwrap();
        assert!(!raw.contains("\"title\": \"\""), "empty title is omitted as in Go");
        assert!(raw.ends_with("}\n"));
        assert_eq!(load_from(&p, NOW).unwrap(), b);
        assert!(!p.with_extension("json.tmp").exists());
        let _ = std::fs::remove_dir_all(p.parent().unwrap());
    }

    #[test]
    fn a_broken_file_is_an_error_not_a_reset() {
        let p = tmp("broken");
        std::fs::create_dir_all(p.parent().unwrap()).unwrap();
        std::fs::write(&p, "{not json").unwrap();
        assert!(load_from(&p, NOW).unwrap_err().starts_with("parse notes"));
        assert_eq!(std::fs::read_to_string(&p).unwrap(), "{not json");
        let _ = std::fs::remove_dir_all(p.parent().unwrap());
    }
}
