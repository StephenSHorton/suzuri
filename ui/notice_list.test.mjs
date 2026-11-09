// node --test 'ui/*.test.mjs'
import { test } from "node:test"
import assert from "node:assert/strict"

import { bellLabel, noticeTime, noticeText, moveIndex } from "./notice_list.js"

test("bell label shows the unread count, capped like Go", () => {
  assert.equal(bellLabel(0), "🔔")
  assert.equal(bellLabel(undefined), "🔔")
  assert.equal(bellLabel(3), "🔔 3")
  assert.equal(bellLabel(9), "🔔 9")
  assert.equal(bellLabel(12), "🔔 9+")
})

test("notice time is local HH:MM", () => {
  const at = new Date(2026, 10, 9, 7, 5).getTime()
  assert.equal(noticeTime(at), "07:05")
  assert.equal(noticeTime(new Date(2026, 0, 1, 23, 59).getTime()), "23:59")
})

test("notice text joins title and body", () => {
  assert.equal(noticeText({ title: "Build done", body: "3 warnings" }), "Build done — 3 warnings")
  assert.equal(noticeText({ title: "Copied", body: "" }), "Copied")
  assert.equal(noticeText({}), "")
})

test("moveIndex wraps and clamps", () => {
  assert.equal(moveIndex(0, "ArrowDown", 3), 1)
  assert.equal(moveIndex(2, "ArrowDown", 3), 0)
  assert.equal(moveIndex(0, "ArrowUp", 3), 2)
  assert.equal(moveIndex(1, "End", 3), 2)
  assert.equal(moveIndex(2, "Home", 3), 0)
  assert.equal(moveIndex(1, "x", 3), 1)
  assert.equal(moveIndex(4, "ArrowDown", 0), 0)
})
