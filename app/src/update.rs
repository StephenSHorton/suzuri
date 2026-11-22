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
/// update_check's error in a dev build (ui/update.js shows it as is).
const DEV_BUILD: &str = "dev build: updates are off";

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

/// Checks now (the palette's "Check for updates", and Retry). Off in debug
/// builds without a test feed, like the timed check (Go: "dev build").
#[tauri::command]
pub async fn update_check(app: AppHandle) -> Result<Option<Available>, String> {
    if cfg!(debug_assertions) && feed_override().is_none() {
        return Err(DEV_BUILD.into());
    }
    check(&app).await
}

/// Shows the update offer as a desktop card with Install and Skip
/// (ui/update.js calls it for each new version, and after a manual check).
#[tauri::command]
pub fn update_offer(app: AppHandle, version: String, current: String) {
    let a = app.clone();
    toast::post(crate::notices::update_note(&version, &current, move || install_or_ask(&a)));
}

/// The offer's Install. A restart ends every shell and the programs in
/// them (hearth docs/design/21-terminals.md, section 2), so with a program
/// running in any pane the card first names those panes and asks; with
/// only idle prompts it installs at once, as before.
fn install_or_ask(app: &AppHandle) {
    let busy = busy_panes(app);
    if busy.is_empty() {
        return install_from_card(app);
    }
    log::info!("update: asking first, {} pane(s) running a program", busy.len());
    let a = app.clone();
    toast::post(crate::notices::update_confirm_note(&busy, move || install_from_card(&a)));
}

/// Each pane with a program running (ShellStatus::program, or one in front
/// of the shell now), as "claude in suzuri": the program's name, else "a
/// program", and the pane's folder. A pane started with its own command
/// (not a shell) is that program itself and is not counted.
fn busy_panes(app: &AppHandle) -> Vec<String> {
    let Some(panes) = app.try_state::<std::sync::Arc<crate::Panes>>() else { return Vec::new() };
    let mut list: Vec<(u32, std::sync::Arc<rsz_term::Pane>)> = panes.map.lock().iter().map(|(id, p)| (*id, p.clone())).collect();
    list.sort_by_key(|(id, _)| *id);
    let home = std::env::var("HOME").or_else(|_| std::env::var("USERPROFILE")).unwrap_or_default();
    list.iter()
        .filter(|(_, p)| p.state() == rsz_term::State::Running)
        .filter_map(|(_, p)| {
            let st = p.shell_status();
            // Asked live too: a shell without suzuri's hooks never sets
            // `program`, but the OS still knows what is in front.
            let name = p.program_name();
            if !st.program && name.is_none() {
                return None;
            }
            let cwd = if st.cwd.is_empty() { p.cwd().unwrap_or_default() } else { st.cwd };
            Some(busy_label(name.as_deref(), &cwd, &home))
        })
        .collect()
}

/// "claude in suzuri": a program and the last part of its pane's folder
/// ("~" for home, nothing when unknown).
fn busy_label(program: Option<&str>, cwd: &str, home: &str) -> String {
    let name = program.filter(|n| !n.is_empty()).unwrap_or("a program");
    let trimmed = cwd.trim_end_matches(['/', '\\']);
    if trimmed.is_empty() && cwd.is_empty() {
        return name.to_string();
    }
    let home = home.trim_end_matches(['/', '\\']);
    let folder = if !home.is_empty() && trimmed == home {
        "~"
    } else {
        trimmed.rsplit(['/', '\\']).next().filter(|s| !s.is_empty()).unwrap_or("/")
    };
    format!("{name} in {folder}")
}

/// The card's Install: says it's installing, installs and restarts. On a
/// failure it says why and, once the feed answers again, offers it anew.
fn install_from_card(app: &AppHandle) {
    let app = app.clone();
    std::thread::spawn(move || {
        let say = |m: &str| {
            if let Some(n) = app.try_state::<crate::notices::Notices>() {
                n.toast(m);
            }
        };
        say("Installing update…");
        let a = app.clone();
        let res = tauri::async_runtime::block_on(async move {
            let update = a.state::<Pending>().0.lock().take().ok_or("no update pending".to_string())?;
            log::info!("update: installing {}", update.version);
            update.download_and_install(|_, _| {}, || {}).await.map_err(|e| e.to_string())
        });
        match res {
            Ok(()) => app.restart(),
            Err(e) => {
                log::error!("update: install failed: {e}");
                say(&format!("Update failed: {e}"));
                if let Ok(Some(info)) = tauri::async_runtime::block_on(check(&app)) {
                    update_offer(app.clone(), info.version, info.current);
                }
            }
        }
    });
}

/// Downloads, verifies and installs the pending update, then restarts.
/// On Windows the installer exits the app itself. With a program running
/// in a pane it refuses and names the panes, unless `force` (the person
/// already said yes).
#[tauri::command]
pub async fn update_install(app: AppHandle, pending: State<'_, Pending>, force: Option<bool>) -> Result<(), String> {
    let busy = busy_panes(&app);
    if !force.unwrap_or(false) && !busy.is_empty() {
        return Err(format!("programs running: {}", busy.join(", ")));
    }
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

#[cfg(test)]
mod tests {
    use super::busy_label;

    #[test]
    fn a_busy_pane_reads_as_its_program_and_folder() {
        assert_eq!(busy_label(Some("claude"), "/Users/s/projects/suzuri", "/Users/s"), "claude in suzuri");
        assert_eq!(busy_label(Some("vim"), "/Users/s/", "/Users/s"), "vim in ~");
        assert_eq!(busy_label(None, "/tmp/x", "/Users/s"), "a program in x");
        assert_eq!(busy_label(Some(""), "", "/Users/s"), "a program");
        assert_eq!(busy_label(Some("top"), "/", "/Users/s"), "top in /");
        assert_eq!(busy_label(Some("node"), "C:\\Users\\s\\web", "C:\\Users\\s"), "node in web");
    }
}
