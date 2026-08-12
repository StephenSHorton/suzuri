//! suzuri-chrome — native GPU shell chrome.
//!
//! Owns the framebuffer (wgpu). No React, no HTML, no Chromium.
//! Layout contract matches the surface spike: traffic / title · tabs · cell well · warp.
//! Terminal cells are painted as mono text labels inside the glass well (live PTY).

mod ansi;
mod app;
mod caffeine;
mod cells;
mod chrome_ui;
mod commands;
mod config_store;
mod control_mailbox;
mod input;
mod layout;
#[cfg(target_os = "macos")]
mod macos_window;
mod notes;
mod notes_ops;
mod panes;
mod pty;
mod rain_atlas;
mod rain_sim;
mod renderer;
mod selection;
mod session;
mod settings;
mod shell;
mod text;
mod theme;
mod transfer_ui;
mod workspace_store;
mod workspace_ui;

use winit::event_loop::EventLoop;

fn main() {
    let event_loop = EventLoop::new().expect("event loop");
    event_loop.set_control_flow(winit::event_loop::ControlFlow::Poll);

    let mut app = app::ChromeApp::default();
    event_loop
        .run_app(&mut app)
        .expect("suzuri-chrome event loop");
}
