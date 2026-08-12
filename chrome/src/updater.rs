//! GitHub-Releases update mailbox (chrome ↔ Go host).
//!
//! The host owns `internal/update` (check / download / replace / relaunch).
//! Chrome only:
//! - writes `{config}/update_req` (`check` / `install` / `later`)
//! - polls `{config}/update_evt` (`toast …` / `offer <version>`)
//!
//! Missing files are a soft no-op so a lone `suzuri-chrome` still runs.

use std::fs;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use crate::config_store;

/// Chrome → host request file.
pub const UPDATE_REQ_FILE: &str = "update_req";
/// Host → chrome event file.
pub const UPDATE_EVT_FILE: &str = "update_evt";

const POLL_INTERVAL: Duration = Duration::from_millis(250);

/// One event from the host updater.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum UpdateEvent {
    Toast(String),
    Offer { version: String },
}

/// Parse a single host event line.
pub fn parse_event(line: &str) -> Option<UpdateEvent> {
    let line = line.trim();
    if line.is_empty() {
        return None;
    }
    if let Some(rest) = line.strip_prefix("toast ") {
        let msg = rest.trim();
        if msg.is_empty() {
            return None;
        }
        return Some(UpdateEvent::Toast(msg.to_string()));
    }
    if let Some(rest) = line.strip_prefix("offer ") {
        let ver = rest.trim().trim_start_matches('v');
        if ver.is_empty() {
            return None;
        }
        return Some(UpdateEvent::Offer {
            version: ver.to_string(),
        });
    }
    None
}

fn mailbox_dir() -> PathBuf {
    if let Ok(p) = std::env::var("SUZURI_CONFIG_DIR") {
        let t = p.trim();
        if !t.is_empty() {
            return PathBuf::from(t);
        }
    }
    config_store::product_config_dir()
}

pub fn update_req_path() -> PathBuf {
    mailbox_dir().join(UPDATE_REQ_FILE)
}

pub fn update_evt_path() -> PathBuf {
    mailbox_dir().join(UPDATE_EVT_FILE)
}

fn write_req_at(path: &Path, verb: &str) {
    if let Some(dir) = path.parent() {
        let _ = fs::create_dir_all(dir);
    }
    let tmp = path.with_extension("req.tmp");
    if fs::write(&tmp, format!("{verb}\n")).is_ok() {
        if fs::rename(&tmp, path).is_err() {
            let _ = fs::remove_file(path);
            let _ = fs::rename(&tmp, path);
        }
    }
}

fn take_evt_at(path: &Path) -> Vec<UpdateEvent> {
    let Ok(body) = fs::read_to_string(path) else {
        return Vec::new();
    };
    let _ = fs::write(path, "");
    body.lines().filter_map(parse_event).collect()
}

/// Rate-limited poller + request writer.
#[derive(Debug)]
pub struct UpdateMailbox {
    req_path: PathBuf,
    evt_path: PathBuf,
    last_poll: Instant,
}

impl Default for UpdateMailbox {
    fn default() -> Self {
        Self::new()
    }
}

impl UpdateMailbox {
    pub fn new() -> Self {
        Self::with_paths(update_req_path(), update_evt_path())
    }

    pub fn with_paths(req_path: PathBuf, evt_path: PathBuf) -> Self {
        Self {
            req_path,
            evt_path,
            last_poll: Instant::now()
                .checked_sub(POLL_INTERVAL)
                .unwrap_or_else(Instant::now),
        }
    }

    pub fn request_check(&self) {
        write_req_at(&self.req_path, "check");
    }

    pub fn request_install(&self) {
        write_req_at(&self.req_path, "install");
    }

    pub fn request_later(&self) {
        write_req_at(&self.req_path, "later");
    }

    /// Drain host events if the poll interval has elapsed.
    pub fn poll(&mut self) -> Vec<UpdateEvent> {
        let now = Instant::now();
        if now.duration_since(self.last_poll) < POLL_INTERVAL {
            return Vec::new();
        }
        self.last_poll = now;
        take_evt_at(&self.evt_path)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_toast_and_offer() {
        assert_eq!(
            parse_event("toast checking for updates…"),
            Some(UpdateEvent::Toast("checking for updates…".into()))
        );
        assert_eq!(
            parse_event("offer v1.2.3"),
            Some(UpdateEvent::Offer {
                version: "1.2.3".into()
            })
        );
        assert_eq!(
            parse_event("offer 1.2.3"),
            Some(UpdateEvent::Offer {
                version: "1.2.3".into()
            })
        );
        assert_eq!(parse_event("toast "), None);
        assert_eq!(parse_event("offer "), None);
        assert_eq!(parse_event("nope"), None);
        assert_eq!(parse_event(""), None);
    }

    #[test]
    fn mailbox_round_trip() {
        let dir = std::env::temp_dir().join(format!(
            "suzuri-upd-test-{}",
            std::process::id()
        ));
        let _ = fs::create_dir_all(&dir);
        let req = dir.join("update_req");
        let evt = dir.join("update_evt");
        let box_ = UpdateMailbox::with_paths(req.clone(), evt.clone());
        box_.request_check();
        let body = fs::read_to_string(&req).unwrap();
        assert_eq!(body.trim(), "check");
        box_.request_install();
        assert_eq!(fs::read_to_string(&req).unwrap().trim(), "install");
        box_.request_later();
        assert_eq!(fs::read_to_string(&req).unwrap().trim(), "later");

        fs::write(&evt, "toast up to date (v1.0.0)\noffer 1.1.0\n").unwrap();
        let mut box_ = UpdateMailbox::with_paths(req, evt.clone());
        let evs = take_evt_at(&evt);
        assert_eq!(
            evs,
            vec![
                UpdateEvent::Toast("up to date (v1.0.0)".into()),
                UpdateEvent::Offer {
                    version: "1.1.0".into()
                },
            ]
        );
        assert_eq!(fs::read_to_string(&evt).unwrap(), "");
        let _ = fs::remove_dir_all(&dir);
        let _ = box_.poll();
    }
}
