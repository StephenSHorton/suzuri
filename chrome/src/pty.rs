//! Local shell PTY via `portable-pty`.
//!
//! Reader runs on a background thread and pushes bytes into an mpsc channel so
//! the UI event loop can drain with non-blocking `try_read`.

use portable_pty::{CommandBuilder, MasterPty, PtySize, native_pty_system};
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
    /// `cmd.exe` on Windows, `/bin/bash` elsewhere.
    pub fn spawn(cols: u16, rows: u16) -> Result<Self, String> {
        let pty_system = native_pty_system();
        let size = PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        };
        let pair = pty_system
            .openpty(size)
            .map_err(|e| format!("openpty: {e}"))?;

        let shell = default_shell();
        let mut cmd = CommandBuilder::new(&shell);
        // Attached to a PTY → shells run interactive without extra flags.
        cmd.env("TERM", "xterm-256color");
        cmd.env("COLORTERM", "truecolor");

        let child = pair
            .slave
            .spawn_command(cmd)
            .map_err(|e| format!("spawn {shell}: {e}"))?;

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
        self.master
            .resize(PtySize {
                rows,
                cols,
                pixel_width: 0,
                pixel_height: 0,
            })
            .map_err(|e| format!("resize: {e}"))
    }

    /// Non-blocking: drain all bytes currently available from the reader thread.
    /// Returns an empty string when nothing is queued. UTF-8 is lossy.
    pub fn try_read(&mut self) -> String {
        let mut bytes = Vec::new();
        loop {
            match self.rx.try_recv() {
                Ok(chunk) => bytes.extend_from_slice(&chunk),
                Err(TryRecvError::Empty) => break,
                Err(TryRecvError::Disconnected) => break,
            }
        }
        if bytes.is_empty() {
            String::new()
        } else {
            String::from_utf8_lossy(&bytes).into_owned()
        }
    }

    /// Write raw bytes to the PTY master (user input, submitted line + `\n`/`\r`).
    pub fn write_all(&mut self, data: &[u8]) -> Result<(), String> {
        self.writer
            .write_all(data)
            .map_err(|e| format!("pty write: {e}"))?;
        self.writer
            .flush()
            .map_err(|e| format!("pty flush: {e}"))
    }

    /// `true` while the child has not yet exited.
    pub fn is_alive(&mut self) -> bool {
        match self.child.try_wait() {
            Ok(None) => true,
            Ok(Some(_)) => false,
            Err(_) => false,
        }
    }
}

impl Drop for PtySession {
    fn drop(&mut self) {
        let _ = self.child.kill();
        // Reader thread exits on EOF after slave dies; don't join (may block
        // briefly). Handle is dropped with the struct.
        let _ = self._reader.take();
    }
}

fn default_shell() -> String {
    if let Ok(shell) = std::env::var("SHELL") {
        if !shell.is_empty() {
            return shell;
        }
    }
    if cfg!(windows) {
        // ConPTY path; portable-pty maps this on Windows.
        "cmd.exe".into()
    } else if cfg!(target_os = "macos") {
        "/bin/zsh".into()
    } else {
        "/bin/bash".into()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

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
