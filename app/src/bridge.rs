//! The agent bridge (#286): the same loopback HTTP endpoints Go suzuri's GUI
//! serves, so Go's `suzuri mcp` (the stdio MCP server agents spawn; this app
//! has none since #330) and the
//! curl-based AI control API work against suzuri unchanged.
//!
//! - MCP bridge: `{config}/bridge.json` {pid,url,token}; GET /v1/status,
//!   /v1/snapshot, /v1/diag, /v1/logs; POST /v1/submit. /v1/notes and
//!   /v1/workspace answer 404 on purpose: `suzuri mcp` then uses Go's own
//!   store on disk, which is the single writer of those files.
//! - AI control: `{config}/ai.json`, `{config}/ai/{pid}.json` and the
//!   SUZURI_CONTROL_URL / SUZURI_CONTROL_TOKEN / SUZURI / SUZURI_PANE_ID
//!   variables in every pane; GET /help, /tools, /v1/layout, /v1/panes/:id;
//!   POST /v1/call. The pane tree lives in the webview, so layout and pane
//!   commands go to it as `aicontrol` events and come back via
//!   `aicontrol_reply`.
//!
//! Go suzuri and suzuri both write bridge.json and ai.json: run one at a
//! time.

use std::collections::HashMap;
use std::io::Read;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{self, Sender};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use parking_lot::Mutex;
use serde_json::{Value, json};
use tauri::{AppHandle, Emitter, State};
use tiny_http::{Header, Method, Request, Response, Server};

use crate::Panes;

pub struct Bridge {
    pub url: String,
    pub token: String,
    app: Mutex<Option<AppHandle>>,
    panes: Arc<Panes>,
    next: AtomicU64,
    pending: Mutex<HashMap<u64, Sender<(u16, Value)>>>,
}

fn config_dir() -> PathBuf {
    crate::sidecar::config_dir()
}

fn token() -> String {
    let mut b = [0u8; 16];
    if getrandom::fill(&mut b).is_err() {
        let t = SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_nanos();
        b.copy_from_slice(&(t ^ ((std::process::id() as u128) << 64)).to_le_bytes());
    }
    b.iter().map(|x| format!("{x:02x}")).collect()
}

fn write_private(path: &PathBuf, body: &[u8]) {
    let tmp = path.with_extension("json.tmp");
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        let _ = std::fs::OpenOptions::new().write(true).create(true).truncate(true).mode(0o600).open(&tmp).and_then(|mut f| {
            use std::io::Write;
            f.write_all(body)
        });
    }
    #[cfg(not(unix))]
    let _ = std::fs::write(&tmp, body);
    let _ = std::fs::rename(&tmp, path);
}

impl Bridge {
    /// Listen on 127.0.0.1:0, write the discovery files, serve on a thread.
    pub fn start(panes: Arc<Panes>) -> std::io::Result<Arc<Bridge>> {
        if let Some(why) = off_reason(std::env::var("RSZ_BRIDGE").ok().as_deref(), is_dev_build()) {
            return Err(std::io::Error::other(why));
        }
        for f in ["bridge.json", "ai.json"] {
            if let Some(url) = live_owner(&config_dir().join(f)) {
                return Err(std::io::Error::other(format!("another suzuri owns {f} ({url}); leaving it alone")));
            }
        }
        let server = Server::http("127.0.0.1:0").map_err(std::io::Error::other)?;
        let port = server.server_addr().to_ip().map(|a| a.port()).unwrap_or(0);
        let b = Arc::new(Bridge {
            url: format!("http://127.0.0.1:{port}"),
            token: token(),
            app: Mutex::new(None),
            panes,
            next: AtomicU64::new(0),
            pending: Mutex::default(),
        });
        let dir = config_dir();
        let _ = std::fs::create_dir_all(dir.join("ai"));
        let pid = std::process::id();
        let ep = serde_json::to_vec_pretty(&json!({ "pid": pid, "url": b.url, "token": b.token })).unwrap_or_default();
        write_private(&dir.join("bridge.json"), &ep);
        let ai = serde_json::to_vec(&json!({ "url": b.url, "token": b.token, "pid": pid, "help": "/help", "tools": "/tools" })).unwrap_or_default();
        write_private(&dir.join("ai").join(format!("{pid}.json")), &ai);
        write_private(&dir.join("ai.json"), &ai);
        let srv = Arc::clone(&b);
        std::thread::Builder::new().name("bridge".into()).spawn(move || {
            for req in server.incoming_requests() {
                let b = Arc::clone(&srv);
                // Calls into the webview block; don't hold up the next request.
                std::thread::spawn(move || b.handle(req));
            }
        })?;
        eprintln!("bridge: {} ({})", b.url, dir.join("bridge.json").display());
        Ok(b)
    }

    pub fn attach(&self, app: AppHandle) {
        *self.app.lock() = Some(app);
    }

    /// Remove the discovery files if they are still ours.
    pub fn stop(&self) {
        let dir = config_dir();
        let pid = std::process::id();
        let _ = std::fs::remove_file(dir.join("ai").join(format!("{pid}.json")));
        for f in ["bridge.json", "ai.json"] {
            let p = dir.join(f);
            let mine = std::fs::read_to_string(&p)
                .ok()
                .and_then(|s| serde_json::from_str::<Value>(&s).ok())
                .is_some_and(|v| v.get("pid").and_then(Value::as_u64) == Some(pid as u64));
            if mine {
                let _ = std::fs::remove_file(p);
            }
        }
    }

    /// Variables every pane's shell gets, as Go suzuri sets them.
    pub fn pane_env(&self, pane: u32) -> Vec<(String, String)> {
        vec![
            ("SUZURI".into(), "1".into()),
            ("SUZURI_CONTROL_URL".into(), self.url.clone()),
            ("SUZURI_CONTROL_TOKEN".into(), self.token.clone()),
            ("SUZURI_PANE_ID".into(), pane.to_string()),
        ]
    }

    fn authorized(&self, req: &Request) -> bool {
        let want = &self.token;
        let hdr = |name: &'static str| req.headers().iter().find(|h| h.field.equiv(name)).map(|h| h.value.as_str().to_string());
        if let Some(a) = hdr("Authorization") {
            if a.strip_prefix("Bearer ").is_some_and(|t| t == want) {
                return true;
            }
        }
        if hdr("X-Suzuri-Token").as_deref() == Some(want.as_str()) {
            return true;
        }
        query(req.url(), "token").as_deref() == Some(want.as_str())
    }

    fn handle(&self, mut req: Request) {
        let path = req.url().split('?').next().unwrap_or("").to_string();
        let open = matches!(path.as_str(), "/help" | "/tools");
        if !open && !self.authorized(&req) {
            return reply(req, 401, json!({ "error": "unauthorized" }));
        }
        let mut body = String::new();
        let _ = req.as_reader().take(1 << 20).read_to_string(&mut body);
        let (status, out) = match (req.method(), path.as_str()) {
            (Method::Get, "/help") => return reply_text(req, HELP),
            (Method::Get, "/tools") => (200, tools()),
            (Method::Get, "/v1/status") => (200, self.status()),
            (Method::Get, "/v1/snapshot" | "/v1/diag") => (200, self.snapshot()),
            (Method::Get, "/v1/logs") => (200, json!({ "ok": false, "path": "", "lines": [], "count": 0, "error": "suzuri has no app log file yet; it logs to stderr" })),
            (Method::Post, "/v1/submit") => self.submit(&body),
            (Method::Get, "/v1/layout") => self.ui("layout", json!({})),
            (Method::Get, p) if p.starts_with("/v1/panes/") => {
                let id = p.trim_start_matches("/v1/panes/").parse::<u64>().unwrap_or(0);
                let lines = query(req.url(), "lines").and_then(|v| v.parse::<u64>().ok()).unwrap_or(200);
                self.ui("pane", json!({ "pane_id": id, "lines": lines }))
            }
            (Method::Post, "/v1/call") => match serde_json::from_str::<Value>(&body) {
                Ok(v) => {
                    let tool = v.get("tool").and_then(Value::as_str).unwrap_or("").to_string();
                    self.ui(&tool, v.get("args").cloned().unwrap_or(json!({})))
                }
                Err(e) => (400, json!({ "error": format!("bad json: {e}") })),
            },
            // notes and workspace: 404 so `suzuri mcp` uses Go's store on disk.
            _ => (404, json!({ "error": format!("{path} is not served by suzuri") })),
        };
        reply(req, status, out)
    }

    fn pane_ids(&self) -> Vec<u32> {
        let mut ids: Vec<u32> = self.panes.map.lock().keys().copied().collect();
        ids.sort();
        ids
    }

    fn active(&self) -> u32 {
        let f = self.panes.focused.load(Ordering::Relaxed);
        if f != 0 && self.panes.get(f).is_some() { f } else { self.pane_ids().first().copied().unwrap_or(0) }
    }

    fn status(&self) -> Value {
        json!({
            "ok": true,
            "pid": std::process::id(),
            "tabs": self.pane_ids().len(),
            "active_tab": self.active(),
            "bridge": self.url,
            "message": "suzuri: each pane is reported as a tab",
        })
    }

    /// Go's Snapshot, one "tab" per pane until suzuri has tabs (#283).
    fn snapshot(&self) -> Value {
        let titles = self.panes.titles.lock().clone();
        let tabs: Vec<Value> = self
            .pane_ids()
            .into_iter()
            .filter_map(|id| {
                let p = self.panes.get(id)?;
                let text = p.screen_text();
                let viewport: Vec<&str> = text.lines().collect();
                let mut live = viewport.clone();
                while live.last().is_some_and(|l| l.trim().is_empty()) {
                    live.pop();
                }
                Some(json!({
                    "id": id,
                    "title": titles.get(&id).cloned().unwrap_or_default(),
                    "alive": true,
                    "input": "",
                    "alt_screen": false,
                    "echo": { "armed": false, "phase": 0 },
                    "live_lines": live,
                    "viewport": viewport,
                    "blocks": [],
                    "history_tail": p.history_tail(12),
                    "pty_tail": "",
                }))
            })
            .collect();
        let first = tabs.first();
        // Go reports the active tab's grid size.
        let (cols, rows) = self.panes.sizes.lock().get(&self.active()).copied().unwrap_or((0, 0));
        let _ = first;
        json!({
            "pid": std::process::id(),
            "updated_at": rfc3339_now(),
            "cols": cols,
            "rows": rows,
            "active_tab": self.active(),
            "tabs": tabs,
        })
    }

    /// Type a line into a pane's shell (the focused one by default). Until the
    /// input bar (#285) lands this is the line plus Enter on the PTY.
    fn submit(&self, body: &str) -> (u16, Value) {
        let v: Value = match serde_json::from_str(body) {
            Ok(v) => v,
            Err(e) => return (400, json!({ "ok": false, "error": format!("bad json: {e}") })),
        };
        let line = v.get("line").and_then(Value::as_str).unwrap_or("").to_string();
        let id = v.get("tab_id").and_then(Value::as_u64).map(|x| x as u32).unwrap_or_else(|| self.active());
        let Some(p) = self.panes.get(id) else {
            return (200, json!({ "ok": false, "tab_id": id, "line": line, "error": format!("no pane {id}") }));
        };
        let mut bytes = line.replace('\n', "\r").into_bytes();
        bytes.push(b'\r');
        p.write(bytes);
        (200, json!({ "ok": true, "tab_id": id, "line": line }))
    }

    /// Ask the webview to run an AI control tool; wait up to 5 s.
    fn ui(&self, tool: &str, args: Value) -> (u16, Value) {
        let Some(app) = self.app.lock().clone() else {
            return (503, json!({ "error": "window not ready" }));
        };
        let id = self.next.fetch_add(1, Ordering::Relaxed) + 1;
        let (tx, rx) = mpsc::channel();
        self.pending.lock().insert(id, tx);
        if app.emit("aicontrol", json!({ "id": id, "tool": tool, "args": args })).is_err() {
            self.pending.lock().remove(&id);
            return (503, json!({ "error": "window not reachable" }));
        }
        match rx.recv_timeout(Duration::from_secs(5)) {
            Ok(r) => r,
            Err(_) => {
                self.pending.lock().remove(&id);
                (504, json!({ "error": "the window did not answer within 5 s" }))
            }
        }
    }

    fn resolve(&self, id: u64, status: u16, body: Value) {
        if let Some(tx) = self.pending.lock().remove(&id) {
            let _ = tx.send((status, body));
        }
    }
}

/// The webview's answer to an `aicontrol` event.
#[tauri::command]
pub fn aicontrol_reply(bridge: State<'_, Arc<Bridge>>, id: u64, status: u16, body: Value) {
    bridge.resolve(id, status, body);
}

/// A pane's text for the AI control `pane` tool: the last `lines` lines.
#[tauri::command]
pub fn pane_text(panes: State<'_, Arc<Panes>>, id: u32, lines: Option<usize>) -> Option<String> {
    let p = panes.get(id)?;
    let text = p.screen_text();
    let screen: Vec<&str> = text.lines().collect();
    let n = lines.unwrap_or(screen.len());
    // Like Go's pane tool: past the screen, the tail reaches into scrollback.
    let history = p.history_tail(n.saturating_sub(screen.len()));
    let all: Vec<&str> = history.iter().map(String::as_str).chain(screen.iter().copied()).collect();
    let n = n.min(all.len());
    Some(all[all.len() - n..].join("\n"))
}

/// A build run from a cargo `target/` dir (debug or release) is a dev build:
/// it must not take over the person's live suzuri's discovery files.
fn is_dev_build() -> bool {
    std::env::current_exe()
        .map(|p| p.components().any(|c| c.as_os_str() == "target"))
        .unwrap_or(true)
}

/// Why the bridge stays off, or None to start it. RSZ_BRIDGE=1 forces it on
/// (tests: pair it with LOCALAPPDATA=<scratch> for a private config dir),
/// RSZ_BRIDGE=0 forces it off; otherwise only an installed build starts it.
fn off_reason(env: Option<&str>, dev: bool) -> Option<String> {
    match env {
        Some("1") => None,
        Some("0") => Some("off (RSZ_BRIDGE=0)".into()),
        _ if dev => Some("off in dev builds; RSZ_BRIDGE=1 with LOCALAPPDATA=<scratch dir> turns it on".into()),
        _ => None,
    }
}

/// Variables a pane would inherit from the suzuri that started this process.
/// Removed at startup so a pane never points at another window's control API.
pub const INHERITED: [&str; 4] = ["SUZURI", "SUZURI_CONTROL_URL", "SUZURI_CONTROL_TOKEN", "SUZURI_PANE_ID"];

/// The URL in an existing bridge.json, if something still listens there
/// (Go suzuri or another suzuri). A stale file from a crash is ignored.
fn live_owner(path: &PathBuf) -> Option<String> {
    let v: Value = serde_json::from_str(&std::fs::read_to_string(path).ok()?).ok()?;
    if v.get("pid").and_then(Value::as_u64) == Some(std::process::id() as u64) {
        return None;
    }
    let url = v.get("url")?.as_str()?.to_string();
    let addr: std::net::SocketAddr = url.strip_prefix("http://")?.parse().ok()?;
    std::net::TcpStream::connect_timeout(&addr, Duration::from_millis(300)).ok()?;
    Some(url)
}

fn query(url: &str, key: &str) -> Option<String> {
    let q = url.split_once('?')?.1;
    q.split('&').find_map(|kv| {
        let (k, v) = kv.split_once('=').unwrap_or((kv, ""));
        (k == key).then(|| v.to_string())
    })
}

fn reply(req: Request, status: u16, body: Value) {
    let ct = Header::from_bytes("Content-Type", "application/json; charset=utf-8").unwrap();
    let _ = req.respond(Response::from_string(body.to_string()).with_status_code(status).with_header(ct));
}

fn reply_text(req: Request, text: &str) {
    let ct = Header::from_bytes("Content-Type", "text/plain; charset=utf-8").unwrap();
    let _ = req.respond(Response::from_string(text).with_header(ct));
}

pub fn rfc3339_now() -> String {
    let secs = SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_secs() as i64;
    let (days, rem) = (secs.div_euclid(86_400), secs.rem_euclid(86_400));
    // civil-from-days (Howard Hinnant)
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!("{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z", rem / 3600, rem % 3600 / 60, rem % 60)
}

fn tools() -> Value {
    json!({ "tools": [
        { "name": "layout", "args": {} },
        { "name": "pane", "args": { "pane_id": "int", "lines": "int?" } },
        { "name": "split", "args": { "pane_id": "int?", "axis": "right|down" } },
        { "name": "focus", "args": { "pane_id": "int" } },
        { "name": "close", "args": { "pane_id": "int?" } },
        { "name": "rename", "args": { "pane_id": "int", "title": "string" } },
        { "name": "report_agent", "args": { "pane_id": "int", "state": "idle|working|blocked|done|clear" } },
        { "name": "new_tab", "args": {} },
        { "name": "move_to_tab", "args": { "pane_id": "int", "tab_id": "int?" } },
    ], "not_yet": ["move", "rotate", "swap", "grow", "shrink", "equalize"] })
}

const HELP: &str = "# Suzuri AI control

Loopback HTTP for this window. Not MCP. Prefer curl.

Auth (except GET /help and /tools): Authorization: Bearer $SUZURI_CONTROL_TOKEN,
or X-Suzuri-Token, or ?token=.

Discovery: $SUZURI_CONTROL_URL (in every pane), $SUZURI=1, $SUZURI_PANE_ID,
{config_dir}/ai.json, {config_dir}/ai/{pid}.json

  GET  /help, /tools
  GET  /v1/layout          the pane tree, titles, tail text
  GET  /v1/panes/:id       one pane (?lines=N)
  POST /v1/call            {\"tool\":\"<name>\",\"args\":{...}}

Tools: layout; pane {pane_id, lines?}; split {pane_id?, axis: right|down};
focus {pane_id}; close {pane_id?}; rename {pane_id, title};
report_agent {pane_id, state: idle|working|blocked|done|clear};
new_tab; move_to_tab {pane_id, tab_id?} (no tab_id: its own new tab).
Not yet in suzuri: move, rotate, swap, grow, shrink, equalize.
";

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn query_finds_keys() {
        assert_eq!(query("/v1/panes/3?lines=40&token=ab", "token").as_deref(), Some("ab"));
        assert_eq!(query("/v1/panes/3?lines=40", "lines").as_deref(), Some("40"));
        assert_eq!(query("/v1/layout", "token"), None);
    }

    #[test]
    fn bridge_off_in_dev_unless_asked() {
        assert!(off_reason(None, true).is_some());
        assert!(off_reason(Some("1"), true).is_none());
        assert!(off_reason(None, false).is_none());
        assert!(off_reason(Some("0"), false).is_some());
    }

    #[test]
    fn rfc3339_shape() {
        let s = rfc3339_now();
        assert_eq!(s.len(), 20);
        assert!(s.ends_with('Z') && s.as_bytes()[10] == b'T');
    }
}
