// Command palette (⌘K / Ctrl+K): Go suzuri's filter-and-run list
// (internal/chrome/palette.go). A plain substring match over title, chord
// and category; Up/Down to move, Enter to run, Esc to close. The view is
// Astryx's CommandPalette (chrome/Palette.jsx), mounted on the React chrome
// through chrome/overlays.js; this module keeps the API app.js uses.
import { createElement as h } from "react"
import { mountOverlay, unmountOverlay } from "./chrome/overlays.js"

export function filterCommands(items, query) {
  const q = query.trim().toLowerCase()
  if (!q) return items.slice()
  // Every word must appear somewhere ("move 2" finds "Move pane to tab: 2 …").
  const words = q.split(/\s+/)
  return items.filter((it) => {
    const hay = `${it.title} ${it.hint || ""} ${it.category || ""}`.toLowerCase()
    return words.every((w) => hay.includes(w))
  })
}

const MAC_MODS = { "⌘": "meta", "⌥": "alt", "⇧": "shift", "⌃": "ctrl" }
const NAMED = { "←": "left", "→": "right", "↑": "up", "↓": "down", "+": "plus", enter: "enter", tab: "tab", esc: "escape", escape: "escape" }
const keyName = (k) => NAMED[k.toLowerCase()] || k

// One chord label ("Ctrl+Shift+T", "Ctrl++", "⌘⇧W", "F2") as Kbd keys
// ("ctrl+shift+T", "ctrl+plus", "meta+shift+W", "F2"); null when it is not
// a chord (a profile's shell, "Stay awake 15m").
function chordKeys(label) {
  let s = label.trim()
  const mods = []
  while (s.length > 1 && MAC_MODS[s[0]]) {
    mods.push(MAC_MODS[s[0]])
    s = s.slice(1)
  }
  if (!mods.length) {
    const m = /^((?:(?:ctrl|alt|shift|win|cmd)\+)+)(.+)$/i.exec(s)
    if (m) {
      for (const mod of m[1].split("+").filter(Boolean)) {
        const l = mod.toLowerCase()
        mods.push(l === "cmd" || l === "win" ? "meta" : l)
      }
      s = m[2]
    }
  }
  if (!s || /\s/.test(s)) return null
  if (!mods.length && !/^F\d{1,2}$/.test(s)) return null
  if ([...s].length > 5 && !NAMED[s.toLowerCase()]) return null
  return [...mods, keyName(s)].join("+")
}

// A command's hint as Kbd chords, one per alternative ("Ctrl+Shift+] ·
// Ctrl+Tab", "⌘⇧M / ⌃⇧M"); null when any part is not a chord, so the hint
// shows as plain text.
export function hintChords(hint) {
  if (!hint) return null
  const chords = hint.split(/\s+[·/]\s+/).map(chordKeys)
  return chords.every(Boolean) ? chords : null
}

// The view is JSX (Vite only): loaded when the first Palette is made, so
// node's tests can import filterCommands without it.
let viewModule = null
const loadView = () => (viewModule ??= import("./chrome/Palette.jsx"))

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
    this.run = run
    this.shown = true
    // A fresh key each time, so the query and highlight start over.
    const key = ++this.serial
    loadView().then(({ PaletteView }) => {
      if (!this.shown || key !== this.serial) return
      mountOverlay("palette", h(PaletteView, {
        key,
        items,
        onDone: (it) => this.choose(it),
        onToggle: () => this.close(),
      }))
    })
  }

  close() {
    if (!this.shown) return
    this.shown = false
    unmountOverlay("palette")
    this.onClose?.()
  }

  choose(it) {
    if (!this.shown) return
    this.close()
    if (it) this.run(it)
  }
}
