import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

const host = process.env.TAURI_DEV_HOST;
const rootDir = path.dirname(fileURLToPath(import.meta.url));
const reactDir = path.resolve(rootDir, "node_modules/react");
const reactDomDir = path.resolve(rootDir, "node_modules/react-dom");
const reconcilerDir = path.resolve(rootDir, "node_modules/react-reconciler");

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    // file:../../kussetsu brings its own nested react — must share one copy or
    // Kussetsu's reconciler throws "Invalid hook call" and paints nothing.
    dedupe: ["react", "react-dom", "react-reconciler", "scheduler"],
    alias: {
      "@": path.resolve(rootDir, "src"),
      react: reactDir,
      "react-dom": reactDomDir,
      "react-reconciler": reconcilerDir,
      "react/jsx-runtime": path.resolve(reactDir, "jsx-runtime.js"),
      "react/jsx-dev-runtime": path.resolve(reactDir, "jsx-dev-runtime.js"),
    },
  },
  optimizeDeps: {
    // Prebundle kussetsu against the app's React, not its nested copy.
    include: ["kussetsu", "react", "react-dom", "react-reconciler"],
  },
  // Rolldown is the default bundler in Vite 8.
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
    host: host || false,
    hmr: host
      ? {
          protocol: "ws",
          host,
          port: 1421,
        }
      : undefined,
    watch: {
      ignored: ["**/src-tauri/**"],
    },
  },
});
