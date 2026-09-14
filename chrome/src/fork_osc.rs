//! OSC 7880 — grok-fork asks this host to split the emitting pane and resume
//! a forked session in the new leaf.
//!
//! ```text
//! ESC]7880;fork=1;resume=…;cwd=…;bin=…;prompt=…;title=…;brand=…BEL
//! ```
//!
//! Values are percent-encoded. The host never execs a raw shell line: argv is
//! built from an allowlisted binary plus `--resume <id>`.

use crate::panes::SplitAxis;
use std::path::{Component, Path};

/// Parsed OSC 7880 fork-pane payload.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ForkPaneRequest {
    pub resume: String,
    pub cwd: String,
    pub bin: String,
    pub prompt: String,
    pub title: String,
    pub brand: String,
}

/// Parse an OSC payload (`7880;…`). Incomplete / missing resume+bin → None.
pub fn parse_fork_osc_payload(payload: &[u8]) -> Option<ForkPaneRequest> {
    let s = std::str::from_utf8(payload).ok()?.trim();
    let rest = s.strip_prefix("7880;")?;
    let mut req = ForkPaneRequest::default();
    let mut forked = false;
    for part in rest.split(';') {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        if let Some((key, val)) = part.split_once('=') {
            let key = key.trim().to_ascii_lowercase();
            let val = pct_decode(val.trim());
            match key.as_str() {
                "fork" if val == "1" || val.eq_ignore_ascii_case("true") || val == "pane" => {
                    forked = true;
                }
                "resume" | "session" | "id" => req.resume = val,
                "cwd" => req.cwd = val,
                "bin" | "exe" => req.bin = val,
                "prompt" | "directive" => req.prompt = val,
                "title" => req.title = val,
                "brand" => req.brand = val,
                _ => {}
            }
        } else if part == "fork" || part == "fork-pane" {
            forked = true;
        }
    }
    if !forked || req.resume.trim().is_empty() || req.bin.trim().is_empty() {
        return None;
    }
    Some(req)
}

pub fn pct_decode(s: &str) -> String {
    let bytes = s.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' && i + 2 < bytes.len() {
            if let (Some(h), Some(l)) = (from_hex(bytes[i + 1]), from_hex(bytes[i + 2])) {
                out.push((h << 4) | l);
                i += 3;
                continue;
            }
        }
        out.push(bytes[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

fn from_hex(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}

/// Absolute path, no `..`, basename grok / grok-fork / xai-grok-pager.
pub fn allowed_fork_bin(path: &str) -> bool {
    let path = path.trim();
    if path.is_empty() {
        return false;
    }
    let p = Path::new(path);
    if !p.is_absolute() {
        return false;
    }
    if p.components().any(|c| matches!(c, Component::ParentDir)) {
        return false;
    }
    let Some(name) = p.file_stem().and_then(|s| s.to_str()) else {
        return false;
    };
    matches!(
        name.to_ascii_lowercase().as_str(),
        "grok" | "grok-fork" | "xai-grok-pager"
    )
}

/// Argv + extra env for the child grok process.
pub fn fork_launch_spec(
    req: &ForkPaneRequest,
) -> Result<(String, Vec<String>, Vec<(String, String)>), String> {
    if !allowed_fork_bin(&req.bin) {
        return Err("bin not allowlisted".into());
    }
    if req.resume.trim().is_empty() {
        return Err("missing resume id".into());
    }
    let mut args = vec!["--resume".into(), req.resume.clone()];
    let prompt = req.prompt.trim();
    if !prompt.is_empty() {
        args.push("--".into());
        args.push(prompt.to_string());
    }
    let mut env = vec![
        ("GROK_SKIP_SYNC".into(), "1".into()),
        ("GROK_SKIP_REBUILD".into(), "1".into()),
        ("GROK_DISABLE_AUTOUPDATER".into(), "1".into()),
    ];
    if req.brand.eq_ignore_ascii_case("fork") {
        env.push(("GROK_PROCESS_BRAND".into(), "fork".into()));
        env.push(("GROK_FORK".into(), "1".into()));
        env.push(("GROK_MCP_CHANNELS".into(), "1".into()));
    }
    Ok((req.bin.clone(), args, env))
}

/// Pick H vs V so the new pane keeps the larger minimum visual dimension.
pub fn choose_fork_split_dir(w: f32, h: f32) -> SplitAxis {
    let w = w.max(1.0);
    let h = h.max(1.0);
    let min_v = (w * 0.5).min(h);
    let min_h = (h * 0.5).min(w);
    if min_h > min_v {
        SplitAxis::Horizontal
    } else {
        SplitAxis::Vertical
    }
}

pub fn fork_title(req: &ForkPaneRequest) -> String {
    let t = req.title.trim();
    if t.is_empty() {
        "fork".into()
    } else {
        t.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_basic() {
        let req = parse_fork_osc_payload(b"7880;fork=1;resume=abc-123;bin=/usr/bin/grok-fork")
            .expect("parse");
        assert_eq!(req.resume, "abc-123");
        assert_eq!(req.bin, "/usr/bin/grok-fork");
    }

    #[test]
    fn parse_percent_encoded_prompt() {
        let req = parse_fork_osc_payload(
            b"7880;fork=1;resume=s;bin=/usr/bin/grok;prompt=try%20the%20async;brand=fork",
        )
        .unwrap();
        assert_eq!(req.prompt, "try the async");
        assert_eq!(req.brand, "fork");
    }

    #[test]
    fn parse_requires_resume_and_bin() {
        assert!(parse_fork_osc_payload(b"7880;fork=1;bin=/usr/bin/grok").is_none());
        assert!(parse_fork_osc_payload(b"7880;fork=1;resume=x").is_none());
    }

    #[test]
    fn allowlist() {
        assert!(allowed_fork_bin("/usr/bin/grok-fork"));
        assert!(allowed_fork_bin("/tmp/xai-grok-pager"));
        assert!(!allowed_fork_bin("grok-fork"));
        assert!(!allowed_fork_bin("/bin/zsh"));
        assert!(!allowed_fork_bin("/usr/bin/../bin/zsh"));
    }

    #[test]
    fn launch_spec_resume_and_prompt() {
        let (bin, args, env) = fork_launch_spec(&ForkPaneRequest {
            resume: "sess-1".into(),
            bin: "/usr/bin/grok-fork".into(),
            prompt: "do the thing".into(),
            brand: "fork".into(),
            ..Default::default()
        })
        .unwrap();
        assert_eq!(bin, "/usr/bin/grok-fork");
        assert_eq!(args, ["--resume", "sess-1", "--", "do the thing"]);
        assert!(env.iter().any(|(k, v)| k == "GROK_SKIP_SYNC" && v == "1"));
        assert!(env
            .iter()
            .any(|(k, v)| k == "GROK_PROCESS_BRAND" && v == "fork"));
    }

    #[test]
    fn choose_dir_wide_vertical() {
        assert_eq!(choose_fork_split_dir(1200.0, 800.0), SplitAxis::Vertical);
    }

    #[test]
    fn choose_dir_tall_horizontal() {
        assert_eq!(choose_fork_split_dir(600.0, 1000.0), SplitAxis::Horizontal);
    }

    #[test]
    fn choose_dir_tie_vertical() {
        assert_eq!(choose_fork_split_dir(800.0, 800.0), SplitAxis::Vertical);
    }
}
