//! Publish live chrome process status for the Go MCP bridge proxy.
//!
//! The host (`internal/chromehost`) reads `{config_dir}/chrome_status.json`
//! while `suzuri chrome` runs and republishes it over loopback bridge.json so
//! `suzuri mcp` can attach without embedding the GPU loop.

use std::fs;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use crate::config_store;
use crate::session::ChromeSession;

/// Filename under the product config directory.
pub const STATUS_FILE: &str = "chrome_status.json";

/// How often we rewrite status (host polls ~500ms).
pub const PUBLISH_INTERVAL: Duration = Duration::from_millis(750);

/// Warp-submit mailbox (host writes a full line; chrome feeds active PTY).
pub const SUBMIT_FILE: &str = "chrome_submit";

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
    pub fn tick(&mut self, session: &ChromeSession) {
        let now = Instant::now();
        if now.duration_since(self.last) < PUBLISH_INTERVAL {
            return;
        }
        self.last = now;
        let _ = publish_status(session);
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

/// Best-effort atomic write of current session status.
pub fn publish_status(session: &ChromeSession) -> Result<(), String> {
    write_status_at(&status_path(), session)
}

fn write_status_at(path: &Path, session: &ChromeSession) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    let tabs = session.tabs.len();
    let active_idx = session
        .tabs
        .iter()
        .position(|t| t.id == session.active_id)
        .unwrap_or(0);
    let active_title = session
        .active_tab()
        .map(|t| {
            let title = t.title.trim();
            if title.is_empty() {
                "suzuri".into()
            } else {
                title.to_string()
            }
        })
        .unwrap_or_else(|| "suzuri".into());
    let grid = session.active_grid();
    let cols = grid.cols() as i32;
    let rows = grid.rows() as i32;
    let pid = std::process::id();
    let body = format!(
        "{{\n  \"pid\": {pid},\n  \"tabs\": {tabs},\n  \"active_tab\": {active_idx},\n  \"active_title\": {title},\n  \"cols\": {cols},\n  \"rows\": {rows},\n  \"version\": \"{ver}\"\n}}\n",
        title = json_escape(&active_title),
        ver = env!("CARGO_PKG_VERSION"),
    );
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

fn json_escape(s: &str) -> String {
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
    fn write_status_roundtrip_shape() {
        let path = temp_path("write");
        let session = ChromeSession::new(80, 24);
        write_status_at(&path, &session).expect("write");
        let raw = fs::read_to_string(&path).unwrap();
        assert!(raw.contains("\"tabs\":"));
        assert!(raw.contains("\"pid\":"));
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
        assert_eq!(json_escape(r#"a"b"#), r#""a\"b""#);
    }
}
