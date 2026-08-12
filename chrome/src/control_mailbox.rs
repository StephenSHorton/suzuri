//! Light host control plane — file mailbox under the product config dir.
//!
//! Phase 2 IPC (see `HOST.md`): the Go host writes one command per line to
//! `chrome_cmd`; chrome polls every ~250ms, reads, truncates, and runs the
//! action. Missing or unreadable mailbox is a soft no-op so default spawn
//! keeps working without any IPC setup.
//!
//! Commands (one line each, snake_case):
//! - `quit`
//! - `open_notes`
//! - `open_workspace`
//! - `open_palette`
//! - `open_settings`
//! - `open_transfer_send`
//! - `open_transfer_receive`
//! - `open_help`
//! - `new_tab`
//! - `toggle_caffeine`

use std::fs;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use crate::commands::CommandAction;
use crate::config_store;

/// Filename under the product config directory.
pub const CHROME_CMD_FILE: &str = "chrome_cmd";

/// How often chrome re-checks the mailbox (host may write anytime).
pub const POLL_INTERVAL: Duration = Duration::from_millis(250);

/// Parsed host control command.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ControlCommand {
    Quit,
    OpenNotes,
    OpenWorkspace,
    OpenPalette,
    OpenSettings,
    OpenTransferSend,
    OpenTransferReceive,
    OpenHelp,
    NewTab,
    ToggleCaffeine,
}

impl ControlCommand {
    /// Parse a single trimmed line. Unknown / empty → `None`.
    pub fn parse(line: &str) -> Option<Self> {
        match line.trim() {
            "quit" => Some(Self::Quit),
            "open_notes" => Some(Self::OpenNotes),
            "open_workspace" => Some(Self::OpenWorkspace),
            "open_palette" => Some(Self::OpenPalette),
            "open_settings" => Some(Self::OpenSettings),
            "open_transfer_send" => Some(Self::OpenTransferSend),
            "open_transfer_receive" => Some(Self::OpenTransferReceive),
            "open_help" => Some(Self::OpenHelp),
            "new_tab" => Some(Self::NewTab),
            "toggle_caffeine" => Some(Self::ToggleCaffeine),
            _ => None,
        }
    }

    /// Map to palette / shortcut [`CommandAction`] for `ChromeApp::run_action`.
    pub fn to_action(self) -> CommandAction {
        match self {
            Self::Quit => CommandAction::Quit,
            Self::OpenNotes => CommandAction::OpenNotes,
            Self::OpenWorkspace => CommandAction::OpenWorkspace,
            Self::OpenPalette => CommandAction::OpenPalette,
            Self::OpenSettings => CommandAction::OpenSettings,
            Self::OpenTransferSend => CommandAction::OpenTransferSend,
            Self::OpenTransferReceive => CommandAction::OpenTransferReceive,
            Self::OpenHelp => CommandAction::OpenHelp,
            Self::NewTab => CommandAction::NewTab,
            Self::ToggleCaffeine => CommandAction::ToggleCaffeine,
        }
    }

    /// Canonical wire string (matches Go `SendCommand`).
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Quit => "quit",
            Self::OpenNotes => "open_notes",
            Self::OpenWorkspace => "open_workspace",
            Self::OpenPalette => "open_palette",
            Self::OpenSettings => "open_settings",
            Self::OpenTransferSend => "open_transfer_send",
            Self::OpenTransferReceive => "open_transfer_receive",
            Self::OpenHelp => "open_help",
            Self::NewTab => "new_tab",
            Self::ToggleCaffeine => "toggle_caffeine",
        }
    }
}

/// Config dir for the mailbox: `SUZURI_CONFIG_DIR` if set, else product default.
pub fn mailbox_config_dir() -> PathBuf {
    if let Ok(p) = std::env::var("SUZURI_CONFIG_DIR") {
        let t = p.trim();
        if !t.is_empty() {
            return PathBuf::from(t);
        }
    }
    config_store::product_config_dir()
}

/// Default path: `{config_dir}/chrome_cmd`.
pub fn chrome_cmd_path() -> PathBuf {
    mailbox_config_dir().join(CHROME_CMD_FILE)
}

/// Rate-limited poller over a single mailbox path.
pub struct ControlMailbox {
    path: PathBuf,
    last_poll: Instant,
}

impl Default for ControlMailbox {
    fn default() -> Self {
        Self::new()
    }
}

impl ControlMailbox {
    /// Mailbox at [`chrome_cmd_path`].
    pub fn new() -> Self {
        Self::with_path(chrome_cmd_path())
    }

    /// Mailbox at an explicit path (tests / custom config dir).
    pub fn with_path(path: PathBuf) -> Self {
        Self {
            path,
            // Allow first poll immediately after construction.
            last_poll: Instant::now()
                .checked_sub(POLL_INTERVAL)
                .unwrap_or_else(Instant::now),
        }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    /// If ≥ [`POLL_INTERVAL`] since last check, take pending commands (fail soft).
    pub fn poll(&mut self) -> Vec<ControlCommand> {
        let now = Instant::now();
        if now.duration_since(self.last_poll) < POLL_INTERVAL {
            return Vec::new();
        }
        self.last_poll = now;
        take_commands(&self.path)
    }
}

/// Read `path` if present and non-empty, truncate (or remove), parse lines.
///
/// Fail soft: missing, empty, or unreadable → empty vec.
pub fn take_commands(path: &Path) -> Vec<ControlCommand> {
    let raw = match fs::read_to_string(path) {
        Ok(s) => s,
        Err(_) => return Vec::new(),
    };
    if raw.trim().is_empty() {
        // Empty file: leave truncated; nothing to run.
        let _ = truncate_mailbox(path);
        return Vec::new();
    }
    // Truncate before dispatch so a slow action cannot re-read the same body.
    let _ = truncate_mailbox(path);

    raw.lines().filter_map(ControlCommand::parse).collect()
}

fn truncate_mailbox(path: &Path) -> std::io::Result<()> {
    match fs::write(path, b"") {
        Ok(()) => Ok(()),
        Err(e) => {
            // Fall back to remove if truncate fails (e.g. permissions race).
            match fs::remove_file(path) {
                Ok(()) => Ok(()),
                Err(_) => Err(e),
            }
        }
    }
}

/// Write a single command for tests / local tooling (overwrite + newline).
#[cfg(test)]
pub fn write_command(path: &Path, cmd: &str) -> std::io::Result<()> {
    if let Some(parent) = path.parent() {
        if !parent.as_os_str().is_empty() {
            fs::create_dir_all(parent)?;
        }
    }
    fs::write(path, format!("{}\n", cmd.trim()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    fn temp_cmd_path(name: &str) -> PathBuf {
        let mut p = std::env::temp_dir();
        p.push(format!(
            "suzuri-chrome-cmd-{}-{}-{}",
            name,
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        ));
        p
    }

    #[test]
    fn parse_known_commands() {
        assert_eq!(ControlCommand::parse("quit"), Some(ControlCommand::Quit));
        assert_eq!(
            ControlCommand::parse("  open_notes\n"),
            Some(ControlCommand::OpenNotes)
        );
        assert_eq!(
            ControlCommand::parse("open_workspace"),
            Some(ControlCommand::OpenWorkspace)
        );
        assert_eq!(
            ControlCommand::parse("open_palette"),
            Some(ControlCommand::OpenPalette)
        );
        assert_eq!(
            ControlCommand::parse("open_settings"),
            Some(ControlCommand::OpenSettings)
        );
        assert_eq!(
            ControlCommand::parse("open_transfer_send"),
            Some(ControlCommand::OpenTransferSend)
        );
        assert_eq!(
            ControlCommand::parse("open_transfer_receive"),
            Some(ControlCommand::OpenTransferReceive)
        );
        assert_eq!(
            ControlCommand::parse("open_help"),
            Some(ControlCommand::OpenHelp)
        );
        assert_eq!(ControlCommand::parse("new_tab"), Some(ControlCommand::NewTab));
        assert_eq!(
            ControlCommand::parse("toggle_caffeine"),
            Some(ControlCommand::ToggleCaffeine)
        );
        assert_eq!(ControlCommand::parse("nope"), None);
        assert_eq!(ControlCommand::parse(""), None);
        // CamelCase / wrong separators stay unknown (snake_case wire only).
        assert_eq!(ControlCommand::parse("OpenSettings"), None);
        assert_eq!(ControlCommand::parse("open-settings"), None);
    }

    #[test]
    fn to_action_maps() {
        assert_eq!(
            ControlCommand::Quit.to_action(),
            CommandAction::Quit
        );
        assert_eq!(
            ControlCommand::OpenNotes.to_action(),
            CommandAction::OpenNotes
        );
        assert_eq!(
            ControlCommand::OpenWorkspace.to_action(),
            CommandAction::OpenWorkspace
        );
        assert_eq!(
            ControlCommand::OpenPalette.to_action(),
            CommandAction::OpenPalette
        );
        assert_eq!(
            ControlCommand::OpenSettings.to_action(),
            CommandAction::OpenSettings
        );
        assert_eq!(
            ControlCommand::OpenTransferSend.to_action(),
            CommandAction::OpenTransferSend
        );
        assert_eq!(
            ControlCommand::OpenTransferReceive.to_action(),
            CommandAction::OpenTransferReceive
        );
        assert_eq!(
            ControlCommand::OpenHelp.to_action(),
            CommandAction::OpenHelp
        );
        assert_eq!(ControlCommand::NewTab.to_action(), CommandAction::NewTab);
        assert_eq!(
            ControlCommand::ToggleCaffeine.to_action(),
            CommandAction::ToggleCaffeine
        );
    }

    #[test]
    fn as_str_round_trips_parse() {
        let all = [
            ControlCommand::Quit,
            ControlCommand::OpenNotes,
            ControlCommand::OpenWorkspace,
            ControlCommand::OpenPalette,
            ControlCommand::OpenSettings,
            ControlCommand::OpenTransferSend,
            ControlCommand::OpenTransferReceive,
            ControlCommand::OpenHelp,
            ControlCommand::NewTab,
            ControlCommand::ToggleCaffeine,
        ];
        for cmd in all {
            assert_eq!(ControlCommand::parse(cmd.as_str()), Some(cmd));
        }
    }

    #[test]
    fn take_commands_missing_is_soft() {
        let path = temp_cmd_path("missing");
        let _ = fs::remove_file(&path);
        assert!(take_commands(&path).is_empty());
    }

    #[test]
    fn take_commands_reads_and_truncates() {
        let path = temp_cmd_path("rw");
        write_command(&path, "open_notes").unwrap();
        let cmds = take_commands(&path);
        assert_eq!(cmds, vec![ControlCommand::OpenNotes]);
        // Second take is empty (truncated).
        assert!(take_commands(&path).is_empty());
        let body = fs::read_to_string(&path).unwrap_or_default();
        assert!(body.trim().is_empty());
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn take_commands_multi_line() {
        let path = temp_cmd_path("multi");
        fs::write(&path, "open_palette\nopen_workspace\nquit\n").unwrap();
        let cmds = take_commands(&path);
        assert_eq!(
            cmds,
            vec![
                ControlCommand::OpenPalette,
                ControlCommand::OpenWorkspace,
                ControlCommand::Quit,
            ]
        );
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn take_commands_skips_unknown_lines() {
        let path = temp_cmd_path("skip");
        fs::write(&path, "bogus\nopen_notes\n\n# comment\n").unwrap();
        let cmds = take_commands(&path);
        assert_eq!(cmds, vec![ControlCommand::OpenNotes]);
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn poll_rate_limits() {
        let path = temp_cmd_path("rate");
        write_command(&path, "quit").unwrap();
        let mut box_ = ControlMailbox::with_path(path.clone());
        let first = box_.poll();
        assert_eq!(first, vec![ControlCommand::Quit]);
        // Immediate re-poll within interval → empty even if we rewrite.
        write_command(&path, "open_notes").unwrap();
        assert!(box_.poll().is_empty());
        // Force interval elapsed.
        box_.last_poll = Instant::now()
            .checked_sub(POLL_INTERVAL + Duration::from_millis(1))
            .unwrap_or_else(Instant::now);
        assert_eq!(box_.poll(), vec![ControlCommand::OpenNotes]);
        let _ = fs::remove_file(&path);
    }

    #[test]
    fn chrome_cmd_path_ends_with_filename() {
        let p = chrome_cmd_path();
        assert_eq!(
            p.file_name().and_then(|s| s.to_str()),
            Some(CHROME_CMD_FILE)
        );
    }
}
