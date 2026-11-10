// Keyboard shortcuts sheet (⌘/ / Ctrl+/, palette "Keyboard shortcuts"):
// Go suzuri's help card (internal/chrome/help.go). The chords come from
// shortcuts.js's table and the command line rows carry a sample key that
// the test runs through bar.js, so the sheet can't drift from the keys.

import { createElement as h } from "react"
import { chordLabel, chordLabels, shortcut } from "./shortcuts.js"
import { mountOverlay, unmountOverlay } from "./chrome/overlays.js"

const key = (k, mods = {}) => ({ key: k, code: "", metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...mods })

// The common start of some labels, e.g. "⌘⌥" for ⌘⌥← ⌘⌥→ ⌘⌥↑ ⌘⌥↓.
function commonPrefix(labels) {
  let p = labels[0] || ""
  for (const l of labels) while (!l.startsWith(p)) p = p.slice(0, -1)
  return p
}

// Sections of rows: { keys, desc, ids? (chord table commands), ev? (a
// sample keydown: bar rows go through barKey, terminal rows clipShortcut) }.
export function helpSections(isMac) {
  const L = chordLabels(isMac)
  const row = (id, desc, alt = "") => ({ keys: L[id] || alt, desc, ids: [id] })
  // Arrow chords as one row: "⌘⌥←→↑↓".
  const arrows = (ids, desc) => {
    const labels = ids.map((id) => L[id])
    const p = commonPrefix(labels)
    return { keys: p + labels.map((l) => l.slice(p.length)).join(""), desc, ids }
  }
  const word = isMac ? { altKey: true } : { ctrlKey: true }
  const wordKey = isMac ? "⌥" : "Ctrl+"
  return [
    { title: "Tabs", rows: [
      row("new_tab", "New tab"),
      row("new_window", "New window"),
      row("next_tab", "Next tab"),
      row("prev_tab", "Previous tab"),
      row("select_tab", "Go to tab 1–9"),
      row("close_tab", "Close tab", "× on the tab"),
      { keys: "Double-click", desc: "Rename tab" },
      arrows(["move_pane_prev_tab", "move_pane_next_tab"], "Pane to previous / next tab"),
    ] },
    { title: "Panes", rows: [
      row("split_right", "Split right"),
      row("split_down", "Split down"),
      row("zoom_pane", "Maximize pane"),
      arrows(["focus_left", "focus_right", "focus_up", "focus_down"], "Focus pane"),
      arrows(["move_pane_left", "move_pane_right", "move_pane_up", "move_pane_down"], "Move pane"),
      { ...row("rename_pane", "Rename pane"), keys: L.rename_pane + " · double-click" },
      row("close_pane", "Close pane"),
    ] },
    { title: "Command line", rows: [
      { keys: "Enter", desc: "Run", ev: key("Enter") },
      { keys: isMac ? "⇧Enter" : "Shift+Enter", desc: "New line", ev: key("Enter", { shiftKey: true }) },
      { keys: "↑ ↓", desc: "History", ev: key("ArrowUp") },
      { keys: isMac ? "Tab · ⇧Tab" : "Tab · Shift+Tab", desc: "Complete", ev: key("Tab") },
      { keys: "→ at the end", desc: "Take the suggestion", ev: key("ArrowRight") },
      { keys: wordKey + "←→", desc: "Word left / right", ev: key("ArrowLeft", word) },
      { keys: wordKey + "⌫", desc: "Delete word", ev: key("Backspace", word) },
      isMac
        ? { keys: "⌘⌫ · Esc", desc: "Clear line", ev: key("Backspace", { metaKey: true }) }
        : { keys: "Esc · Ctrl+U", desc: "Clear line", ev: key("Escape") },
      { keys: "Ctrl+C", desc: "Clear / interrupt", ev: key("c", { ctrlKey: true }) },
      { keys: "Ctrl+A · Ctrl+E", desc: "Line start / end", ev: key("a", { ctrlKey: true }) },
    ] },
    { title: "Terminal", rows: [
      isMac
        ? { keys: "⌘C · ⌘⇧C", desc: "Copy", ev: key("c", { metaKey: true }) }
        : { keys: "Ctrl+Shift+C", desc: "Copy (Ctrl+C with a selection)", ev: key("c", { ctrlKey: true, shiftKey: true }) },
      isMac
        ? { keys: "⌘V · ⌘⇧V", desc: "Paste", ev: key("v", { metaKey: true }) }
        : { keys: "Ctrl+V · Ctrl+Shift+V", desc: "Paste", ev: key("v", { ctrlKey: true }) },
      { keys: "Drag · double-click", desc: "Select" },
      { keys: isMac ? "⌘/⌃-click" : "Ctrl-click", desc: "Open link" },
      { keys: "Wheel", desc: "Scrollback" },
    ] },
    { title: "App", rows: [
      row("palette", "Command palette"),
      row("settings", "Settings"),
      row("help", "Keyboard shortcuts"),
      { keys: isMac ? "⌘⇧M" : "Ctrl+Shift+M", desc: "Notes" }, // notes.js
      row("hud", "Stats HUD"),
      row("zoom_in", "Zoom in"),
      row("zoom_out", "Zoom out"),
      row("zoom_reset", "Reset zoom"),
      { keys: "Esc", desc: "Close a sheet or overlay" },
    ] },
  ]
}

// A keys label from helpSections() as pieces for Astryx Kbd: { kbd } (a
// Kbd `keys` string such as "mod+shift+t" or "ctrl+plus"), { text } (words
// that are no key, "Double-click", "at the end") and { sep } (the " · "
// between alternatives).
const MAC_MODS = { "⌘": "mod", "⌥": "alt", "⇧": "shift", "⌃": "ctrl" }
const WORD_MODS = { "Ctrl+": "ctrl", "Alt+": "alt", "Shift+": "shift" }
const ARROW_KEYS = { "←": "left", "→": "right", "↑": "up", "↓": "down" }
const NAMED_KEYS = { Enter: "enter", Esc: "esc", Tab: "tab", "⌫": "backspace", "+": "plus", "1–9": "1–9" }

// One space-free word to a Kbd keys string, or null when it is no chord.
function chordKeys(word, isMac) {
  const mods = []
  let rest = word
  for (;;) {
    const glyph = isMac && MAC_MODS[rest[0]]
    const named = Object.keys(WORD_MODS).find((m) => rest.startsWith(m) && rest.length > m.length)
    if (glyph) { mods.push(glyph); rest = rest.slice(1) }
    else if (named) { mods.push(WORD_MODS[named]); rest = rest.slice(named.length) }
    else break
  }
  let keys
  if (NAMED_KEYS[rest]) keys = [NAMED_KEYS[rest]]
  else if (/^[←→↑↓]+$/.test(rest)) keys = [...rest].map((a) => ARROW_KEYS[a])
  else if (/^(F\d{1,2}|[A-Za-z0-9[\]\\/,.=\-])$/.test(rest)) keys = [rest.toLowerCase()]
  else return null
  return [...mods, ...keys].join("+")
}

export function kbdParts(label, isMac) {
  const out = []
  label.split(" · ").forEach((alt, i) => {
    if (i) out.push({ sep: true })
    for (const word of alt.split(" ")) {
      const kbd = chordKeys(word, isMac)
      const last = out[out.length - 1]
      if (kbd) out.push({ kbd })
      else if (last?.text != null) last.text += " " + word
      else out.push({ text: word })
    }
  })
  return out
}

// The sheet: an Astryx Dialog (chrome/HelpSheet.jsx) on the overlay host.
// Esc, Enter, ⌘/ or a click outside closes it. It takes every key while
// open: app.js doesn't know about it, so this listener alone keeps keys from
// the terminal.
const SCROLL_KEYS = new Set(["Tab", "ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End"])

export class HelpSheet {
  constructor(_host, isMac) {
    this.isMac = isMac
    this.open_ = false
    this.sections = helpSections(isMac)
    // The view loads only in the webview (node --test imports this module).
    this.view = import("./chrome/HelpSheet.jsx").then((m) => (this.View = m.HelpSheet))
    window.addEventListener("keydown", (e) => this.onKey(e), true)
  }

  get isOpen() { return this.open_ }

  toggle() { this.isOpen ? this.close() : this.open() }

  render() {
    if (!this.View) return void this.view.then(() => this.render())
    mountOverlay("help", h(this.View, { isOpen: this.open_, sections: this.sections, isMac: this.isMac, onClose: () => this.close() }))
  }

  open() {
    if (this.isOpen) return
    this.back = document.activeElement
    this.open_ = true
    clearTimeout(this.drop)
    this.render()
  }

  close() {
    if (!this.isOpen) return
    this.open_ = false
    // Close through the dialog (it leaves the top layer cleanly), then drop
    // it and give the keyboard back once the page is no longer inert.
    this.render()
    const back = this.back
    this.back = null
    this.drop = setTimeout(() => {
      if (this.isOpen) return
      unmountOverlay("help")
      if (document.activeElement !== back) back?.focus?.()
    }, 200)
  }

  onKey(e) {
    if (!this.isOpen || e.isComposing) return
    e.stopPropagation()
    if (e.key === "Escape" || e.key === "Enter" || shortcut(e, this.isMac)?.id === "help") {
      e.preventDefault()
      this.close()
    } else if (!SCROLL_KEYS.has(e.key)) e.preventDefault()
  }
}

// In the app (not under node --test): the sheet and its palette entry. ⌘/
// reaches it through shortcuts.js and app.js's run().
if (globalThis.window?.rsz) {
  const isMac = navigator.platform.startsWith("Mac")
  const sheet = new HelpSheet(null, isMac)
  window.rsz.addCommands([
    { id: "help", title: "Keyboard shortcuts", category: "Help", hint: chordLabel("help", isMac), run: () => sheet.toggle() },
  ])
}
