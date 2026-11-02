//! rust-suzuri's terminal core: no UI, no Tauri. A `Pane` owns one shell
//! (PTY actor + reader + frame thread) and hands binary frames to a sink.

pub mod engine;
pub mod frame;
pub mod palette;
pub mod pane;

pub use engine::{Alacritty, Engine, EngineEvent};
pub use pane::{Out, Pane, PaneConfig, Sink, State};
