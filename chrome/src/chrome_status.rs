//! Publish live chrome process status for the Go MCP bridge proxy.
//!
//! The host (`internal/chromehost`) reads `{config_dir}/chrome_status.json`
//! while `suzuri chrome` runs and republishes it over loopback bridge.json so
//! `suzuri mcp` can attach without embedding the GPU loop.
//!
//! Shape mirrors `bridge.Snapshot` / `bridge.TabSnap` (subset) so Go can
//! unmarshal tabs with viewport + live_lines for `suzuri_diag` / snapshot.

use std::fs;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use crate::cells::CellGrid;
use crate::config_store;
use crate::session::ChromeSession;

/// Filename under the product config directory.
pub const STATUS_FILE: &str = "chrome_status.json";

/// How often we rewrite status (host polls ~500ms).
pub const PUBLISH_INTERVAL: Duration = Duration::from_millis(750);

/// Warp-submit mailbox (host writes a full line; chrome feeds active PTY).
pub const SUBMIT_FILE: &str = "chrome_submit";

/// Max history lines included per tab (scrollback tail).
pub const HISTORY_TAIL: usize = 40;

/// One tab/pane snapshot for the bridge (aligned with Go `bridge.TabSnap`).
#[derive(Clone, Debug, Default)]
pub struct TabSnapOut {
    pub id: i32,
    pub title: String,
    pub alive: bool,
    pub shell: String,
    pub input: String,
    pub alt_screen: bool,
    /// Echo filter status (MCP diag).
    pub echo_armed: bool,
    pub echo_cmd: String,
    pub echo_phase: i32,
    pub live_lines: Vec<String>,
    pub viewport: Vec<String>,
    pub history: Vec<(String, String)>, // (text, kind)
    /// Recent host-injected command blocks (newest last).
    pub blocks: Vec<String>,
    pub pty_tail: String,
}

/// Full chrome → host status document.
#[derive(Clone, Debug, Default)]
pub struct ChromeSnapOut {
    pub pid: u32,
    pub version: String,
    pub cols: i32,
    pub rows: i32,
    pub active_tab: i32,
    pub tabs: Vec<TabSnapOut>,
    pub notes: Vec<String>,
}

/// Rate-limited status publisher.
#[derive(Debug)]
pub struct StatusPublisher {
    last: Instant,
}

impl Default for StatusPublisher {
    fn default() -> Self {
        Self {
            last: Instant::now()
                .checked_sub(PUBLISH_INTERVAL)
                .unwrap_or_else(Instant::now),
        }
    }
}

impl StatusPublisher {
    pub fn new() -> Self {
        Self::default()
    }

    /// Write status if the publish interval has elapsed.
    pub fn tick(&mut self, snap: &ChromeSnapOut) {
        let now = Instant::now();
        if now.duration_since(self.last) < PUBLISH_INTERVAL {
            return;
        }
        self.last = now;
        let _ = write_snap_at(&status_path(), snap);
    }

    /// Convenience: build a minimal snap from session alone (no PTY meta).
    pub fn tick_session(&mut self, session: &ChromeSession) {
        self.tick(&snap_from_session(session, &[]));
    }
}

/// Absolute path of the status file.
pub fn status_path() -> PathBuf {
    config_store::product_config_dir().join(STATUS_FILE)
}

/// Absolute path of the submit mailbox.
pub fn submit_path() -> PathBuf {
    config_store::product_config_dir().join(SUBMIT_FILE)
}

/// Optional per-pane extras for echo / blocks / pty (keyed by pane id).
#[derive(Clone, Debug, Default)]
pub struct PaneSnapExtra {
    pub pane_id: u64,
    pub alive: bool,
    pub alt_screen: bool,
    pub shell: String,
    pub pty_tail: String,
    pub echo_armed: bool,
    pub echo_cmd: String,
    pub echo_phase: i32,
    pub blocks: Vec<String>,
    /// When non-empty, used for `history_tail` instead of plain grid text.
    pub history: Vec<(String, String)>,
}

/// Build a bridge snap from session + optional per-pane extras.
pub fn snap_from_session(session: &ChromeSession, extras: &[PaneSnapExtra]) -> ChromeSnapOut {
    let extra = |pane_id: u64| -> PaneSnapExtra {
        extras
            .iter()
            .find(|e| e.pane_id == pane_id)
            .cloned()
            .unwrap_or(PaneSnapExtra {
                pane_id,
                alive: true,
                ..Default::default()
            })
    };

    let active_idx = session
        .tabs
        .iter()
        .position(|t| t.id == session.active_id)
        .unwrap_or(0) as i32;

    let mut tabs = Vec::with_capacity(session.tabs.len());
    for (i, tab) in session.tabs.iter().enumerate() {
        let pane_id = tab.focus_pane;
        let ex = extra(pane_id);
        let pane = session.panes.get(&pane_id);
        let grid = pane.map(|p| &p.grid);
        let title = {
            let t = tab.title.trim();
            if t.is_empty() {
                pane
                    .map(|p| {
                        let pt = p.title.trim();
                        if pt.is_empty() {
                            format!("shell {}", pane_id)
                        } else {
                            pt.to_string()
                        }
                    })
                    .unwrap_or_else(|| format!("tab {i}"))
            } else {
                t.to_string()
            }
        };
        let input = pane.map(|p| p.draft.clone()).unwrap_or_default();
        let (live_lines, viewport) = match grid {
            Some(g) => (live_lines_of(g), viewport_lines_of(g)),
            None => (Vec::new(), Vec::new()),
        };
        let history = if !ex.history.is_empty() {
            ex.history.clone()
        } else {
            match grid {
                Some(g) => history_tail_of(g, HISTORY_TAIL),
                None => Vec::new(),
            }
        };
        tabs.push(TabSnapOut {
            id: i as i32,
            title,
            alive: ex.alive,
            shell: ex.shell,
            input,
            alt_screen: ex.alt_screen,
            echo_armed: ex.echo_armed,
            echo_cmd: ex.echo_cmd,
            echo_phase: ex.echo_phase,
            live_lines,
            viewport,
            history,
            blocks: ex.blocks,
            pty_tail: ex.pty_tail,
        });
    }

    let (cols, rows) = {
        let g = session.active_grid();
        (g.cols() as i32, g.rows() as i32)
    };

    let mut notes = vec![
        "ui=chrome".into(),
        "bridge=chrome_status.json".into(),
        format!("version={}", env!("CARGO_PKG_VERSION")),
    ];
    if let Some(t) = tabs.iter().find(|t| t.id == active_idx) {
        if t.echo_armed {
            notes.push(format!("echo filter armed for: {}", t.echo_cmd));
        }
        for b in t.blocks.iter().rev().take(3) {
            notes.push(format!("recent block: {b}"));
        }
    }

    ChromeSnapOut {
        pid: std::process::id(),
        version: env!("CARGO_PKG_VERSION").into(),
        cols,
        rows,
        active_tab: active_idx,
        tabs,
        notes,
    }
}

/// Live viewport rows (offset 0), trailing spaces trimmed.
pub fn live_lines_of(grid: &CellGrid) -> Vec<String> {
    let rows = grid.rows();
    let mut out = Vec::with_capacity(rows as usize);
    for r in 0..rows {
        let abs = grid.scrollback_len() + r as usize;
        let line = grid.line_text_abs(abs);
        out.push(trim_right_spaces(&line));
    }
    // Drop trailing blank lines for agent readability (product trimLiveLines keeps all;
    // agents prefer compact — keep full height but trim each line).
    out
}

/// What the user currently sees (pin-aware stick-bottom + view_offset).
pub fn viewport_lines_of(grid: &CellGrid) -> Vec<String> {
    let rows = grid.rows();
    let mut out = Vec::with_capacity(rows as usize);
    for r in 0..rows {
        let cells = grid.visible_row_cells(r);
        let line: String = cells.iter().map(|c| c.ch).collect();
        out.push(trim_right_spaces(&line));
    }
    out
}

/// Oldest→newest scrollback tail as (text, kind=normal).
pub fn history_tail_of(grid: &CellGrid, max: usize) -> Vec<(String, String)> {
    let n = grid.scrollback_len();
    if n == 0 || max == 0 {
        return Vec::new();
    }
    let start = n.saturating_sub(max);
    let mut out = Vec::with_capacity(n - start);
    for abs in start..n {
        let line = grid.line_text_abs(abs);
        let t = trim_right_spaces(&line);
        if !t.is_empty() {
            out.push((t, "normal".into()));
        }
    }
    out
}

fn trim_right_spaces(s: &str) -> String {
    s.trim_end_matches([' ', '\t']).to_string()
}

/// Best-effort atomic write of a rich snapshot.
pub fn publish_status(session: &ChromeSession) -> Result<(), String> {
    write_snap_at(&status_path(), &snap_from_session(session, &[]))
}

fn write_snap_at(path: &Path, snap: &ChromeSnapOut) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    let body = encode_snap(snap);
    let tmp = path.with_extension("json.tmp");
    fs::write(&tmp, body.as_bytes()).map_err(|e| e.to_string())?;
    if let Err(e) = fs::rename(&tmp, path) {
        let _ = fs::remove_file(path);
        fs::rename(&tmp, path)
            .map_err(|_| e)
            .map_err(|e| e.to_string())?;
    }
    Ok(())
}

fn encode_snap(snap: &ChromeSnapOut) -> String {
    let mut s = String::with_capacity(4096);
    s.push('{');
    s.push_str(&format!("\"pid\":{},", snap.pid));
    s.push_str(&format!("\"version\":{},", json_str(&snap.version)));
    s.push_str(&format!("\"cols\":{},", snap.cols));
    s.push_str(&format!("\"rows\":{},", snap.rows));
    s.push_str(&format!("\"active_tab\":{},", snap.active_tab));
    s.push_str("\"tabs\":[");
    for (i, t) in snap.tabs.iter().enumerate() {
        if i > 0 {
            s.push(',');
        }
        encode_tab(&mut s, t);
    }
    s.push_str("],\"notes\":[");
    for (i, n) in snap.notes.iter().enumerate() {
        if i > 0 {
            s.push(',');
        }
        s.push_str(&json_str(n));
    }
    s.push_str("]}");
    s
}

fn encode_tab(s: &mut String, t: &TabSnapOut) {
    s.push('{');
    s.push_str(&format!("\"id\":{},", t.id));
    s.push_str(&format!("\"title\":{},", json_str(&t.title)));
    s.push_str(&format!("\"alive\":{},", if t.alive { "true" } else { "false" }));
    s.push_str(&format!("\"shell\":{},", json_str(&t.shell)));
    s.push_str(&format!("\"input\":{},", json_str(&t.input)));
    s.push_str(&format!(
        "\"alt_screen\":{},",
        if t.alt_screen { "true" } else { "false" }
    ));
    s.push_str(&format!(
        "\"echo\":{{\"armed\":{},\"cmd\":{},\"phase\":{}}},",
        if t.echo_armed { "true" } else { "false" },
        json_str(&t.echo_cmd),
        t.echo_phase
    ));
    s.push_str("\"live_lines\":");
    encode_str_array(s, &t.live_lines);
    s.push(',');
    s.push_str("\"viewport\":");
    encode_str_array(s, &t.viewport);
    s.push(',');
    s.push_str("\"history_tail\":[");
    for (i, (text, kind)) in t.history.iter().enumerate() {
        if i > 0 {
            s.push(',');
        }
        s.push_str(&format!(
            "{{\"text\":{},\"kind\":{}}}",
            json_str(text),
            json_str(kind)
        ));
    }
    s.push_str("],\"blocks\":[");
    for (i, b) in t.blocks.iter().enumerate() {
        if i > 0 {
            s.push(',');
        }
        s.push_str(&format!("{{\"command\":{}}}", json_str(b)));
    }
    s.push_str(&format!("],\"pty_tail\":{}", json_str(&t.pty_tail)));
    s.push('}');
}

fn encode_str_array(s: &mut String, lines: &[String]) {
    s.push('[');
    for (i, line) in lines.iter().enumerate() {
        if i > 0 {
            s.push(',');
        }
        s.push_str(&json_str(line));
    }
    s.push(']');
}

fn json_str(s: &str) -> String {
    let mut out = String::from("\"");
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if c.is_control() => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

/// Read + truncate submit mailbox. Returns the line to feed to the shell, if any.
pub fn take_submit() -> Option<String> {
    take_submit_at(&submit_path())
}

fn take_submit_at(path: &Path) -> Option<String> {
    let raw = fs::read_to_string(path).ok()?;
    let _ = fs::write(path, b"");
    let line = raw.lines().next()?.trim();
    if line.is_empty() {
        None
    } else {
        Some(line.to_string())
    }
}

/// Remove status file on clean exit (best-effort).
pub fn clear_status() {
    let _ = fs::remove_file(status_path());
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::session::ChromeSession;

    fn temp_path(name: &str) -> PathBuf {
        let n = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        std::env::temp_dir().join(format!("suzuri-status-{name}-{n}.json"))
    }

    #[test]
    fn write_status_includes_tabs_and_viewport() {
        let path = temp_path("rich");
        let mut session = ChromeSession::new(40, 8);
        // Put some text on the live grid.
        {
            let g = session.active_grid_mut();
            g.set_cursor(0, 0);
            g.put_str("hello bridge");
        }
        let snap = snap_from_session(
            &session,
            &[PaneSnapExtra {
                pane_id: 1,
                alive: true,
                shell: "zsh".into(),
                echo_armed: true,
                echo_cmd: "ls".into(),
                blocks: vec!["ls".into()],
                ..Default::default()
            }],
        );
        write_snap_at(&path, &snap).expect("write");
        let raw = fs::read_to_string(&path).unwrap();
        assert!(raw.contains("\"tabs\":["));
        assert!(raw.contains("\"viewport\":"));
        assert!(raw.contains("\"live_lines\":"));
        assert!(raw.contains("hello bridge"));
        assert!(raw.contains("\"shell\":\"zsh\""));
        assert!(raw.contains("\"armed\":true"));
        assert!(raw.contains("\"command\":\"ls\""));
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn take_submit_reads_and_clears() {
        let path = temp_path("submit");
        fs::write(&path, b"echo hello\n").unwrap();
        let line = take_submit_at(&path).expect("line");
        assert_eq!(line, "echo hello");
        let left = fs::read_to_string(&path).unwrap();
        assert!(left.trim().is_empty());
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn take_submit_missing_soft() {
        let path = temp_path("missing");
        let _ = fs::remove_file(&path);
        assert!(take_submit_at(&path).is_none());
    }

    #[test]
    fn json_escape_quotes() {
        assert_eq!(json_str(r#"a"b"#), r#""a\"b""#);
    }

    #[test]
    fn live_and_viewport_len_match_rows() {
        let session = ChromeSession::new(20, 5);
        let g = session.active_grid();
        assert_eq!(live_lines_of(g).len(), 5);
        assert_eq!(viewport_lines_of(g).len(), 5);
    }
}
