fn main() {
    // The webview is built by Vite into ui/dist (`npm run build` in ui/,
    // which `cargo tauri build` runs first). A plain `cargo build` embeds
    // whatever is there, so refuse to build without it rather than ship an
    // empty window.
    let dist = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../ui/dist/index.html");
    println!("cargo:rerun-if-changed=../ui/dist/index.html");
    if !dist.exists() {
        panic!("ui/dist is missing: run `npm ci && npm run build` in ui/ first (cargo tauri build does it for you)");
    }
    tauri_build::build();
}
