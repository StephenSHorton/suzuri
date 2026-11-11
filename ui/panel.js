// A modal panel over the panes, used by notes. The view is an Astryx Dialog
// (chrome/Panel.jsx) rendered by the chrome's React root (chrome/overlays.js);
// this module keeps the open state so app.js can ask isOpen() at any time,
// and gives the keyboard back to the terminal on close ("rsz-panel-closed").
import { createElement as h } from "react"
import { flushSync } from "react-dom"
import { mountOverlay, unmountOverlay } from "./chrome/overlays.js"
import { Panel } from "./chrome/Panel.jsx"

let current = null

export function isOpen() {
  return current != null
}

export function closePanel() {
  if (!current) return
  const p = current
  current = null
  // Unmount now, so the Dialog's own focus return runs before app.js puts
  // the keyboard back on the pane.
  flushSync(() => unmountOverlay("panel"))
  p.onClose?.()
  window.dispatchEvent(new Event("rsz-panel-closed"))
}

// Open a panel showing content (a React element) under title; returns the
// panel. Escape, the close button and closePanel() close it.
export function openPanel(title, content, { onClose, wide } = {}) {
  closePanel()
  current = { title, onClose }
  mountOverlay("panel", h(Panel, { title, wide, onClose: closePanel }, content))
  return current
}
