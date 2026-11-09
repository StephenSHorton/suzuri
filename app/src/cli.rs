//! The command-line side of the suzuri binary: `suzuri version` (Go's
//! cmd/suzuri/main.go) and, on Windows release builds, the parent console
//! for subcommands that print (Go's internal/winconsole).

/// Whether argv asks for the version (Go accepts all three spellings).
pub fn wants_version(args: &[String]) -> bool {
    matches!(args.get(1).map(String::as_str), Some("version" | "-version" | "--version"))
}

/// Whether the subcommand prints to a terminal and needs its console.
/// Not `mcp`: a host runs it over pipes, which it must keep.
pub fn wants_console(args: &[String]) -> bool {
    matches!(
        args.get(1).map(String::as_str),
        Some("version" | "-version" | "--version" | "notify" | "sound" | "workspace-sync" | "guest" | "chrome")
    )
}

/// Windows release builds have no console (windows_subsystem = "windows"),
/// so println! from a terminal goes nowhere. Attach to the console of the
/// shell that started us and point stdout/stderr at it. A no-op when stdout
/// already works (redirected to a file or pipe, or a debug build) or when
/// there is no parent console (started from Explorer).
#[cfg(windows)]
pub fn attach_parent_console() {
    use windows_sys::Win32::Foundation::{GENERIC_READ, GENERIC_WRITE, HANDLE, INVALID_HANDLE_VALUE};
    use windows_sys::Win32::Storage::FileSystem::{
        CreateFileW, FILE_SHARE_READ, FILE_SHARE_WRITE, FILE_TYPE_CHAR, FILE_TYPE_DISK, FILE_TYPE_PIPE, GetFileType, OPEN_EXISTING,
    };
    use windows_sys::Win32::System::Console::{ATTACH_PARENT_PROCESS, AttachConsole, GetStdHandle, STD_ERROR_HANDLE, STD_OUTPUT_HANDLE, SetStdHandle};

    let usable = |h: HANDLE| !h.is_null() && h != INVALID_HANDLE_VALUE && matches!(unsafe { GetFileType(h) }, FILE_TYPE_DISK | FILE_TYPE_PIPE | FILE_TYPE_CHAR);
    // SAFETY: plain Win32 calls on handles we own or just got.
    unsafe {
        if usable(GetStdHandle(STD_OUTPUT_HANDLE)) {
            return;
        }
        if AttachConsole(ATTACH_PARENT_PROCESS) == 0 {
            return;
        }
        // Rust's stdout looks the handle up on every write, so setting it here
        // is enough. GetStdHandle alone is often still unusable after attaching.
        let name: Vec<u16> = "CONOUT$\0".encode_utf16().collect();
        let out = CreateFileW(name.as_ptr(), GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, std::ptr::null(), OPEN_EXISTING, 0, std::ptr::null_mut());
        if out != INVALID_HANDLE_VALUE {
            let _ = SetStdHandle(STD_OUTPUT_HANDLE, out);
            let _ = SetStdHandle(STD_ERROR_HANDLE, out);
        }
    }
}

/// Elsewhere stdout is the terminal already.
#[cfg(not(windows))]
pub fn attach_parent_console() {}

#[cfg(test)]
mod tests {
    use super::*;

    fn argv(a: &[&str]) -> Vec<String> {
        std::iter::once("suzuri").chain(a.iter().copied()).map(String::from).collect()
    }

    #[test]
    fn version_spellings() {
        for a in ["version", "-version", "--version"] {
            assert!(wants_version(&argv(&[a])), "{a}");
            assert!(wants_console(&argv(&[a])), "{a}");
        }
        assert!(!wants_version(&argv(&[])));
        assert!(!wants_version(&argv(&["-V"])));
        assert!(!wants_version(&argv(&["notify", "version"])));
    }

    #[test]
    fn console_for_printing_subcommands_only() {
        assert!(wants_console(&argv(&["notify"])));
        assert!(wants_console(&argv(&["sound", "fail"])));
        assert!(wants_console(&argv(&["guest"])));
        // mcp keeps the host's pipes; a plain launch opens a window.
        assert!(!wants_console(&argv(&["mcp"])));
        assert!(!wants_console(&argv(&[])));
    }
}
