//! suzuri-chrome as a library — host-merge surface.
//!
//! The binary (`main.rs` / `app` / `renderer` / `text`) is the standalone GPU
//! window. Downstream hosts (Rust crate, or Go via process spawn / optional C
//! ABI) depend on these modules for protocol + state:
//!
//! | Module | Role |
//! |--------|------|
//! | [`layout`] | Geometry contract (title / tabs / terminal hole / warp) |
//! | [`cells`] | Cell grid buffer + inkstone VT defaults |
//! | [`theme`] | Named chrome paint palettes (bg/fg/jade/muted) |
//! | [`ansi`] | VT decoder into a grid |
//! | [`selection`] | Cell drag selection + copy text extraction |
//! | [`links`] | Terminal URL detect / normalize / open-in-browser |
//! | [`pty`] | Portable local shell PTY |
//! | [`session`] | Multi-tab session (grids; PTY map owned by host) |
//! | [`panes`] | Split-pane tree + jelly animation state |
//! | [`input`] | Hit-test for chrome affordances |
//! | [`settings`] | Settings overlay state + prefs |
//! | [`config_store`] | `chrome_prefs.json` + `SUZURI_CONFIG_DIR` |
//! | [`control_mailbox`] | Phase 2 light IPC (`chrome_cmd` file) |
//! | [`commands`] | Palette / shortcuts registry |
//! | [`confirm`] | Crush-style yes/no confirm (quit) |
//! | [`new_window`] | Spawn a second OS window (new process) |
//! | [`rename`] | Tab / pane rename dialog |
//! | [`toast`] | Ephemeral frost-chip status ("Copied") |
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
pub mod chrome_status;
pub mod chrome_ui;
pub mod commands;
pub mod confirm;
pub mod config_store;
pub mod control_mailbox;
pub mod input;
pub mod layout;
pub mod links;
pub mod new_window;
pub mod notes;
pub mod notes_ops;
pub mod panes;
pub mod pty;
pub mod rename;
pub mod selection;
pub mod session;
pub mod settings;
pub mod shell;
pub mod theme;
pub mod toast;

/// Optional C ABI for cgo / static link (session handles + metrics).
/// Enable with `--features ffi`.
#[cfg(feature = "ffi")]
pub mod ffi;

// ── Crate-root re-exports (host convenience) ────────────────────────────────

pub use ansi::AnsiDecoder;
pub use cells::{theme as cell_theme, Cell, CellGrid, Cursor};
pub use commands::{
    default_commands, filter_commands, splash_hint_rows, Command, CommandAction, HelpState,
    PaletteState, SplashState,
};
pub use chrome_status::{
    clear_status, publish_status, status_path, submit_path, take_submit, StatusPublisher,
    PUBLISH_INTERVAL, STATUS_FILE, SUBMIT_FILE,
};
pub use confirm::{ConfirmChoice, ConfirmState};
pub use config_store::{chrome_prefs_path, product_config_dir, ENV_CONFIG_DIR};
pub use control_mailbox::{
    chrome_cmd_path, mailbox_config_dir, ControlCommand, ControlMailbox, CHROME_CMD_FILE,
    POLL_INTERVAL,
};
pub use input::{hit_test, is_mac, traffic_light_rects, HitTarget};
pub use layout::{FrameLayout, Metrics, PaneLayout, PanelInstance, PanelKind, Rect, Spacing};
pub use links::{
    clean_url, find_links_in_line, link_at, link_span_at_col, link_url_at_col, normalize_url,
    open_url_in_browser, LinkHoverSpan, LinkSpan,
};
pub use new_window::{canonicalize_exe, resolve_self_exe, spawn_new_window};
pub use panes::{FocusDir, RemoveResult, SplitAxis, SplitNode};
pub use pty::PtySession;
pub use rename::{RenameState, RenameTarget};
pub use selection::Selection;
pub use session::{ChromeSession, CloseOutcome, Pane, Tab};
pub use settings::{ChromePrefs, SettingsState, GLASS_DARKEN_DEFAULT};
pub use shell::{ShellOutput, PROMPT_GLYPH};
pub use theme::{colors as theme_colors, ThemeColors, DEFAULT_THEME_ID, THEME_IDS};
pub use toast::{ToastState, TOAST_DURATION_S};

/// Semver of this crate (`Cargo.toml` package version).
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

/// Host-merge API level for Rust consumers (independent of package patch).
/// Bump when removing/renaming public modules or re-exports.
pub const HOST_API: u32 = 1;
