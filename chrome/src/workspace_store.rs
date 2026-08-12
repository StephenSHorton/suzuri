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
//!     files/<file_id>_<safe_name>
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

/// Product `maxMembers`.
pub const MAX_MEMBERS: usize = 128;

/// Product `maxUploadBytes` (64 MiB).
pub const MAX_UPLOAD_BYTES: u64 = 64 << 20;

/// Max channel slug length after normalize.
const MAX_SLUG_LEN: usize = 48;

/// Product availability codes (presence).
pub const STATUS_IDLE: &str = "idle";
pub const STATUS_WORKING: &str = "working";
pub const STATUS_WAITING: &str = "waiting";
pub const STATUS_BLOCKED: &str = "blocked";
pub const STATUS_AWAY: &str = "away";

/// Cycle order for local human status (Ctrl+Shift+A / presence chip).
pub const AVAILABILITY_CYCLE: &[&str] = &[
    STATUS_IDLE,
    STATUS_WORKING,
    STATUS_WAITING,
    STATUS_BLOCKED,
    STATUS_AWAY,
];

/// Next status in [`AVAILABILITY_CYCLE`] after normalizing `current`.
///
/// Unknown / custom values restart at idle's successor (`working`).
pub fn next_availability(current: &str) -> &'static str {
    let cur = normalize_availability(current);
    let idx = AVAILABILITY_CYCLE
        .iter()
        .position(|&s| s == cur.as_str())
        .unwrap_or(0);
    AVAILABILITY_CYCLE[(idx + 1) % AVAILABILITY_CYCLE.len()]
}

/// File attached to a channel message (product `FileRef`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct WsFileRef {
    pub id: String,
    pub name: String,
    pub bytes: u64,
    pub sha256: String,
    /// Relative to workspace root (`channels/<slug>/files/…`).
    pub rel_path: String,
}

/// Workspace participant (product `Member`).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WsMember {
    pub id: String,
    pub name: String,
    /// `"human"` | `"agent"`.
    pub kind: String,
    pub session_id: String,
    /// `idle` | `working` | `waiting` | `blocked` | `away` | custom.
    pub status: String,
    pub status_note: String,
    /// Unix seconds.
    pub joined_at: u64,
    /// Unix seconds.
    pub last_seen: u64,
}

impl WsMember {
    /// Short presence label for paint (`idle` if empty).
    pub fn presence(&self) -> &str {
        if self.status.is_empty() {
            STATUS_IDLE
        } else {
            self.status.as_str()
        }
    }
}

/// One chat line for the chrome UI (display subset of product Message).
#[derive(Clone, Debug)]
pub struct WsMessage {
    pub id: String,
    pub channel: String,
    /// Display name (`from_name` in product JSONL).
    pub from: String,
    pub from_kind: String,
    /// `text` | `system` | `file`.
    pub kind: String,
    pub body: String,
    /// Unix seconds (parsed from RFC3339 `ts` when present).
    pub ts: u64,
    pub file: Option<WsFileRef>,
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

    /// Open at an explicit root (tests / injectable path).
    pub fn open_at(root: impl Into<PathBuf>) -> Self {
        let s = Self { root: root.into() };
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
            file: None,
        };
        self.append_message(&msg)?;
        Ok(msg)
    }

    // ── members / presence ───────────────────────────────────────────────────

    /// Load `members.json` (empty list if missing/corrupt).
    pub fn list_members(&self) -> Result<Vec<WsMember>, String> {
        self.ensure()?;
        self.read_members()
    }

    /// Register or refresh a member (product `Join`). Matches by session_id or name+kind.
    /// Posts a system line in #general only when a new member is created.
    pub fn join(
        &self,
        name: &str,
        kind: &str,
        session_id: &str,
    ) -> Result<WsMember, String> {
        self.ensure()?;
        let name = name.trim();
        if name.is_empty() {
            return Err("name required".into());
        }
        let kind = if kind == "human" { "human" } else { "agent" };
        let session_id = session_id.trim();
        let mut members = self.read_members()?;
        let now = now_secs();

        // Match existing by session_id (agents) or exact name+kind.
        for m in &mut members {
            if !session_id.is_empty()
                && !m.session_id.is_empty()
                && m.session_id == session_id
            {
                m.name = name.to_string();
                m.kind = kind.into();
                m.last_seen = now;
                if m.status.is_empty() {
                    m.status = STATUS_IDLE.into();
                }
                let out = m.clone();
                self.write_members(&members)?;
                return Ok(out);
            }
            if session_id.is_empty() && m.name == name && m.kind == kind {
                m.last_seen = now;
                if m.status.is_empty() {
                    m.status = STATUS_IDLE.into();
                }
                let out = m.clone();
                self.write_members(&members)?;
                return Ok(out);
            }
        }

        if members.len() >= MAX_MEMBERS {
            return Err(format!("member limit reached ({MAX_MEMBERS})"));
        }
        let m = WsMember {
            id: new_id("m"),
            name: name.to_string(),
            kind: kind.into(),
            session_id: session_id.to_string(),
            status: STATUS_IDLE.into(),
            status_note: String::new(),
            joined_at: now,
            last_seen: now,
        };
        members.push(m.clone());
        self.write_members(&members)?;
        // System line in #general (product Join).
        let sys = WsMessage {
            id: new_id("msg"),
            channel: DEFAULT_CHANNEL.into(),
            from: m.name.clone(),
            from_kind: m.kind.clone(),
            kind: "system".into(),
            body: format!("{} joined the workspace", m.name),
            ts: now,
            file: None,
        };
        let _ = self.ensure_channel(DEFAULT_CHANNEL, "");
        let _ = self.append_message(&sys);
        Ok(m)
    }

    /// Update availability. Identify by member_id or name.
    pub fn set_status(
        &self,
        member_id: &str,
        name: &str,
        status: &str,
        note: Option<&str>,
    ) -> Result<WsMember, String> {
        self.ensure()?;
        let status = normalize_availability(status);
        let mut members = self.read_members()?;
        let now = now_secs();
        let member_id = member_id.trim();
        let name = name.trim();
        for m in &mut members {
            let hit = (!member_id.is_empty() && m.id == member_id)
                || (member_id.is_empty() && !name.is_empty() && m.name == name);
            if hit {
                m.status = status;
                m.last_seen = now;
                if let Some(n) = note {
                    let n = n.trim();
                    let rs: Vec<char> = n.chars().collect();
                    m.status_note = if rs.len() > 120 {
                        rs.into_iter().take(120).collect()
                    } else {
                        n.to_string()
                    };
                }
                let out = m.clone();
                self.write_members(&members)?;
                return Ok(out);
            }
        }
        Err("member not found".into())
    }

    // ── file attach ──────────────────────────────────────────────────────────

    /// Copy `src_path` into the channel `files/` dir and post a file message.
    /// `caption` is optional body text (defaults to the file name).
    pub fn upload(
        &self,
        channel: &str,
        src_path: &str,
        from_name: &str,
        from_kind: &str,
        caption: &str,
    ) -> Result<WsMessage, String> {
        self.ensure()?;
        let mut src = src_path.trim().to_string();
        if src.is_empty() {
            return Err("path required".into());
        }
        // Strip surrounding quotes (compose / paste).
        if (src.starts_with('"') && src.ends_with('"'))
            || (src.starts_with('\'') && src.ends_with('\''))
        {
            src = src[1..src.len() - 1].to_string();
        }
        if let Some(rest) = src.strip_prefix("~/") {
            if let Some(home) = std::env::var_os("HOME") {
                src = PathBuf::from(home).join(rest).to_string_lossy().into();
            }
        }
        let src_path = PathBuf::from(&src);
        let meta = fs::metadata(&src_path).map_err(|e| format!("stat: {e}"))?;
        if meta.is_dir() {
            return Err("path is a directory (files only for now)".into());
        }
        if meta.len() > MAX_UPLOAD_BYTES {
            return Err(format!("file too large (max {MAX_UPLOAD_BYTES} bytes)"));
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
        let from_kind = if from_kind == "agent" { "agent" } else { "human" };

        let file_id = new_id("f");
        let base = src_path
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("file")
            .to_string();
        let safe = sanitize_file_name(&base);
        let rel = format!("channels/{slug}/files/{file_id}_{safe}");
        let dst = self.root.join(PathBuf::from(&rel));
        if let Some(parent) = dst.parent() {
            fs::create_dir_all(parent).map_err(err)?;
        }
        fs::copy(&src_path, &dst).map_err(|e| {
            let _ = fs::remove_file(&dst);
            e.to_string()
        })?;
        let bytes = meta.len();
        let ref_ = WsFileRef {
            id: file_id,
            name: base.clone(),
            bytes,
            sha256: String::new(),
            rel_path: rel,
        };
        let body = {
            let c = caption.trim();
            if c.is_empty() {
                base
            } else {
                c.to_string()
            }
        };
        let msg = WsMessage {
            id: new_id("msg"),
            channel: slug,
            from: name.to_string(),
            from_kind: from_kind.into(),
            kind: "file".into(),
            body,
            ts: now_secs(),
            file: Some(ref_),
        };
        self.append_message(&msg)?;
        Ok(msg)
    }

    fn append_message(&self, msg: &WsMessage) -> Result<(), String> {
        let path = self
            .root
            .join("channels")
            .join(&msg.channel)
            .join("messages.jsonl");
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).map_err(err)?;
        }
        let mut f = OpenOptions::new()
            .create(true)
            .append(true)
            .open(&path)
            .map_err(err)?;
        let line = format_product_line(msg);
        f.write_all(line.as_bytes()).map_err(err)?;
        Ok(())
    }

    fn read_members(&self) -> Result<Vec<WsMember>, String> {
        let path = self.root.join("members.json");
        let Ok(raw) = fs::read_to_string(&path) else {
            return Ok(Vec::new());
        };
        Ok(parse_members_json(&raw))
    }

    fn write_members(&self, members: &[WsMember]) -> Result<(), String> {
        let path = self.root.join("members.json");
        atomic_write(&path, format_members_json(members).as_bytes())
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

/// Product `NormalizeAvailability` (empty → idle).
pub fn normalize_availability(s: &str) -> String {
    match s.trim().to_ascii_lowercase().as_str() {
        "" | "idle" | "online" | "available" | "ready" => STATUS_IDLE.into(),
        "working" | "busy" | "active" | "in_progress" | "in-progress" => STATUS_WORKING.into(),
        "waiting" | "waiting_for" | "waiting-for" | "pending" => STATUS_WAITING.into(),
        "blocked" | "stuck" | "error" => STATUS_BLOCKED.into(),
        "away" | "offline" | "dnd" | "brb" => STATUS_AWAY.into(),
        other => {
            let mut a = other.to_string();
            if a.len() > 24 {
                a = a.chars().take(24).collect();
            }
            a
        }
    }
}

/// Single-line chip for presence strip paint (`● name` / `◐ name · note`).
pub fn member_chip(m: &WsMember) -> String {
    let st = m.presence();
    let glyph = match st {
        STATUS_WORKING => "◐",
        STATUS_WAITING => "…",
        STATUS_BLOCKED => "✕",
        STATUS_AWAY => "○",
        _ => "●", // idle / default
    };
    let mut name = m.name.clone();
    if name.is_empty() {
        name = "?".into();
    }
    let rs: Vec<char> = name.chars().collect();
    if rs.len() > 14 {
        name = rs.into_iter().take(13).collect::<String>() + "…";
    }
    let mut chip = format!("{glyph} {name}");
    if !m.status_note.is_empty()
        && matches!(st, STATUS_WAITING | STATUS_BLOCKED | STATUS_WORKING)
    {
        let mut note = m.status_note.clone();
        let nrs: Vec<char> = note.chars().collect();
        if nrs.len() > 18 {
            note = nrs.into_iter().take(17).collect::<String>() + "…";
        }
        chip.push_str(" · ");
        chip.push_str(&note);
    }
    chip
}

// ── JSONL ────────────────────────────────────────────────────────────────────

fn format_product_line(msg: &WsMessage) -> String {
    // Product Message shape so Go MCP / chrome share the same file.
    let ts = iso_from_secs(msg.ts);
    let from_id = new_id("m");
    let mut line = format!(
        "{{\"id\":{},\"channel\":{},\"ts\":{},\"from_id\":{},\"from_name\":{},\"from_kind\":{},\"kind\":{},\"body\":{}",
        json_str(&msg.id),
        json_str(&msg.channel),
        json_str(&ts),
        json_str(&from_id),
        json_str(&msg.from),
        json_str(&msg.from_kind),
        json_str(&msg.kind),
        json_str(&msg.body),
    );
    if let Some(ref f) = msg.file {
        line.push_str(&format!(
            ",\"file\":{{\"id\":{},\"name\":{},\"bytes\":{},\"sha256\":{},\"rel_path\":{}}}",
            json_str(&f.id),
            json_str(&f.name),
            f.bytes,
            json_str(&f.sha256),
            json_str(&f.rel_path),
        ));
    }
    line.push_str("}\n");
    line
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
    let file = parse_file_ref(line);
    Some(WsMessage {
        id,
        channel,
        from,
        from_kind,
        kind,
        body,
        ts,
        file,
    })
}

/// Best-effort nested `file` object parse (product FileRef).
fn parse_file_ref(line: &str) -> Option<WsFileRef> {
    // Prefer `"file":` so we don't match kind:"file" string values.
    let key = "\"file\":";
    let i = line.find(key)?;
    let rest = line[i + key.len()..].trim_start();
    if !rest.starts_with('{') {
        return None;
    }
    // Slice through matching brace.
    let mut depth = 0i32;
    let mut end = None;
    for (j, c) in rest.char_indices() {
        match c {
            '{' => depth += 1,
            '}' => {
                depth -= 1;
                if depth == 0 {
                    end = Some(j);
                    break;
                }
            }
            _ => {}
        }
    }
    let obj = &rest[..=end?];
    let id = extract_str(obj, "id").unwrap_or_default();
    let name = extract_str(obj, "name")?;
    let rel_path = extract_str(obj, "rel_path").unwrap_or_default();
    let sha256 = extract_str(obj, "sha256").unwrap_or_default();
    let bytes = extract_u64(obj, "bytes").unwrap_or(0);
    Some(WsFileRef {
        id,
        name,
        bytes,
        sha256,
        rel_path,
    })
}

fn extract_u64(s: &str, key: &str) -> Option<u64> {
    let pat = format!("\"{key}\"");
    let i = s.find(&pat)?;
    let rest = &s[i + pat.len()..];
    let colon = rest.find(':')?;
    let rest = rest[colon + 1..].trim_start();
    let num: String = rest
        .chars()
        .take_while(|c| c.is_ascii_digit())
        .collect();
    num.parse().ok()
}

// ── members.json ─────────────────────────────────────────────────────────────

fn parse_members_json(text: &str) -> Vec<WsMember> {
    let mut out = Vec::new();
    let mut depth = 0i32;
    let mut start: Option<usize> = None;
    for (i, c) in text.char_indices() {
        match c {
            '{' => {
                if depth == 0 {
                    start = Some(i);
                }
                depth += 1;
            }
            '}' => {
                depth -= 1;
                if depth == 0 {
                    if let Some(s) = start {
                        let obj = &text[s..=i];
                        if let Some(m) = parse_member_obj(obj) {
                            out.push(m);
                        }
                    }
                    start = None;
                }
            }
            _ => {}
        }
    }
    out
}

fn parse_member_obj(obj: &str) -> Option<WsMember> {
    let name = extract_str(obj, "name")?;
    let id = extract_str(obj, "id").unwrap_or_default();
    let kind = extract_str(obj, "kind").unwrap_or_else(|| "agent".into());
    let session_id = extract_str(obj, "session_id").unwrap_or_default();
    let status = extract_str(obj, "status").unwrap_or_default();
    let status_note = extract_str(obj, "status_note").unwrap_or_default();
    let joined_at = extract_str(obj, "joined_at")
        .and_then(|s| parse_rfc3339_secs(&s))
        .unwrap_or(0);
    let last_seen = extract_str(obj, "last_seen")
        .and_then(|s| parse_rfc3339_secs(&s))
        .unwrap_or(0);
    Some(WsMember {
        id,
        name,
        kind,
        session_id,
        status,
        status_note,
        joined_at,
        last_seen,
    })
}

fn format_members_json(members: &[WsMember]) -> String {
    if members.is_empty() {
        return "[]\n".into();
    }
    let mut out = String::from("[\n");
    for (i, m) in members.iter().enumerate() {
        out.push_str("  {\n");
        out.push_str(&format!("    \"id\": {},\n", json_str(&m.id)));
        out.push_str(&format!("    \"name\": {},\n", json_str(&m.name)));
        out.push_str(&format!("    \"kind\": {},\n", json_str(&m.kind)));
        if !m.session_id.is_empty() {
            out.push_str(&format!(
                "    \"session_id\": {},\n",
                json_str(&m.session_id)
            ));
        }
        let st = if m.status.is_empty() {
            STATUS_IDLE
        } else {
            m.status.as_str()
        };
        out.push_str(&format!("    \"status\": {},\n", json_str(st)));
        if !m.status_note.is_empty() {
            out.push_str(&format!(
                "    \"status_note\": {},\n",
                json_str(&m.status_note)
            ));
        }
        out.push_str(&format!(
            "    \"joined_at\": {},\n",
            json_str(&iso_from_secs(m.joined_at))
        ));
        out.push_str(&format!(
            "    \"last_seen\": {}\n",
            json_str(&iso_from_secs(m.last_seen))
        ));
        out.push_str("  }");
        if i + 1 < members.len() {
            out.push(',');
        }
        out.push('\n');
    }
    out.push_str("]\n");
    out
}

fn sanitize_file_name(name: &str) -> String {
    let name = Path::new(name)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or(name);
    let mut b = String::new();
    for r in name.chars() {
        match r {
            'a'..='z' | 'A'..='Z' | '0'..='9' | '.' | '-' | '_' => b.push(r),
            ' ' => b.push('_'),
            _ => b.push('_'),
        }
    }
    let mut out = b;
    if out.is_empty() || out == "." || out == ".." {
        out = "file".into();
    }
    if out.len() > 80 {
        out = out.chars().take(80).collect();
    }
    out
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
    fn normalize_avail() {
        assert_eq!(normalize_availability(""), STATUS_IDLE);
        assert_eq!(normalize_availability("busy"), STATUS_WORKING);
        assert_eq!(normalize_availability("away"), STATUS_AWAY);
    }

    #[test]
    fn next_availability_cycles() {
        assert_eq!(next_availability(STATUS_IDLE), STATUS_WORKING);
        assert_eq!(next_availability(STATUS_WORKING), STATUS_WAITING);
        assert_eq!(next_availability(STATUS_WAITING), STATUS_BLOCKED);
        assert_eq!(next_availability(STATUS_BLOCKED), STATUS_AWAY);
        assert_eq!(next_availability(STATUS_AWAY), STATUS_IDLE);
        // Aliases normalize first.
        assert_eq!(next_availability("busy"), STATUS_WAITING);
        assert_eq!(next_availability("pending"), STATUS_BLOCKED);
        // Full cycle length.
        let mut s = STATUS_IDLE;
        for _ in 0..AVAILABILITY_CYCLE.len() {
            s = next_availability(s);
        }
        assert_eq!(s, STATUS_IDLE);
    }

    #[test]
    fn parse_product_line() {
        let line = r#"{"id":"msg_1","channel":"general","ts":"2026-08-07T17:58:03.472543Z","from_id":"m_x","from_name":"alice","from_kind":"human","kind":"text","body":"hello"}"#;
        let m = parse_msg_line(line).unwrap();
        assert_eq!(m.from, "alice");
        assert_eq!(m.body, "hello");
        assert_eq!(m.channel, "general");
        assert!(m.ts > 0);
        assert!(m.file.is_none());
    }

    #[test]
    fn parse_minimal_line() {
        let line = r#"{"from":"bob","body":"hi","ts":1700000000}"#;
        let m = parse_msg_line(line).unwrap();
        assert_eq!(m.from, "bob");
        assert_eq!(m.body, "hi");
        assert_eq!(m.ts, 1700000000);
    }

    #[test]
    fn parse_file_message_line() {
        let line = r#"{"id":"msg_f","channel":"general","ts":"2026-08-07T17:58:03Z","from_id":"m_x","from_name":"alice","from_kind":"human","kind":"file","body":"notes.txt","file":{"id":"f_1","name":"notes.txt","bytes":12,"sha256":"","rel_path":"channels/general/files/f_1_notes.txt"}}"#;
        let m = parse_msg_line(line).unwrap();
        assert_eq!(m.kind, "file");
        let f = m.file.unwrap();
        assert_eq!(f.name, "notes.txt");
        assert_eq!(f.bytes, 12);
        assert!(f.rel_path.contains("files/"));
    }

    #[test]
    fn members_roundtrip_and_join() {
        let dir = std::env::temp_dir().join(format!(
            "suzuri-ws-members-{}-{}",
            std::process::id(),
            now_secs()
        ));
        let _ = fs::remove_dir_all(&dir);
        let store = WorkspaceStore::open_at(&dir);
        let m = store.join("alice", "human", "").unwrap();
        assert_eq!(m.name, "alice");
        assert_eq!(m.presence(), STATUS_IDLE);
        // Second join updates last_seen, no duplicate.
        let m2 = store.join("alice", "human", "").unwrap();
        assert_eq!(m2.id, m.id);
        let list = store.list_members().unwrap();
        assert_eq!(list.len(), 1);
        store
            .set_status(&m.id, "", STATUS_WORKING, Some("shipping"))
            .unwrap();
        let list = store.list_members().unwrap();
        assert_eq!(list[0].status, STATUS_WORKING);
        assert_eq!(list[0].status_note, "shipping");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn upload_copies_and_posts() {
        let dir = std::env::temp_dir().join(format!(
            "suzuri-ws-upload-{}-{}",
            std::process::id(),
            now_secs()
        ));
        let _ = fs::remove_dir_all(&dir);
        let store = WorkspaceStore::open_at(&dir);
        let src = dir.join("src-hello.txt");
        fs::write(&src, b"hello attach").unwrap();
        let msg = store
            .upload("general", src.to_str().unwrap(), "bob", "human", "")
            .unwrap();
        assert_eq!(msg.kind, "file");
        assert_eq!(msg.body, "src-hello.txt");
        let f = msg.file.as_ref().unwrap();
        assert_eq!(f.name, "src-hello.txt");
        assert_eq!(f.bytes, 12);
        let abs = store.root().join(&f.rel_path);
        assert_eq!(fs::read_to_string(&abs).unwrap(), "hello attach");
        let hist = store.history("general", 10).unwrap();
        assert!(hist.iter().any(|m| m.kind == "file"));
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn parse_members_pretty() {
        let raw = r#"[
  {
    "id": "m_1",
    "name": "stephenhorton",
    "kind": "human",
    "status": "idle",
    "joined_at": "2026-08-07T17:58:24Z",
    "last_seen": "2026-08-10T18:28:50Z"
  }
]
"#;
        let list = parse_members_json(raw);
        assert_eq!(list.len(), 1);
        assert_eq!(list[0].name, "stephenhorton");
        assert_eq!(list[0].status, "idle");
    }
}
