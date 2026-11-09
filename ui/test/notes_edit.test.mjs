// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { displayTitle, isNotesChord, renameSeed, insertTab, onFirstLine, caretShape } from "../notes_edit.js"

const key = (mods = {}, code = "KeyM") => ({ code, metaKey: false, ctrlKey: false, altKey: false, shiftKey: true, ...mods })

test("Mac: ⌘⇧M and ⌃⇧M both toggle Notes, as Go", () => {
  assert.ok(isNotesChord(key({ metaKey: true }), true))
  assert.ok(isNotesChord(key({ ctrlKey: true }), true))
  assert.ok(!isNotesChord(key({ metaKey: true, shiftKey: false }), true))
  assert.ok(!isNotesChord(key({ metaKey: true, altKey: true }), true))
  assert.ok(!isNotesChord(key({ metaKey: true }, "KeyN"), true))
})

test("Windows: only Ctrl+Shift+M toggles Notes", () => {
  assert.ok(isNotesChord(key({ ctrlKey: true }), false))
  assert.ok(!isNotesChord(key({ metaKey: true }), false))
})

test("rename starts from the stored name, else the body's first line", () => {
  assert.equal(renameSeed({ title: "  Plan ", body: "x" }), "Plan")
  assert.equal(renameSeed({ title: "", body: "\n  first line that is longer than twenty-eight  \nsecond" }), "first line that is longer than twenty-eight")
  assert.equal(renameSeed({ title: " ", body: "  " }), "")
  assert.equal(displayTitle({ title: "", body: "" }), "Untitled")
})

test("Tab replaces the selection with a tab", () => {
  assert.deepEqual(insertTab("ab", 1, 1), { value: "a\tb", caret: 2 })
  assert.deepEqual(insertTab("abcd", 1, 3), { value: "a\td", caret: 2 })
})

test("first line: no newline before the caret", () => {
  assert.ok(onFirstLine("", 0))
  assert.ok(onFirstLine("abc\ndef", 3))
  assert.ok(!onFirstLine("abc\ndef", 4))
})

test("caret shape follows the cursor setting; unknown is a block", () => {
  assert.equal(caretShape("underline"), "underline")
  assert.equal(caretShape("bar"), "bar")
  assert.equal(caretShape("block"), "block")
  assert.equal(caretShape(undefined), "block")
})
