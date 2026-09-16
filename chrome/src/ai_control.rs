//! Loopback HTTP control plane for agents (grok-fork curls this, not MCP).
//!
//! Binds `127.0.0.1:0` when chrome starts. Discovery:
//! `{config_dir}/ai.json` and `{config_dir}/ai/{pid}.json`, plus PTY env
//! `SUZURI_CONTROL_URL` / `SUZURI_CONTROL_TOKEN`.
//!
//! GET `/help` and `/tools` are answered on the HTTP thread. Layout and
//! mutations go to the UI thread via [`AiJob`].

use std::collections::HashMap;
use std::fs;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, Receiver, Sender, TryRecvError};
use std::sync::{Arc, OnceLock};
use std::thread;
use std::time::Duration;

use serde_json::{json, Value};

use crate::chrome_status::{history_tail_of, live_lines_of};
use crate::config_store;
use crate::panes::{DockEdge, SplitAxis, SplitNode};
use crate::session::{ChromeSession, PaneKind, WidgetKind};

const HELP: &str = r#"# Suzuri AI control

Loopback HTTP for this suzuri window. Not MCP. Prefer curl.

Auth (required except GET /help and GET /tools):
  Authorization: Bearer $SUZURI_CONTROL_TOKEN
  or header X-Suzuri-Token: $SUZURI_CONTROL_TOKEN
  or query ?token=

Discovery:
  $SUZURI_CONTROL_URL  (injected into every pane PTY)
  {config_dir}/ai.json
  {config_dir}/ai/{pid}.json   (one file per window process)

Endpoints:
  GET  /help          this text
  GET  /tools         JSON tools + argument schemas
  GET  /v1/layout     windows, tabs, pane trees, titles, cwd, tail text
  GET  /v1/panes/:id  one pane (more scrollback)
  POST /v1/call       {"tool":"<name>","args":{...}}

Tools (POST /v1/call):
  layout                         snapshot
  pane        {pane_id, lines?}  content
  split       {pane_id?, axis: "right"|"down"}
  focus       {pane_id}
  move        {pane_id, target_pane_id, edge: "left"|"right"|"top"|"bottom"}
  move_to_tab {pane_id, tab_id?}  omit tab_id → extract to a new tab
  rotate / swap / grow / shrink / equalize  {pane_id?}
  close       {pane_id?}
  rename      {pane_id, title}
  new_tab

Describe placement in terms of pane ids from /v1/layout, then `move` / `split`.
"#;

static ADVERTISED: OnceLock<Advertised> = OnceLock::new();

#[derive(Clone, Debug)]
struct Advertised {
    url: String,
    token: String,
}

/// Env keys injected into pane PTYs.
pub const ENV_URL: &str = "SUZURI_CONTROL_URL";
pub const ENV_TOKEN: &str = "SUZURI_CONTROL_TOKEN";

/// Job the UI thread must answer.
pub struct AiJob {
    pub op: AiOp,
    pub reply: Sender<AiReply>,
}

pub enum AiOp {
    Layout,
    Pane { id: u64, lines: usize },
    Call { tool: String, args: Value },
}

pub struct AiReply {
    pub status: u16,
    pub body: Vec<u8>,
    pub content_type: &'static str,
}

impl AiReply {
    fn json_ok(v: Value) -> Self {
        Self {
            status: 200,
            body: v.to_string().into_bytes(),
            content_type: "application/json; charset=utf-8",
        }
    }

    fn json_status(status: u16, v: Value) -> Self {
        Self {
            status,
            body: v.to_string().into_bytes(),
            content_type: "application/json; charset=utf-8",
        }
    }

    fn text(status: u16, s: &str) -> Self {
        Self {
            status,
            body: s.as_bytes().to_vec(),
            content_type: "text/plain; charset=utf-8",
        }
    }
}

/// Running server + UI-thread receiver.
pub struct AiHandle {
    pub url: String,
    pub token: String,
    rx: Receiver<AiJob>,
    stop: Arc<AtomicBool>,
    pid: u32,
}

impl AiHandle {
    /// Non-blocking take of queued jobs.
    pub fn try_recv(&self) -> Result<AiJob, TryRecvError> {
        self.rx.try_recv()
    }
}

impl Drop for AiHandle {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::SeqCst);
        let _ = TcpStream::connect_timeout(
            &self
                .url
                .trim_start_matches("http://")
                .parse()
                .unwrap_or_else(|_| "127.0.0.1:9".parse().unwrap()),
            Duration::from_millis(50),
        );
        remove_discovery(self.pid);
    }
}

/// Bind loopback, spawn the accept thread, write discovery files.
pub fn start() -> Option<AiHandle> {
    let listener = TcpListener::bind("127.0.0.1:0").ok()?;
    listener.set_nonblocking(true).ok()?;
    let addr = listener.local_addr().ok()?;
    let url = format!("http://{addr}");
    let token = make_token(&url);
    let pid = std::process::id();
    let advertised = Advertised {
        url: url.clone(),
        token: token.clone(),
    };
    let _ = ADVERTISED.set(advertised.clone());
    write_discovery(&advertised, pid);

    let (tx, rx) = mpsc::channel::<AiJob>();
    let stop = Arc::new(AtomicBool::new(false));
    let stop_t = stop.clone();
    let token_t = token.clone();
    thread::Builder::new()
        .name("suzuri-ai-http".into())
        .spawn(move || accept_loop(listener, tx, token_t, stop_t))
        .ok()?;

    Some(AiHandle {
        url,
        token,
        rx,
        stop,
        pid,
    })
}

/// Extra env for pane PTY children (no-op if the server did not start).
pub fn pty_env() -> Vec<(String, String)> {
    match ADVERTISED.get() {
        Some(a) => vec![
            (ENV_URL.to_string(), a.url.clone()),
            (ENV_TOKEN.to_string(), a.token.clone()),
        ],
        None => Vec::new(),
    }
}

pub fn help_text() -> &'static str {
    HELP
}

pub fn tools_json() -> Value {
    json!({
        "tools": [
            {"name": "layout", "args": {}, "doc": "Snapshot windows, tabs, pane trees, titles, cwd, tail text."},
            {"name": "pane", "args": {"pane_id": "u64", "lines": "u32?"}, "doc": "One pane including more scrollback."},
            {"name": "split", "args": {"pane_id": "u64?", "axis": "right|down"}, "doc": "Split a pane (default: focused)."},
            {"name": "focus", "args": {"pane_id": "u64"}, "doc": "Focus a pane."},
            {"name": "move", "args": {"pane_id": "u64", "target_pane_id": "u64", "edge": "left|right|top|bottom"}, "doc": "Re-dock pane_id onto an edge of target_pane_id."},
            {"name": "move_to_tab", "args": {"pane_id": "u64", "tab_id": "u64?"}, "doc": "Move onto an existing tab, or extract to a new tab if tab_id omitted."},
            {"name": "rotate", "args": {"pane_id": "u64?"}, "doc": "Rotate the split around the pane."},
            {"name": "swap", "args": {"pane_id": "u64?"}, "doc": "Swap the two sides of the split."},
            {"name": "grow", "args": {"pane_id": "u64?"}, "doc": "Grow the pane."},
            {"name": "shrink", "args": {"pane_id": "u64?"}, "doc": "Shrink the pane."},
            {"name": "equalize", "args": {"pane_id": "u64?"}, "doc": "50/50 the split."},
            {"name": "close", "args": {"pane_id": "u64?"}, "doc": "Close pane (or its tab if last pane)."},
            {"name": "rename", "args": {"pane_id": "u64", "title": "string"}, "doc": "Set the pane title."},
            {"name": "new_tab", "args": {}, "doc": "Open a new tab with a shell."}
        ]
    })
}

pub fn layout_json(session: &ChromeSession) -> Value {
    let mut by_surface: HashMap<u64, Vec<Value>> = HashMap::new();
    for tab in &session.tabs {
        let panes: Vec<Value> = tab
            .root
            .leaf_ids()
            .into_iter()
            .filter_map(|id| pane_json(session, id, 12))
            .collect();
        let tab_json = json!({
            "id": tab.id,
            "title": tab.title,
            "active": tab.id == session.active_id,
            "focus_pane": tab.focus_pane,
            "tree": tree_json(&tab.root),
            "panes": panes,
        });
        by_surface.entry(tab.surface).or_default().push(tab_json);
    }
    let windows: Vec<Value> = {
        let mut keys: Vec<u64> = by_surface.keys().copied().collect();
        keys.sort_unstable();
        keys.into_iter()
            .map(|surface| {
                json!({
                    "surface": surface,
                    "tabs": by_surface.get(&surface).cloned().unwrap_or_default(),
                })
            })
            .collect()
    };
    json!({
        "pid": std::process::id(),
        "focus_pane": session.focus_pane_id(),
        "active_tab": session.active_id,
        "windows": windows,
    })
}

pub fn pane_json(session: &ChromeSession, id: u64, tail: usize) -> Option<Value> {
    let p = session.panes.get(&id)?;
    let kind = match p.kind {
        PaneKind::Terminal => "terminal",
        PaneKind::Widget(WidgetKind::Workspace) => "workspace",
        PaneKind::Widget(WidgetKind::Guest) => "guest",
    };
    let hist: Vec<String> = history_tail_of(&p.grid, tail)
        .into_iter()
        .map(|(t, _)| t)
        .collect();
    let live = live_lines_of(&p.grid);
    Some(json!({
        "id": p.id,
        "title": p.title,
        "kind": kind,
        "cwd": p.cwd,
        "cols": p.grid.cols(),
        "rows": p.grid.rows(),
        "guest_url": if p.guest_url.is_empty() { Value::Null } else { json!(p.guest_url) },
        "tail": hist,
        "live": live,
    }))
}

pub fn parse_axis(s: &str) -> Option<SplitAxis> {
    match s.trim().to_ascii_lowercase().as_str() {
        "right" | "vertical" | "v" => Some(SplitAxis::Vertical),
        "down" | "below" | "horizontal" | "h" => Some(SplitAxis::Horizontal),
        _ => None,
    }
}

pub fn parse_edge(s: &str) -> Option<DockEdge> {
    match s.trim().to_ascii_lowercase().as_str() {
        "left" => Some(DockEdge::Left),
        "right" => Some(DockEdge::Right),
        "top" | "up" => Some(DockEdge::Top),
        "bottom" | "down" => Some(DockEdge::Bottom),
        _ => None,
    }
}

pub fn arg_u64(args: &Value, key: &str) -> Option<u64> {
    args.get(key).and_then(|v| {
        v.as_u64()
            .or_else(|| v.as_i64().and_then(|n| u64::try_from(n).ok()))
            .or_else(|| v.as_str()?.parse().ok())
    })
}

fn tree_json(n: &SplitNode) -> Value {
    match n {
        SplitNode::Leaf(id) => json!({"pane": id}),
        SplitNode::Branch {
            axis, ratio, a, b, ..
        } => json!({
            "axis": match axis {
                SplitAxis::Vertical => "right",
                SplitAxis::Horizontal => "down",
            },
            "ratio": ratio,
            "a": tree_json(a),
            "b": tree_json(b),
        }),
    }
}

fn make_token(url: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut h = Sha256::new();
    h.update(std::process::id().to_le_bytes());
    h.update(url.as_bytes());
    if let Ok(d) = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH) {
        h.update(d.as_nanos().to_le_bytes());
    }
    let bytes = h.finalize();
    bytes[..16].iter().map(|b| format!("{b:02x}")).collect()
}

fn discovery_dir() -> PathBuf {
    config_store::product_config_dir().join("ai")
}

fn write_discovery(adv: &Advertised, pid: u32) {
    let body = json!({
        "url": adv.url,
        "token": adv.token,
        "pid": pid,
        "help": "/help",
        "tools": "/tools",
    });
    let dir = discovery_dir();
    let _ = fs::create_dir_all(&dir);
    let pid_path = dir.join(format!("{pid}.json"));
    let _ = fs::write(&pid_path, body.to_string());
    let _ = fs::write(
        config_store::product_config_dir().join("ai.json"),
        body.to_string(),
    );
}

fn remove_discovery(pid: u32) {
    let _ = fs::remove_file(discovery_dir().join(format!("{pid}.json")));
    let root = config_store::product_config_dir().join("ai.json");
    if let Ok(raw) = fs::read_to_string(&root) {
        if raw.contains(&format!("\"pid\":{pid}")) || raw.contains(&format!("\"pid\": {pid}")) {
            let _ = fs::remove_file(&root);
        }
    }
}

fn accept_loop(listener: TcpListener, tx: Sender<AiJob>, token: String, stop: Arc<AtomicBool>) {
    loop {
        if stop.load(Ordering::Relaxed) {
            break;
        }
        match listener.accept() {
            Ok((stream, _)) => {
                let tx = tx.clone();
                let token = token.clone();
                let _ = thread::Builder::new()
                    .name("suzuri-ai-http-conn".into())
                    .spawn(move || handle_conn(stream, tx, token));
            }
            Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                thread::sleep(Duration::from_millis(20));
            }
            Err(_) => break,
        }
    }
}

struct ParsedReq {
    method: String,
    path: String,
    query: HashMap<String, String>,
    headers: HashMap<String, String>,
    body: Vec<u8>,
}

fn handle_conn(mut stream: TcpStream, tx: Sender<AiJob>, token: String) {
    let _ = stream.set_read_timeout(Some(Duration::from_secs(5)));
    let _ = stream.set_write_timeout(Some(Duration::from_secs(5)));
    let req = match read_req(&mut stream) {
        Some(r) => r,
        None => {
            let _ = write_res(&mut stream, 400, "text/plain", b"bad request");
            return;
        }
    };
    let authed = token_ok(&req, &token);
    let path = req.path.as_str();
    let method = req.method.to_ascii_uppercase();

    let reply = if method == "GET" && (path == "/" || path == "/help") {
        AiReply::text(200, HELP)
    } else if method == "GET" && path == "/tools" {
        AiReply::json_ok(tools_json())
    } else if !authed {
        AiReply::json_status(401, json!({"error": "unauthorized"}))
    } else if method == "GET" && path == "/v1/layout" {
        roundtrip(&tx, AiOp::Layout)
    } else if method == "GET" && path.starts_with("/v1/panes/") {
        let id = path.trim_start_matches("/v1/panes/").parse::<u64>().ok();
        match id {
            Some(id) => {
                let lines = req
                    .query
                    .get("lines")
                    .and_then(|s| s.parse().ok())
                    .unwrap_or(80);
                roundtrip(&tx, AiOp::Pane { id, lines })
            }
            None => AiReply::json_status(400, json!({"error": "bad pane id"})),
        }
    } else if method == "POST" && path == "/v1/call" {
        match serde_json::from_slice::<Value>(&req.body) {
            Ok(v) => {
                let tool = v
                    .get("tool")
                    .and_then(|x| x.as_str())
                    .unwrap_or("")
                    .to_string();
                let args = v.get("args").cloned().unwrap_or(json!({}));
                if tool.is_empty() {
                    AiReply::json_status(400, json!({"error": "missing tool"}))
                } else {
                    roundtrip(&tx, AiOp::Call { tool, args })
                }
            }
            Err(e) => AiReply::json_status(400, json!({"error": format!("json: {e}")})),
        }
    } else {
        AiReply::json_status(404, json!({"error": "not found", "see": "/help"}))
    };

    let _ = write_res(&mut stream, reply.status, reply.content_type, &reply.body);
}

fn roundtrip(tx: &Sender<AiJob>, op: AiOp) -> AiReply {
    let (rtx, rrx) = mpsc::channel();
    if tx.send(AiJob { op, reply: rtx }).is_err() {
        return AiReply::json_status(503, json!({"error": "ui thread gone"}));
    }
    match rrx.recv_timeout(Duration::from_secs(4)) {
        Ok(r) => r,
        Err(_) => AiReply::json_status(504, json!({"error": "ui timeout"})),
    }
}

fn token_ok(req: &ParsedReq, token: &str) -> bool {
    if token.is_empty() {
        return false;
    }
    if req.query.get("token").is_some_and(|t| t == token) {
        return true;
    }
    if req
        .headers
        .get("x-suzuri-token")
        .is_some_and(|t| t == token)
    {
        return true;
    }
    if let Some(a) = req.headers.get("authorization") {
        let a = a.trim();
        if let Some(rest) = a
            .strip_prefix("Bearer ")
            .or_else(|| a.strip_prefix("bearer "))
        {
            return rest.trim() == token;
        }
    }
    false
}

fn read_req(stream: &mut TcpStream) -> Option<ParsedReq> {
    let mut buf = Vec::new();
    let mut tmp = [0u8; 2048];
    loop {
        let n = stream.read(&mut tmp).ok()?;
        if n == 0 {
            break;
        }
        buf.extend_from_slice(&tmp[..n]);
        if buf.len() > 1024 * 1024 {
            return None;
        }
        if buf.windows(4).any(|w| w == b"\r\n\r\n") {
            break;
        }
    }
    let header_end = buf.windows(4).position(|w| w == b"\r\n\r\n")?;
    let head = std::str::from_utf8(&buf[..header_end]).ok()?;
    let mut lines = head.split("\r\n");
    let req = lines.next()?;
    let mut parts = req.split_whitespace();
    let method = parts.next()?.to_string();
    let target = parts.next()?.to_string();
    let (path, query) = split_query(&target);
    let mut headers = HashMap::new();
    for line in lines {
        if let Some((k, v)) = line.split_once(':') {
            headers.insert(k.trim().to_ascii_lowercase(), v.trim().to_string());
        }
    }
    let mut body = buf[header_end + 4..].to_vec();
    let want = headers
        .get("content-length")
        .and_then(|s| s.parse::<usize>().ok())
        .unwrap_or(0);
    while body.len() < want {
        let n = stream.read(&mut tmp).ok()?;
        if n == 0 {
            break;
        }
        body.extend_from_slice(&tmp[..n]);
        if body.len() > 1024 * 1024 {
            return None;
        }
    }
    body.truncate(want);
    Some(ParsedReq {
        method,
        path,
        query,
        headers,
        body,
    })
}

fn split_query(target: &str) -> (String, HashMap<String, String>) {
    match target.split_once('?') {
        Some((p, q)) => {
            let mut map = HashMap::new();
            for pair in q.split('&') {
                if let Some((k, v)) = pair.split_once('=') {
                    map.insert(url_decode(k), url_decode(v));
                } else if !pair.is_empty() {
                    map.insert(url_decode(pair), String::new());
                }
            }
            (p.to_string(), map)
        }
        None => (target.to_string(), HashMap::new()),
    }
}

fn url_decode(s: &str) -> String {
    let mut out = Vec::new();
    let b = s.as_bytes();
    let mut i = 0;
    while i < b.len() {
        match b[i] {
            b'%' if i + 2 < b.len() => {
                let hex = &s[i + 1..i + 3];
                if let Ok(v) = u8::from_str_radix(hex, 16) {
                    out.push(v);
                    i += 3;
                    continue;
                }
                out.push(b[i]);
                i += 1;
            }
            b'+' => {
                out.push(b' ');
                i += 1;
            }
            c => {
                out.push(c);
                i += 1;
            }
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

fn write_res(stream: &mut TcpStream, status: u16, ctype: &str, body: &[u8]) -> std::io::Result<()> {
    let reason = match status {
        200 => "OK",
        400 => "Bad Request",
        401 => "Unauthorized",
        404 => "Not Found",
        503 => "Service Unavailable",
        504 => "Gateway Timeout",
        _ => "Error",
    };
    let head = format!(
        "HTTP/1.1 {status} {reason}\r\nContent-Type: {ctype}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        body.len()
    );
    stream.write_all(head.as_bytes())?;
    stream.write_all(body)?;
    stream.flush()
}

/// Shared reply helper for the UI thread.
pub fn reply_ok(job: AiJob, v: Value) {
    let _ = job.reply.send(AiReply::json_ok(v));
}

pub fn reply_err(job: AiJob, status: u16, msg: &str) {
    let _ = job
        .reply
        .send(AiReply::json_status(status, json!({"error": msg})));
}

pub fn reply_json(job: AiJob, status: u16, v: Value) {
    let _ = job.reply.send(AiReply::json_status(status, v));
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::session::ChromeSession;

    #[test]
    fn tools_include_move_and_split() {
        let names: Vec<String> = tools_json()["tools"]
            .as_array()
            .unwrap()
            .iter()
            .map(|t| t["name"].as_str().unwrap().to_string())
            .collect();
        for need in ["layout", "split", "move", "focus", "rotate", "new_tab"] {
            assert!(names.iter().any(|n| n == need), "missing {need}");
        }
    }

    #[test]
    fn parse_axis_and_edge() {
        assert_eq!(parse_axis("right"), Some(SplitAxis::Vertical));
        assert_eq!(parse_axis("down"), Some(SplitAxis::Horizontal));
        assert_eq!(parse_edge("left"), Some(DockEdge::Left));
        assert_eq!(parse_edge("top"), Some(DockEdge::Top));
        assert!(parse_axis("sideways").is_none());
    }

    #[test]
    fn layout_lists_split_panes() {
        let mut s = ChromeSession::new(40, 12);
        let id = s.focus_pane_id();
        let right = s.split_focused(SplitAxis::Vertical, 40, 12).unwrap();
        let v = layout_json(&s);
        let tabs = &v["windows"][0]["tabs"][0];
        assert_eq!(tabs["tree"]["axis"], "right");
        let panes = tabs["panes"].as_array().unwrap();
        assert_eq!(panes.len(), 2);
        let ids: Vec<u64> = panes.iter().map(|p| p["id"].as_u64().unwrap()).collect();
        assert!(ids.contains(&id) && ids.contains(&right));
    }

    #[test]
    fn help_mentions_curl_not_mcp() {
        assert!(HELP.contains("Not MCP"));
        assert!(HELP.contains("/v1/call"));
        assert!(HELP.contains("move"));
    }

    #[test]
    fn token_from_query() {
        let mut q = HashMap::new();
        q.insert("token".into(), "abc".into());
        let req = ParsedReq {
            method: "GET".into(),
            path: "/v1/layout".into(),
            query: q,
            headers: HashMap::new(),
            body: Vec::new(),
        };
        assert!(token_ok(&req, "abc"));
        assert!(!token_ok(&req, "nope"));
    }

    #[test]
    fn server_serves_help_and_tools() {
        let h = start().expect("bind");
        let url = h.url.clone();
        let help = ureq_get(&format!("{url}/help"));
        assert!(help.contains("Suzuri AI control"), "{help}");
        let tools = ureq_get(&format!("{url}/tools"));
        assert!(tools.contains("\"move\""), "{tools}");
        drop(h);
    }

    fn ureq_get(url: &str) -> String {
        let rest = url.trim_start_matches("http://");
        let (addr, path) = rest.split_once('/').unwrap_or((rest, ""));
        let mut stream = TcpStream::connect(addr).expect("connect ai");
        let req = format!("GET /{path} HTTP/1.1\r\nHost: {addr}\r\nConnection: close\r\n\r\n");
        stream.write_all(req.as_bytes()).unwrap();
        let mut buf = Vec::new();
        stream.read_to_end(&mut buf).unwrap();
        String::from_utf8_lossy(&buf).into_owned()
    }
}
