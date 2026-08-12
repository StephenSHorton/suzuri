//! Optional C ABI surface for embedding from Go (cgo) or other hosts.
//!
//! Enable with:
//!
//! ```bash
//! cargo build --release --features ffi
//! # produce a staticlib for cgo (example):
//! cargo rustc --release --features ffi --crate-type staticlib
//! ```
//!
//! These are **stubs** that pin symbol names and calling conventions. Default
//! builds (`cargo build --release`) do **not** include this module.
//!
//! Production path today: spawn the `suzuri-chrome` binary (see `HOST.md`).
//! In-process embed lands after the spawn integration proves layout/PTY IPC.

use std::os::raw::{c_char, c_int, c_uint};

/// C ABI version. Bump when breaking exported symbols or layouts.
pub const ABI_VERSION: u32 = 1;

/// Returns the C ABI version. Always safe to call.
#[no_mangle]
pub extern "C" fn suzuri_chrome_abi_version() -> c_uint {
    ABI_VERSION
}

/// Crate package version as a NUL-terminated UTF-8 C string (static lifetime).
#[no_mangle]
pub extern "C" fn suzuri_chrome_version() -> *const c_char {
    // CARGO_PKG_VERSION is ASCII digits/dots — safe as CStr.
    concat!(env!("CARGO_PKG_VERSION"), "\0").as_ptr() as *const c_char
}

/// Returns 1 when the library was built with the `ffi` feature (always true here).
#[no_mangle]
pub extern "C" fn suzuri_chrome_is_ready() -> c_int {
    1
}

/// Placeholder: create a host-owned session handle.
///
/// Returns `0` until the embed path is wired. Hosts should spawn
/// `suzuri-chrome` (process) per `HOST.md` Phase 1–2.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_create(_cols: c_uint, _rows: c_uint) -> usize {
    0
}

/// Placeholder: destroy a session handle from [`suzuri_chrome_session_create`].
///
/// No-op while create returns null handles.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_destroy(_handle: usize) {}

/// Write default layout metrics (logical CSS-px) into out-params.
///
/// * `out_title_h` — chrome title / tab bar height
/// * `out_tab_h` — reserved tab strip (0 when tabs live in the title bar)
/// * `out_edge` — window edge inset
/// * `out_input_strip_h` — warp / input strip inside the glass well
///
/// Returns `0` on success, `-1` if any pointer is null.
///
/// `win_w` / `win_h` are reserved for size-dependent layout; currently ignored.
#[no_mangle]
pub extern "C" fn suzuri_chrome_layout_metrics(
    _win_w: f32,
    _win_h: f32,
    out_title_h: *mut f32,
    out_tab_h: *mut f32,
    out_edge: *mut f32,
    out_input_strip_h: *mut f32,
) -> c_int {
    if out_title_h.is_null()
        || out_tab_h.is_null()
        || out_edge.is_null()
        || out_input_strip_h.is_null()
    {
        return -1;
    }
    let m = crate::layout::Metrics::default();
    unsafe {
        *out_title_h = m.title_h;
        *out_tab_h = m.tab_h;
        *out_edge = m.edge();
        *out_input_strip_h = m.input_strip_h;
    }
    0
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn abi_version_nonzero() {
        assert!(suzuri_chrome_abi_version() >= 1);
    }

    #[test]
    fn layout_metrics_ok() {
        let mut title = 0.0f32;
        let mut tab = 0.0f32;
        let mut edge = 0.0f32;
        let mut strip = 0.0f32;
        let rc = suzuri_chrome_layout_metrics(
            1280.0,
            800.0,
            &mut title,
            &mut tab,
            &mut edge,
            &mut strip,
        );
        assert_eq!(rc, 0);
        assert!(title > 0.0);
        assert!(edge > 0.0);
        assert!(strip > 0.0);
    }

    #[test]
    fn layout_metrics_null_fails() {
        let rc = suzuri_chrome_layout_metrics(
            0.0,
            0.0,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
        );
        assert_eq!(rc, -1);
    }
}
