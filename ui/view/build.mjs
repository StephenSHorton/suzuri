// Compiles suzuri-view's React parts (react/Settings.jsx, react/Palette.jsx)
// to plain ESM in dist/, so the package ships no JSX and an app's bundler
// needs no React plugin for it. React, react-dom, Astryx and lucide-react
// stay imports (the package's peer dependencies): the app's own copies,
// inside its own Astryx <Theme>. npm runs this before packing (prepack);
// it uses Vite from ui/node_modules (npm ci in ui/ first).
//
//   node ui/view/build.mjs
import { build } from "vite"
import react from "@vitejs/plugin-react"
import { fileURLToPath } from "node:url"
import { isAbsolute } from "node:path"

const here = (f) => fileURLToPath(new URL(f, import.meta.url))

await build({
  configFile: false,
  root: here("."),
  logLevel: "warn",
  plugins: [react()],
  build: {
    outDir: here("dist"),
    emptyOutDir: true,
    target: "es2022",
    minify: false,
    sourcemap: false,
    lib: {
      entry: { settings: here("react/Settings.jsx"), palette: here("react/Palette.jsx") },
      formats: ["es"],
      fileName: (_format, name) => `${name}.js`,
    },
    rollupOptions: {
      // Every package import stays an import; only this package's files are bundled.
      external: (id) => !id.startsWith(".") && !isAbsolute(id) && !id.startsWith("\0"),
    },
  },
})
console.log("built dist/settings.js and dist/palette.js")
