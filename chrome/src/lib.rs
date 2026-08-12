//! suzuri-chrome as a library — host-merge surface.
//!
//! The binary (`main.rs` / `app`) is the standalone window. Downstream hosts
//! (future Rust host or C-ABI from Go) can depend on these modules:
//!
//! - [`layout`] — geometry contract (title / tabs / terminal hole / warp)
//! - [`cells`] — cell grid buffer
//! - [`ansi`] — VT decoder into a grid
//! - [`pty`] — portable local shell PTY
//! - [`session`] — multi-tab session (grids only; PTY map owned by host)
//! - [`input`] — hit-test
//! - [`settings`] — settings overlay state
//! - [`notes`] / [`notes_ops`] — multi-note bank + product-compatible `notes.json`
//!
//! Rendering (`renderer`, `text`, `app`) stays binary-only for now so library
//! consumers can plug their own present loop.

pub mod ansi;
pub mod cells;
pub mod chrome_ui;
pub mod commands;
pub mod input;
pub mod layout;
pub mod notes;
pub mod notes_ops;
pub mod panes;
pub mod pty;
pub mod session;
pub mod settings;
pub mod shell;
