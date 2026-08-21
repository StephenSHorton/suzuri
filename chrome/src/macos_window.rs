//! macOS window chrome — rounded corners + transparent frame.
//!
//! Frameless `winit` windows are square by default. We clip the content view's
//! CALayer and keep the window non-opaque so the system shadow follows the
//! rounded alpha silhouette.

#![cfg(target_os = "macos")]

use raw_window_handle::{HasWindowHandle, RawWindowHandle};
use winit::platform::macos::WindowExtMacOS;
use winit::window::Window;

use crate::layout::Rect;

/// Apply macOS-native rounded corners (points) and transparent backdrop.
///
/// `radius_pts` is in logical points (CSS-px style), matching AppKit.
///
/// Do not toggle `NSWindow.styleMask` from the present path. winit 0.30
/// `is_maximized` does that on borderless windows; Tahoe then rebuilds
/// the glass titlebar every frame.
pub fn configure_rounded_window(window: &Window, radius_pts: f64) {
    // System drop shadow under the rounded silhouette.
    window.set_has_shadow(true);

    let Ok(handle) = window.window_handle() else {
        return;
    };
    let RawWindowHandle::AppKit(appkit) = handle.as_raw() else {
        return;
    };

    // SAFETY: ns_view is valid for the lifetime of the winit Window.
    unsafe {
        use objc2::rc::Retained;
        use objc2_app_kit::{NSColor, NSView};

        let ns_view = appkit.ns_view.as_ptr() as *const NSView;
        if ns_view.is_null() {
            return;
        }
        // Borrow the view without transferring ownership.
        let view: &NSView = &*ns_view;

        view.setWantsLayer(true);
        if let Some(layer) = view.layer() {
            layer.setCornerRadius(radius_pts);
            layer.setMasksToBounds(true);
        }

        if let Some(ns_window) = view.window() {
            ns_window.setOpaque(false);
            // Fully clear windows force WindowServer to sample GPU alpha
            // before delivering mouseDown — clicks wait on the next present.
            // A near-zero fill keeps the silhouette hittable immediately.
            let bg = NSColor::colorWithWhite_alpha(0.0, 0.001);
            ns_window.setBackgroundColor(Some(&bg));
            ns_window.setAcceptsMouseMovedEvents(true);
            ns_window.setHasShadow(true);
            ns_window.invalidateShadow();
            // Keep a local retain so the temporary doesn't drop mid-call.
            let _keep: Retained<objc2_app_kit::NSWindow> = ns_window;
        }
    }
}

/// Fade the whole OS window (chrome + content + shadow) for last-tab close.
pub fn set_window_alpha(window: &Window, alpha: f64) {
    let Ok(handle) = window.window_handle() else {
        return;
    };
    let RawWindowHandle::AppKit(appkit) = handle.as_raw() else {
        return;
    };
    unsafe {
        use objc2::rc::Retained;
        use objc2_app_kit::NSView;

        let ns_view = appkit.ns_view.as_ptr() as *const NSView;
        if ns_view.is_null() {
            return;
        }
        let view: &NSView = &*ns_view;
        if let Some(ns_window) = view.window() {
            let a = alpha.clamp(0.0, 1.0);
            // Tick_world used to set 1.0 every 8 ms. Unchanged alpha is a no-op
            // for the pixels, but AppKit still dirties the theme frame.
            if (ns_window.alphaValue() - a).abs() < 0.002 {
                let _keep: Retained<objc2_app_kit::NSWindow> = ns_window;
                return;
            }
            ns_window.setAlphaValue(a);
            let _keep: Retained<objc2_app_kit::NSWindow> = ns_window;
        }
    }
}

/// AppKit `windowNumber` for the winit window, or `None` if the handle is gone.
pub fn window_number(window: &Window) -> Option<i64> {
    ns_window(window).map(|w| unsafe { w.windowNumber() as i64 })
}

/// Map a top-left logical content rect (suzuri layout) to AppKit screen
/// coordinates (bottom-left origin, points).
///
/// Uses the window's content rect — not the wgpu NSView — so a flipped Metal
/// layer cannot shrink or offset the hole.
pub fn content_rect_to_screen(window: &Window, rect: Rect) -> Option<Rect> {
    if rect.w <= 0.0 || rect.h <= 0.0 {
        return None;
    }
    let ns_window = ns_window(window)?;
    let content = ns_window.contentRectForFrameRect(ns_window.frame());
    let x = content.origin.x + rect.x as f64;
    let y = content.origin.y + (content.size.height - rect.y as f64 - rect.h as f64);
    Some(Rect::new(
        x as f32,
        y as f32,
        rect.w,
        rect.h,
    ))
}

fn ns_window(window: &Window) -> Option<objc2::rc::Retained<objc2_app_kit::NSWindow>> {
    let Ok(handle) = window.window_handle() else {
        return None;
    };
    let RawWindowHandle::AppKit(appkit) = handle.as_raw() else {
        return None;
    };
    unsafe {
        use objc2_app_kit::NSView;
        let ns_view = appkit.ns_view.as_ptr() as *const NSView;
        if ns_view.is_null() {
            return None;
        }
        let view: &NSView = &*ns_view;
        view.window()
    }
}
