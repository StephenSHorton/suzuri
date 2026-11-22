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
/// else's. A socket path must fit in sun_path (104 bytes on macOS).
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
