//! suzuri-chrome — native GPU shell chrome.
//!
//! Owns the framebuffer (wgpu). No React, no HTML, no Chromium.
//! Layout contract matches the surface spike: traffic / title · tabs · cell well · warp.
//! Terminal cells are painted as mono text labels inside the glass well (mock shell).

mod ansi;
mod app;
mod cells;
mod commands;
mod input;
mod layout;
#[cfg(target_os = "macos")]
mod macos_window;
mod panes;
mod pty;
mod rain_atlas;
mod rain_sim;
mod renderer;
mod session;
mod settings;
mod shell;
mod text;
mod transfer_ui;

use winit::event_loop::EventLoop;

fn main() {
    let event_loop = EventLoop::new().expect("event loop");
    event_loop.set_control_flow(winit::event_loop::ControlFlow::Poll);

    let mut app = app::ChromeApp::default();
    event_loop
        .run_app(&mut app)
        .expect("suzuri-chrome event loop");
}
