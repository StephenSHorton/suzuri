// Where the plain-JS modules (palette.js, settings.js, confirm.js, help.js,
// notes.js, bell.js, the toasts) put their Astryx UI: one React root
// (chrome/main.jsx) renders every mounted overlay inside the Astryx Theme,
// so portals, focus handling and tokens all work. A module keeps its old
// exported API for app.js and renders through here:
//
//   import { createElement as h } from "react"
//   import { mountOverlay, unmountOverlay } from "./chrome/overlays.js"
//   mountOverlay("palette", h(PaletteView, { ... }))   // show or update
//   unmountOverlay("palette")                          // hide
//
// Keys are the module's name; mounting the same key again re-renders it.

let items = new Map()
const subs = new Set()
const emit = () => {
  items = new Map(items)
  for (const f of subs) f()
}

export function mountOverlay(key, element) {
  items.set(key, element)
  emit()
}

export function unmountOverlay(key) {
  if (items.delete(key)) emit()
}

export const overlays = {
  get: () => items,
  subscribe(f) {
    subs.add(f)
    return () => subs.delete(f)
  },
}
