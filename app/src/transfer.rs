//! File transfer: the UI's commands around the `suzuri-transfer` engine
//! (`transfer_engine.rs`, lifted from Go suzuri's chrome crate). One transfer
//! at a time, like Go suzuri; a new one cancels the old. Updates reach the
//! webview as `transfer` events tagged with a generation, so a late event from
//! a cancelled job is ignored there.

use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::thread;
use std::time::Duration;

use parking_lot::Mutex;
use serde::Serialize;
use tauri::{AppHandle, Emitter, State};

use crate::transfer_engine::{self as engine, EngineJob, EngineMode};

#[derive(Default)]
pub struct Transfers {
    next: AtomicU64,
    job: Mutex<Option<(u64, EngineJob)>>,
}

#[derive(Clone, Serialize)]
struct Update {
    generation: u64,
    mode: &'static str,
    phase: String,
    ticket: Option<String>,
    done: Option<u64>,
    total: Option<u64>,
    message: Option<String>,
    finished: bool,
    ok: bool,
}

fn start(app: AppHandle, tr: Arc<Transfers>, mode: EngineMode, value: &str) -> Result<u64, String> {
    let job = engine::spawn_engine(mode, value)?;
    let generation = tr.next.fetch_add(1, Ordering::SeqCst) + 1;
    if let Some((_, old)) = tr.job.lock().replace((generation, job)) {
        thread::spawn(move || old.cancel());
    }
    let mode_name = match mode {
        EngineMode::Send => "send",
        EngineMode::Receive => "receive",
    };
    // Pump this job's updates to the webview until it finishes or is replaced.
    let tr_ptr = tr;
    thread::spawn(move || {
        loop {
            let mut ended = false;
            {
                let guard = tr_ptr.job.lock();
                let Some((g, job)) = guard.as_ref() else { break };
                if *g != generation {
                    break;
                }
                while let Some(u) = job.try_recv() {
                    ended |= u.finished;
                    let _ = app.emit(
                        "transfer",
                        Update {
                            generation,
                            mode: mode_name,
                            phase: u.phase,
                            ticket: u.ticket,
                            done: u.done,
                            total: u.total,
                            message: u.message,
                            finished: u.finished,
                            ok: u.ok,
                        },
                    );
                }
                if !ended && job.is_finished() {
                    // Worker gone without a final event: drain once more next loop.
                    ended = job.try_recv().is_none();
                }
            }
            if ended {
                let mut guard = tr_ptr.job.lock();
                if guard.as_ref().is_some_and(|(g, _)| *g == generation) {
                    guard.take();
                }
                break;
            }
            thread::sleep(Duration::from_millis(50));
        }
    });
    Ok(generation)
}

/// Send a file or folder; the ticket arrives in a `transfer` event (phase "ready").
#[tauri::command]
pub fn transfer_send(app: AppHandle, tr: State<'_, Arc<Transfers>>, path: String) -> Result<u64, String> {
    start(app, Arc::clone(&tr), EngineMode::Send, &path)
}

/// Receive a ticket into the default receive dir (Downloads).
#[tauri::command]
pub fn transfer_receive(app: AppHandle, tr: State<'_, Arc<Transfers>>, ticket: String) -> Result<u64, String> {
    start(app, Arc::clone(&tr), EngineMode::Receive, &ticket)
}

/// Stop the running transfer, if any.
#[tauri::command]
pub fn transfer_cancel(tr: State<'_, Arc<Transfers>>) {
    if let Some((_, job)) = tr.job.lock().take() {
        thread::spawn(move || job.cancel());
    }
}

#[derive(Serialize)]
pub struct TransferInfo {
    engine: Option<String>,
    receive_dir: String,
}

/// Stop any running transfer (app exit): the engine must not outlive us.
pub fn stop_all(tr: &Transfers) {
    if let Some((_, job)) = tr.job.lock().take() {
        job.cancel();
    }
}

/// Where the engine is (None: not found) and where receives land.
#[tauri::command]
pub fn transfer_info() -> TransferInfo {
    TransferInfo {
        engine: engine::find_transfer_bin().map(|p| p.display().to_string()),
        receive_dir: engine::default_receive_dir().display().to_string(),
    }
}

#[cfg(test)]
mod tests {
    use crate::transfer_engine::{EngineMode, find_transfer_bin, spawn_engine};
    use std::time::{Duration, Instant};

    /// Live round trip through the real engine: `cargo test -- --ignored`.
    #[test]
    #[ignore]
    fn send_then_receive_round_trip() {
        assert!(find_transfer_bin().is_some(), "suzuri-transfer not found");
        let dir = std::env::temp_dir().join(format!("rsz-transfer-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let file = dir.join("probe.txt");
        std::fs::write(&file, b"rust-suzuri transfer probe\n").unwrap();
        // Receives land in HOME/Downloads: point HOME at the temp dir.
        unsafe { std::env::set_var("HOME", &dir) };
        std::fs::create_dir_all(dir.join("Downloads")).unwrap();
        unsafe { std::env::set_var("HATO_CONFIG_DIR", dir.join("cfg-send")) };
        let send = spawn_engine(EngineMode::Send, file.to_str().unwrap()).unwrap();
        let start = Instant::now();
        let ticket = loop {
            assert!(start.elapsed() < Duration::from_secs(30), "no ticket");
            if let Some(u) = send.try_recv() {
                assert!(u.ok, "send failed: {:?}", u.message);
                if let Some(t) = u.ticket {
                    break t;
                }
            }
            std::thread::sleep(Duration::from_millis(50));
        };
        unsafe { std::env::set_var("HATO_CONFIG_DIR", dir.join("cfg-recv")) };
        let recv = spawn_engine(EngineMode::Receive, &ticket).unwrap();
        let start = Instant::now();
        loop {
            assert!(start.elapsed() < Duration::from_secs(60), "receive did not finish");
            if let Some(u) = recv.try_recv() {
                eprintln!("recv: {} {:?} {:?}/{:?}", u.phase, u.message, u.done, u.total);
                if u.finished {
                    assert!(u.ok, "receive failed: {:?}", u.message);
                    break;
                }
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        let got = std::fs::read_to_string(dir.join("Downloads/probe.txt")).unwrap();
        assert_eq!(got, "rust-suzuri transfer probe\n");
        send.cancel();
        let _ = std::fs::remove_dir_all(&dir);
    }
}
