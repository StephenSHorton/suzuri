//! Windows: a bare Alt tap (or F10) must not start the window's menu loop
//! (hearth#979). Left to DefWindowProc, the key's release becomes
//! WM_SYSCOMMAND SC_KEYMENU with lParam 0, and Windows runs its modal menu
//! loop on the UI thread until Esc or a click. The window has no menu bar
//! (the tab strip is the title bar), so the loop shows nothing, yet while it
//! runs the AI control bridge's `aicontrol` calls get no answer (504 after
//! 5 s). We swallow just that message. Alt+Space (lParam ' ') still opens the
//! system menu, and the webview still sees every Alt key, so Alt shortcuts
//! (ui/shortcuts.js) and Alt as a terminal Meta key are untouched.

/// WM_SYSCOMMAND and SC_KEYMENU, as in WinUser.h.
#[cfg_attr(not(windows), allow(dead_code))]
const WM_SYSCOMMAND: u32 = 0x0112;
#[cfg_attr(not(windows), allow(dead_code))]
const SC_KEYMENU: usize = 0xF100;

/// Whether the main window drops this message: a keyboard menu request with
/// no menu key (a bare Alt or F10). The low four bits of wParam are the
/// system's own, so they are masked off.
#[cfg_attr(not(windows), allow(dead_code))]
pub fn swallows(msg: u32, wparam: usize, lparam: isize) -> bool {
    msg == WM_SYSCOMMAND && wparam & 0xFFF0 == SC_KEYMENU && lparam == 0
}

/// Install on the main window. Call from setup (the main thread).
#[cfg(windows)]
pub fn install(app: &tauri::AppHandle) {
    use tauri::Manager;
    let Some(hwnd) = app.get_webview_window("main").and_then(|w| w.hwnd().ok()) else {
        log::warn!("alt menu: no main window");
        return;
    };
    if !win::subclass(hwnd.0 as _) {
        log::warn!("alt menu: SetWindowSubclass failed; a bare Alt tap will start the menu loop");
    }
}

#[cfg(not(windows))]
pub fn install(_app: &tauri::AppHandle) {}

#[cfg(windows)]
mod win {
    use windows_sys::Win32::Foundation::{HWND, LPARAM, LRESULT, WPARAM};
    use windows_sys::Win32::UI::Shell::{DefSubclassProc, RemoveWindowSubclass, SetWindowSubclass};
    use windows_sys::Win32::UI::WindowsAndMessaging::WM_NCDESTROY;

    /// Our subclass id ("rszA"), so it never clashes with tao's.
    const ID: usize = 0x7273_7A41;

    pub fn subclass(hwnd: HWND) -> bool {
        // SAFETY: hwnd is the live main window, and this runs on its thread.
        unsafe { SetWindowSubclass(hwnd, Some(proc_), ID, 0) != 0 }
    }

    unsafe extern "system" fn proc_(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM, _id: usize, _data: usize) -> LRESULT {
        if super::swallows(msg, wparam, lparam) {
            return 0;
        }
        if msg == WM_NCDESTROY {
            // SAFETY: the subclass we set on this window, on its thread.
            unsafe { RemoveWindowSubclass(hwnd, Some(proc_), ID) };
        }
        // SAFETY: passes the message on down the chain unchanged.
        unsafe { DefSubclassProc(hwnd, msg, wparam, lparam) }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn swallows_only_a_bare_alt_or_f10() {
        // A bare Alt tap or F10: lParam 0, low bits may be set by Windows.
        assert!(swallows(WM_SYSCOMMAND, SC_KEYMENU, 0));
        assert!(swallows(WM_SYSCOMMAND, SC_KEYMENU | 0x3, 0));
        // Alt+Space opens the system menu; Alt+letter would open a menu.
        assert!(!swallows(WM_SYSCOMMAND, SC_KEYMENU, b' ' as isize));
        assert!(!swallows(WM_SYSCOMMAND, SC_KEYMENU, b'f' as isize));
        // Other system commands (close, minimise, mouse menu) pass.
        assert!(!swallows(WM_SYSCOMMAND, 0xF060, 0)); // SC_CLOSE
        assert!(!swallows(WM_SYSCOMMAND, 0xF020, 0)); // SC_MINIMIZE
        assert!(!swallows(WM_SYSCOMMAND, 0xF090, 0)); // SC_MOUSEMENU
        assert!(!swallows(0x0105, SC_KEYMENU, 0)); // WM_SYSKEYUP
    }
}
