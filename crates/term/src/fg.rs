//! Is a program other than the shell running in the pane? The input bar
//! steps aside while one is (Claude Code, ssh, a REPL, a long build), and
//! keys go straight to it.
//!
//! - Unix: the PTY's foreground process group, which the shell hands to the
//!   job it runs. A group other than the shell's own means a program has the
//!   terminal.
//! - Windows: ConPTY has no foreground group, so (as in Go suzuri's
//!   session_windows_fg.go) any live descendant of the shell counts.

/// What the probe needs, captured when the pane starts.
#[derive(Clone, Copy, Debug)]
pub struct Probe {
    pub shell_pid: u32,
    #[cfg(unix)]
    pub master_fd: std::os::fd::RawFd,
}

impl Probe {
    /// True while a program other than the shell owns the terminal.
    pub fn program_running(&self) -> bool {
        if self.shell_pid == 0 {
            return false;
        }
        imp::program_running(self)
    }

    /// The name of the program that owns the terminal, when it is not the
    /// shell: the foreground group's leader (Unix) or the shell's first
    /// child (Windows), without a path or ".exe".
    pub fn program_name(&self) -> Option<String> {
        if self.shell_pid == 0 {
            return None;
        }
        imp::program_name(self).map(|n| clean_name(&n)).filter(|n| !n.is_empty())
    }

    /// True while something in the pane reads keys one at a time or without
    /// echo (a start-up question, a passphrase, a line editor): the tty is
    /// out of canonical mode or has echo off. Always false on Windows.
    pub fn reads_keys(&self) -> bool {
        imp::reads_keys(self)
    }
}

/// A process name as a person reads it: no folder, no ".exe", and a login
/// shell's leading "-" dropped.
pub(crate) fn clean_name(raw: &str) -> String {
    let base = raw.rsplit(['/', '\\']).next().unwrap_or(raw).trim();
    let base = base.strip_prefix('-').unwrap_or(base);
    match base.len().checked_sub(4) {
        Some(i) if base.is_char_boundary(i) && base[i..].eq_ignore_ascii_case(".exe") => base[..i].to_string(),
        _ => base.to_string(),
    }
}

#[cfg(unix)]
mod imp {
    use super::Probe;

    pub fn program_name(p: &Probe) -> Option<String> {
        // SAFETY: as in program_running.
        let pg = unsafe { libc::tcgetpgrp(p.master_fd) };
        if pg <= 0 || pg as u32 == p.shell_pid {
            return None;
        }
        name_of(pg as u32)
    }

    /// argv[0], as `ps` shows it: the exe path can be a version folder
    /// (Claude Code's native build runs as `…/claude/versions/2.1.3`).
    #[cfg(target_os = "macos")]
    fn name_of(pid: u32) -> Option<String> {
        let mut mib = [libc::CTL_KERN, libc::KERN_PROCARGS2, pid as libc::c_int];
        let mut buf = vec![0u8; 64 * 1024];
        let mut len = buf.len();
        // SAFETY: sysctl writes at most len bytes and sets len to what it wrote.
        let ok = unsafe { libc::sysctl(mib.as_mut_ptr(), 3, buf.as_mut_ptr().cast(), &mut len, std::ptr::null_mut(), 0) } == 0;
        if let Some(a) = ok.then(|| argv0(&buf[..len])).flatten() {
            return Some(a);
        }
        let mut buf = [0u8; 256];
        // SAFETY: proc_name fills at most the given length.
        let n = unsafe { libc::proc_name(pid as libc::c_int, buf.as_mut_ptr().cast(), buf.len() as u32) };
        (n > 0).then(|| String::from_utf8_lossy(&buf[..n as usize]).into_owned())
    }

    #[cfg(test)]
    pub(super) fn name_of_for_test(pid: u32) -> Option<String> {
        name_of(pid)
    }

    /// KERN_PROCARGS2: argc (an int), the exe path, NUL padding, then argv.
    #[cfg(target_os = "macos")]
    pub(super) fn argv0(b: &[u8]) -> Option<String> {
        let rest = b.get(std::mem::size_of::<libc::c_int>()..)?;
        let path_end = rest.iter().position(|&c| c == 0)?;
        let rest = &rest[path_end..];
        let start = rest.iter().position(|&c| c != 0)?;
        let rest = &rest[start..];
        let end = rest.iter().position(|&c| c == 0).unwrap_or(rest.len());
        let a = String::from_utf8_lossy(&rest[..end]).into_owned();
        (!a.is_empty()).then_some(a)
    }

    #[cfg(not(target_os = "macos"))]
    fn name_of(pid: u32) -> Option<String> {
        let cmd = std::fs::read(format!("/proc/{pid}/cmdline")).unwrap_or_default();
        let a = cmd.split(|&c| c == 0).next().map(|a| String::from_utf8_lossy(a).into_owned()).unwrap_or_default();
        if !a.is_empty() {
            return Some(a);
        }
        std::fs::read_to_string(format!("/proc/{pid}/comm")).ok().map(|s| s.trim().to_string())
    }

    pub fn program_running(p: &Probe) -> bool {
        // SAFETY: tcgetpgrp only reads; a closed or reused fd returns -1.
        let pg = unsafe { libc::tcgetpgrp(p.master_fd) };
        pg > 0 && pg as u32 != p.shell_pid
    }

    pub fn reads_keys(p: &Probe) -> bool {
        // SAFETY: tcgetattr only fills the struct; a bad fd returns -1. The
        // master side shows the modes the program set on the slave.
        let mut t: libc::termios = unsafe { std::mem::zeroed() };
        if unsafe { libc::tcgetattr(p.master_fd, &mut t) } != 0 {
            return false;
        }
        t.c_lflag & libc::ICANON == 0 || t.c_lflag & libc::ECHO == 0
    }
}

#[cfg(windows)]
mod imp {
    use super::Probe;
    use windows_sys::Win32::Foundation::{CloseHandle, INVALID_HANDLE_VALUE};
    use windows_sys::Win32::System::Diagnostics::ToolHelp::{
        CreateToolhelp32Snapshot, PROCESSENTRY32W, Process32FirstW, Process32NextW, TH32CS_SNAPPROCESS,
    };

    pub fn program_running(p: &Probe) -> bool {
        let pairs: Vec<(u32, u32)> = processes().into_iter().map(|(pid, ppid, _)| (pid, ppid)).collect();
        has_child(&pairs, p.shell_pid)
    }

    pub fn program_name(p: &Probe) -> Option<String> {
        processes().into_iter().find(|&(pid, ppid, _)| ppid == p.shell_pid && pid != p.shell_pid && pid != 0).map(|(_, _, n)| n)
    }

    /// ConPTY shows no tty modes.
    pub fn reads_keys(_: &Probe) -> bool {
        false
    }

    /// (pid, parent pid, exe name) for every process. A direct child is enough: a program the shell runs is its child.
    fn processes() -> Vec<(u32, u32, String)> {
        let mut out = Vec::new();
        // SAFETY: plain ToolHelp walk; the handle is closed below.
        unsafe {
            let snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
            if snap == INVALID_HANDLE_VALUE {
                return out;
            }
            let mut e: PROCESSENTRY32W = std::mem::zeroed();
            e.dwSize = std::mem::size_of::<PROCESSENTRY32W>() as u32;
            let mut ok = Process32FirstW(snap, &mut e) != 0;
            while ok {
                let len = e.szExeFile.iter().position(|&c| c == 0).unwrap_or(e.szExeFile.len());
                out.push((e.th32ProcessID, e.th32ParentProcessID, String::from_utf16_lossy(&e.szExeFile[..len])));
                ok = Process32NextW(snap, &mut e) != 0;
            }
            CloseHandle(snap);
        }
        out
    }

    pub(super) fn has_child(pairs: &[(u32, u32)], root: u32) -> bool {
        pairs.iter().any(|&(pid, ppid)| ppid == root && pid != root && pid != 0)
    }
}

#[cfg(test)]
mod tests {
    #[cfg(unix)]
    #[test]
    fn unknown_shell_is_not_a_program() {
        let p = super::Probe { shell_pid: 0, master_fd: -1 };
        assert!(!p.program_running());
        let p = super::Probe { shell_pid: 1234, master_fd: -1 };
        assert!(!p.program_running(), "a bad fd reads as no program");
    }

    #[test]
    fn names_read_plainly() {
        use super::clean_name;
        assert_eq!(clean_name("/opt/homebrew/bin/claude"), "claude");
        assert_eq!(clean_name("node.exe"), "node");
        assert_eq!(clean_name("C:\\Windows\\System32\\PING.EXE"), "PING");
        assert_eq!(clean_name("-zsh"), "zsh");
        assert_eq!(clean_name("vim"), "vim");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn argv0_is_read_from_procargs() {
        let mut b = 2i32.to_ne_bytes().to_vec();
        b.extend_from_slice(b"/Users/x/.local/share/claude/versions/2.1.3\0\0\0claude\0--resume\0");
        assert_eq!(super::imp::argv0(&b).as_deref(), Some("claude"));
        assert_eq!(super::imp::argv0(&[0, 0]), None);
    }

    /// The probe names a real program in front: this test's own process
    /// asks about itself through a pid, as the pane's probe does.
    #[cfg(target_os = "macos")]
    #[test]
    fn a_running_program_is_named_by_argv0() {
        let mut c = std::process::Command::new("/bin/sleep").arg("5").spawn().unwrap();
        let n = super::imp::name_of_for_test(c.id());
        let _ = c.kill();
        let _ = c.wait();
        assert_eq!(n.map(|n| super::clean_name(&n)).as_deref(), Some("sleep"));
    }

    #[cfg(unix)]
    #[test]
    fn a_bad_fd_has_no_program_name() {
        assert_eq!(super::Probe { shell_pid: 1234, master_fd: -1 }.program_name(), None);
    }

    #[cfg(unix)]
    #[test]
    fn reads_keys_is_false_for_a_bad_fd() {
        let p = super::Probe { shell_pid: 1234, master_fd: -1 };
        assert!(!p.reads_keys());
    }
}
