// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { mirrorParts } from "../notes_caret.js"

// The mirror keeps the rest of the value, so a word that wraps in the field
// (its start on row 2) wraps the same way and onFirstRow sees row 2.
test("mirror keeps the text after the caret so words wrap as in the field", () => {
  const v = "aaaa bbbb cccc dddd eeee"
  assert.deepEqual(mirrorParts(v, 20), ["aaaa bbbb cccc dddd ", "e", "eee"])
  assert.equal(mirrorParts(v, 20).join(""), v)
})

test("mirror marks a space at the end and keeps a newline under the caret", () => {
  assert.deepEqual(mirrorParts("ab", 2), ["ab", " ", ""])
  assert.deepEqual(mirrorParts("ab\ncd", 2), ["ab", " ", "\ncd"])
})
