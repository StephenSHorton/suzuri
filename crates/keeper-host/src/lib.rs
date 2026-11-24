//! suzuri's shell keeper host (cards hearth#913, hearth#923 for Windows,
//! hearth#190 for this crate). Each kept shell runs in its own host
//! process, which owns the whole pane (PTY or ConPTY, screen, scrollback)
//! and serves it on `<dir>/<id>.sock`; the wire is crates/keeper
//! (rsz-keeper), which viewers use to attach.
//!
//! Two programs run it with the same arguments and the same wire:
//! - `suzuri --keeper-host <dir> <id>`, the app's own hosts (app/src/keeper.rs);
//! - `rsz-keeper-host [--keeper-host] <dir> <id>`, this crate's program, a
//!   few MB with no Tauri and no window, which other apps (Hearth) ship so
//!   a person without Suzuri installed can still keep shells.
//!
//! Its config is JSON on stdin (`cols`, `rows`, `command`, `cwd`, `env`,
//! `scrollback`, `plain_shell`, `cursor`, `term_program`, `keep_prompt`,
//! `preamble`, `palette`, `version`). It prints `ready <pid>` once it serves
//! its socket (or `error ...`), then lives until its shell ends. On Windows
//! it starts itself once more with `--detached` and that one stays.

pub mod codec;
pub mod host;

/// Start `cmd` in its own session: no hangup when the app's terminal or
/// process group goes.
#[cfg(unix)]
pub fn detached(cmd: &mut std::process::Command) -> std::io::Result<std::process::Child> {
    use std::os::unix::process::CommandExt;
    // SAFETY: setsid is async-signal-safe.
    unsafe {
        cmd.pre_exec(|| {
            libc::setsid();
            Ok(())
        });
    }
    cmd.spawn()
}

/// Start `cmd` with no console, in its own process group and out of the
/// app's job (when the job allows it), so ending the app or its job leaves
/// it. The host starts itself once more and that one stays (host::run), so
/// it is not in the app's process tree either: ending suzuri's tree (Task
/// Manager's End task) leaves it.
#[cfg(windows)]
pub fn detached(cmd: &mut std::process::Command) -> std::io::Result<std::process::Child> {
    win::spawn(cmd)
}

#[cfg(windows)]
pub mod win {
    use std::os::windows::process::CommandExt;

    const DETACHED_PROCESS: u32 = 0x0000_0008;
    const CREATE_NEW_PROCESS_GROUP: u32 = 0x0000_0200;
    const CREATE_BREAKAWAY_FROM_JOB: u32 = 0x0100_0000;

    pub fn spawn(cmd: &mut std::process::Command) -> std::io::Result<std::process::Child> {
        cmd.creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_BREAKAWAY_FROM_JOB);
        match cmd.spawn() {
            // The job forbids breaking away: still detached, but in the job.
            Err(e) if e.raw_os_error() == Some(5) => {
                log::warn!(
                    "keeper: the app's job does not let hosts leave it; they end with the job"
                );
                cmd.creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP);
                cmd.spawn()
            }
            r => r,
        }
    }
}
