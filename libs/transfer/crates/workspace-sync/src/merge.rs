//! Append-only jsonl merge: skip lines whose message `id` is already present.

use anyhow::{Context, Result};
use serde_json::Value;
use std::collections::HashSet;
use std::fs::{self, OpenOptions};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};

pub fn extract_id(line: &str) -> Option<String> {
    let v: Value = serde_json::from_str(line.trim()).ok()?;
    v.get("id")?.as_str().map(|s| s.to_string())
}

pub fn load_ids(path: &Path) -> Result<HashSet<String>> {
    let mut ids = HashSet::new();
    if !path.exists() {
        return Ok(ids);
    }
    let f = fs::File::open(path).with_context(|| format!("open {}", path.display()))?;
    for line in BufReader::new(f).lines() {
        let line = line?;
        if let Some(id) = extract_id(&line) {
            ids.insert(id);
        }
    }
    Ok(ids)
}

/// Append `line` to channel jsonl if `id` is new. Creates channel dir + empty meta.json.
/// Returns true if appended.
pub fn ingest_line(root: &Path, channel: &str, id: &str, line: &str) -> Result<bool> {
    let slug = normalize_channel(channel);
    if slug.is_empty() {
        anyhow::bail!("invalid channel");
    }
    let ch_dir = root.join("channels").join(&slug);
    fs::create_dir_all(&ch_dir)?;
    let meta = ch_dir.join("meta.json");
    if !meta.exists() {
        let body = format!(
            "{{\n  \"id\": \"{slug}\",\n  \"name\": \"{slug}\",\n  \"created_at\": \"1970-01-01T00:00:00Z\"\n}}\n"
        );
        fs::write(&meta, body)?;
    }
    let path = ch_dir.join("messages.jsonl");
    if !path.exists() {
        fs::write(&path, b"")?;
    }
    let ids = load_ids(&path)?;
    if ids.contains(id) {
        return Ok(false);
    }
    let mut f = OpenOptions::new().create(true).append(true).open(&path)?;
    let mut raw = line.trim().to_string();
    if !raw.ends_with('\n') {
        raw.push('\n');
    }
    f.write_all(raw.as_bytes())?;
    Ok(true)
}

pub fn list_channel_slugs(root: &Path) -> Result<Vec<String>> {
    let dir = root.join("channels");
    let mut out = Vec::new();
    if !dir.exists() {
        return Ok(out);
    }
    for e in fs::read_dir(&dir)? {
        let e = e?;
        if e.path().is_dir() {
            if let Some(n) = e.file_name().to_str() {
                if !n.is_empty() && !n.starts_with('.') {
                    out.push(n.to_string());
                }
            }
        }
    }
    out.sort();
    Ok(out)
}

pub fn messages_path(root: &Path, slug: &str) -> PathBuf {
    root.join("channels").join(slug).join("messages.jsonl")
}

/// Snapshot of (channel, id, raw line) for every jsonl row under root.
pub fn snapshot(root: &Path) -> Result<Vec<(String, String, String)>> {
    let mut rows = Vec::new();
    for slug in list_channel_slugs(root)? {
        let path = messages_path(root, &slug);
        if !path.exists() {
            continue;
        }
        let f = fs::File::open(&path)?;
        for line in BufReader::new(f).lines() {
            let line = line?;
            let t = line.trim();
            if t.is_empty() {
                continue;
            }
            if let Some(id) = extract_id(t) {
                rows.push((slug.clone(), id, t.to_string()));
            }
        }
    }
    Ok(rows)
}

/// Merge two jsonl documents: first-seen id wins, order is `a` then unique from `b`.
/// Idempotent: `merge(merge(a, b), b) == merge(a, b)`. Does not rewrite history.
pub fn merge_jsonl(a: &str, b: &str) -> String {
    let mut seen = HashSet::new();
    let mut out = String::new();
    for src in [a, b] {
        for line in src.lines() {
            let line = line.trim();
            if line.is_empty() {
                continue;
            }
            let key = extract_id(line).unwrap_or_else(|| line.to_string());
            if seen.insert(key) {
                out.push_str(line);
                out.push('\n');
            }
        }
    }
    out
}

pub fn normalize_channel(s: &str) -> String {
    let s = s.trim().trim_start_matches('#');
    let mut out = String::new();
    let mut prev_dash = false;
    for c in s.chars() {
        let lower = c.to_ascii_lowercase();
        match lower {
            'a'..='z' | '0'..='9' => {
                out.push(lower);
                prev_dash = false;
            }
            ' ' | '_' | '-' | '.' => {
                if !prev_dash && !out.is_empty() {
                    out.push('-');
                    prev_dash = true;
                }
            }
            _ => {}
        }
    }
    out.trim_matches('-').to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp() -> PathBuf {
        let p = std::env::temp_dir().join(format!(
            "ws-merge-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let _ = fs::remove_dir_all(&p);
        fs::create_dir_all(&p).unwrap();
        p
    }

    #[test]
    fn extract_id_from_product_line() {
        let line = r#"{"id":"msg_1","channel":"general","body":"hi"}"#;
        assert_eq!(extract_id(line).as_deref(), Some("msg_1"));
    }

    #[test]
    fn ingest_is_idempotent_and_merges() {
        let root = tmp();
        let a = r#"{"id":"msg_a","channel":"general","body":"one"}"#;
        let b = r#"{"id":"msg_b","channel":"general","body":"two"}"#;
        assert!(ingest_line(&root, "general", "msg_a", a).unwrap());
        assert!(!ingest_line(&root, "general", "msg_a", a).unwrap());
        assert!(ingest_line(&root, "general", "msg_b", b).unwrap());
        let snap = snapshot(&root).unwrap();
        assert_eq!(snap.len(), 2);
        let ids: Vec<_> = snap.into_iter().map(|(_, id, _)| id).collect();
        assert!(ids.contains(&"msg_a".into()));
        assert!(ids.contains(&"msg_b".into()));
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn merge_jsonl_two_files_idempotent() {
        let a = "{\"id\":\"msg_a\",\"body\":\"from-a\"}\n{\"id\":\"msg_shared\",\"body\":\"a-wins\"}\n";
        let b = "{\"id\":\"msg_shared\",\"body\":\"b-loses\"}\n{\"id\":\"msg_b\",\"body\":\"from-b\"}\n";
        let once = merge_jsonl(a, b);
        assert_eq!(once, merge_jsonl(&once, b));
        assert_eq!(once, merge_jsonl(&once, a));
        assert!(once.contains("from-a"));
        assert!(once.contains("from-b"));
        assert!(once.contains("a-wins"));
        assert!(!once.contains("b-loses"));
        let ids: Vec<_> = once.lines().filter_map(extract_id).collect();
        assert_eq!(ids, vec!["msg_a", "msg_shared", "msg_b"]);
    }
}
