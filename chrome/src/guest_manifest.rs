//! Guest manifests under `{config}/guests/*.json`.
//!
//! Missing directory or a file that does not resolve to a binary is a soft
//! no-op — the app still launches. Unknown capability strings are ignored.

use std::fs;
use std::path::{Path, PathBuf};

use crate::config_store::product_config_dir;

/// Protocol version this chrome speaks.
pub const GUEST_PROTOCOL: u32 = 1;

/// One palette row a guest may contribute.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct GuestCommand {
    pub id: String,
    pub title: String,
    pub desc: String,
}

/// One installable guest (binary + JSON).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct GuestManifest {
    pub id: String,
    pub name: String,
    pub command: PathBuf,
    pub protocol: u32,
    pub capabilities: Vec<String>,
    pub args: Vec<String>,
    pub commands: Vec<GuestCommand>,
    /// First location after spawn. Empty → guest default (Ladybird: Google).
    pub home: String,
    pub path: PathBuf,
}

impl GuestManifest {
    pub fn accepts_navigate(&self) -> bool {
        self.capabilities.iter().any(|c| c == "navigate")
    }

    /// URL to load when the pane opens. Ladybird defaults to Google.
    pub fn home_url(&self) -> Option<&str> {
        let home = self.home.trim();
        if !home.is_empty() {
            return Some(home);
        }
        if self.id == "ladybird" {
            return Some("https://www.google.com");
        }
        None
    }
}

/// `{config}/guests`.
pub fn guests_dir() -> PathBuf {
    product_config_dir().join("guests")
}

/// Load every `*.json` whose `command` exists on disk.
///
/// Skips unreadable files, JSON that is not an object, protocol ≠ 1, and
/// commands that do not resolve to a file.
pub fn load_guests() -> Vec<GuestManifest> {
    load_guests_from(&guests_dir())
}

pub fn load_guests_from(dir: &Path) -> Vec<GuestManifest> {
    let mut out = Vec::new();
    let entries = match fs::read_dir(dir) {
        Ok(e) => e,
        Err(_) => return out,
    };
    let mut files: Vec<PathBuf> = entries
        .filter_map(|e| e.ok())
        .map(|e| e.path())
        .filter(|p| {
            p.extension()
                .and_then(|x| x.to_str())
                .is_some_and(|x| x.eq_ignore_ascii_case("json"))
        })
        .collect();
    files.sort();
    for path in files {
        match parse_manifest_file(&path) {
            Ok(Some(m)) => out.push(m),
            Ok(None) => {}
            Err(_) => {}
        }
    }
    out
}

/// Pick a guest for "New guest pane": exact id match, else the only one, else `example`.
pub fn pick_guest<'a>(
    guests: &'a [GuestManifest],
    prefer: Option<&str>,
) -> Option<&'a GuestManifest> {
    if guests.is_empty() {
        return None;
    }
    if let Some(id) = prefer {
        if let Some(g) = guests
            .iter()
            .find(|g| g.id == id || g.name.eq_ignore_ascii_case(id))
        {
            return Some(g);
        }
    }
    if guests.len() == 1 {
        return guests.first();
    }
    guests
        .iter()
        .find(|g| g.id == "example")
        .or_else(|| guests.first())
}

fn parse_manifest_file(path: &Path) -> Result<Option<GuestManifest>, String> {
    let raw = fs::read_to_string(path).map_err(|e| e.to_string())?;
    parse_manifest(&raw, path)
}

fn parse_manifest(raw: &str, path: &Path) -> Result<Option<GuestManifest>, String> {
    let v: serde_json::Value =
        serde_json::from_str(raw).map_err(|e| format!("{}: {e}", path.display()))?;
    let obj = match v.as_object() {
        Some(o) => o,
        None => return Ok(None),
    };
    let id = obj.get("id").and_then(|x| x.as_str()).unwrap_or("").trim();
    let name = obj
        .get("name")
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .trim();
    let command = obj
        .get("command")
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .trim();
    if id.is_empty() || command.is_empty() {
        return Ok(None);
    }
    let protocol = obj
        .get("protocol")
        .and_then(|x| x.as_u64())
        .unwrap_or(GUEST_PROTOCOL as u64) as u32;
    if protocol != GUEST_PROTOCOL {
        return Ok(None);
    }
    let capabilities = obj
        .get("capabilities")
        .and_then(|x| x.as_array())
        .map(|a| {
            a.iter()
                .filter_map(|x| x.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();
    let extra_args = obj
        .get("args")
        .and_then(|x| x.as_array())
        .map(|a| {
            a.iter()
                .filter_map(|x| x.as_str().map(|s| s.to_string()))
                .collect()
        })
        .unwrap_or_default();
    let commands = obj
        .get("commands")
        .and_then(|x| x.as_array())
        .map(|a| {
            a.iter()
                .filter_map(|x| {
                    let o = x.as_object()?;
                    let title = o.get("title").and_then(|v| v.as_str()).unwrap_or("").trim();
                    if title.is_empty() {
                        return None;
                    }
                    Some(GuestCommand {
                        id: o
                            .get("id")
                            .and_then(|v| v.as_str())
                            .unwrap_or("open")
                            .trim()
                            .to_string(),
                        title: title.to_string(),
                        desc: o
                            .get("desc")
                            .and_then(|v| v.as_str())
                            .unwrap_or("")
                            .trim()
                            .to_string(),
                    })
                })
                .collect()
        })
        .unwrap_or_default();
    let home = obj
        .get("home")
        .or_else(|| obj.get("url"))
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .trim()
        .to_string();
    let resolved = resolve_command(command)?;
    if !resolved.is_file() {
        return Ok(None);
    }
    let display_name = if name.is_empty() { id } else { name };
    Ok(Some(GuestManifest {
        id: id.to_string(),
        name: display_name.to_string(),
        command: resolved,
        protocol,
        capabilities,
        args: extra_args,
        commands,
        home,
        path: path.to_path_buf(),
    }))
}

fn resolve_command(command: &str) -> Result<PathBuf, String> {
    let p = PathBuf::from(command);
    if p.is_absolute() || command.contains('/') || command.contains('\\') {
        return Ok(p);
    }
    // Bare name: look on PATH.
    if let Some(found) = which(command) {
        return Ok(found);
    }
    Ok(p)
}

fn which(name: &str) -> Option<PathBuf> {
    let paths = std::env::var_os("PATH")?;
    for dir in std::env::split_paths(&paths) {
        let cand = dir.join(name);
        if cand.is_file() {
            return Some(cand);
        }
        #[cfg(windows)]
        {
            let exe = dir.join(format!("{name}.exe"));
            if exe.is_file() {
                return Some(exe);
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn write_json(dir: &Path, name: &str, body: &str) -> PathBuf {
        let p = dir.join(name);
        let mut f = fs::File::create(&p).unwrap();
        f.write_all(body.as_bytes()).unwrap();
        p
    }

    #[test]
    fn missing_dir_is_empty() {
        let dir = std::env::temp_dir().join(format!("suzuri-guest-missing-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        assert!(load_guests_from(&dir).is_empty());
    }

    #[test]
    fn skips_missing_binary() {
        let dir = std::env::temp_dir().join(format!("suzuri-guest-skip-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        write_json(
            &dir,
            "gone.json",
            r#"{"id":"gone","name":"Gone","command":"/no/such/suzuri-guest","protocol":1}"#,
        );
        assert!(load_guests_from(&dir).is_empty());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn loads_existing_binary() {
        let dir = std::env::temp_dir().join(format!("suzuri-guest-ok-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let bin = dir.join(if cfg!(windows) {
            "fake-guest.exe"
        } else {
            "fake-guest"
        });
        fs::write(&bin, b"#!/bin/sh\n").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut p = fs::metadata(&bin).unwrap().permissions();
            p.set_mode(0o755);
            fs::set_permissions(&bin, p).unwrap();
        }
        let body = serde_json::json!({
            "id": "example",
            "name": "Example",
            "command": bin,
            "protocol": 1,
            "capabilities": ["pane", "navigate"]
        })
        .to_string();
        write_json(&dir, "example.json", &body);
        let got = load_guests_from(&dir);
        assert_eq!(got.len(), 1);
        assert_eq!(got[0].id, "example");
        assert_eq!(got[0].name, "Example");
        assert!(got[0].accepts_navigate());
        assert!(got[0].commands.is_empty());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn loads_declared_commands() {
        let dir = std::env::temp_dir().join(format!("suzuri-guest-cmds-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let bin = dir.join(if cfg!(windows) {
            "fake-guest.exe"
        } else {
            "fake-guest"
        });
        fs::write(&bin, b"#!/bin/sh\n").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut p = fs::metadata(&bin).unwrap().permissions();
            p.set_mode(0o755);
            fs::set_permissions(&bin, p).unwrap();
        }
        let body = serde_json::json!({
            "id": "ladybird",
            "name": "Ladybird",
            "command": bin,
            "protocol": 1,
            "capabilities": ["pane", "navigate"],
            "commands": [{
                "id": "open",
                "title": "Open Browser Pane",
                "desc": "Ladybird · new pane"
            }]
        })
        .to_string();
        write_json(&dir, "ladybird.json", &body);
        let got = load_guests_from(&dir);
        assert_eq!(got.len(), 1);
        assert_eq!(got[0].commands.len(), 1);
        assert_eq!(got[0].commands[0].title, "Open Browser Pane");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn pick_prefers_example_when_many() {
        let a = GuestManifest {
            id: "alpha".into(),
            name: "Alpha".into(),
            command: PathBuf::from("/bin/true"),
            protocol: 1,
            capabilities: vec![],
            args: vec![],
            commands: vec![],
            home: String::new(),
            path: PathBuf::from("a.json"),
        };
        let e = GuestManifest {
            id: "example".into(),
            name: "Example".into(),
            command: PathBuf::from("/bin/true"),
            protocol: 1,
            capabilities: vec![],
            args: vec![],
            commands: vec![],
            home: String::new(),
            path: PathBuf::from("e.json"),
        };
        let list = vec![a.clone(), e.clone()];
        assert_eq!(pick_guest(&list, None).unwrap().id, "example");
        assert_eq!(pick_guest(&list, Some("alpha")).unwrap().id, "alpha");
        assert!(pick_guest(&[], None).is_none());
    }

    #[test]
    fn ladybird_home_defaults_to_google() {
        let g = GuestManifest {
            id: "ladybird".into(),
            name: "Ladybird".into(),
            command: PathBuf::from("/bin/true"),
            protocol: 1,
            capabilities: vec!["pane".into(), "navigate".into()],
            args: vec![],
            commands: vec![],
            home: String::new(),
            path: PathBuf::from("ladybird.json"),
        };
        assert_eq!(g.home_url(), Some("https://www.google.com"));
        let mut custom = g.clone();
        custom.home = "https://ladybird.org".into();
        assert_eq!(custom.home_url(), Some("https://ladybird.org"));
    }
}
