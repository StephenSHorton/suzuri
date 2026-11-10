// suzuri's webview build. The main window's page (notices are native
// rust-toast cards, app/src/notices.rs), the paint worker and the Gohu font
// all come through here; Tauri embeds ui/dist (app/tauri.conf.json
// frontendDist). `npm run build` in ui/, which `cargo tauri build` runs
// first (beforeBuildCommand).
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import { resolve } from "node:path"

export default defineConfig({
  root: __dirname,
  base: "./",
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2022",
    // Readable stacks in the app log; the webview loads from disk.
    sourcemap: true,
    rollupOptions: {
      input: { main: resolve(__dirname, "index.html") },
    },
  },
  worker: { format: "es" },
  server: { port: 5199, strictPort: true },
})
