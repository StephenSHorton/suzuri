//! Opt-in iroh sync of suzuri workspace channel messages (jsonl).
//!
//! Default remains local-only. Enable with `SUZURI_WORKSPACE_IROH=1` or `--enable`.
//! Chrome / hosts pass `--json` for NDJSON machine-mode events.

pub mod events;
pub mod merge;
pub mod proto;
pub mod sync;
