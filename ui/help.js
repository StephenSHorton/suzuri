// Keyboard shortcuts sheet (⌘/ / Ctrl+/, palette "Keyboard shortcuts"):
// Go suzuri's help card (internal/chrome/help.go). The chords come from
// shortcuts.js's table and the command line rows carry a sample key that
// the test runs through bar.js, so the sheet can't drift from the keys.

import { chordLabel, chordLabels, shortcut } from "./shortcuts.js"

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

// The overlay: the sections in two columns (one when narrow); Esc, Enter,
// ⌘/ or a click outside closes it. It takes every key while open.
export class HelpSheet {
  constructor(host, isMac) {
    this.isMac = isMac
    this.el = document.createElement("div")
    this.el.className = "overlay help"
    this.el.hidden = true
    this.el.innerHTML = `<div class="card" tabindex="-1"><div class="head">Keyboard shortcuts</div><div class="cols"></div><div class="foot"><kbd>Esc</kbd> close</div></div>`
    host.appendChild(this.el)
    this.card = this.el.querySelector(".card")
    this.el.querySelector(".cols").replaceChildren(...helpSections(isMac).map(renderSection))
    this.el.addEventListener("mousedown", (e) => {
      if (e.target === this.el) { e.preventDefault(); this.close() }
    })
    window.addEventListener("keydown", (e) => this.onKey(e), true)
  }

  get isOpen() { return !this.el.hidden }

  toggle() { this.isOpen ? this.close() : this.open() }

  open() {
    if (this.isOpen) return
    this.back = document.activeElement
    this.el.hidden = false
    this.card.focus()
  }

  close() {
    if (!this.isOpen) return
    this.el.hidden = true
    this.back?.focus?.()
    this.back = null
  }

  onKey(e) {
    if (!this.isOpen || e.isComposing) return
    e.preventDefault()
    e.stopPropagation()
    if (e.key === "Escape" || e.key === "Enter" || shortcut(e, this.isMac)?.id === "help") this.close()
  }
}

function renderSection(s) {
  const sec = document.createElement("section")
  const h = document.createElement("h3")
  h.textContent = s.title
  sec.appendChild(h)
  for (const r of s.rows) {
    const row = document.createElement("div")
    row.className = "row"
    const k = document.createElement("span")
    k.className = "keys"
    k.textContent = r.keys
    const d = document.createElement("span")
    d.className = "desc"
    d.textContent = r.desc
    row.append(k, d)
    sec.appendChild(row)
  }
  return sec
}

// In the app (not under node --test): the sheet and its palette entry. ⌘/
// reaches it through shortcuts.js and app.js's run().
if (globalThis.window?.rsz) {
  const isMac = navigator.platform.startsWith("Mac")
  const sheet = new HelpSheet(document.getElementById("overlays") || document.body, isMac)
  window.rsz.addCommands([
    { id: "help", title: "Keyboard shortcuts", category: "Help", hint: chordLabel("help", isMac), run: () => sheet.toggle() },
  ])
}
