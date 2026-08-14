//! macOS window chrome — rounded corners + transparent frame.
//!
//! Frameless `winit` windows are square by default. We clip the content view's
//! CALayer and keep the window non-opaque so the system shadow follows the
//! rounded alpha silhouette.

#![cfg(target_os = "macos")]

use raw_window_handle::{HasWindowHandle, RawWindowHandle};
use winit::platform::macos::WindowExtMacOS;
use winit::window::Window;

/// Apply macOS-native rounded corners (points) and transparent backdrop.
///
/// `radius_pts` is in logical points (CSS-px style), matching AppKit.
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
            ns_window.setBackgroundColor(Some(&NSColor::clearColor()));
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
            ns_window.setAlphaValue(alpha.clamp(0.0, 1.0));
            let _keep: Retained<objc2_app_kit::NSWindow> = ns_window;
        }
    }
}
