//! A pane's working directory, so a new pane can start where the focused one
//! is (Go suzuri's cwd.go). The shell's own report (OSC 7 or Go suzuri's
//! OSC 7878 from the shell hooks, fed in through Pane::set_reported_cwd) wins;
//! without one, macOS and Linux ask the OS for the shell process's cwd.
//! Windows has no clean way to read another process's cwd, so it relies on
//! the report.

/// The current directory of process `pid`, if the OS will say.
#[cfg(target_os = "macos")]
pub fn process_cwd(pid: u32) -> Option<String> {
    use std::ffi::CStr;
    let mut info: libc::proc_vnodepathinfo = unsafe { std::mem::zeroed() };
    let size = std::mem::size_of::<libc::proc_vnodepathinfo>() as libc::c_int;
    let n = unsafe {
        libc::proc_pidinfo(pid as libc::c_int, libc::PROC_PIDVNODEPATHINFO, 0, (&mut info as *mut libc::proc_vnodepathinfo).cast(), size)
    };
    if n != size {
        return None;
    }
    let path = unsafe { CStr::from_ptr(info.pvi_cdir.vip_path.as_ptr().cast()) };
    let s = path.to_str().ok()?;
    (!s.is_empty()).then(|| s.to_string())
}

#[cfg(target_os = "linux")]
pub fn process_cwd(pid: u32) -> Option<String> {
    std::fs::read_link(format!("/proc/{pid}/cwd")).ok().map(|p| p.to_string_lossy().into_owned())
}

#[cfg(not(any(target_os = "macos", target_os = "linux")))]
pub fn process_cwd(_pid: u32) -> Option<String> {
    None
}

/// The path from an OSC 7 file URI (file://host/path, percent-encoded), or
/// None for anything else.
pub fn osc7_path(uri: &str) -> Option<String> {
    let rest = uri.strip_prefix("file://")?;
    let path = &rest[rest.find('/')?..];
    let bytes = path.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' && i + 2 < bytes.len() {
            if let Some(b) = std::str::from_utf8(&bytes[i + 1..i + 3]).ok().and_then(|h| u8::from_str_radix(h, 16).ok()) {
                out.push(b);
                i += 3;
                continue;
            }
        }
        out.push(bytes[i]);
        i += 1;
    }
    let s = String::from_utf8(out).ok()?;
    // file:///C:/Users/... on Windows.
    #[cfg(windows)]
    let s = if s.len() > 2 && s.as_bytes()[2] == b':' { s[1..].replace('/', "\\") } else { s };
    Some(s)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn own_process_cwd() {
        #[cfg(any(target_os = "macos", target_os = "linux"))]
        {
            let got = process_cwd(std::process::id()).unwrap();
            let want = std::env::current_dir().unwrap();
            assert_eq!(std::fs::canonicalize(got).unwrap(), std::fs::canonicalize(want).unwrap());
        }
    }

    #[test]
    fn osc7_uris() {
        assert_eq!(osc7_path("file://mac.local/Users/me/My%20Dir").as_deref(), Some("/Users/me/My Dir"));
        assert_eq!(osc7_path("file:///tmp").as_deref(), Some("/tmp"));
        assert_eq!(osc7_path("http://x/y"), None);
    }
}
