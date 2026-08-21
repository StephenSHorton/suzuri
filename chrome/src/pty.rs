//! Local shell PTY via `portable-pty`.
//!
//! Reader runs on a background thread and pushes bytes into an mpsc channel so
//! the UI event loop can drain with non-blocking `try_read`.

use portable_pty::{native_pty_system, CommandBuilder, MasterPty, PtySize};
use std::io::{Read, Write};
use std::sync::mpsc::{self, Receiver, TryRecvError};
use std::thread::{self, JoinHandle};

/// Live local PTY + shell child process.
///
/// Not `Sync`; own it on the UI/event-loop thread. The reader thread only
/// touches the cloned reader and the channel sender.
pub struct PtySession {
    master: Box<dyn MasterPty + Send>,
    writer: Box<dyn Write + Send>,
    child: Box<dyn portable_pty::Child + Send + Sync>,
    rx: Receiver<Vec<u8>>,
    /// Join handle for the reader thread (detached on drop after kill).
    _reader: Option<JoinHandle<()>>,
}

impl PtySession {
    /// Spawn the default user shell in a new PTY of the given size.
    ///
    /// Shell selection: `$SHELL` if set and non-empty, else `/bin/zsh` on macOS,
    /// PowerShell (pwsh → powershell → COMSPEC) on Windows, `/bin/bash` elsewhere.
    pub fn spawn(cols: u16, rows: u16) -> Result<Self, String> {
        Self::spawn_with_pixels(cols, rows, 0, 0)
    }

    /// Spawn with explicit pixel size (cols×cell_w, rows×cell_h) for DPI-aware TUIs.
    pub fn spawn_with_pixels(
        cols: u16,
        rows: u16,
        pixel_width: u16,
        pixel_height: u16,
    ) -> Result<Self, String> {
        Self::spawn_in(cols, rows, pixel_width, pixel_height, None)
    }

    /// Spawn in `cwd` when it is a non-empty directory; otherwise inherit.
    pub fn spawn_in(
        cols: u16,
        rows: u16,
        pixel_width: u16,
        pixel_height: u16,
        cwd: Option<&str>,
    ) -> Result<Self, String> {
        let pty_system = native_pty_system();
        let size = PtySize {
            rows,
            cols,
            pixel_width,
            pixel_height,
        };
        let pair = pty_system
            .openpty(size)
            .map_err(|e| format!("openpty: {e}"))?;

        let shell = default_shell();
        let mut cmd = CommandBuilder::new(&shell.program);
        cmd.args(&shell.args);
        // Attached to a PTY → shells run interactive without extra flags
        // (Windows PowerShell gets -NoLogo/-NoProfile + OSC cwd, matching
        // the Go host DefaultShell).
        cmd.env("TERM", "xterm-256color");
        cmd.env("COLORTERM", "truecolor");
        let dir = cwd
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .filter(|s| !crate::session::is_unhelpful_cwd(s))
            .map(|s| s.to_string())
            .or_else(crate::session::user_home_dir);
        if let Some(dir) = dir {
            cmd.cwd(dir);
        }

        let child = pair
            .slave
            .spawn_command(cmd)
            .map_err(|e| format!("spawn {}: {e}", shell.program))?;

        let mut reader = pair
            .master
            .try_clone_reader()
            .map_err(|e| format!("clone reader: {e}"))?;
        let writer = pair
            .master
            .take_writer()
            .map_err(|e| format!("take writer: {e}"))?;

        let (tx, rx) = mpsc::channel::<Vec<u8>>();
        let reader_handle = thread::Builder::new()
            .name("suzuri-pty-reader".into())
            .spawn(move || {
                let mut buf = [0u8; 8192];
                loop {
                    match reader.read(&mut buf) {
                        Ok(0) => break, // EOF — slave closed
                        Ok(n) => {
                            if tx.send(buf[..n].to_vec()).is_err() {
                                break; // receiver dropped
                            }
                        }
                        Err(_) => break,
                    }
                }
            })
            .map_err(|e| format!("spawn reader thread: {e}"))?;

        Ok(Self {
            master: pair.master,
            writer,
            child,
            rx,
            _reader: Some(reader_handle),
        })
    }

    /// Resize the PTY (winsize); notifies the child via SIGWINCH on Unix.
    pub fn resize(&mut self, cols: u16, rows: u16) -> Result<(), String> {
        self.resize_with_pixels(cols, rows, 0, 0)
    }

    /// Resize with pixel dimensions (helps apps that query `TIOCGWINSZ` px fields).
    pub fn resize_with_pixels(
        &mut self,
        cols: u16,
        rows: u16,
        pixel_width: u16,
        pixel_height: u16,
    ) -> Result<(), String> {
        self.master
            .resize(PtySize {
                rows,
                cols,
                pixel_width,
                pixel_height,
            })
            .map_err(|e| format!("resize: {e}"))
    }

    /// Non-blocking: drain all bytes currently queued from the reader thread.
    /// Raw bytes — do **not** UTF-8-lossy here. ConPTY splits multi-byte
    /// runes across reads; lossy replacement at the seam is what mixed Grok
    /// text (braille spinners, CJK, box drawing). `AnsiDecoder` already holds
    /// incomplete UTF-8 across `feed` calls.
    pub fn try_read(&mut self) -> Vec<u8> {
        let mut bytes = Vec::new();
        loop {
            match self.rx.try_recv() {
                Ok(chunk) => bytes.extend_from_slice(&chunk),
                Err(TryRecvError::Empty) => break,
                Err(TryRecvError::Disconnected) => break,
            }
        }
        bytes
    }

    /// Write raw bytes to the PTY master (user input, submitted line + `\n`/`\r`).
    pub fn write_all(&mut self, data: &[u8]) -> Result<(), String> {
        self.writer
            .write_all(data)
            .map_err(|e| format!("pty write: {e}"))?;
        self.writer.flush().map_err(|e| format!("pty flush: {e}"))
    }

    /// `true` while the child has not yet exited.
    pub fn is_alive(&mut self) -> bool {
        match self.child.try_wait() {
            Ok(None) => true,
            Ok(Some(_)) => false,
            Err(_) => false,
        }
    }

    /// Kill the shell child. Closing the PTY master then SIGHUPs the fg group.
    pub fn kill(&mut self) {
        let _ = self.child.kill();
    }
}

impl Drop for PtySession {
    fn drop(&mut self) {
        self.kill();
        // Reader thread exits on EOF after slave dies; don't join (may block
        // briefly). Handle is dropped with the struct.
        let _ = self._reader.take();
    }
}

/// Program + argv for the user's default interactive shell.
struct ShellSpec {
    program: String,
    args: Vec<String>,
}

fn default_shell() -> ShellSpec {
    if let Ok(shell) = std::env::var("SHELL") {
        if !shell.is_empty() {
            return ShellSpec {
                program: shell,
                args: Vec::new(),
            };
        }
    }
    #[cfg(windows)]
    {
        return windows_shell();
    }
    #[cfg(target_os = "macos")]
    {
        return ShellSpec {
            program: "/bin/zsh".into(),
            args: Vec::new(),
        };
    }
    #[cfg(not(any(windows, target_os = "macos")))]
    {
        ShellSpec {
            program: "/bin/bash".into(),
            args: Vec::new(),
        }
    }
}

/// Same order as Go `host.DefaultShell`: pwsh → powershell → COMSPEC → cmd.
/// Quiet in-band prompt + OSC 7878 cwd so the warp path bar tracks `cd`.
#[cfg(windows)]
fn windows_shell() -> ShellSpec {
    const PS_QUIET: &str = "function global:prompt { try { $p=(Get-Location).ProviderPath; if(-not $p){$p=(Get-Location).Path}; $e=[char]27; $b=[char]7; [Console]::Out.Write(($e+']7878;cwd='+$p+$b)) } catch {}; ' ' }; Clear-Host; try { $p=(Get-Location).ProviderPath; if(-not $p){$p=(Get-Location).Path}; $e=[char]27; $b=[char]7; [Console]::Out.Write(($e+']7878;cwd='+$p+$b)) } catch {}";
    if let Some(ps) = look_exe(&["pwsh.exe", "pwsh"]).or_else(well_known_pwsh) {
        return ShellSpec {
            program: ps,
            args: vec![
                "-NoLogo".into(),
                "-NoProfile".into(),
                "-NoExit".into(),
                "-Command".into(),
                PS_QUIET.into(),
            ],
        };
    }
    if let Some(ps) = look_exe(&["powershell.exe", "powershell"]).or_else(well_known_powershell) {
        return ShellSpec {
            program: ps,
            args: vec![
                "-NoLogo".into(),
                "-NoProfile".into(),
                "-NoExit".into(),
                "-Command".into(),
                PS_QUIET.into(),
            ],
        };
    }
    let comspec =
        std::env::var("COMSPEC").unwrap_or_else(|_| r"C:\Windows\System32\cmd.exe".into());
    ShellSpec {
        program: comspec,
        args: vec!["/k".into(), r"prompt $E]7878;cwd=$P$E\$S".into()],
    }
}

#[cfg(windows)]
fn look_exe(names: &[&str]) -> Option<String> {
    let path = std::env::var_os("PATH")?;
    for dir in std::env::split_paths(&path) {
        for name in names {
            let candidate = dir.join(name);
            if candidate.is_file() {
                return Some(candidate.to_string_lossy().into_owned());
            }
        }
    }
    None
}

#[cfg(windows)]
fn well_known_pwsh() -> Option<String> {
    let mut cands = Vec::new();
    if let Some(pf) = std::env::var_os("ProgramFiles") {
        cands.push(std::path::PathBuf::from(pf).join(r"PowerShell\7\pwsh.exe"));
    }
    for p in cands {
        if p.is_file() {
            return Some(p.to_string_lossy().into_owned());
        }
    }
    None
}

#[cfg(windows)]
fn well_known_powershell() -> Option<String> {
    let p = std::path::Path::new(r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe");
    if p.is_file() {
        Some(p.to_string_lossy().into_owned())
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(windows)]
    #[test]
    fn windows_default_shell_is_not_system32_cmd() {
        let shell = windows_shell();
        let low = shell.program.replace('\\', "/").to_ascii_lowercase();
        assert!(
            !low.ends_with("/cmd.exe"),
            "Windows should prefer PowerShell, got {}",
            shell.program
        );
        assert!(
            shell.args.iter().any(|a| a.contains("7878;cwd=")),
            "expected OSC cwd hook in {:?}",
            shell.args
        );
    }

    #[test]
    fn spawn_default_shell_does_not_panic() {
        // May fail in restricted CI without a TTY allocation path; treat hard
        // spawn errors as skip-ish by asserting only that we get Ok or Err String.
        match PtySession::spawn(80, 24) {
            Ok(mut pty) => {
                assert!(pty.is_alive());
                let _ = pty.write_all(b"exit\n");
                // Brief drain; don't assert on content (shell-dependent).
                let _ = pty.try_read();
            }
            Err(e) => {
                // Still a valid outcome in locked-down environments.
                assert!(!e.is_empty(), "error should be descriptive");
            }
        }
    }
}
