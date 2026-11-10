// A yes/no box (Go suzuri's confirm modal, chrome/model.go: "Quit suzuri?"
// on closing the last tab), drawn as an Astryx AlertDialog
// (chrome/ConfirmDialog.jsx) through the overlay host. While it is up it
// takes every key: Enter (or the close chord again) says yes, Esc says no.

import { createElement as h } from "react"
import { mountOverlay, unmountOverlay } from "./chrome/overlays.js"

// A keydown to "yes", "no" or null (ignored).
export function confirmKey(e, isMac) {
  // A held key (⌘W closing pane after pane) must not answer the box.
  if (e.repeat) return null
  const k = (e.key || "").toLowerCase()
  if (k === "escape") return "no"
  if (k === "enter") return "yes"
  // ⌘W / ⌘⇧W on macOS, Ctrl+Shift+W on Windows: the chord that asked.
  if (k === "w" && (isMac ? e.metaKey : e.ctrlKey && e.shiftKey)) return "yes"
  return null
}

// The view loads only in the webview (node --test imports this module for
// confirmKey); started at load so the first ask doesn't wait on it.
const view = globalThis.document ? import("./chrome/ConfirmDialog.jsx").then((m) => m.ConfirmDialog) : null

let openBox = null
let shown = 0 // which ask the mounted view belongs to

export const isOpen = () => !!openBox

// Resolves true for yes, false for no. A second ask while one is up gets
// the first one's answer.
export function confirm({ title, body, yes = "OK", no = "Cancel" }) {
  if (openBox) return openBox.answer
  const isMac = navigator.platform.startsWith("Mac")
  const back = document.activeElement
  const id = ++shown
  let View = null
  let done
  const answer = new Promise((r) => (done = r))
  const render = (isOpen) => View && mountOverlay("confirm", h(View, { isOpen, title, body, yes, no, onAnswer: finish }))
  function finish(v) {
    if (openBox?.answer !== answer) return // answered already
    window.removeEventListener("keydown", onKey, true)
    openBox = null
    // Close through the dialog (so it leaves the top layer cleanly), then
    // drop it and give the keyboard back to whoever had it.
    render(false)
    setTimeout(() => {
      if (shown === id) unmountOverlay("confirm")
      if (!openBox && document.activeElement !== back) back?.focus?.()
    }, 200)
    done(v)
  }
  // Every key is the box's while it is up; Enter must say yes even though
  // the dialog focuses Cancel, so the keys never reach its buttons.
  const onKey = (e) => {
    e.preventDefault()
    e.stopImmediatePropagation()
    const a = confirmKey(e, isMac)
    if (a) finish(a === "yes")
  }
  window.addEventListener("keydown", onKey, true)
  openBox = { answer }
  view?.then((V) => {
    View = V
    if (openBox?.answer === answer) render(true)
  })
  return answer
}
