// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { confirmKey } from "../confirm.js"

const key = (key, mods = {}) => ({ key, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...mods })

test("Enter or the close chord again says yes, Esc says no", () => {
  assert.equal(confirmKey(key("Enter"), true), "yes")
  assert.equal(confirmKey(key("Escape"), true), "no")
  assert.equal(confirmKey(key("w", { metaKey: true }), true), "yes")
  assert.equal(confirmKey(key("W", { metaKey: true, shiftKey: true }), true), "yes")
  assert.equal(confirmKey(key("W", { ctrlKey: true, shiftKey: true }), false), "yes")
  assert.equal(confirmKey(key("w", { ctrlKey: true }), false), null)
  assert.equal(confirmKey(key("w"), true), null)
  assert.equal(confirmKey(key("x"), true), null)
  assert.equal(confirmKey(key("y"), true), null) // a stray key never quits
})
