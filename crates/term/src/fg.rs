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
}

#[cfg(unix)]
mod imp {
    use super::Probe;

    pub fn program_running(p: &Probe) -> bool {
        // SAFETY: tcgetpgrp only reads; a closed or reused fd returns -1.
        let pg = unsafe { libc::tcgetpgrp(p.master_fd) };
        pg > 0 && pg as u32 != p.shell_pid
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
        let pairs = processes();
        has_child(&pairs, p.shell_pid)
    }

    /// (pid, parent pid) for every process. A direct child is enough: a program the shell runs is its child.
    fn processes() -> Vec<(u32, u32)> {
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
                out.push((e.th32ProcessID, e.th32ParentProcessID));
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
}
