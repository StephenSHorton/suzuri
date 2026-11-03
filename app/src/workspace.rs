//! Workspace: the shared room for humans and agents. Served by Go suzuri's
//! own store through `suzuri mcp` (mcp_client.rs), the same path agents use,
//! so the on-disk files stay in one format.

use std::sync::Arc;

use serde_json::{Value, json};
use tauri::State;

use crate::mcp_client::McpClient;

/// Only the workspace tools; the panel never calls anything else.
fn allowed(tool: &str) -> bool {
    tool.starts_with("workspace_") && tool != "workspace_wait"
}

/// Run one `workspace_*` tool (args as the MCP tool takes them).
#[tauri::command]
pub async fn workspace_call(mcp: State<'_, Arc<McpClient>>, tool: String, args: Option<Value>) -> Result<Value, String> {
    if !allowed(&tool) {
        return Err(format!("not a workspace tool: {tool}"));
    }
    let mcp = Arc::clone(&mcp);
    tauri::async_runtime::spawn_blocking(move || mcp.call(&tool, args.unwrap_or_else(|| json!({}))))
        .await
        .map_err(|e| e.to_string())?
}

/// The local human's display name (Go suzuri: the OS user name) and a stable
/// session id, so reopening the panel rejoins as the same member.
#[tauri::command]
pub fn workspace_me() -> Value {
    let name = std::env::var("USER")
        .or_else(|_| std::env::var("USERNAME"))
        .ok()
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| "you".into());
    json!({ "name": name, "session_id": format!("rust-suzuri-human-{name}") })
}

#[cfg(test)]
mod tests {
    #[test]
    fn only_workspace_tools() {
        assert!(super::allowed("workspace_post"));
        assert!(!super::allowed("workspace_wait"));
        assert!(!super::allowed("submit"));
    }
}
