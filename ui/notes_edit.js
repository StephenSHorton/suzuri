// Notes editor logic without the DOM or Tauri, so node can test it (#294).

// Go's NoteDisplayTitle: the name, else the body's first line, else Untitled.
export function displayTitle(n, max = 28) {
  let t = (n.title || "").trim()
  if (!t) t = ((n.body || "").trim().split("\n")[0] || "").trim()
  if (!t) return "Untitled"
  const cs = [...t]
  return cs.length <= max ? t : cs.slice(0, max - 1).join("") + "…"
}

// ⌘⇧M or ⌃⇧M on Mac (Go took Ctrl and Cmd alike), Ctrl+Shift+M elsewhere.
export function isNotesChord(e, isMac) {
  const mod = isMac ? e.metaKey || e.ctrlKey : e.ctrlKey
  return !!(mod && e.shiftKey && !e.altKey && e.code === "KeyM")
}

// What the rename field starts with, as Go's beginNotesTitleEdit: the stored
// name, else the body's first line (whole, not cut to 28), else empty.
export function renameSeed(n) {
  const t = (n.title || "").trim()
  if (t) return t
  return ((n.body || "").trim().split("\n")[0] || "").trim()
}

// Tab and Shift+Tab both insert a tab, as Go's editor did.
export function insertTab(value, start, end) {
  return { value: value.slice(0, start) + "\t" + value.slice(end), caret: start + 1 }
}

// True when nothing but the first line comes before the caret. Soft wrap is
// checked separately by measuring (notes_caret.js).
export function onFirstLine(value, caret) {
  return !value.slice(0, caret).includes("\n")
}

// The settings cursor name for the notes caret. Unknown names are a block,
// as config.rs's cursor_code.
export function caretShape(cursor) {
  return cursor === "underline" || cursor === "bar" ? cursor : "block"
}
