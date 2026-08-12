//! File-backed workspace store — product-compatible with
//! `internal/workspace` (Go).
//!
//! Layout under config dir:
//! ```text
//! workspace/
//!   workspace.json
//!   members.json
//!   channels/<slug>/
//!     meta.json
//!     messages.jsonl
//! ```
//!
//! On macOS the root is
//! `~/Library/Application Support/suzuri/workspace`.

use std::fs::{self, OpenOptions};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

/// Default channel slug (matches product `workspace.DefaultChannel`).
pub const DEFAULT_CHANNEL: &str = "general";

/// Cap on history lines loaded into the UI (product default/cap ~50–200).
pub const HISTORY_LIMIT: usize = 120;

/// Max body runes when posting (product `maxBodyRunes` is larger; chrome keeps compose snappy).
pub const MAX_BODY_RUNES: usize = 2000;

/// Max channel slug length after normalize.
const MAX_SLUG_LEN: usize = 48;

/// One chat line for the chrome UI (display subset of product Message).
#[derive(Clone, Debug)]
pub struct WsMessage {
    pub id: String,
    pub channel: String,
    /// Display name (`from_name` in product JSONL).
    pub from: String,
    pub from_kind: String,
    pub kind: String,
    pub body: String,
    /// Unix seconds (parsed from RFC3339 `ts` when present).
    pub ts: u64,
}

/// Handle rooted at the suzuri workspace directory.
#[derive(Clone, Debug)]
pub struct WorkspaceStore {
    root: PathBuf,
}

impl WorkspaceStore {
    pub fn open_default() -> Self {
        let root = workspace_root();
        let s = Self { root };
        let _ = s.ensure();
        s
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    /// Create root, workspace.json, members.json, and #general if missing.
    pub fn ensure(&self) -> Result<(), String> {
        fs::create_dir_all(self.root.join("channels")).map_err(err)?;
        let meta = self.root.join("workspace.json");
        if !meta.exists() {
            let id = new_id("ws");
            let now = iso_now();
            let body = format!(
                "{{\n  \"id\": {},\n  \"title\": \"Suzuri workspace\",\n  \"created_at\": {}\n}}\n",
                json_str(&id),
                json_str(&now),
            );
            atomic_write(&meta, body.as_bytes())?;
        }
        let members = self.root.join("members.json");
        if !members.exists() {
            atomic_write(&members, b"[]\n")?;
        }
        self.ensure_channel(DEFAULT_CHANNEL, "")?;
        Ok(())
    }

    pub fn list_channels(&self) -> Result<Vec<String>, String> {
        self.ensure()?;
        let ch_root = self.root.join("channels");
        let mut list = Vec::new();
        if let Ok(rd) = fs::read_dir(&ch_root) {
            for e in rd.flatten() {
                if e.path().is_dir() {
                    if let Some(n) = e.file_name().to_str() {
                        if !n.is_empty() && !n.starts_with('.') {
                            list.push(n.to_string());
                        }
                    }
                }
            }
        }
        if list.is_empty() {
            list.push(DEFAULT_CHANNEL.into());
            let _ = self.ensure_channel(DEFAULT_CHANNEL, "");
        }
        // Prefer #general first (product listChannelsLocked).
        list.sort();
        if let Some(i) = list.iter().position(|c| c == DEFAULT_CHANNEL) {
            let g = list.remove(i);
            list.insert(0, g);
        }
        Ok(list)
    }

    /// Create channel directory + meta + empty messages.jsonl. Idempotent.
    pub fn create_channel(&self, name: &str, topic: &str) -> Result<String, String> {
        self.ensure()?;
        let slug = normalize_channel(name);
        if slug.is_empty() {
            return Err("invalid channel name".into());
        }
        let existing = self.list_channels()?;
        if existing.len() >= 64 && !existing.iter().any(|c| c == &slug) {
            return Err("channel limit reached (64)".into());
        }
        self.ensure_channel(&slug, topic)?;
        Ok(slug)
    }

    pub fn ensure_channel(&self, slug: &str, topic: &str) -> Result<(), String> {
        let slug = normalize_channel(slug);
        if slug.is_empty() {
            return Err("invalid channel name".into());
        }
        let dir = self.root.join("channels").join(&slug);
        fs::create_dir_all(&dir).map_err(err)?;
        let meta_path = dir.join("meta.json");
        if !meta_path.exists() {
            let now = iso_now();
            let body = format!(
                "{{\n  \"id\": {},\n  \"name\": {},\n  \"created_at\": {},\n  \"topic\": {}\n}}\n",
                json_str(&slug),
                json_str(&slug),
                json_str(&now),
                json_str(topic),
            );
            atomic_write(&meta_path, body.as_bytes())?;
        }
        let msg_path = dir.join("messages.jsonl");
        if !msg_path.exists() {
            OpenOptions::new()
                .create(true)
                .write(true)
                .open(&msg_path)
                .map_err(err)?;
        }
        Ok(())
    }

    /// Last `limit` messages, oldest first.
    pub fn history(&self, channel: &str, limit: usize) -> Result<Vec<WsMessage>, String> {
        self.ensure()?;
        let mut slug = normalize_channel(channel);
        if slug.is_empty() {
            slug = DEFAULT_CHANNEL.into();
        }
        self.ensure_channel(&slug, "")?;
        let limit = if limit == 0 {
            HISTORY_LIMIT
        } else {
            limit.min(200)
        };
        let path = self
            .root
            .join("channels")
            .join(&slug)
            .join("messages.jsonl");
        let Ok(f) = fs::File::open(&path) else {
            return Ok(Vec::new());
        };
        let mut all = Vec::new();
        for line in BufReader::new(f).lines().flatten() {
            let line = line.trim();
            if line.is_empty() {
                continue;
            }
            if let Some(m) = parse_msg_line(line) {
                all.push(m);
            }
        }
        if all.len() > limit {
            let n = all.len() - limit;
            all.drain(0..n);
        }
        Ok(all)
    }

    /// Append a product-shaped text message; returns the stored record.
    pub fn post(
        &self,
        channel: &str,
        body: &str,
        from_name: &str,
        from_kind: &str,
    ) -> Result<WsMessage, String> {
        self.ensure()?;
        let body = body.trim();
        if body.is_empty() {
            return Err("body required".into());
        }
        if body.chars().count() > MAX_BODY_RUNES {
            return Err(format!("message too long (max {MAX_BODY_RUNES})"));
        }
        let mut slug = normalize_channel(channel);
        if slug.is_empty() {
            slug = DEFAULT_CHANNEL.into();
        }
        self.ensure_channel(&slug, "")?;
        let name = from_name.trim();
        if name.is_empty() {
            return Err("name required".into());
        }
        let kind = if from_kind == "agent" { "agent" } else { "human" };
        let msg = WsMessage {
            id: new_id("msg"),
            channel: slug.clone(),
            from: name.to_string(),
            from_kind: kind.into(),
            kind: "text".into(),
            body: body.to_string(),
            ts: now_secs(),
        };
        let path = self
            .root
            .join("channels")
            .join(&slug)
            .join("messages.jsonl");
        let mut f = OpenOptions::new()
            .create(true)
            .append(true)
            .open(&path)
            .map_err(err)?;
        let line = format_product_line(&msg);
        f.write_all(line.as_bytes()).map_err(err)?;
        Ok(msg)
    }
}

// ── path / identity ──────────────────────────────────────────────────────────

pub fn workspace_root() -> PathBuf {
    #[cfg(target_os = "macos")]
    {
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join("Library/Application Support/suzuri/workspace");
        }
    }
    if let Ok(xdg) = std::env::var("XDG_CONFIG_HOME") {
        return PathBuf::from(xdg).join("suzuri/workspace");
    }
    if let Some(home) = std::env::var_os("HOME") {
        return PathBuf::from(home).join(".config/suzuri/workspace");
    }
    PathBuf::from("workspace")
}

/// Human display name: `$USER` / `$USERNAME`, else `"human"`.
pub fn local_human_name() -> String {
    std::env::var("USER")
        .or_else(|_| std::env::var("USERNAME"))
        .unwrap_or_else(|_| "human".into())
}

/// Product `NormalizeChannel`: `"#Fix Auth"` → `"fix-auth"`.
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
    let out = out.trim_matches('-').to_string();
    if out.len() > MAX_SLUG_LEN {
        let mut t = out.chars().take(MAX_SLUG_LEN).collect::<String>();
        while t.ends_with('-') {
            t.pop();
        }
        t
    } else {
        out
    }
}

// ── JSONL ────────────────────────────────────────────────────────────────────

fn format_product_line(msg: &WsMessage) -> String {
    // Product Message shape so Go MCP / chrome share the same file.
    let ts = iso_from_secs(msg.ts);
    let from_id = new_id("m");
    format!(
        "{{\"id\":{},\"channel\":{},\"ts\":{},\"from_id\":{},\"from_name\":{},\"from_kind\":{},\"kind\":{},\"body\":{}}}\n",
        json_str(&msg.id),
        json_str(&msg.channel),
        json_str(&ts),
        json_str(&from_id),
        json_str(&msg.from),
        json_str(&msg.from_kind),
        json_str(&msg.kind),
        json_str(&msg.body),
    )
}

fn parse_msg_line(line: &str) -> Option<WsMessage> {
    // Prefer product keys; fall back to chrome-minimal {from,body,ts}.
    let from = extract_str(line, "from_name")
        .or_else(|| extract_str(line, "from"))
        .unwrap_or_else(|| "unknown".into());
    let body = extract_str(line, "body")?;
    let id = extract_str(line, "id").unwrap_or_default();
    let channel = extract_str(line, "channel").unwrap_or_else(|| DEFAULT_CHANNEL.into());
    let from_kind = extract_str(line, "from_kind").unwrap_or_else(|| "human".into());
    let kind = extract_str(line, "kind").unwrap_or_else(|| "text".into());
    let ts = parse_ts(line);
    Some(WsMessage {
        id,
        channel,
        from,
        from_kind,
        kind,
        body,
        ts,
    })
}

fn parse_ts(line: &str) -> u64 {
    // Numeric "ts": 1712… (minimal chrome format)
    if let Some(rest) = line.split("\"ts\"").nth(1) {
        let rest = rest.trim_start_matches(|c: char| c == ':' || c.is_whitespace());
        if rest.starts_with('"') {
            // RFC3339 string
            if let Some(s) = extract_str(line, "ts") {
                return parse_rfc3339_secs(&s).unwrap_or(0);
            }
        } else if let Some(num) = rest
            .split(|c: char| !c.is_ascii_digit())
            .next()
            .and_then(|n| n.parse().ok())
        {
            return num;
        }
    }
    0
}

/// Minimal RFC3339 → unix secs (date+time only; ignores sub-seconds/offset fine enough for UI).
fn parse_rfc3339_secs(s: &str) -> Option<u64> {
    // Accept "2026-08-07T17:58:03.472543Z" style.
    let s = s.trim().trim_end_matches('Z');
    let (date, time) = s.split_once('T')?;
    let mut d = date.split('-');
    let y: i64 = d.next()?.parse().ok()?;
    let mo: i64 = d.next()?.parse().ok()?;
    let day: i64 = d.next()?.parse().ok()?;
    let time = time.split(['.', '+', '-']).next()?;
    let mut t = time.split(':');
    let h: i64 = t.next()?.parse().ok()?;
    let mi: i64 = t.next()?.parse().ok()?;
    let se: i64 = t.next()?.parse().ok()?;
    // Days from civil date (Howard Hinnant algorithm) → unix.
    let y = if mo <= 2 { y - 1 } else { y };
    let era = if y >= 0 { y } else { y - 399 } / 400;
    let yoe = (y - era * 400) as u64;
    let mp = if mo > 2 { mo - 3 } else { mo + 9 } as u64;
    let doy = (153 * mp + 2) / 5 + day as u64 - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146097 + doe as i64 - 719468;
    let secs = days * 86400 + h * 3600 + mi * 60 + se;
    if secs < 0 {
        None
    } else {
        Some(secs as u64)
    }
}

fn extract_str(s: &str, key: &str) -> Option<String> {
    let pat = format!("\"{key}\"");
    let i = s.find(&pat)?;
    let rest = &s[i + pat.len()..];
    let colon = rest.find(':')?;
    let rest = rest[colon + 1..].trim_start();
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
                    // skip \uXXXX roughly
                    let hex: String = chars.by_ref().take(4).collect();
                    if let Ok(cp) = u32::from_str_radix(&hex, 16) {
                        if let Some(ch) = char::from_u32(cp) {
                            out.push(ch);
                        }
                    }
                }
                o => out.push(o),
            },
            c => out.push(c),
        }
    }
    None
}

fn json_str(s: &str) -> String {
    let mut o = String::from("\"");
    for c in s.chars() {
        match c {
            '"' => o.push_str("\\\""),
            '\\' => o.push_str("\\\\"),
            '\n' => o.push_str("\\n"),
            '\r' => o.push_str("\\r"),
            '\t' => o.push_str("\\t"),
            c if (c as u32) < 0x20 => {
                o.push_str(&format!("\\u{:04x}", c as u32));
            }
            c => o.push(c),
        }
    }
    o.push('"');
    o
}

fn new_id(prefix: &str) -> String {
    let n = now_secs();
    let r = (n.wrapping_mul(0x9E37_79B9_7F4A_7C15)) ^ (std::process::id() as u64);
    format!("{prefix}_{r:016x}")
}

fn now_secs() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

fn iso_now() -> String {
    iso_from_secs(now_secs())
}

fn iso_from_secs(secs: u64) -> String {
    // UTC Y-M-DTHH:MM:SSZ via civil from days (enough for product interop).
    let days = (secs / 86400) as i64;
    let rem = (secs % 86400) as i64;
    let h = rem / 3600;
    let mi = (rem % 3600) / 60;
    let se = rem % 60;
    let (y, mo, d) = civil_from_days(days);
    format!("{y:04}-{mo:02}-{d:02}T{h:02}:{mi:02}:{se:02}Z")
}

fn civil_from_days(days: i64) -> (i64, i64, i64) {
    // Inverse of Howard Hinnant days_from_civil; z = days since 1970-01-01.
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

fn atomic_write(path: &Path, bytes: &[u8]) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(err)?;
    }
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, bytes).map_err(err)?;
    if let Err(e) = fs::rename(&tmp, path) {
        let _ = fs::remove_file(path);
        fs::rename(&tmp, path).map_err(|_| e).map_err(err)?;
    }
    Ok(())
}

fn err(e: impl ToString) -> String {
    e.to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_channel_slug() {
        assert_eq!(normalize_channel("#Fix Auth"), "fix-auth");
        assert_eq!(normalize_channel("  General  "), "general");
        assert_eq!(normalize_channel(""), "");
    }

    #[test]
    fn parse_product_line() {
        let line = r#"{"id":"msg_1","channel":"general","ts":"2026-08-07T17:58:03.472543Z","from_id":"m_x","from_name":"alice","from_kind":"human","kind":"text","body":"hello"}"#;
        let m = parse_msg_line(line).unwrap();
        assert_eq!(m.from, "alice");
        assert_eq!(m.body, "hello");
        assert_eq!(m.channel, "general");
        assert!(m.ts > 0);
    }

    #[test]
    fn parse_minimal_line() {
        let line = r#"{"from":"bob","body":"hi","ts":1700000000}"#;
        let m = parse_msg_line(line).unwrap();
        assert_eq!(m.from, "bob");
        assert_eq!(m.body, "hi");
        assert_eq!(m.ts, 1700000000);
    }
}
