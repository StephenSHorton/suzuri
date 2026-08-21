//! suzuri-chrome — native GPU shell chrome.
//!
//! Owns the framebuffer (wgpu). No React, no HTML, no Chromium.
//! Layout contract matches the surface spike: traffic / title · tabs · cell well · warp.
//! Terminal cells are painted as mono text labels inside the glass well (live PTY).

// Dual bin/lib crate: many helpers are host/API surface used by the library or
// future hosts; the binary path does not exercise every method.
#![allow(dead_code)]
// Release GUI: no spare console when launched from Start Menu / Store / a
// windowsgui host. Debug keeps a console for `cargo run` logs.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod ansi;
mod app;
mod caffeine;
mod cells;
mod chrome_status;
mod chrome_ui;
mod cmd_blocks;
mod commands;
mod config_store;
mod confirm;
mod control_mailbox;
mod draft;
mod echo_filter;
mod eco;
mod guest_fb;
mod guest_host;
mod guest_install;
#[cfg(target_os = "macos")]
mod guest_iosurface;
mod guest_manifest;
mod guest_ui;
mod input;
mod kitty;
mod kitty_gfx;
mod layout;
mod links;
#[cfg(target_os = "macos")]
mod macos_window;
mod mouse_pty;
mod new_window;
mod notes;
mod notes_ops;
mod panes;
mod pty;
mod rain_atlas;
mod rain_sim;
mod rain_thread;
mod rename;
mod renderer;
mod selection;
mod session;
mod settings;
mod shell;
mod sync_hold;
mod text;
mod theme;
mod toast;
mod transfer_ui;
mod updater;
mod workspace_store;
mod workspace_sync;
mod workspace_ui;

use winit::event_loop::EventLoop;

fn main() {
    session::normalize_process_cwd();
    let event_loop = EventLoop::new().expect("event loop");
    // Wait (not Poll): wake on input or a short timer. Continuous Poll + full
    // GPU frames starved keyboard repeat and made typing feel laggy.
    event_loop.set_control_flow(winit::event_loop::ControlFlow::Wait);

    let mut app = app::ChromeApp::default();
    event_loop
        .run_app(&mut app)
        .expect("suzuri-chrome event loop");
}
