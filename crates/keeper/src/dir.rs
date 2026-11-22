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
pub(crate) mod win {
    use std::io;
    use std::path::Path;

    use windows_sys::Win32::Foundation::{CloseHandle, HANDLE, INVALID_HANDLE_VALUE, LocalFree};
    use windows_sys::Win32::Security::Authorization::{ConvertSidToStringSidW, GetSecurityInfo, SE_FILE_OBJECT};
    use windows_sys::Win32::Security::{
        ACE_HEADER, ACL, ACL_SIZE_INFORMATION, AclSizeInformation, DACL_SECURITY_INFORMATION, EqualSid, GetAce, GetAclInformation, GetTokenInformation, INHERIT_ONLY_ACE,
        IsWellKnownSid, OWNER_SECURITY_INFORMATION, PSECURITY_DESCRIPTOR, PSID, TOKEN_QUERY, TOKEN_USER, TokenUser, WinBuiltinAdministratorsSid, WinCreatorOwnerRightsSid,
        WinLocalSystemSid,
    };
    use windows_sys::Win32::Storage::FileSystem::{
        CreateFileW, FILE_ATTRIBUTE_TAG_INFO, FILE_FLAG_BACKUP_SEMANTICS, FILE_FLAG_OPEN_REPARSE_POINT, FILE_READ_ATTRIBUTES, FILE_SHARE_DELETE, FILE_SHARE_READ,
        FILE_SHARE_WRITE, FileAttributeTagInfo, GetFileInformationByHandleEx, OPEN_EXISTING, READ_CONTROL,
    };
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

    const FILE_ATTRIBUTE_DIRECTORY: u32 = 0x10;
    const FILE_ATTRIBUTE_REPARSE_POINT: u32 = 0x400;
    /// What an AF_UNIX socket is on disk: a reparse point with this tag.
    const IO_REPARSE_TAG_AF_UNIX: u32 = 0x8000_0023;

    struct Handle(HANDLE);
    impl Drop for Handle {
        fn drop(&mut self) {
            // SAFETY: an open handle from CreateFileW.
            unsafe { CloseHandle(self.0) };
        }
    }

    /// `path` itself, never what a junction, symlink or socket points at.
    fn open(path: &Path) -> io::Result<Handle> {
        use std::os::windows::ffi::OsStrExt;
        let wide: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
        // SAFETY: wide is NUL-terminated; no security attributes or template.
        let h = unsafe {
            CreateFileW(
                wide.as_ptr(),
                READ_CONTROL | FILE_READ_ATTRIBUTES,
                FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                std::ptr::null(),
                OPEN_EXISTING,
                FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT,
                std::ptr::null_mut(),
            )
        };
        if h == INVALID_HANDLE_VALUE { Err(io::Error::last_os_error()) } else { Ok(Handle(h)) }
    }

    /// (attributes, reparse tag).
    fn attributes(h: &Handle) -> io::Result<(u32, u32)> {
        let mut info = FILE_ATTRIBUTE_TAG_INFO { FileAttributes: 0, ReparseTag: 0 };
        // SAFETY: info is a FILE_ATTRIBUTE_TAG_INFO and its size is given.
        let ok = unsafe { GetFileInformationByHandleEx(h.0, FileAttributeTagInfo, (&mut info as *mut FILE_ATTRIBUTE_TAG_INFO).cast(), std::mem::size_of::<FILE_ATTRIBUTE_TAG_INFO>() as u32) };
        if ok == 0 { Err(io::Error::last_os_error()) } else { Ok((info.FileAttributes, info.ReparseTag)) }
    }

    /// A security descriptor GetSecurityInfo made, with the owner and the
    /// access list (null: none, open to everyone) pointing into it.
    struct Security {
        sd: PSECURITY_DESCRIPTOR,
        owner: PSID,
        dacl: *mut ACL,
    }
    impl Drop for Security {
        fn drop(&mut self) {
            // SAFETY: GetSecurityInfo's descriptor is freed with LocalFree.
            unsafe { LocalFree(self.sd) };
        }
    }

    fn security(h: &Handle) -> io::Result<Security> {
        let mut s = Security { sd: std::ptr::null_mut(), owner: std::ptr::null_mut(), dacl: std::ptr::null_mut() };
        // SAFETY: an open handle with READ_CONTROL; the out pointers are valid.
        let r = unsafe {
            GetSecurityInfo(h.0, SE_FILE_OBJECT, OWNER_SECURITY_INFORMATION | DACL_SECURITY_INFORMATION, &mut s.owner, std::ptr::null_mut(), &mut s.dacl, std::ptr::null_mut(), &mut s.sd)
        };
        if r != 0 { Err(io::Error::from_raw_os_error(r as i32)) } else { Ok(s) }
    }

    /// This process's user, as a TOKEN_USER buffer.
    fn me() -> io::Result<Vec<u64>> {
        // SAFETY: the pseudo handle needs no closing.
        token_user(unsafe { GetCurrentProcess() }).ok_or_else(|| io::Error::other("keeper: this process's user is unknown"))
    }

    fn sid_of(user: &[u64]) -> PSID {
        // SAFETY: a TOKEN_USER buffer GetTokenInformation filled.
        unsafe { (*(user.as_ptr() as *const TOKEN_USER)).User.Sid }
    }

    /// `S-1-5-...`, for messages (and the tests' icacls).
    pub(crate) fn sid_text(sid: PSID) -> String {
        let mut w: windows_sys::core::PWSTR = std::ptr::null_mut();
        // SAFETY: sid is a valid SID; w is written on success and freed below.
        if unsafe { ConvertSidToStringSidW(sid, &mut w) } == 0 {
            return "an unknown user".into();
        }
        // SAFETY: a NUL-terminated string ConvertSidToStringSidW made.
        let t = unsafe {
            let n = (0..).take_while(|&i| *w.add(i) != 0).count();
            String::from_utf16_lossy(std::slice::from_raw_parts(w, n))
        };
        // SAFETY: allocated by ConvertSidToStringSidW.
        unsafe { LocalFree(w.cast()) };
        t
    }

    #[cfg(test)]
    pub(crate) fn my_sid_text() -> String {
        sid_text(sid_of(&me().unwrap()))
    }

    /// Who may be let into a folder of this user's: the user, SYSTEM,
    /// Administrators, and OWNER RIGHTS (the owner, checked to be the user).
    fn trusted(sid: PSID, me: PSID) -> bool {
        // SAFETY: both are valid SIDs.
        unsafe {
            EqualSid(sid, me) != 0
                || IsWellKnownSid(sid, WinLocalSystemSid) != 0
                || IsWellKnownSid(sid, WinBuiltinAdministratorsSid) != 0
                || IsWellKnownSid(sid, WinCreatorOwnerRightsSid) != 0
        }
    }

    /// What in the access list lets someone else in, or None.
    fn lets_others_in(dacl: *mut ACL, me: PSID) -> Option<String> {
        if dacl.is_null() {
            return Some("has no access list: it is open to everyone".into());
        }
        let mut info = ACL_SIZE_INFORMATION { AceCount: 0, AclBytesInUse: 0, AclBytesFree: 0 };
        // SAFETY: dacl points into a live descriptor; info and its size match.
        if unsafe { GetAclInformation(dacl, (&mut info as *mut ACL_SIZE_INFORMATION).cast(), std::mem::size_of::<ACL_SIZE_INFORMATION>() as u32, AclSizeInformation) } == 0 {
            return Some("has an access list that cannot be read".into());
        }
        for i in 0..info.AceCount {
            let mut ace: *mut core::ffi::c_void = std::ptr::null_mut();
            // SAFETY: i is below the ACL's count; ace is written on success.
            if unsafe { GetAce(dacl, i, &mut ace) } == 0 {
                return Some("has an access list that cannot be read".into());
            }
            // SAFETY: every ACE starts with an ACE_HEADER.
            let head = unsafe { &*(ace as *const ACE_HEADER) };
            // For what is made in it later, not for the folder itself.
            if head.AceFlags as u32 & INHERIT_ONLY_ACE != 0 {
                continue;
            }
            match head.AceType {
                // Allowed, and allowed-with-a-condition: the SID follows the
                // header and the access mask.
                0 | 9 => {
                    // SAFETY: ACCESS_ALLOWED_ACE and its callback twin keep the SID at byte 8.
                    let sid = unsafe { (ace as *mut u8).add(8) } as PSID;
                    if !trusted(sid, me) {
                        return Some(format!("lets another user in ({})", sid_text(sid)));
                    }
                }
                // Denied: shuts people out, never lets them in.
                1 | 6 | 10 | 12 => {}
                t => return Some(format!("has an access entry this does not read (kind {t})")),
            }
        }
        None
    }

    /// Why `folder` is not a folder of this user's only, or None.
    pub(super) fn foreign_folder(folder: &Path) -> io::Result<Option<String>> {
        let h = open(folder)?;
        let (attr, _) = attributes(&h)?;
        if attr & FILE_ATTRIBUTE_DIRECTORY == 0 {
            return Ok(Some("is not a folder".into()));
        }
        if attr & FILE_ATTRIBUTE_REPARSE_POINT != 0 {
            return Ok(Some("is a junction or a link, not a folder of its own".into()));
        }
        let me = me()?;
        let s = security(&h)?;
        // SAFETY: both are valid SIDs.
        if s.owner.is_null() || unsafe { EqualSid(s.owner, sid_of(&me)) } == 0 {
            return Ok(Some(format!("is not a folder of this user's (its owner is {})", if s.owner.is_null() { "nobody".into() } else { sid_text(s.owner) })));
        }
        Ok(lets_others_in(s.dacl, sid_of(&me)).map(|why| format!("is open to other users: it {why}")))
    }

    /// Why `path` is not a socket of this user's, or None.
    pub(super) fn foreign_socket(path: &Path) -> io::Result<Option<String>> {
        let h = open(path)?;
        let (attr, tag) = attributes(&h)?;
        if attr & FILE_ATTRIBUTE_REPARSE_POINT == 0 || tag != IO_REPARSE_TAG_AF_UNIX {
            return Ok(Some("is not a socket".into()));
        }
        let me = me()?;
        let s = security(&h)?;
        // SAFETY: both are valid SIDs.
        if s.owner.is_null() || unsafe { EqualSid(s.owner, sid_of(&me)) } == 0 {
            return Ok(Some("is not a socket of this user's".into()));
        }
        Ok(None)
    }
}

/// Whether `path` is a socket another program (Hearth) keeps a shell on
/// that suzuri may show (its open_kept): an absolute `<id>.sock` that is a
/// socket of this user's, in a folder of this user's that nobody else can
/// enter, not reached through a link, and short enough for a socket
/// address. The folder is only checked, never changed: it is the other
/// program's.
///
/// Unix: the folder's mode is 0700, as [`ensure`] makes one. Windows: the
/// folder and the socket are owned by this user, the folder's access list
/// lets in no one but this user, SYSTEM and Administrators (as a user's own
/// `%LOCALAPPDATA%` is set), and neither is a junction or a symlink. On both,
/// connecting checks the host runs as this user too ([`peer_is_me`]).
pub fn foreign_sock(path: &Path) -> io::Result<PathBuf> {
    let bad = |why: String| io::Error::new(io::ErrorKind::PermissionDenied, why);
    if !path.is_absolute() {
        return Err(bad(format!("{} is not an absolute path", path.display())));
    }
    let named = path.file_name().and_then(|n| n.to_str()).and_then(|n| n.strip_suffix(".sock")).is_some_and(|stem| !stem.is_empty());
    if !named || path.components().any(|c| matches!(c, std::path::Component::ParentDir | std::path::Component::CurDir)) {
        return Err(bad(format!("{} is not a keeper socket (<id>.sock)", path.display())));
    }
    // A plain C:\... path: no \\?\, \\.\ or share (AF_UNIX takes none).
    #[cfg(windows)]
    if !matches!(path.components().next(), Some(std::path::Component::Prefix(p)) if matches!(p.kind(), std::path::Prefix::Disk(_))) {
        return Err(bad(format!("{} is not a path on a drive (C:\\...)", path.display())));
    }
    // sun_path: 104 bytes on macOS, 108 on Linux and Windows; 104 for all.
    if path.as_os_str().len() >= 104 {
        return Err(bad(format!("{} is too long for a socket", path.display())));
    }
    let folder = path.parent().ok_or_else(|| bad("no folder".into()))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::{FileTypeExt, MetadataExt};
        // SAFETY: getuid has no failure mode.
        let me = unsafe { libc::getuid() };
        let fm = std::fs::symlink_metadata(folder)?;
        if !fm.is_dir() || fm.uid() != me {
            return Err(bad(format!("{} is not a folder of this user's", folder.display())));
        }
        if fm.mode() & 0o077 != 0 {
            return Err(bad(format!("{} is open to other users (mode {:o}, needs 0700)", folder.display(), fm.mode() & 0o777)));
        }
        let sm = std::fs::symlink_metadata(path)?;
        if !sm.file_type().is_socket() || sm.uid() != me {
            return Err(bad(format!("{} is not a socket of this user's", path.display())));
        }
    }
    #[cfg(windows)]
    {
        if let Some(why) = win::foreign_folder(folder)? {
            return Err(bad(format!("{} {why}", folder.display())));
        }
        if let Some(why) = win::foreign_socket(path)? {
            return Err(bad(format!("{} {why}", path.display())));
        }
    }
    Ok(path.to_path_buf())
}

/// A scratch folder for tests whose socket paths fit: the temp folder, or
/// when its path is too long (a long user profile or a check runner's own
/// temp folder) `/tmp`, or on Windows the system drive's root.
#[doc(hidden)]
pub fn short_test_dir(name: &str) -> PathBuf {
    let d = std::env::temp_dir().join(name);
    let fits = sock_path(&d, u32::MAX).as_os_str().len() < 104;
    if fits {
        return d;
    }
    if cfg!(not(windows)) {
        return PathBuf::from("/tmp").join(name);
    }
    let drive = std::env::var("SystemDrive").unwrap_or_else(|_| "C:".into());
    PathBuf::from(format!("{drive}\\")).join(name)
}

#[cfg(test)]
mod tests {
    #[test]
    fn a_socket_from_this_process_is_this_user() {
        let d = super::short_test_dir(&format!("rszk-test-{}", std::process::id()));
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

#[cfg(all(test, unix))]
mod foreign_tests {
    use super::foreign_sock;
    use std::os::unix::fs::PermissionsExt;
    use std::path::PathBuf;

    fn folder(tag: &str, mode: u32) -> PathBuf {
        let d = PathBuf::from(format!("/tmp/rszf-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        std::fs::set_permissions(&d, std::fs::Permissions::from_mode(mode)).unwrap();
        d
    }

    #[test]
    fn a_socket_in_a_private_folder_is_taken() {
        let d = folder("ok", 0o700);
        let s = d.join("12.sock");
        let _l = std::os::unix::net::UnixListener::bind(&s).unwrap();
        assert_eq!(foreign_sock(&s).unwrap(), s);
        let _ = std::fs::remove_dir_all(&d);
    }

    #[test]
    fn anything_else_is_refused() {
        let d = folder("no", 0o700);
        let s = d.join("3.sock");
        let _l = std::os::unix::net::UnixListener::bind(&s).unwrap();
        // Not absolute, not <id>.sock, a dot-dot, a plain file, a symlink.
        assert!(foreign_sock(std::path::Path::new("3.sock")).is_err());
        assert!(foreign_sock(&d.join("3.socket")).is_err());
        assert!(foreign_sock(&d.join("..").join(d.file_name().unwrap()).join("3.sock")).is_err());
        std::fs::write(d.join("4.sock"), b"").unwrap();
        assert!(foreign_sock(&d.join("4.sock")).is_err());
        std::os::unix::fs::symlink(&s, d.join("5.sock")).unwrap();
        assert!(foreign_sock(&d.join("5.sock")).is_err());
        assert!(foreign_sock(&d.join("6.sock")).is_err(), "missing");
        // A folder others can enter.
        std::fs::set_permissions(&d, std::fs::Permissions::from_mode(0o755)).unwrap();
        assert!(foreign_sock(&s).is_err());
        let _ = std::fs::remove_dir_all(&d);
    }
}

/// The same on Windows: the folder is made private with icacls (no
/// inherited entries; this user, SYSTEM and Administrators), as a user's
/// own %LOCALAPPDATA% is, then opened up, linked to and so on.
#[cfg(all(test, windows))]
mod foreign_tests {
    use super::{foreign_sock, short_test_dir};
    use std::path::{Path, PathBuf};
    use std::process::Command;

    fn icacls(d: &Path, args: &[&str]) {
        let st = Command::new("icacls").arg(d).args(args).arg("/Q").status().unwrap();
        assert!(st.success(), "icacls {args:?}");
    }

    fn private(tag: &str) -> PathBuf {
        let d = short_test_dir(&format!("rszf-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        let me = format!("*{}:(OI)(CI)F", super::win::my_sid_text());
        icacls(&d, &["/inheritance:r", "/grant:r", &me, "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F"]);
        d
    }

    #[test]
    fn a_socket_in_a_private_folder_is_taken() {
        let d = private("ok");
        let s = d.join("12.sock");
        let _l = crate::sock::UnixListener::bind(&s).unwrap();
        assert_eq!(foreign_sock(&s).unwrap(), s);
        drop(_l);
        let _ = std::fs::remove_dir_all(&d);
    }

    /// No icacls: a folder made by plain `ensure` in the user's own
    /// %LOCALAPPDATA% (Suzuri's) and %APPDATA% (Hearth's: its app config
    /// folder) is taken as it is, with what it inherits there.
    #[test]
    fn a_plain_folder_in_the_users_app_data_is_taken() {
        for var in ["LOCALAPPDATA", "APPDATA"] {
            let base = std::env::var_os(var).map(PathBuf::from).unwrap_or_else(|| panic!("no %{var}%"));
            let d = base.join(format!("rszf-plain-{}", std::process::id()));
            let _ = std::fs::remove_dir_all(&d);
            super::ensure(&d).unwrap();
            let s = d.join("9.sock");
            let l = crate::sock::UnixListener::bind(&s).unwrap();
            assert_eq!(foreign_sock(&s).unwrap(), s, "a plain folder in %{var}%");
            drop(l);
            let _ = std::fs::remove_dir_all(&d);
        }
    }

    #[test]
    fn anything_else_is_refused() {
        let d = private("no");
        let s = d.join("3.sock");
        let _l = crate::sock::UnixListener::bind(&s).unwrap();
        assert!(foreign_sock(&s).is_ok(), "{:?}", foreign_sock(&s));
        // Not absolute, not <id>.sock, a dot-dot, a \\?\ path, a plain file.
        assert!(foreign_sock(Path::new("3.sock")).is_err());
        assert!(foreign_sock(&d.join("3.socket")).is_err());
        assert!(foreign_sock(&d.join("..").join(d.file_name().unwrap()).join("3.sock")).is_err());
        assert!(foreign_sock(&PathBuf::from(format!(r"\\?\{}", s.display()))).is_err());
        std::fs::write(d.join("4.sock"), b"").unwrap();
        let e = foreign_sock(&d.join("4.sock")).unwrap_err();
        assert!(e.to_string().contains("not a socket"), "{e}");
        std::fs::create_dir(d.join("5.sock")).unwrap();
        assert!(foreign_sock(&d.join("5.sock")).is_err(), "a folder named 5.sock");
        assert!(foreign_sock(&d.join("6.sock")).is_err(), "missing");
        // The folder reached through a junction.
        let j = PathBuf::from(format!("{}-j", d.display()));
        let _ = std::fs::remove_dir(&j);
        let st = Command::new("cmd").args(["/C", "mklink", "/J"]).arg(&j).arg(&d).status().unwrap();
        assert!(st.success(), "mklink /J");
        let e = foreign_sock(&j.join("3.sock")).unwrap_err();
        assert!(e.to_string().contains("junction"), "{e}");
        std::fs::remove_dir(&j).unwrap();
        // The folder opened to every signed-in user, then only to read.
        icacls(&d, &["/grant", "*S-1-5-11:(RX)"]);
        let e = foreign_sock(&s).unwrap_err();
        assert!(e.to_string().contains("open to other users"), "{e}");
        icacls(&d, &["/remove:g", "*S-1-5-11"]);
        assert!(foreign_sock(&s).is_ok(), "private again: {:?}", foreign_sock(&s));
        icacls(&d, &["/grant", "*S-1-1-0:(R)"]);
        assert!(foreign_sock(&s).is_err(), "Everyone may read");
        drop(_l);
        let _ = std::fs::remove_dir_all(&d);
    }
}
