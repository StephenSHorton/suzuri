//! A small MCP (JSON-RPC over stdio) client for Go suzuri's `suzuri mcp`.
//!
//! rust-suzuri's workspace panel reads and writes the shared workspace through
//! the same Go store the agents use, so the files never drift between two
//! implementations. One long-lived child; calls are serialized by id.

use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::Arc;
use std::sync::mpsc::{self, Sender};
use std::thread;
use std::time::Duration;

use parking_lot::Mutex;
use serde_json::{Value, json};

type Pending = Arc<Mutex<HashMap<u64, Sender<Value>>>>;

struct Proc {
    child: Child,
    stdin: ChildStdin,
    pending: Pending,
    next: u64,
}

impl Drop for Proc {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

#[derive(Default)]
pub struct McpClient {
    proc: Mutex<Option<Proc>>,
}

/// Go suzuri's binary (`suzuri`), which serves `suzuri mcp`.
pub fn find_suzuri() -> Option<std::path::PathBuf> {
    crate::sidecar::find("SUZURI_BIN", &["suzuri"])
}

impl McpClient {
    fn spawn() -> Result<Proc, String> {
        let bin = find_suzuri().ok_or("Go suzuri was not found (it serves the workspace). Install it or set SUZURI_BIN.")?;
        let mut cmd = Command::new(&bin);
        crate::sidecar::no_window(&mut cmd);
        let mut child = cmd
            .arg("mcp")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .map_err(|e| format!("start {} mcp: {e}", bin.display()))?;
        let stdin = child.stdin.take().ok_or("no stdin")?;
        let stdout = child.stdout.take().ok_or("no stdout")?;
        let pending: Pending = Arc::default();
        let p2 = Arc::clone(&pending);
        thread::Builder::new()
            .name("suzuri-mcp".into())
            .spawn(move || {
                for line in BufReader::new(stdout).lines() {
                    let Ok(line) = line else { break };
                    let Ok(v) = serde_json::from_str::<Value>(&line) else { continue };
                    // Responses carry our numeric id; server notifications are ignored.
                    if let Some(id) = v.get("id").and_then(Value::as_u64) {
                        if let Some(tx) = p2.lock().remove(&id) {
                            let _ = tx.send(v);
                        }
                    }
                }
                // Child gone: fail anyone still waiting.
                p2.lock().clear();
            })
            .map_err(|e| e.to_string())?;
        let mut p = Proc { child, stdin, pending, next: 0 };
        p.request(
            "initialize",
            json!({
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": { "name": "rust-suzuri", "version": env!("CARGO_PKG_VERSION") }
            }),
        )?;
        p.notify("notifications/initialized")?;
        Ok(p)
    }

    /// Call one tool and return its result. Text content that is JSON comes
    /// back parsed; anything else as a string.
    pub fn call(&self, tool: &str, args: Value) -> Result<Value, String> {
        let mut guard = self.proc.lock();
        for attempt in 0..2 {
            if guard.is_none() {
                *guard = Some(Self::spawn()?);
            }
            let p = guard.as_mut().unwrap();
            match p.request("tools/call", json!({ "name": tool, "arguments": args })) {
                Ok(res) => return Ok(tool_result(res)),
                Err(e) if attempt == 0 => {
                    // The child may have died: restart once.
                    eprintln!("suzuri mcp: {e}; restarting");
                    *guard = None;
                }
                Err(e) => return Err(e),
            }
        }
        unreachable!()
    }
}

impl Proc {
    fn send(&mut self, msg: &Value) -> Result<(), String> {
        let mut line = serde_json::to_vec(msg).map_err(|e| e.to_string())?;
        line.push(b'\n');
        self.stdin.write_all(&line).and_then(|_| self.stdin.flush()).map_err(|e| format!("write to suzuri mcp: {e}"))
    }

    fn notify(&mut self, method: &str) -> Result<(), String> {
        self.send(&json!({ "jsonrpc": "2.0", "method": method }))
    }

    fn request(&mut self, method: &str, params: Value) -> Result<Value, String> {
        self.next += 1;
        let id = self.next;
        let (tx, rx) = mpsc::channel();
        self.pending.lock().insert(id, tx);
        self.send(&json!({ "jsonrpc": "2.0", "id": id, "method": method, "params": params }))?;
        let v = rx.recv_timeout(Duration::from_secs(15)).map_err(|_| format!("suzuri mcp: no answer to {method}"))?;
        if let Some(err) = v.get("error") {
            return Err(format!("suzuri mcp {method}: {err}"));
        }
        Ok(v.get("result").cloned().unwrap_or(Value::Null))
    }
}

fn tool_result(res: Value) -> Value {
    let text = res
        .get("content")
        .and_then(Value::as_array)
        .and_then(|c| c.iter().find_map(|x| x.get("text").and_then(Value::as_str)))
        .unwrap_or("");
    serde_json::from_str(text).unwrap_or_else(|_| Value::String(text.to_string()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tool_result_parses_json_text() {
        let r = json!({ "content": [{ "type": "text", "text": "{\"ok\":true,\"n\":2}" }] });
        assert_eq!(tool_result(r), json!({ "ok": true, "n": 2 }));
        let r = json!({ "content": [{ "type": "text", "text": "plain" }] });
        assert_eq!(tool_result(r), json!("plain"));
    }
}
