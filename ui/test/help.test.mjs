// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { helpSections } from "../help.js"
import { chordLabels, shortcut } from "../shortcuts.js"
import { InputBar, barKey } from "../bar.js"
import { clipShortcut } from "../view/index.js"

const key = (key, mods = {}, code = "") => ({ key, code, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...mods })

for (const isMac of [true, false]) {
  const os = isMac ? "macOS" : "Windows"
  const rows = helpSections(isMac).flatMap((s) => s.rows)

  test(`${os}: the sheet lists every chord in shortcuts.js`, () => {
    const listed = new Set(rows.flatMap((r) => r.ids || []))
    const missing = Object.entries(chordLabels(isMac)).filter(([id, label]) => label && !listed.has(id)).map(([id]) => id)
    assert.deepEqual(missing, [])
  })

  test(`${os}: every row has keys and a description`, () => {
    for (const r of rows) assert.ok(r.keys && r.desc, JSON.stringify(r))
  })

  test(`${os}: command line and terminal rows are real keys`, () => {
    const cl = helpSections(isMac).find((s) => s.title === "Command line").rows
    for (const r of cl) {
      const bar = new InputBar()
      bar.insert("echo hi")
      assert.notEqual(barKey(bar, r.ev, { isMac, cols: 80 }), null, r.keys)
    }
    const term = helpSections(isMac).find((s) => s.title === "Terminal").rows.filter((r) => r.ev)
    assert.equal(term.length, 2)
    for (const r of term) assert.ok(clipShortcut(r.ev, isMac, false), r.keys)
  })
}

test("arrow chords collapse to one row", () => {
  const panes = helpSections(true).find((s) => s.title === "Panes").rows
  assert.equal(panes.find((r) => r.desc === "Focus pane").keys, "⌘⌥←→↑↓")
  const win = helpSections(false).find((s) => s.title === "Panes").rows
  assert.equal(win.find((r) => r.desc === "Focus pane").keys, "Alt+←→↑↓")
  assert.equal(win.find((r) => r.desc === "Move pane").keys, "Ctrl+Alt+←→↑↓")
})

test("⌘/ and Ctrl+/ open the sheet", () => {
  assert.equal(shortcut(key("/", { metaKey: true }, "Slash"), true)?.id, "help")
  assert.equal(shortcut(key("/", { ctrlKey: true }, "Slash"), false)?.id, "help")
  // Ctrl+/ on macOS stays the terminal's (^_).
  assert.equal(shortcut(key("/", { ctrlKey: true }, "Slash"), true), null)
})
