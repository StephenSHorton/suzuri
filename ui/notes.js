// Notes (#294): Go suzuri's notes, in sparrow's panel (#286). Two screens,
// as in Go: the list, and the editor (name + body). The bank is Go's
// notes.json (app/src/notes.rs), so Go suzuri and `suzuri mcp` share it.
//
// Keys, as Go's: ⌘⇧M / ⌃⇧M (Ctrl+Shift+M on Windows) opens and closes. List:
// ↑↓, Enter/→/Tab opens, N new, D/Delete/Backspace deletes (asks first),
// F2/R renames, Esc closes. Editor: Esc back to the list, F2 or ↑ on the
// first row renames, Tab inserts a tab, ⌘N / Ctrl+N new note; undo, redo and
// selection are the text box's own. Name: Enter/↓ saves and goes to the
// body, Esc cancels.
import { openPanel, closePanel, isOpen, h } from "./panel.js"
import { displayTitle, isNotesChord, renameSeed, insertTab, onFirstLine, caretShape } from "./notes_edit.js"
import { caretStyle, onFirstRow } from "./notes_caret.js"

export { displayTitle }

const { invoke } = window.__TAURI__.core
const isMac = navigator.platform.startsWith("Mac")
const MAX_NOTES = 48 // Go's notesMaxBank

let bank = null // {active_id, notes:[{id,title,body,updated}]}
let panel = null
let saveTimer = 0
let pick = 0 // list cursor
let caret = "bar" // the terminal cursor setting, for the notes caret

const now = () => new Date().toISOString()
const newId = () => Array.from(crypto.getRandomValues(new Uint8Array(8)), (b) => b.toString(16).padStart(2, "0")).join("")
const active = () => bank.notes.find((n) => n.id === bank.active_id) || bank.notes[0]

const isBlank = (n) => !(n.title || "").trim() && !(n.body || "").trim()

function save(now_ = false) {
  clearTimeout(saveTimer)
  // Keep editing the same objects: the saved copy only differs by Rust's
  // repairs, which never apply to notes made here.
  const run = () => invoke("notes_save", { bank }).then(() => status("")).catch((e) => status(`Not saved: ${e}`, true))
  if (now_) return run()
  saveTimer = setTimeout(run, 400)
}

function status(text, err = false) {
  const s = panel?.el.querySelector(".rsz-status")
  if (s) { s.textContent = text; s.classList.toggle("err", err) }
}

function touch(n) {
  n.updated = now()
  save()
}

function newNote() {
  if (bank.notes.length >= MAX_NOTES) return status(`At most ${MAX_NOTES} notes.`, true)
  const n = { id: newId(), title: "", body: "", updated: now() }
  bank.notes.push(n)
  bank.active_id = n.id
  save()
  showEditor(true)
}

function deleteActive() {
  const n = active()
  const last = bank.notes.length <= 1
  const label = displayTitle(n)
  const ask = last ? `Clear “${label}”? The last note is emptied, not removed.` : `Delete “${label}”? This cannot be undone.`
  confirmBox(ask, last ? "Clear" : "Delete", () => {
    if (last) Object.assign(n, { title: "", body: "", updated: now() })
    else {
      const i = bank.notes.indexOf(n)
      bank.notes.splice(i, 1)
      bank.active_id = bank.notes[Math.min(i, bank.notes.length - 1)].id
      pick = Math.min(pick, bank.notes.length - 1)
    }
    save(true)
    showList()
  })
}

function confirmBox(text, yes, onYes) {
  const body = panel.body
  body.replaceChildren(
    h("div", {}, text),
    h("div", { class: "rsz-row end" },
      h("button", { onclick: () => showList() }, "Cancel"),
      h("button", { class: "primary", onclick: onYes }, yes)),
  )
  body.querySelector("button.primary").focus()
}

function showList() {
  const body = panel.body
  pick = Math.max(0, Math.min(pick, bank.notes.length - 1))
  const rows = bank.notes.map((n, i) =>
    h("button", { class: "notes-row" + (i === pick ? " on" : ""), onclick: () => { pick = i; open(i) } },
      h("span", { class: "notes-name" }, displayTitle(n, 48)),
      h("span", { class: "notes-when" }, (n.updated || "").slice(0, 10))))
  const list = h("div", { class: "notes-list", tabindex: "0" }, rows)
  list.onkeydown = (e) => {
    const k = e.key.toLowerCase()
    const move = (d) => { pick = (pick + d + bank.notes.length) % bank.notes.length; showList() }
    if (k === "arrowup") move(-1)
    else if (k === "arrowdown") move(1)
    else if (k === "enter" || k === "arrowright" || k === "tab") open(pick)
    else if (k === "n" && !e.metaKey) newNote()
    else if (k === "d" || k === "delete" || k === "backspace") { bank.active_id = bank.notes[pick].id; deleteActive() }
    else if (k === "f2" || k === "r") { bank.active_id = bank.notes[pick].id; showEditor(true) }
    else return
    e.preventDefault()
  }
  body.replaceChildren(
    list,
    h("div", { class: "rsz-row" },
      h("button", { onclick: newNote }, "New note"),
      h("span", { class: "rsz-hint" }, "↵ open · N new · D delete · R rename · Esc close"),
      h("span", { class: "rsz-status" })),
  )
  list.focus()
  rows[pick]?.scrollIntoView({ block: "nearest" })
}

function open(i) {
  bank.active_id = bank.notes[i].id
  save()
  showEditor(false)
}

function showEditor(nameFirst) {
  const n = active()
  const name = h("input", { type: "text", class: "notes-title", placeholder: displayTitle(n) === "Untitled" ? "Name this note" : displayTitle(n), spellcheck: "false" })
  name.value = n.title || ""
  const text = h("textarea", { class: "notes-body", spellcheck: "false", placeholder: "Write here. Saved as you type." })
  text.value = n.body || ""
  // Renaming starts whenever the name gets focus: it's filled with the
  // stored or derived name, saves as you type, and Esc puts back the old one.
  let before = null
  name.onfocus = () => {
    if (before !== null) return
    before = n.title || ""
    name.value = renameSeed(n)
    name.select()
  }
  name.onblur = () => { before = null; name.value = n.title || "" }
  name.oninput = () => { n.title = name.value.trim() ? name.value : ""; touch(n) }
  text.oninput = () => { n.body = text.value; touch(n) }
  name.onkeydown = (e) => {
    if (e.isComposing) return
    if (e.key === "Enter" || e.key === "ArrowDown") {
      e.preventDefault()
      // As Go: what's in the field is the name, even an untouched seed.
      const t = name.value.trim()
      if (n.title !== t) { n.title = t; touch(n) }
      text.focus()
      text.setSelectionRange(0, 0)
      text.scrollTop = 0
    } else if (e.key === "Escape") {
      e.preventDefault()
      e.stopPropagation()
      if (n.title !== before) { n.title = before; touch(n) }
      text.focus()
    }
  }
  text.onkeydown = (e) => {
    if (e.isComposing) return
    const mod = isMac ? e.metaKey : e.ctrlKey
    if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); pick = bank.notes.indexOf(n); save(true); showList() }
    else if (e.key === "F2") { e.preventDefault(); name.focus() }
    else if (e.key === "Tab" && !mod && !e.altKey) { e.preventDefault(); typeTab(text) }
    else if (e.key === "ArrowUp" && !e.shiftKey && !mod && !e.altKey && atTop(text)) { e.preventDefault(); name.focus() }
    else if (mod && e.key.toLowerCase() === "n" && !e.shiftKey) { e.preventDefault(); newNote() }
  }
  panel.body.replaceChildren(
    name,
    text,
    h("div", { class: "rsz-row" },
      h("button", { onclick: () => { pick = bank.notes.indexOf(n); showList() } }, "All notes"),
      h("button", { onclick: deleteActive }, isBlank(n) && bank.notes.length <= 1 ? "Clear" : "Delete"),
      h("span", { class: "rsz-hint" }, "Esc list · F2 or ↑ rename · Tab indents"),
      h("span", { class: "rsz-status" })),
  )
  caretStyle(name, caret)
  caretStyle(text, caret)
  if (nameFirst || isBlank(n)) name.focus()
  else { text.focus(); text.setSelectionRange(text.value.length, text.value.length) }
}

// Tab through the text box's own editing, so ⌘Z undoes it.
function typeTab(text) {
  if (document.execCommand?.("insertText", false, "\t")) return
  const r = insertTab(text.value, text.selectionStart, text.selectionEnd)
  text.value = r.value
  text.setSelectionRange(r.caret, r.caret)
  text.oninput()
}

// ↑ leaves the body for the name on its first row, wrapped rows included.
function atTop(text) {
  if (text.selectionStart !== text.selectionEnd || !onFirstLine(text.value, text.selectionStart)) return false
  try {
    return onFirstRow(text)
  } catch {
    return true
  }
}

export async function toggleNotes() {
  if (panel && isOpen()) return closePanel()
  try {
    bank = await invoke("notes_load") // fresh from disk: Go suzuri may have written it
    caret = await invoke("config_get").then((c) => caretShape(c?.cursor), () => "bar")
  } catch (e) {
    panel = openPanel("Notes", (body) => body.append(h("div", { class: "rsz-status err" }, `${e}`)))
    return
  }
  pick = Math.max(0, bank.notes.findIndex((n) => n.id === bank.active_id))
  panel = openPanel("Notes", () => {}, { wide: true, onClose: () => { save(true); panel = null } })
  showEditor(false)
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
