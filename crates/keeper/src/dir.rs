//! The keeper's folder: one socket per kept shell.
use std::io;
use std::path::{Path, PathBuf};

/// `$RSZ_KEEPER_DIR`, else `$RSZ_CONFIG_DIR/keeper`, else suzuri's config
/// folder (the one bridge.json is in) + `keeper`.
pub fn default_dir() -> PathBuf {
    if let Some(d) = std::env::var_os("RSZ_KEEPER_DIR").filter(|d| !d.is_empty()) {
        return PathBuf::from(d);
    }
    if let Some(d) = std::env::var_os("RSZ_CONFIG_DIR").filter(|d| !d.is_empty()) {
        return PathBuf::from(d).join("keeper");
    }
    config_dir().join("keeper")
}

/// Go suzuri's config.Dir (app/src/sidecar.rs): `%LOCALAPPDATA%\suzuri`,
/// else macOS `~/Library/Application Support/suzuri`, else XDG.
fn config_dir() -> PathBuf {
    if let Some(local) = std::env::var_os("LOCALAPPDATA").filter(|v| !v.is_empty()) {
        return PathBuf::from(local).join("suzuri");
    }
    let home = std::env::var_os("HOME").map(PathBuf::from);
    if cfg!(target_os = "macos") {
        if let Some(h) = home {
            return h.join("Library/Application Support/suzuri");
        }
    } else {
        if let Some(x) = std::env::var_os("XDG_CONFIG_HOME").filter(|v| !v.is_empty()) {
            return PathBuf::from(x).join("suzuri");
        }
        if let Some(h) = home {
            return h.join(".config/suzuri");
        }
    }
    std::env::temp_dir().join("suzuri")
}

pub fn sock_path(dir: &Path, id: u32) -> PathBuf {
    dir.join(format!("{id}.sock"))
}

/// Whether shell `id` has a socket in the folder (live or stale). Not
/// `Path::exists`: on Windows that follows the socket's reparse point and
/// says no.
pub fn has(dir: &Path, id: u32) -> bool {
    std::fs::symlink_metadata(sock_path(dir, id)).is_ok()
}

/// The ids with a socket in the folder, lowest first (live or stale: a
/// connect tells).
pub fn list(dir: &Path) -> Vec<u32> {
    let mut ids: Vec<u32> = std::fs::read_dir(dir)
        .into_iter()
        .flatten()
        .flatten()
        .filter_map(|e| e.file_name().to_str()?.strip_suffix(".sock")?.parse().ok())
        .collect();
    ids.sort_unstable();
    ids
}

/// Make the folder (0700) and check that it is this user's and nobody
/// else's (Windows: it is in the user's own LOCALAPPDATA, and each end
/// checks the other's user). A socket path must fit in sun_path (104 bytes
/// on macOS, 108 on Windows).
pub fn ensure(dir: &Path) -> io::Result<()> {
    std::fs::create_dir_all(dir)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        let md = std::fs::symlink_metadata(dir)?;
        // SAFETY: getuid has no failure mode.
        let me = unsafe { libc::getuid() };
        if !md.is_dir() || md.uid() != me {
            return Err(io::Error::new(io::ErrorKind::PermissionDenied, format!("keeper: {} is not a folder of this user's", dir.display())));
        }
        if md.mode() & 0o077 != 0 {
            std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700))?;
        }
    }
    let longest = sock_path(dir, u32::MAX);
    if longest.as_os_str().len() >= 104 {
        return Err(io::Error::new(io::ErrorKind::InvalidInput, format!("keeper: socket path too long: {}", longest.display())));
    }
    Ok(())
}

/// The user id at the other end of a Unix socket.
#[cfg(unix)]
pub fn peer_uid(s: &std::os::unix::net::UnixStream) -> Option<u32> {
    use std::os::fd::AsRawFd;
    let fd = s.as_raw_fd();
    #[cfg(any(target_os = "macos", target_os = "freebsd", target_os = "openbsd", target_os = "netbsd"))]
    {
        let (mut uid, mut gid) = (0, 0);
        // SAFETY: fd is an open socket; the out pointers are valid.
        let r = unsafe { libc::getpeereid(fd, &mut uid, &mut gid) };
        (r == 0).then_some(uid)
    }
    #[cfg(target_os = "linux")]
    {
        let mut cred: libc::ucred = unsafe { std::mem::zeroed() };
        let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
        // SAFETY: fd is an open socket; cred and len are valid for writing.
        let r = unsafe { libc::getsockopt(fd, libc::SOL_SOCKET, libc::SO_PEERCRED, &mut cred as *mut _ as *mut libc::c_void, &mut len) };
        (r == 0).then_some(cred.uid)
    }
}

/// Whether the other end is this same user.
#[cfg(unix)]
pub fn peer_is_me(s: &std::os::unix::net::UnixStream) -> bool {
    // SAFETY: getuid has no failure mode.
    peer_uid(s) == Some(unsafe { libc::getuid() })
}

/// Whether the other end is this same user: the process at the other end
/// of the socket (SIO_AF_UNIX_GETPEERPID) runs as this process's user.
#[cfg(windows)]
pub fn peer_is_me(s: &crate::sock::UnixStream) -> bool {
    use std::os::windows::io::AsRawSocket;
    use windows_sys::Win32::Networking::WinSock::{SIO_AF_UNIX_GETPEERPID, WSAIoctl};
    let mut pid: u32 = 0;
    let mut got: u32 = 0;
    // SAFETY: an open socket; the out buffer is a u32 and its size is given.
    let r = unsafe {
        WSAIoctl(s.as_raw_socket() as usize, SIO_AF_UNIX_GETPEERPID, std::ptr::null(), 0, (&mut pid as *mut u32).cast(), 4, &mut got, std::ptr::null_mut(), None)
    };
    // It fills pid but may leave the byte count at 0.
    r == 0 && pid != 0 && win::same_user(pid)
}

#[cfg(windows)]
mod win {
    use windows_sys::Win32::Foundation::{CloseHandle, HANDLE};
    use windows_sys::Win32::Security::{EqualSid, GetTokenInformation, TOKEN_QUERY, TOKEN_USER, TokenUser};
    use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcess, OpenProcessToken, PROCESS_QUERY_LIMITED_INFORMATION};

    /// A process's token user (the SID bytes, in a TOKEN_USER buffer).
    fn token_user(process: HANDLE) -> Option<Vec<u64>> {
        let mut token: HANDLE = std::ptr::null_mut();
        // SAFETY: a process handle with query rights; token is written on success.
        if unsafe { OpenProcessToken(process, TOKEN_QUERY, &mut token) } == 0 {
            return None;
        }
        let mut buf = vec![0u64; 64];
        let mut len = 0u32;
        // SAFETY: buf is 512 bytes, 8-aligned, and its size is given.
        let ok = unsafe { GetTokenInformation(token, TokenUser, buf.as_mut_ptr().cast(), (buf.len() * 8) as u32, &mut len) };
        // SAFETY: token came from OpenProcessToken.
        unsafe { CloseHandle(token) };
        (ok != 0).then_some(buf)
    }

    pub fn same_user(pid: u32) -> bool {
        // SAFETY: OpenProcess has no preconditions; a null handle is checked.
        let h = unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid) };
        if h.is_null() {
            return false;
        }
        let theirs = token_user(h);
        // SAFETY: h came from OpenProcess.
        unsafe { CloseHandle(h) };
        // SAFETY: the pseudo handle needs no closing.
        let mine = token_user(unsafe { GetCurrentProcess() });
        match (theirs, mine) {
            (Some(a), Some(b)) => {
                let (a, b) = (a.as_ptr() as *const TOKEN_USER, b.as_ptr() as *const TOKEN_USER);
                // SAFETY: both are TOKEN_USER buffers GetTokenInformation filled.
                unsafe { EqualSid((*a).User.Sid, (*b).User.Sid) != 0 }
            }
            _ => false,
        }
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn a_socket_from_this_process_is_this_user() {
        let d = std::env::temp_dir().join(format!("rszk-test-{}", std::process::id()));
        super::ensure(&d).unwrap();
        let p = super::sock_path(&d, 7);
        let _ = std::fs::remove_file(&p);
        let l = crate::sock::UnixListener::bind(&p).unwrap();
        assert!(super::has(&d, 7));
        assert_eq!(super::list(&d), vec![7]);
        let c = crate::sock::UnixStream::connect(&p).unwrap();
        let (s, _) = l.accept().unwrap();
        assert!(super::peer_is_me(&c), "client side");
        assert!(super::peer_is_me(&s), "host side");
        drop((c, s, l));
        let _ = std::fs::remove_dir_all(&d);
    }
}
