// Directory listings for the input bar's Tab completion and ghost text
// (#295, ui/complete.js). The webview can't read directories, so it asks for
// the token's directory part, as typed, with the shell's cwd: "" (the cwd),
// "sub/", "../x/", "~", "~/Doc", "/usr/", "C:\Users\".

use std::path::{Path, PathBuf};

use serde::Serialize;

/// The most entries one listing returns: a huge directory still completes,
/// from its first entries.
const MAX_ENTRIES: usize = 5000;

#[derive(Serialize, Debug, PartialEq)]
pub struct Entry {
    name: String,
    /// A directory, or a link to one.
    dir: bool,
}

/// The directory `dir` names: under home for "~", itself when absolute
/// (a Windows drive or UNC path too), else under cwd. None when it needs a
/// cwd or home that isn't known.
fn resolve(cwd: &str, home: &str, dir: &str) -> Option<PathBuf> {
    if dir == "~" || dir.starts_with("~/") || dir.starts_with("~\\") {
        if home.is_empty() {
            return None;
        }
        let rest = dir[1..].trim_start_matches(['/', '\\']);
        return Some(if rest.is_empty() { PathBuf::from(home) } else { Path::new(home).join(rest) });
    }
    if is_absolute(dir) {
        return Some(PathBuf::from(dir));
    }
    if cwd.is_empty() {
        return None;
    }
    Some(if dir.is_empty() { PathBuf::from(cwd) } else { Path::new(cwd).join(dir) })
}

fn is_absolute(dir: &str) -> bool {
    let b = dir.as_bytes();
    dir.starts_with('/') || dir.starts_with("\\\\") || (b.len() >= 2 && b[0].is_ascii_alphabetic() && b[1] == b':') || Path::new(dir).is_absolute()
}

/// The entries of a directory, unsorted (the UI sorts and filters); None
/// when it can't be read.
fn list(path: &Path) -> Option<Vec<Entry>> {
    let rd = std::fs::read_dir(path).ok()?;
    let mut out = Vec::new();
    for e in rd.flatten().take(MAX_ENTRIES) {
        let Some(name) = e.file_name().to_str().map(str::to_owned) else { continue };
        // file_type doesn't follow links; metadata does (a link to a directory
        // completes with the separator, as in a shell).
        let dir = match e.file_type() {
            Ok(t) if t.is_symlink() => std::fs::metadata(e.path()).is_ok_and(|m| m.is_dir()),
            Ok(t) => t.is_dir(),
            Err(_) => false,
        };
        out.push(Entry { name, dir });
    }
    Some(out)
}

fn home() -> String {
    std::env::var("HOME").or_else(|_| std::env::var("USERPROFILE")).unwrap_or_default()
}

/// The entries of the directory a completion token names (see the top of
/// this file); None when it can't be read. Off the main thread: a slow disk
/// or network share never stalls the window.
#[tauri::command(async)]
pub fn complete_list(cwd: String, dir: String) -> Option<Vec<Entry>> {
    list(&resolve(&cwd, &home(), &dir)?)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn resolves_relative_home_and_absolute() {
        let cwd = if cfg!(windows) { r"C:\work" } else { "/work" };
        let home = if cfg!(windows) { r"C:\Users\u" } else { "/home/u" };
        assert_eq!(resolve(cwd, home, ""), Some(PathBuf::from(cwd)));
        assert_eq!(resolve(cwd, home, "src/"), Some(Path::new(cwd).join("src/")));
        assert_eq!(resolve(cwd, home, "~"), Some(PathBuf::from(home)));
        assert_eq!(resolve(cwd, home, "~/Doc"), Some(Path::new(home).join("Doc")));
        assert_eq!(resolve(cwd, home, "~\\Doc"), Some(Path::new(home).join("Doc")));
        assert_eq!(resolve(cwd, home, "/usr/"), Some(PathBuf::from("/usr/")));
        assert_eq!(resolve(cwd, home, "C:\\"), Some(PathBuf::from("C:\\")));
        assert_eq!(resolve(cwd, home, "\\\\srv\\share\\"), Some(PathBuf::from("\\\\srv\\share\\")));
        assert_eq!(resolve("", home, "src/"), None, "relative needs a cwd");
        assert_eq!(resolve(cwd, "", "~"), None, "~ needs a home");
    }

    #[test]
    fn lists_names_and_directories() {
        let root = std::env::temp_dir().join(format!("rsz-complete-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("Documents")).unwrap();
        std::fs::write(root.join("readme.txt"), "x").unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(root.join("Documents"), root.join("docs")).unwrap();
        let mut got = complete_list(root.to_string_lossy().into_owned(), String::new()).unwrap();
        got.sort_by(|a, b| a.name.cmp(&b.name));
        let mut want = vec![Entry { name: "Documents".into(), dir: true }, Entry { name: "readme.txt".into(), dir: false }];
        #[cfg(unix)]
        want.push(Entry { name: "docs".into(), dir: true });
        want.sort_by(|a, b| a.name.cmp(&b.name));
        assert_eq!(got, want);
        assert_eq!(complete_list(root.to_string_lossy().into_owned(), "nope/".into()), None);
        std::fs::remove_dir_all(&root).unwrap();
    }
}
