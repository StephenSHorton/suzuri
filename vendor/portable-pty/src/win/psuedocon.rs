use super::WinChild;
use crate::cmdbuilder::CommandBuilder;
use crate::win::procthreadattr::ProcThreadAttributeList;
use anyhow::{bail, ensure, Error};
use filedescriptor::{FileDescriptor, OwnedHandle};
use lazy_static::lazy_static;
use shared_library::shared_library;
use std::ffi::OsString;
use std::io::Error as IoError;
use std::os::windows::ffi::OsStringExt;
use std::os::windows::io::{AsRawHandle, FromRawHandle};
use std::path::Path;
use std::sync::Mutex;
use std::{mem, ptr};
use winapi::shared::minwindef::DWORD;
use winapi::shared::winerror::{HRESULT, S_OK};
use winapi::um::handleapi::*;
use winapi::um::processthreadsapi::*;
use winapi::um::winbase::{
    CREATE_UNICODE_ENVIRONMENT, EXTENDED_STARTUPINFO_PRESENT, STARTF_USESTDHANDLES, STARTUPINFOEXW,
};
use winapi::um::wincon::COORD;
use winapi::um::winnt::HANDLE;

pub type HPCON = HANDLE;

#[allow(dead_code)]
pub const PSUEDOCONSOLE_INHERIT_CURSOR: DWORD = 0x1;
#[allow(dead_code)]
pub const PSEUDOCONSOLE_RESIZE_QUIRK: DWORD = 0x2;
#[allow(dead_code)]
pub const PSEUDOCONSOLE_WIN32_INPUT_MODE: DWORD = 0x4;
#[allow(dead_code)]
pub const PSEUDOCONSOLE_PASSTHROUGH_MODE: DWORD = 0x8;

shared_library!(ConPtyFuncs,
    pub fn CreatePseudoConsole(
        size: COORD,
        hInput: HANDLE,
        hOutput: HANDLE,
        flags: DWORD,
        hpc: *mut HPCON
    ) -> HRESULT,
    pub fn ResizePseudoConsole(hpc: HPCON, size: COORD) -> HRESULT,
    pub fn ClosePseudoConsole(hpc: HPCON),
);

fn load_conpty() -> ConPtyFuncs {
    // If the kernel doesn't export these functions then their system is
    // too old and we cannot run.
    let kernel = ConPtyFuncs::open(Path::new("kernel32.dll")).expect(
        "this system does not support conpty.  Windows 10 October 2018 or newer is required",
    );

    // We prefer to use a sideloaded conpty.dll and openconsole.exe host deployed
    // alongside the application.  We check for this after checking for kernel
    // support so that we don't try to proceed and do something crazy.
    if let Ok(sideloaded) = ConPtyFuncs::open(Path::new("conpty.dll")) {
        SIDELOADED.store(true, std::sync::atomic::Ordering::Relaxed);
        sideloaded
    } else {
        kernel
    }
}

static SIDELOADED: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

lazy_static! {
    static ref CONPTY: ConPtyFuncs = load_conpty();
}

pub struct PsuedoCon {
    con: HPCON,
}

unsafe impl Send for PsuedoCon {}
unsafe impl Sync for PsuedoCon {}

impl Drop for PsuedoCon {
    fn drop(&mut self) {
        unsafe { (CONPTY.ClosePseudoConsole)(self.con) };
    }
}

impl PsuedoCon {
    pub fn new(size: COORD, input: FileDescriptor, output: FileDescriptor) -> Result<Self, Error> {
        let mut con: HPCON = INVALID_HANDLE_VALUE;
        // rust-suzuri (#288): Go suzuri's flags, 0. portable-pty's
        // defaults (INHERIT_CURSOR | RESIZE_QUIRK | WIN32_INPUT_MODE) broke
        // the bundled OpenConsole: with WIN32_INPUT_MODE a raw 0x03 is a
        // character, not Ctrl+C, and INHERIT_CURSOR's ESC[6n start put
        // ConPTY's rows one off from ours. RSZ_CONPTY_FLAGS=<n> (0x.. or
        // decimal) overrides them for A/B runs.
        let base = std::env::var("RSZ_CONPTY_FLAGS")
            .ok()
            .and_then(|v| {
                let v = v.trim();
                match v.strip_prefix("0x").or_else(|| v.strip_prefix("0X")) {
                    Some(hex) => DWORD::from_str_radix(hex, 16).ok(),
                    None => v.parse::<DWORD>().ok(),
                }
            })
            .unwrap_or(0);
        // Passthrough mode is opt-in (RSZ_CONPTY_PASSTHROUGH=1): it changed
        // nothing on the inbox conhost or OpenConsole 1.25, so it stays off
        // rather than switch on untested under a later Windows. A Windows
        // that refuses the flag gets the base flags.
        let passthrough = std::env::var("RSZ_CONPTY_PASSTHROUGH").map_or(false, |v| v == "1");
        let create = |flags: DWORD, con: &mut HPCON| unsafe {
            (CONPTY.CreatePseudoConsole)(size, input.as_raw_handle() as _, output.as_raw_handle() as _, flags, con)
        };
        let mut flags = if passthrough { base | PSEUDOCONSOLE_PASSTHROUGH_MODE } else { base };
        let mut result = create(flags, &mut con);
        if passthrough && result != S_OK {
            con = INVALID_HANDLE_VALUE;
            flags = base;
            result = create(flags, &mut con);
        }
        if std::env::var_os("RSZ_TRACE").is_some() {
            let dll = if SIDELOADED.load(std::sync::atomic::Ordering::Relaxed) { "conpty.dll" } else { "kernel32" };
            eprintln!("rsz: conpty {dll} flags={flags:#x} hr={result:#x}");
        }
        ensure!(
            result == S_OK,
            "failed to create psuedo console: HRESULT {}",
            result
        );
        Ok(Self { con })
    }

    pub fn resize(&self, size: COORD) -> Result<(), Error> {
        let result = unsafe { (CONPTY.ResizePseudoConsole)(self.con, size) };
        ensure!(
            result == S_OK,
            "failed to resize console to {}x{}: HRESULT: {}",
            size.X,
            size.Y,
            result
        );
        Ok(())
    }

    pub fn spawn_command(&self, cmd: CommandBuilder) -> anyhow::Result<WinChild> {
        let mut si: STARTUPINFOEXW = unsafe { mem::zeroed() };
        si.StartupInfo.cb = mem::size_of::<STARTUPINFOEXW>() as u32;
        // Explicitly set the stdio handles as invalid handles otherwise
        // we can end up with a weird state where the spawned process can
        // inherit the explicitly redirected output handles from its parent.
        // For example, when daemonizing wezterm-mux-server, the stdio handles
        // are redirected to a log file and the spawned process would end up
        // writing its output there instead of to the pty we just created.
        si.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
        si.StartupInfo.hStdInput = INVALID_HANDLE_VALUE;
        si.StartupInfo.hStdOutput = INVALID_HANDLE_VALUE;
        si.StartupInfo.hStdError = INVALID_HANDLE_VALUE;

        let mut attrs = ProcThreadAttributeList::with_capacity(1)?;
        attrs.set_pty(self.con)?;
        si.lpAttributeList = attrs.as_mut_ptr();

        let mut pi: PROCESS_INFORMATION = unsafe { mem::zeroed() };

        let (mut exe, mut cmdline) = cmd.cmdline()?;
        let cmd_os = OsString::from_wide(&cmdline);

        let cwd = cmd.current_directory();

        let res = unsafe {
            CreateProcessW(
                exe.as_mut_slice().as_mut_ptr(),
                cmdline.as_mut_slice().as_mut_ptr(),
                ptr::null_mut(),
                ptr::null_mut(),
                0,
                EXTENDED_STARTUPINFO_PRESENT | CREATE_UNICODE_ENVIRONMENT,
                cmd.environment_block().as_mut_slice().as_mut_ptr() as *mut _,
                cwd.as_ref()
                    .map(|c| c.as_slice().as_ptr())
                    .unwrap_or(ptr::null()),
                &mut si.StartupInfo,
                &mut pi,
            )
        };
        if res == 0 {
            let err = IoError::last_os_error();
            let msg = format!(
                "CreateProcessW `{:?}` in cwd `{:?}` failed: {}",
                cmd_os,
                cwd.as_ref().map(|c| OsString::from_wide(c)),
                err
            );
            log::error!("{}", msg);
            bail!("{}", msg);
        }

        // Make sure we close out the thread handle so we don't leak it;
        // we do this simply by making it owned
        let _main_thread = unsafe { OwnedHandle::from_raw_handle(pi.hThread as _) };
        let proc = unsafe { OwnedHandle::from_raw_handle(pi.hProcess as _) };

        Ok(WinChild {
            proc: Mutex::new(proc),
        })
    }
}
