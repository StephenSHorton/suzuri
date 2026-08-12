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
//! Phase 3 partial: real **session handles** (create / destroy / size / tabs)
//! live behind a process-wide mutex registry. Layout metrics and version
//! probes stay available without a session. Default builds
//! (`cargo build --release`) do **not** include this module.
//!
//! GPU present-in-process is **not** in this ABI — spawn the `suzuri-chrome`
//! binary (see `HOST.md` Phase 1–2) for the product framebuffer.

use std::collections::HashMap;
use std::os::raw::{c_char, c_int, c_uint};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Mutex, OnceLock};

use crate::session::ChromeSession;

/// C ABI version. Bump when breaking exported symbols or layouts.
pub const ABI_VERSION: u32 = 1;

/// Host-owned session: grid size at create time + [`ChromeSession`] state.
struct FfiSession {
    cols: u16,
    rows: u16,
    session: ChromeSession,
}

fn sessions() -> &'static Mutex<HashMap<usize, FfiSession>> {
    static REG: OnceLock<Mutex<HashMap<usize, FfiSession>>> = OnceLock::new();
    REG.get_or_init(|| Mutex::new(HashMap::new()))
}

/// Next non-zero handle id (starts at 1).
static NEXT_HANDLE: AtomicUsize = AtomicUsize::new(1);

fn clamp_dim(v: c_uint) -> u16 {
    if v == 0 {
        1
    } else if v > u16::MAX as c_uint {
        u16::MAX
    } else {
        v as u16
    }
}

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

/// Create a host-owned session handle with one tab / one pane at `cols`×`rows`.
///
/// Dimensions of `0` are clamped to `1`. Returns a **non-zero** opaque handle,
/// or `0` if the registry lock is poisoned.
///
/// Boot banner is not written automatically; call
/// [`suzuri_chrome_session_write_banner`] if the host wants mock shell text.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_create(cols: c_uint, rows: c_uint) -> usize {
    let cols = clamp_dim(cols);
    let rows = clamp_dim(rows);
    let mut map = match sessions().lock() {
        Ok(g) => g,
        Err(_) => return 0,
    };
    let handle = NEXT_HANDLE.fetch_add(1, Ordering::Relaxed);
    // Extremely defensive: skip 0 if the counter ever wraps.
    let handle = if handle == 0 {
        NEXT_HANDLE.fetch_add(1, Ordering::Relaxed)
    } else {
        handle
    };
    map.insert(
        handle,
        FfiSession {
            cols,
            rows,
            session: ChromeSession::new(cols, rows),
        },
    );
    handle
}

/// Destroy a session handle from [`suzuri_chrome_session_create`].
///
/// Unknown / zero handles are a no-op.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_destroy(handle: usize) {
    if handle == 0 {
        return;
    }
    if let Ok(mut map) = sessions().lock() {
        map.remove(&handle);
    }
}

/// Write session grid size into out-params.
///
/// Returns `0` on success, `-1` if the handle is invalid or a pointer is null.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_size(
    handle: usize,
    out_cols: *mut c_uint,
    out_rows: *mut c_uint,
) -> c_int {
    if handle == 0 || out_cols.is_null() || out_rows.is_null() {
        return -1;
    }
    let map = match sessions().lock() {
        Ok(g) => g,
        Err(_) => return -1,
    };
    let Some(s) = map.get(&handle) else {
        return -1;
    };
    unsafe {
        *out_cols = s.cols as c_uint;
        *out_rows = s.rows as c_uint;
    }
    0
}

/// Columns for a session handle, or `0` if invalid.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_cols(handle: usize) -> c_uint {
    if handle == 0 {
        return 0;
    }
    sessions()
        .lock()
        .ok()
        .and_then(|m| m.get(&handle).map(|s| s.cols as c_uint))
        .unwrap_or(0)
}

/// Rows for a session handle, or `0` if invalid.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_rows(handle: usize) -> c_uint {
    if handle == 0 {
        return 0;
    }
    sessions()
        .lock()
        .ok()
        .and_then(|m| m.get(&handle).map(|s| s.rows as c_uint))
        .unwrap_or(0)
}

/// Number of tabs in the session, or `-1` if the handle is invalid.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_tab_count(handle: usize) -> c_int {
    if handle == 0 {
        return -1;
    }
    let map = match sessions().lock() {
        Ok(g) => g,
        Err(_) => return -1,
    };
    match map.get(&handle) {
        Some(s) => s.session.tabs.len() as c_int,
        None => -1,
    }
}

/// Open a new tab (one pane) at the session's create-time size.
///
/// Returns `0` on success, `-1` if the handle is invalid.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_new_tab(handle: usize) -> c_int {
    if handle == 0 {
        return -1;
    }
    let mut map = match sessions().lock() {
        Ok(g) => g,
        Err(_) => return -1,
    };
    let Some(s) = map.get_mut(&handle) else {
        return -1;
    };
    let (cols, rows) = (s.cols, s.rows);
    s.session.new_tab(cols, rows);
    0
}

/// Write the mock boot banner + prompt into the active pane.
///
/// Returns `0` on success, `-1` if the handle is invalid.
#[no_mangle]
pub extern "C" fn suzuri_chrome_session_write_banner(handle: usize) -> c_int {
    if handle == 0 {
        return -1;
    }
    let mut map = match sessions().lock() {
        Ok(g) => g,
        Err(_) => return -1,
    };
    let Some(s) = map.get_mut(&handle) else {
        return -1;
    };
    s.session.boot_mock_on_active();
    0
}

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

/// In-process GPU present loop.
///
/// **Not implemented.** Always returns `-1`. Product path: spawn the
/// `suzuri-chrome` process (see `HOST.md` Phase 1–2 and chromehost bridge proxy).
/// Session handles remain for headless state / metrics experiments only.
#[no_mangle]
pub extern "C" fn suzuri_chrome_present(_handle: usize) -> c_int {
    -1
}

/// Returns 1 when GPU present is available in-process (always 0 today).
#[no_mangle]
pub extern "C" fn suzuri_chrome_present_available() -> c_int {
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
    fn present_not_implemented() {
        assert_eq!(suzuri_chrome_present_available(), 0);
        assert_eq!(suzuri_chrome_present(0), -1);
        let h = suzuri_chrome_session_create(40, 12);
        assert_eq!(suzuri_chrome_present(h), -1);
        suzuri_chrome_session_destroy(h);
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

    #[test]
    fn session_create_destroy_tab_count() {
        let h = suzuri_chrome_session_create(80, 24);
        assert_ne!(h, 0, "create must return a non-zero handle");
        assert_eq!(suzuri_chrome_session_tab_count(h), 1);
        assert_eq!(suzuri_chrome_session_cols(h), 80);
        assert_eq!(suzuri_chrome_session_rows(h), 24);

        let mut cols = 0u32;
        let mut rows = 0u32;
        assert_eq!(suzuri_chrome_session_size(h, &mut cols, &mut rows), 0);
        assert_eq!(cols, 80);
        assert_eq!(rows, 24);

        assert_eq!(suzuri_chrome_session_new_tab(h), 0);
        assert_eq!(suzuri_chrome_session_tab_count(h), 2);

        assert_eq!(suzuri_chrome_session_write_banner(h), 0);

        suzuri_chrome_session_destroy(h);
        assert_eq!(suzuri_chrome_session_tab_count(h), -1);
        assert_eq!(suzuri_chrome_session_cols(h), 0);
        assert_eq!(suzuri_chrome_session_new_tab(h), -1);
        assert_eq!(suzuri_chrome_session_write_banner(h), -1);
    }

    #[test]
    fn session_bad_handle() {
        assert_eq!(suzuri_chrome_session_tab_count(0), -1);
        assert_eq!(suzuri_chrome_session_tab_count(999_999_999), -1);
        assert_eq!(suzuri_chrome_session_new_tab(0), -1);
        assert_eq!(suzuri_chrome_session_cols(0), 0);
        assert_eq!(suzuri_chrome_session_rows(0), 0);
        let mut c = 1u32;
        let mut r = 1u32;
        assert_eq!(suzuri_chrome_session_size(0, &mut c, &mut r), -1);
        // destroy of unknown is safe
        suzuri_chrome_session_destroy(0);
        suzuri_chrome_session_destroy(42);
    }

    #[test]
    fn session_create_clamps_zero_dims() {
        let h = suzuri_chrome_session_create(0, 0);
        assert_ne!(h, 0);
        assert_eq!(suzuri_chrome_session_cols(h), 1);
        assert_eq!(suzuri_chrome_session_rows(h), 1);
        suzuri_chrome_session_destroy(h);
    }

    #[test]
    fn is_ready_and_version() {
        assert_eq!(suzuri_chrome_is_ready(), 1);
        let v = suzuri_chrome_version();
        assert!(!v.is_null());
        let s = unsafe { std::ffi::CStr::from_ptr(v) };
        assert!(!s.to_bytes().is_empty());
    }
}
