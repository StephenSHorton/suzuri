// node --test 'ui/*.test.mjs'
import { test } from "node:test"
import assert from "node:assert/strict"

import { fontToast, hostToast } from "./host_toast.js"

test("font zoom says the size, as Go's zoomFont does", () => {
  assert.equal(fontToast(1, 14, 15, 14), "font 15px")
  assert.equal(fontToast(-1, 14, 13, 14), "font 13px")
  assert.equal(fontToast(1, 36, 36, 14), "font 36px (limit)")
  assert.equal(fontToast(0, 18, 14, 14), "font 14px (reset)")
  assert.equal(fontToast(0, 14, 14, 14), "font 14px (default)")
})

test("hostToast outside the app does nothing", () => {
  assert.doesNotThrow(() => hostToast("copied"))
  assert.doesNotThrow(() => hostToast(""))
})
