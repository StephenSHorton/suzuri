//! rust-suzuri's terminal core: no UI, no Tauri. A `Pane` owns one shell
//! (PTY actor + reader + frame thread) and hands binary frames to a sink.

pub mod block;
pub mod cwd;
pub mod echo;
pub mod engine;
pub mod fg;
pub mod frame;
pub mod input;
pub mod images;
pub mod palette;
pub mod pane;
pub mod shell;
pub mod protocols;

pub use engine::{Alacritty, Engine, EngineEvent, Link, SelectKind};
pub use input::{MouseEvent, MouseKind};
pub use pane::{Out, Pane, PaneConfig, Sink, State, Submit};
