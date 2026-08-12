//! suzuri-chrome as a library — host-merge surface.
//!
//! The binary (`main.rs` / `app` / `renderer` / `text`) is the standalone GPU
//! window. Downstream hosts (Rust crate, or Go via process spawn / optional C
//! ABI) depend on these modules for protocol + state:
//!
//! | Module | Role |
//! |--------|------|
//! | [`layout`] | Geometry contract (title / tabs / terminal hole / warp) |
//! | [`cells`] | Cell grid buffer + inkstone theme |
//! | [`ansi`] | VT decoder into a grid |
//! | [`selection`] | Cell drag selection + copy text extraction |
//! | [`pty`] | Portable local shell PTY |
//! | [`session`] | Multi-tab session (grids; PTY map owned by host) |
//! | [`panes`] | Split-pane tree + jelly animation state |
//! | [`input`] | Hit-test for chrome affordances |
//! | [`settings`] | Settings overlay state + prefs |
//! | [`config_store`] | `chrome_prefs.json` (not product `config.json`) |
//! | [`control_mailbox`] | Phase 2 light IPC (`chrome_cmd` file) |
//! | [`commands`] | Palette / shortcuts registry |
//! | [`shell`] | Mock shell helpers (fallback when no PTY) |
//! | [`notes`] / [`notes_ops`] | Multi-note bank + product-compatible `notes.json` |
//!
//! Binary-only (not exported here): `renderer`, `text`, rain, caffeine, workspace
//! and transfer UI — wire those via the `suzuri-chrome` process or later exports.
//!
//! See [`HOST.md`](../HOST.md) for Go embed / spawn phases and `surface/`
//! replacement. Terminal mouse/OSC: `TERMINAL_HOOKS.md`. Settings: `SETTINGS_HOOKS.md`.

#![doc(html_no_source)]

pub mod ansi;
pub mod cells;
pub mod chrome_ui;
pub mod commands;
pub mod config_store;
pub mod control_mailbox;
pub mod input;
pub mod layout;
pub mod notes;
pub mod notes_ops;
pub mod panes;
pub mod pty;
pub mod selection;
pub mod session;
pub mod settings;
pub mod shell;

/// Optional C ABI stubs for cgo / static link. Enable with `--features ffi`.
#[cfg(feature = "ffi")]
pub mod ffi;

// ── Crate-root re-exports (host convenience) ────────────────────────────────

pub use ansi::AnsiDecoder;
pub use cells::{theme as cell_theme, Cell, CellGrid, Cursor};
pub use commands::{
    default_commands, filter_commands, Command, CommandAction, HelpState, PaletteState,
};
pub use control_mailbox::{
    chrome_cmd_path, mailbox_config_dir, ControlCommand, ControlMailbox, CHROME_CMD_FILE,
    POLL_INTERVAL,
};
pub use input::{hit_test, is_mac, traffic_light_rects, HitTarget};
pub use layout::{FrameLayout, Metrics, PaneLayout, PanelInstance, PanelKind, Rect, Spacing};
pub use panes::{FocusDir, RemoveResult, SplitAxis, SplitNode};
pub use pty::PtySession;
pub use selection::Selection;
pub use session::{ChromeSession, CloseOutcome, Pane, Tab};
pub use settings::{ChromePrefs, SettingsState, GLASS_DARKEN_DEFAULT};
pub use shell::{ShellOutput, PROMPT_GLYPH};

/// Semver of this crate (`Cargo.toml` package version).
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

/// Host-merge API level for Rust consumers (independent of package patch).
/// Bump when removing/renaming public modules or re-exports.
pub const HOST_API: u32 = 1;
