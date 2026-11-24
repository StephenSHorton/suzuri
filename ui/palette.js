// Command palette (⌘K / Ctrl+K): Go suzuri's filter-and-run list
// (internal/chrome/palette.go). The palette itself, its filtering and its
// chord labels are suzuri-view's (view/react/Palette.jsx,
// view/palette_rules.js), the same one Hearth's terminal mounts; this module
// keeps the API app.js uses and mounts it on the React chrome through
// chrome/overlays.js.
import { createElement as h } from "react"
import { mountOverlay, unmountOverlay } from "./chrome/overlays.js"

export { filterCommands, hintChords } from "./view/palette_rules.js"

// The view is JSX (Vite only): loaded when the first Palette is made, so
// node's tests can import filterCommands without it.
let viewModule = null
const loadView = () => (viewModule ??= import("./view/react/Palette.jsx"))

export class Palette {
  // host was the old overlay container; the view now mounts on the React
  // chrome, so it is unused but stays in app.js's call.
  constructor(host, onClose) {
    this.onClose = onClose
    this.shown = false
    this.serial = 0
    loadView()
  }

  get isOpen() { return this.shown }

  open(items, run) {
    this.shown = true
    // A fresh key each time, so the query and highlight start over.
    const key = ++this.serial
    loadView().then(({ Palette: PaletteWindow }) => {
      if (!this.shown || key !== this.serial) return
      mountOverlay("palette", h(PaletteWindow, {
        key,
        commands: items,
        onRun: run,
        onClose: () => this.close(),
      }))
    })
  }

  close() {
    if (!this.shown) return
    this.shown = false
    unmountOverlay("palette")
    this.onClose?.()
  }
}
