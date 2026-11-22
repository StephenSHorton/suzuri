//! A pane's working directory, so a new pane can start where the focused one
//! is (Go suzuri's cwd.go). The shell's own report (OSC 7 or Go suzuri's
//! OSC 7878 from the shell hooks, fed in through Pane::set_reported_cwd) wins;
//! without one, macOS and Linux ask the OS for the shell process's cwd.
//! Windows reads no shell folder from the OS (PowerShell's cd leaves its
//! process folder alone), so it relies on the report; while a program runs,
//! restore takes that program's folder instead ([`program_cwd`]).

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

/// Windows: the current folder of process `pid`, read from its process
/// parameters (what Process Explorer shows). Only for a program a shell
/// started (hearth#950): PowerShell's `cd` leaves its own process folder
/// where it started, but a program it runs starts in the new location. None
/// when the process cannot be read (elevated, gone) or on 32-bit builds.
#[cfg(all(windows, target_pointer_width = "64"))]
pub fn program_cwd(pid: u32) -> Option<String> {
    use std::ffi::c_void;
    use windows_sys::Win32::Foundation::{CloseHandle, HANDLE};
    use windows_sys::Win32::System::Diagnostics::Debug::ReadProcessMemory;
    use windows_sys::Win32::System::Threading::{OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION, PROCESS_VM_READ};

    #[repr(C)]
    struct BasicInfo {
        exit_status: i32,
        peb: usize,
        affinity: usize,
        priority: i32,
        pid: usize,
        parent: usize,
    }
    #[link(name = "ntdll")]
    unsafe extern "system" {
        fn NtQueryInformationProcess(h: HANDLE, class: u32, info: *mut c_void, len: u32, ret: *mut u32) -> i32;
    }
    // x64 layout: PEB.ProcessParameters at 0x20; in RTL_USER_PROCESS_PARAMETERS,
    // CurrentDirectory.DosPath (a UNICODE_STRING: length, max, buffer at +8) at 0x38.
    const PARAMS: usize = 0x20;
    const CWD: usize = 0x38;

    // SAFETY: reads into local buffers of the given sizes; the handle is closed.
    unsafe {
        let h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_VM_READ, 0, pid);
        if h.is_null() {
            return None;
        }
        let read = |at: usize, buf: *mut c_void, len: usize| ReadProcessMemory(h, at as *const c_void, buf, len, std::ptr::null_mut()) != 0;
        let got = (|| {
            let mut bi: BasicInfo = std::mem::zeroed();
            if NtQueryInformationProcess(h, 0, (&mut bi as *mut BasicInfo).cast(), std::mem::size_of::<BasicInfo>() as u32, std::ptr::null_mut()) != 0 || bi.peb == 0 {
                return None;
            }
            let mut params = 0usize;
            if !read(bi.peb + PARAMS, (&mut params as *mut usize).cast(), 8) || params == 0 {
                return None;
            }
            let mut us = [0u8; 16];
            if !read(params + CWD, us.as_mut_ptr().cast(), 16) {
                return None;
            }
            let len = u16::from_le_bytes([us[0], us[1]]) as usize / 2;
            let buf = usize::from_le_bytes(us[8..16].try_into().ok()?);
            if len == 0 || len > 32_768 || buf == 0 {
                return None;
            }
            let mut w = vec![0u16; len];
            if !read(buf, w.as_mut_ptr().cast(), len * 2) {
                return None;
            }
            let s = String::from_utf16(&w).ok()?;
            // "C:\dir\" -> "C:\dir"; a drive's root keeps its slash.
            let t = s.strip_suffix('\\').filter(|t| !t.ends_with(':')).unwrap_or(&s);
            Some(t.to_string())
        })();
        CloseHandle(h);
        got
    }
}

#[cfg(not(all(windows, target_pointer_width = "64")))]
pub fn program_cwd(_pid: u32) -> Option<String> {
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
        #[cfg(all(windows, target_pointer_width = "64"))]
        {
            let got = program_cwd(std::process::id()).unwrap();
            let want = std::env::current_dir().unwrap();
            assert_eq!(std::fs::canonicalize(got).unwrap(), std::fs::canonicalize(want).unwrap());
            assert!(program_cwd(u32::MAX - 3).is_none(), "no such process");
        }
    }

    #[test]
    fn osc7_uris() {
        assert_eq!(osc7_path("file://mac.local/Users/me/My%20Dir").as_deref(), Some("/Users/me/My Dir"));
        assert_eq!(osc7_path("file:///tmp").as_deref(), Some("/tmp"));
        assert_eq!(osc7_path("http://x/y"), None);
    }
}
