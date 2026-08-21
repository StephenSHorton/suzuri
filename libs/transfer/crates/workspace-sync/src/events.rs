//! NDJSON machine-mode events for chrome / `suzuri workspace-sync --json`.
//!
//! When `json` is false, listen still prints the raw ticket on stdout (CLI).
//! When `json` is true, stdout is one JSON object per line; human text stays
//! on stderr.

use std::io::{self, Write};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use serde_json::{json, Value};

static STDOUT: Mutex<()> = Mutex::new(());

fn emit(obj: &Value) {
    let _g = STDOUT.lock().unwrap_or_else(|e| e.into_inner());
    let mut out = io::stdout().lock();
    let _ = writeln!(out, "{obj}");
    let _ = out.flush();
}

/// Host-facing event builder (also used by tests).
pub fn ready_event(ticket: &str, role: &str) -> Value {
    json!({"v":1,"event":"ready","ticket":ticket,"role":role})
}

pub fn connecting_event() -> Value {
    json!({"v":1,"event":"connecting"})
}

pub fn connected_event(peers: u64) -> Value {
    json!({"v":1,"event":"connected","peers":peers})
}

pub fn peer_left_event(peers: u64) -> Value {
    json!({"v":1,"event":"peer_left","peers":peers})
}

pub fn error_event(message: &str) -> Value {
    json!({"v":1,"event":"error","code":"generic","message":message})
}

pub fn stopped_event() -> Value {
    json!({"v":1,"event":"stopped"})
}

/// Opt-in reporter. `silent()` never writes stdout (in-process tests).
#[derive(Clone)]
pub struct Reporter {
    json: bool,
    peers: Arc<AtomicUsize>,
}

impl Reporter {
    pub fn new(json: bool) -> Self {
        Self {
            json,
            peers: Arc::new(AtomicUsize::new(0)),
        }
    }

    pub fn silent() -> Self {
        Self::new(false)
    }

    pub fn is_json(&self) -> bool {
        self.json
    }

    pub fn peers(&self) -> usize {
        self.peers.load(Ordering::SeqCst)
    }

    pub fn ready(&self, ticket: &str, role: &str) {
        if self.json {
            emit(&ready_event(ticket, role));
        } else if role == "listen" {
            println!("{ticket}");
            let _ = io::stdout().flush();
        }
    }

    pub fn connecting(&self) {
        if self.json {
            emit(&connecting_event());
        }
    }

    pub fn peer_up(&self) -> usize {
        let n = self.peers.fetch_add(1, Ordering::SeqCst) + 1;
        if self.json {
            emit(&connected_event(n as u64));
        }
        n
    }

    pub fn peer_down(&self) -> usize {
        let n = loop {
            let cur = self.peers.load(Ordering::SeqCst);
            if cur == 0 {
                break 0;
            }
            if self
                .peers
                .compare_exchange(cur, cur - 1, Ordering::SeqCst, Ordering::SeqCst)
                .is_ok()
            {
                break cur - 1;
            }
        };
        if self.json {
            emit(&peer_left_event(n as u64));
        }
        n
    }

    pub fn error(&self, message: &str) {
        if self.json {
            emit(&error_event(message));
        }
    }

    pub fn stopped(&self) {
        if self.json {
            emit(&stopped_event());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ready_event_nests_ticket_as_string() {
        let ticket = r#"{"id":"abc","addrs":[]}"#;
        let v = ready_event(ticket, "listen");
        assert_eq!(v["event"], "ready");
        assert_eq!(v["role"], "listen");
        assert_eq!(v["ticket"], ticket);
        assert_eq!(v["v"], 1);
    }

    #[test]
    fn connected_tracks_peer_count() {
        let v = connected_event(2);
        assert_eq!(v["event"], "connected");
        assert_eq!(v["peers"], 2);
    }
}
