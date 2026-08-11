// suzuri surface — stock Tauri webview + Kussetsu (WebGPU) chrome.
// Terminal cells stay in a DOM hole (xterm); chrome is GPU-owned.

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
