//! Install / remove a catalog guest (Ladybird first).
//!
//! Writes `{config}/guests/{id}.json` and a helper tree chrome already loads.
//! Download uses `curl` when no local Ladybird.app is found.

use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::process::Command;

use crate::guest_manifest::{guests_dir, load_guests};

/// One catalog card. Chrome only ships Ladybird for now.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CatalogGuest {
    pub id: &'static str,
    pub name: &'static str,
    pub desc: &'static str,
}

pub const CATALOG: &[CatalogGuest] = &[CatalogGuest {
    id: "ladybird",
    name: "Ladybird",
    desc: "Independent web engine in a Suzuri pane",
}];

pub fn catalog() -> &'static [CatalogGuest] {
    CATALOG
}

pub fn is_installed(id: &str) -> bool {
    load_guests().iter().any(|g| g.id == id)
}

pub fn install_dir(id: &str) -> PathBuf {
    guests_dir().join(id)
}

pub fn manifest_path(id: &str) -> PathBuf {
    guests_dir().join(format!("{id}.json"))
}

/// Copy or download Ladybird and write the manifest.
pub fn install_ladybird() -> Result<PathBuf, String> {
    #[cfg(not(target_os = "macos"))]
    {
        return Err("ladybird is macOS-only for now".into());
    }
    #[cfg(target_os = "macos")]
    {
        let src = match discover_ladybird() {
            Some(p) => p,
            None => download_ladybird_zip()?,
        };
        let dst = install_dir("ladybird");
        let _ = fs::remove_dir_all(&dst);
        fs::create_dir_all(&dst).map_err(|e| e.to_string())?;
        let bin = place_helper(&src, &dst)?;
        add_rpath(&bin);
        ensure_runnable(&bin);
        write_ladybird_manifest(&bin)?;
        Ok(bin)
    }
}

pub fn remove_guest(id: &str) -> Result<(), String> {
    let id = id.trim();
    if id.is_empty() {
        return Err("missing guest id".into());
    }
    if let Some(g) = load_guests().into_iter().find(|g| g.id == id) {
        kill_by_path(&g.command);
    }
    let man = manifest_path(id);
    if man.exists() {
        fs::remove_file(&man).map_err(|e| e.to_string())?;
    }
    let dir = install_dir(id);
    if dir.exists() {
        fs::remove_dir_all(&dir).map_err(|e| e.to_string())?;
    }
    if !man.exists() && !dir.exists() && !is_installed(id) {
        // already gone
    }
    Ok(())
}

fn write_ladybird_manifest(bin: &Path) -> Result<(), String> {
    let dir = guests_dir();
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let body = format!(
        "{{\n  \"id\": \"ladybird\",\n  \"name\": \"Ladybird\",\n  \"command\": {},\n  \"args\": [\"--temporary-profile\"],\n  \"protocol\": 1,\n  \"capabilities\": [\"pane\", \"navigate\"],\n  \"commands\": [\n    {{\n      \"id\": \"open\",\n      \"title\": \"Open Browser Pane\",\n      \"desc\": \"Ladybird · new pane\"\n    }}\n  ]\n}}\n",
        serde_json::to_string(&bin.to_string_lossy()).unwrap_or_else(|_| "\"\"".into()),
    );
    let path = manifest_path("ladybird");
    let tmp = path.with_extension("json.tmp");
    fs::write(&tmp, body).map_err(|e| e.to_string())?;
    fs::rename(&tmp, &path).map_err(|e| e.to_string())?;
    Ok(())
}

fn discover_ladybird() -> Option<PathBuf> {
    if let Ok(p) = std::env::var("LADYBIRD") {
        let p = PathBuf::from(p.trim());
        if p.exists() {
            return Some(p);
        }
    }
    if let Ok(root) = std::env::var("LADYBIRD_SOURCE_DIR") {
        let p = PathBuf::from(root).join("Build/release/bin/Ladybird.app");
        if p.is_dir() {
            return Some(p);
        }
    }
    if let Some(home) = std::env::var_os("HOME") {
        let p = PathBuf::from(home).join("projects/ladybird/Build/release/bin/Ladybird.app");
        if p.is_dir() {
            return Some(p);
        }
    }
    None
}

#[cfg(target_os = "macos")]
fn download_ladybird_zip() -> Result<PathBuf, String> {
    let url = latest_asset_url()?;
    let dir = std::env::temp_dir().join(format!("suzuri-guest-dl-{}", std::process::id()));
    let _ = fs::create_dir_all(&dir);
    let zip = dir.join("suzuri-ladybird.zip");
    let status = Command::new("curl")
        .args(["-fsSL", "-A", "suzuri-chrome", "-o"])
        .arg(&zip)
        .arg(&url)
        .status()
        .map_err(|e| format!("curl: {e}"))?;
    if !status.success() {
        return Err("download failed".into());
    }
    Ok(zip)
}

#[cfg(target_os = "macos")]
fn latest_asset_url() -> Result<String, String> {
    let out = Command::new("curl")
        .args([
            "-fsSL",
            "-A",
            "suzuri-chrome",
            "https://api.github.com/repos/StephenSHorton/suzuri-ladybird/releases",
        ])
        .output()
        .map_err(|e| format!("curl: {e}"))?;
    if !out.status.success() {
        return Err("could not list helper releases".into());
    }
    let v: serde_json::Value = serde_json::from_slice(&out.stdout).map_err(|e| e.to_string())?;
    let arr = v.as_array().ok_or("no releases")?;
    let arch = if cfg!(target_arch = "aarch64") {
        "macos-arm64"
    } else {
        "macos-amd64"
    };
    let want = format!("suzuri-ladybird-{arch}.zip");
    for rel in arr {
        if rel.get("draft").and_then(|x| x.as_bool()).unwrap_or(false) {
            continue;
        }
        let assets = rel.get("assets").and_then(|x| x.as_array());
        let Some(assets) = assets else { continue };
        for a in assets {
            let name = a.get("name").and_then(|x| x.as_str()).unwrap_or("");
            if name == want {
                if let Some(url) = a.get("browser_download_url").and_then(|x| x.as_str()) {
                    return Ok(url.to_string());
                }
            }
        }
    }
    Err(format!("no {want} on GitHub releases"))
}

fn place_helper(src: &Path, dst: &Path) -> Result<PathBuf, String> {
    let s = src.to_string_lossy();
    if s.to_ascii_lowercase().ends_with(".zip") {
        let status = Command::new("unzip")
            .args(["-o", "-q"])
            .arg(src)
            .arg("-d")
            .arg(dst)
            .status()
            .map_err(|e| format!("unzip: {e}"))?;
        if !status.success() {
            return Err("unzip failed".into());
        }
        return find_ladybird_binary(dst);
    }
    let app = if src.file_name().and_then(|n| n.to_str()) == Some("Ladybird") && s.contains(".app/")
    {
        PathBuf::from(&s[..s.find(".app/").unwrap_or(0) + 4])
    } else {
        src.to_path_buf()
    };
    let name = app.file_name().unwrap_or_default();
    if name.to_string_lossy().ends_with(".app") {
        let target = dst.join("Ladybird.app");
        copy_tree(&app, &target).map_err(|e| e.to_string())?;
        return find_ladybird_binary(&target);
    }
    let target = dst.join(app.file_name().unwrap_or_default());
    fs::copy(&app, &target).map_err(|e| e.to_string())?;
    Ok(target)
}

fn find_ladybird_binary(root: &Path) -> Result<PathBuf, String> {
    let mut found = None;
    visit(root, &mut |p| {
        if p.file_name().and_then(|n| n.to_str()) != Some("Ladybird") {
            return;
        }
        let macos = p.parent();
        let contents = macos.and_then(|d| d.parent());
        if macos.and_then(|d| d.file_name()).and_then(|n| n.to_str()) == Some("MacOS")
            && contents
                .and_then(|d| d.file_name())
                .and_then(|n| n.to_str())
                == Some("Contents")
        {
            found = Some(p.to_path_buf());
        }
    });
    found.ok_or_else(|| format!("no Ladybird binary under {}", root.display()))
}

fn visit(dir: &Path, f: &mut impl FnMut(&Path)) {
    let Ok(rd) = fs::read_dir(dir) else {
        return;
    };
    for e in rd.flatten() {
        let p = e.path();
        if p.is_dir() {
            visit(&p, f);
        } else {
            f(&p);
        }
    }
}

fn copy_tree(src: &Path, dst: &Path) -> io::Result<()> {
    let meta = fs::symlink_metadata(src)?;
    #[cfg(unix)]
    if meta.file_type().is_symlink() {
        if let Some(parent) = dst.parent() {
            fs::create_dir_all(parent)?;
        }
        let _ = fs::remove_file(dst);
        return std::os::unix::fs::symlink(fs::read_link(src)?, dst);
    }
    if meta.is_dir() {
        fs::create_dir_all(dst)?;
        for e in fs::read_dir(src)? {
            let e = e?;
            let to = dst.join(e.file_name());
            copy_tree(&e.path(), &to)?;
        }
        return Ok(());
    }
    if let Some(parent) = dst.parent() {
        fs::create_dir_all(parent)?;
    }
    fs::copy(src, dst)?;
    Ok(())
}

fn add_rpath(bin: &Path) {
    let Some(dir) = bin.parent() else {
        return;
    };
    let Ok(rd) = fs::read_dir(dir) else {
        return;
    };
    for e in rd.flatten() {
        let p = e.path();
        if p.is_file() && !has_rpath(&p, "@executable_path/../lib") {
            let _ = Command::new("install_name_tool")
                .args(["-add_rpath", "@executable_path/../lib"])
                .arg(&p)
                .status();
        }
    }
}

fn has_rpath(bin: &Path, needle: &str) -> bool {
    let Ok(out) = Command::new("otool").args(["-l"]).arg(bin).output() else {
        return false;
    };
    String::from_utf8_lossy(&out.stdout).contains(needle)
}

/// `.app` that contains `bin`, if the helper lives inside a bundle.
pub fn enclosing_app(bin: &Path) -> Option<PathBuf> {
    let mut cur = bin;
    for _ in 0..6 {
        let Some(parent) = cur.parent() else {
            break;
        };
        if parent
            .file_name()
            .and_then(|n| n.to_str())
            .is_some_and(|n| n.ends_with(".app"))
        {
            return Some(parent.to_path_buf());
        }
        cur = parent;
    }
    None
}

fn codesign_ok(target: &Path) -> bool {
    Command::new("codesign")
        .args(["--verify", "--deep"])
        .arg(target)
        .status()
        .is_ok_and(|s| s.success())
}

fn adhoc_sign(target: &Path) {
    let _ = Command::new("xattr").args(["-cr"]).arg(target).status();
    let _ = Command::new("codesign")
        .args(["--force", "--deep", "--sign", "-"])
        .arg(target)
        .status();
}

/// Re-sign a mutated `.app` so macOS will exec it. Bare binaries are left alone.
///
/// `install_name_tool` (and a copy out of a zip) often leaves Ladybird with an
/// invalid page signature. Hardened-runtime Suzuri then gets SIGKILL on spawn,
/// which looks like a Suzuri crash.
pub fn ensure_runnable(command: &Path) {
    let Some(app) = enclosing_app(command) else {
        return;
    };
    if codesign_ok(&app) {
        return;
    }
    adhoc_sign(&app);
}

fn kill_by_path(bin: &Path) {
    let needle = bin.to_string_lossy();
    if needle.is_empty() {
        return;
    }
    let Ok(out) = Command::new("ps")
        .args(["-ax", "-o", "pid=,command="])
        .output()
    else {
        return;
    };
    for line in String::from_utf8_lossy(&out.stdout).lines() {
        let line = line.trim();
        if !line.contains(needle.as_ref()) {
            continue;
        }
        let Some(pid) = line
            .split_whitespace()
            .next()
            .and_then(|s| s.parse::<i32>().ok())
        else {
            continue;
        };
        if pid > 1 {
            let _ = Command::new("kill").arg(pid.to_string()).status();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn catalog_has_ladybird() {
        assert_eq!(catalog()[0].id, "ladybird");
    }

    #[test]
    fn write_and_remove_manifest() {
        let dir = std::env::temp_dir().join(format!("suzuri-guest-install-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        // product_config_dir is not overridable here — just write_ladybird_manifest
        // against guests_dir would hit the real config. Test place_helper instead.
        let src = dir
            .join("src")
            .join("Ladybird.app")
            .join("Contents")
            .join("MacOS")
            .join("Ladybird");
        fs::create_dir_all(src.parent().unwrap()).unwrap();
        fs::write(&src, b"dummy").unwrap();
        let dst = dir.join("dst");
        fs::create_dir_all(&dst).unwrap();
        let bin = place_helper(&dir.join("src").join("Ladybird.app"), &dst).unwrap();
        assert_eq!(bin.file_name().and_then(|n| n.to_str()), Some("Ladybird"));
        assert!(bin.is_file());
        assert_eq!(
            enclosing_app(&bin).as_deref(),
            Some(dst.join("Ladybird.app").as_path())
        );
        let _ = fs::remove_dir_all(&dir);
    }
}
