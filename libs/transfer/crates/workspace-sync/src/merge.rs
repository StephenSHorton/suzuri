//! Append-only jsonl merge: skip lines whose message `id` is already present.

use anyhow::{Context, Result};
use serde_json::{json, Value};
use std::collections::HashSet;
use std::fs::{self, OpenOptions};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

/// Product `maxMembers`.
const MAX_MEMBERS: usize = 128;

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

/// Register the message author in `members.json` so a remote human/agent
/// shows up in the presence strip. Dedup by member `id`. Existing members
/// only bump `last_seen`. `session_id` is `p2p:<id>` so chrome does not
/// fuse two people who share a display name.
pub fn upsert_author(root: &Path, line: &str) -> Result<bool> {
    let v: Value = match serde_json::from_str(line.trim()) {
        Ok(v) => v,
        Err(_) => return Ok(false),
    };
    let id = v
        .get("from_id")
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .trim();
    let name = v
        .get("from_name")
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .trim();
    if id.is_empty() || name.is_empty() {
        return Ok(false);
    }
    let kind = match v
        .get("from_kind")
        .and_then(|x| x.as_str())
        .unwrap_or("human")
    {
        "human" => "human",
        _ => "agent",
    };
    upsert_member(root, id, name, kind)
}

fn upsert_member(root: &Path, id: &str, name: &str, kind: &str) -> Result<bool> {
    let path = root.join("members.json");
    let mut list: Vec<Value> = if path.exists() {
        let raw = fs::read_to_string(&path).unwrap_or_else(|_| "[]".into());
        serde_json::from_str(&raw).unwrap_or_else(|_| Vec::new())
    } else {
        Vec::new()
    };
    let now = iso_now();
    if let Some(obj) = list
        .iter_mut()
        .find(|m| m.get("id").and_then(|x| x.as_str()) == Some(id))
    {
        if let Some(map) = obj.as_object_mut() {
            map.insert("last_seen".into(), Value::String(now));
        }
        atomic_write_json(&path, &list)?;
        return Ok(false);
    }
    if list.len() >= MAX_MEMBERS {
        return Ok(false);
    }
    list.push(json!({
        "id": id,
        "name": name,
        "kind": kind,
        "session_id": format!("p2p:{id}"),
        "status": "idle",
        "joined_at": now,
        "last_seen": now,
    }));
    atomic_write_json(&path, &list)?;
    Ok(true)
}

fn atomic_write_json(path: &Path, list: &[Value]) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let body = serde_json::to_vec_pretty(list)?;
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, body)?;
    if let Err(e) = fs::rename(&tmp, path) {
        let _ = fs::remove_file(path);
        fs::rename(&tmp, path).map_err(|_| e)?;
    }
    Ok(())
}

fn iso_now() -> String {
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    iso_from_secs(secs)
}

fn iso_from_secs(secs: u64) -> String {
    let days = (secs / 86400) as i64;
    let rem = (secs % 86400) as i64;
    let h = rem / 3600;
    let mi = (rem % 3600) / 60;
    let se = rem % 60;
    let (y, mo, d) = civil_from_days(days);
    format!("{y:04}-{mo:02}-{d:02}T{h:02}:{mi:02}:{se:02}Z")
}

fn civil_from_days(days: i64) -> (i64, i64, i64) {
    let z = days + 719468;
    let era = if z >= 0 { z } else { z - 146096 } / 146097;
    let doe = (z - era * 146097) as u64;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y = yoe as i64 + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = if m <= 2 { y + 1 } else { y };
    (y, m as i64, d as i64)
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
        let a =
            "{\"id\":\"msg_a\",\"body\":\"from-a\"}\n{\"id\":\"msg_shared\",\"body\":\"a-wins\"}\n";
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

    #[test]
    fn upsert_author_adds_then_bumps_last_seen() {
        let root = tmp();
        let line = r#"{"id":"msg_1","from_id":"m_alice","from_name":"alice","from_kind":"human","body":"hi"}"#;
        assert!(upsert_author(&root, line).unwrap());
        assert!(!upsert_author(&root, line).unwrap());
        let raw = fs::read_to_string(root.join("members.json")).unwrap();
        let list: Vec<Value> = serde_json::from_str(&raw).unwrap();
        assert_eq!(list.len(), 1);
        assert_eq!(list[0]["id"], "m_alice");
        assert_eq!(list[0]["name"], "alice");
        assert_eq!(list[0]["kind"], "human");
        assert_eq!(list[0]["session_id"], "p2p:m_alice");
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn upsert_author_skips_system_or_nameless() {
        let root = tmp();
        assert!(!upsert_author(&root, r#"{"id":"msg_s","kind":"system","body":"x"}"#).unwrap());
        assert!(!upsert_author(&root, "not-json").unwrap());
        assert!(!root.join("members.json").exists());
        let _ = fs::remove_dir_all(&root);
    }
}
