//! suzuri's shell keeper, the client side.
//!
//! Each shell suzuri opens (macOS and Linux) runs in its own small host
//! process: `suzuri --keeper-host <dir> <id>`, in its own session, holding
//! the whole `rsz_term::Pane` (PTY, screen, scrollback). The app attaches to
//! it over a Unix socket and draws its frames; quitting, updating or
//! crashing the app leaves the host and its shell running, and the next
//! start attaches again. A host ends when its shell ends (closing the pane
//! closes the shell). There is no supervisor process: the folder of
//! sockets is the list of kept shells, and each host is independent, so a
//! keeper update never touches a running one (it keeps its old program
//! until its shell ends; new shells get the new one).
//!
//! This crate has no rsz-term in it (no Zig to build), so any program can
//! attach: see `rsz-keeper` (src/bin) and [`client::Conn`].
//!
//! # The folder
//! [`dir::default_dir`]: `$RSZ_KEEPER_DIR`, else `$RSZ_CONFIG_DIR/keeper`,
//! else suzuri's config folder + `keeper` (macOS: `~/Library/Application
//! Support/suzuri/keeper`). Mode 0700, owned by the user. One socket per
//! shell, `<id>.sock` (mode 0600), where `id` is the app's pane id (it is
//! the shell's `SUZURI_PANE_ID`); `<id>.log` is the host's own log.
//!
//! # The wire format
//! Every message, both ways: `u32 len` (little endian, counting what
//! follows), `u8 kind`, then `len - 1` bytes of body.
//! - kind 1 [`K_JSON`]: one JSON object, UTF-8. Its `"t"` names it.
//! - kind 2 [`K_FRAME`]: host to client, one rsz-term frame exactly as the
//!   app's UI gets it (crates/term/src/frame.rs; its own version is header
//!   byte 11, and the host's hello says it as `frame`).
//! - kind 3 [`K_INPUT`]: client to host, bytes typed into the shell.
//!
//! The first message each way is a hello (`"t": "hello"`). The client
//! sends `{"t":"hello","proto":1,"role":"viewer"|"control","client":name}`.
//! The host answers `{"t":"hello","proto":P,"min":M,"frame":F,"version":
//! app version,"id","pid","shell_pid","cols","rows","viewers","command",
//! "cwd","title","started","exited"}`. A client serves a host when
//! `min <= PROTO`; both sides ignore fields they don't know, so new fields
//! need no bump. A viewer then gets the host's cached state (title, shell
//! status, progress, images on screen), a full frame, and every event after;
//! a control client gets only answers.
//!
//! Requests carry `"q": n` and get `{"t":"r","q":n,"v":value}` back
//! ([`client::Conn::call`]).
//!
//! A program's clipboard read (OSC 52 "?") goes to the viewers as
//! `{"t":"clip_ask","n":n}`; one answers on a control connection of its own
//! with `{"t":"clip_answer","n":n,"text":string|null}`. With no viewer, or
//! none answering in time, the program gets an empty clipboard. The app side of the messages is
//! app/src/keeper.rs; keep the two in step.

pub mod client;
pub mod dir;
pub mod wire;

/// The protocol this side speaks. Bump on a change an older peer would
/// misread; add `min` on the host for the oldest client it still serves.
pub const PROTO: u32 = 1;

pub const K_JSON: u8 = 1;
pub const K_FRAME: u8 = 2;
pub const K_INPUT: u8 = 3;

#[cfg(unix)]
pub use client::Conn;
pub use client::Event;
