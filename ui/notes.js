// Notes (#294): Go suzuri's notes, in the modal panel (panel.js). Two
// screens, as in Go: the list, and the editor (name + body). The bank is Go's
// notes.json (app/src/notes.rs), so Go suzuri and `suzuri mcp` share it. The
// view is Astryx (chrome/Notes.jsx); the bank, saving and the keys that open
// it live here.
//
// Keys, as Go's: ⌘⇧M / ⌃⇧M (Ctrl+Shift+M on Windows) opens and closes. List:
// ↑↓, Enter/→/Tab opens, N new, D/Delete/Backspace deletes (asks first),
// F2/R renames, Esc closes. Editor: Esc back to the list, F2 or ↑ on the
// first row renames, Tab inserts a tab, ⌘N / Ctrl+N new note; undo, redo and
// selection are the text box's own. Name: Enter/↓ saves and goes to the
// body, Esc cancels.
import { createElement as h } from "react"
import { openPanel, closePanel, isOpen } from "./panel.js"
import { displayTitle, isNotesChord, caretShape } from "./notes_edit.js"
import { NotesView, NotesError } from "./chrome/Notes.jsx"

export { displayTitle }

const { invoke } = window.__TAURI__.core
const isMac = navigator.platform.startsWith("Mac")
const MAX_NOTES = 48 // Go's notesMaxBank
const MAX_RUNES = 64 * 1024 // notes.rs MAX_RUNES: a longer body is cut on save

let open_ = false
let saveTimer = 0

// What the view works on: the bank it edits in place, and saving. The view
// sets onStatus to hear how a save went.
const model = {
  bank: null, // {active_id, notes:[{id,title,body,updated}]}
  caret: "bar", // the terminal cursor setting, for the notes caret
  maxNotes: MAX_NOTES,
  maxRunes: MAX_RUNES,
  isMac,
  onStatus: null,
  save,
  now: () => new Date().toISOString(),
  newId: () => Array.from(crypto.getRandomValues(new Uint8Array(8)), (b) => b.toString(16).padStart(2, "0")).join(""),
}

function save(now_ = false) {
  clearTimeout(saveTimer)
  // Keep editing the same objects: the saved copy only differs by Rust's
  // repairs, which never apply to notes made here.
  const bank = model.bank
  const run = () => invoke("notes_save", { bank }).then(() => model.onStatus?.(null)).catch((e) => model.onStatus?.(`Not saved: ${e}`))
  if (now_) return run()
  saveTimer = setTimeout(run, 400)
}

export async function toggleNotes() {
  if (open_ && isOpen()) return closePanel()
  try {
    model.bank = await invoke("notes_load") // fresh from disk: Go suzuri may have written it
    model.caret = await invoke("config_get").then((c) => caretShape(c?.cursor), () => "bar")
  } catch (e) {
    open_ = true
    openPanel("Notes", h(NotesError, { error: `${e}` }), { onClose: () => { open_ = false } })
    return
  }
  open_ = true
  openPanel("Notes", h(NotesView, { model }), { wide: true, onClose: () => { open_ = false; save(true) } })
}

// Go's chord: ⌘⇧M or ⌃⇧M (Ctrl+Shift+M on Windows). Capture phase, so it
// works while the panel or a pane has focus.
window.addEventListener("keydown", (e) => {
  if (isNotesChord(e, isMac)) {
    e.preventDefault()
    e.stopPropagation()
    toggleNotes()
  }
}, true)

window.rsz?.addCommands([
  { id: "notes", title: "Notes", category: "Notes", hint: isMac ? "⌘⇧M / ⌃⇧M" : "Ctrl+Shift+M", run: () => toggleNotes() },
])
