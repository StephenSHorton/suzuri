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
    let name = human_name(
        std::env::var("USER").ok(),
        std::env::var("USERNAME").ok(),
        std::env::var("USERDOMAIN").ok(),
        cfg!(windows),
    );
    // `short` lets the panel also find a member joined under the bare name.
    let short = name.rsplit('\\').next().unwrap_or(&name).to_string();
    json!({ "name": name, "short": short, "session_id": format!("rust-suzuri-human-{name}") })
}

/// Go suzuri's `localHumanName`: `user.Current().Username`, which on Windows
/// is `DOMAIN\user` (e.g. `DESKTOP-STEPHEN\4step`) and on Unix the login.
fn human_name(user: Option<String>, username: Option<String>, domain: Option<String>, windows: bool) -> String {
    let nonempty = |v: Option<String>| v.filter(|s| !s.is_empty());
    if windows {
        if let Some(u) = nonempty(username) {
            return match nonempty(domain) {
                Some(d) => format!("{d}\\{u}"),
                None => u,
            };
        }
    }
    nonempty(user).unwrap_or_else(|| "you".into())
}

#[cfg(test)]
mod tests {
    #[test]
    fn human_name_matches_go() {
        let s = |x: &str| Some(x.to_string());
        assert_eq!(super::human_name(None, s("4step"), s("DESKTOP-STEPHEN"), true), "DESKTOP-STEPHEN\\4step");
        assert_eq!(super::human_name(s("stephenhorton"), None, None, false), "stephenhorton");
        assert_eq!(super::human_name(None, None, None, false), "you");
    }

    #[test]
    fn only_workspace_tools() {
        assert!(super::allowed("workspace_post"));
        assert!(!super::allowed("workspace_wait"));
        assert!(!super::allowed("submit"));
    }
}
