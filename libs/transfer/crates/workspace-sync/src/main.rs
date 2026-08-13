//! `suzuri-workspace-sync` — opt-in iroh sync of workspace channel messages.

use std::path::PathBuf;
use std::process::ExitCode;

use clap::{Parser, Subcommand};
use suzuri_workspace_sync::sync::{join, listen};

/// Env var that opts into iroh workspace sync.
const ENABLE_ENV: &str = "SUZURI_WORKSPACE_IROH";

#[derive(Parser)]
#[command(
    name = "suzuri-workspace-sync",
    version,
    about = "Opt-in iroh sync of suzuri workspace channel messages (jsonl)"
)]
struct Cli {
    #[command(subcommand)]
    cmd: Cmd,
}

#[derive(Subcommand)]
enum Cmd {
    /// Bind, print a one-line ticket, accept joiners.
    Listen {
        /// Workspace store root (contains `channels/<slug>/messages.jsonl`).
        #[arg(long)]
        root: PathBuf,
        /// Distinct iroh identity dir (default: `<root>/.iroh`).
        #[arg(long)]
        iroh_dir: Option<PathBuf>,
        /// Opt in to iroh sync (otherwise set SUZURI_WORKSPACE_IROH=1).
        #[arg(long)]
        enable: bool,
    },
    /// Connect to a listen ticket and sync messages both ways.
    Join {
        /// Workspace store root (contains `channels/<slug>/messages.jsonl`).
        #[arg(long)]
        root: PathBuf,
        /// Ticket printed by `listen` (`ticket: …` JSON EndpointAddr).
        #[arg(long)]
        ticket: String,
        /// Distinct iroh identity dir (default: `<root>/.iroh`).
        #[arg(long)]
        iroh_dir: Option<PathBuf>,
        /// Opt in to iroh sync (otherwise set SUZURI_WORKSPACE_IROH=1).
        #[arg(long)]
        enable: bool,
    },
}

fn env_value_enabled(v: &str) -> bool {
    matches!(v.trim().to_ascii_lowercase().as_str(), "1" | "true" | "yes")
}

fn is_enabled(flag: bool) -> bool {
    flag || std::env::var(ENABLE_ENV)
        .ok()
        .is_some_and(|v| env_value_enabled(&v))
}

#[tokio::main]
async fn main() -> ExitCode {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .with_writer(std::io::stderr)
        .init();

    let cli = Cli::parse();
    let enable = match &cli.cmd {
        Cmd::Listen { enable, .. } | Cmd::Join { enable, .. } => *enable,
    };
    if !is_enabled(enable) {
        eprintln!(
            "workspace iroh sync is opt-in (default is local-only).\nset {ENABLE_ENV}=1 or pass --enable"
        );
        return ExitCode::from(2);
    }
    if let Err(e) = run(cli.cmd).await {
        eprintln!("error: {e:#}");
        return ExitCode::from(1);
    }
    ExitCode::SUCCESS
}

async fn run(cmd: Cmd) -> anyhow::Result<()> {
    match cmd {
        Cmd::Listen {
            root,
            iroh_dir,
            enable: _,
        } => {
            let iroh_dir = iroh_dir.unwrap_or_else(|| root.join(".iroh"));
            listen(root, iroh_dir).await
        }
        Cmd::Join {
            root,
            ticket,
            iroh_dir,
            enable: _,
        } => {
            let iroh_dir = iroh_dir.unwrap_or_else(|| root.join(".iroh"));
            join(root, iroh_dir, &ticket).await
        }
    }
}
