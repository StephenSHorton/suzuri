// In-app updates, like Go suzuri: check the release feed, tell the UI, and
// install only when the person says so. Tauri's updater verifies each
// download against the public key in tauri.conf.json before installing.
//
// RSZ_UPDATE_URL points the check at another latest.json (a test feed).
// Debug builds check only when it is set. RSZ_UPDATE_AUTO=1 installs
// without asking (scripted update tests).

use std::time::Duration;

use parking_lot::Mutex;
use serde::Serialize;
use tauri::{AppHandle, Emitter, Manager, State};
use tauri_plugin_updater::{Update, UpdaterExt};

/// The update found by the last check, kept until it is installed.
#[derive(Default)]
pub struct Pending(Mutex<Option<Update>>);

#[derive(Serialize, Clone)]
pub struct Available {
    version: String,
    current: String,
    notes: Option<String>,
}

const FIRST_CHECK: Duration = Duration::from_secs(5);
const RECHECK: Duration = Duration::from_secs(6 * 60 * 60);

fn feed_override() -> Option<String> {
    std::env::var("RSZ_UPDATE_URL").ok().filter(|s| !s.is_empty())
}

async fn check(app: &AppHandle) -> Result<Option<Available>, String> {
    let mut b = app.updater_builder();
    if let Some(url) = feed_override() {
        let url = url.parse().map_err(|e| format!("RSZ_UPDATE_URL: {e}"))?;
        b = b.endpoints(vec![url]).map_err(|e| e.to_string())?;
    }
    let found = b.build().map_err(|e| e.to_string())?.check().await.map_err(|e| e.to_string())?;
    let info = found.as_ref().map(|u| Available {
        version: u.version.clone(),
        current: u.current_version.clone(),
        notes: u.body.clone(),
    });
    *app.state::<Pending>().0.lock() = found;
    Ok(info)
}

/// Checks soon after start and every few hours; tells the UI ("update")
/// when a newer build is out.
pub fn start(app: &AppHandle) {
    if cfg!(debug_assertions) && feed_override().is_none() {
        return;
    }
    let app = app.clone();
    std::thread::spawn(move || {
        std::thread::sleep(FIRST_CHECK);
        loop {
            match tauri::async_runtime::block_on(check(&app)) {
                Ok(Some(info)) => {
                    log::info!("update: {} available (running {})", info.version, info.current);
                    let _ = app.emit("update", info);
                    if std::env::var("RSZ_UPDATE_AUTO").is_ok_and(|v| v == "1") {
                        install_pending(&app);
                    }
                }
                Ok(None) => log::info!("update: up to date"),
                Err(e) => log::warn!("update: check failed: {e}"),
            }
            std::thread::sleep(RECHECK);
        }
    });
}

/// Checks now (the UI's "check for updates").
#[tauri::command]
pub async fn update_check(app: AppHandle) -> Result<Option<Available>, String> {
    check(&app).await
}

/// Downloads, verifies and installs the pending update, then restarts.
/// On Windows the installer exits the app itself.
#[tauri::command]
pub async fn update_install(app: AppHandle, pending: State<'_, Pending>) -> Result<(), String> {
    let update = pending.0.lock().take().ok_or("no update pending")?;
    log::info!("update: installing {}", update.version);
    update.download_and_install(|_, _| {}, || {}).await.map_err(|e| e.to_string())?;
    app.restart();
}

fn install_pending(app: &AppHandle) {
    let app2 = app.clone();
    let res = tauri::async_runtime::block_on(async move {
        let update = app2.state::<Pending>().0.lock().take().ok_or("no update pending".to_string())?;
        log::info!("update: installing {}", update.version);
        update.download_and_install(|_, _| {}, || {}).await.map_err(|e| e.to_string())
    });
    match res {
        Ok(()) => app.restart(),
        Err(e) => log::error!("update: install failed: {e}"),
    }
}
