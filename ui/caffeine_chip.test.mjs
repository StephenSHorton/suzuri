// node --test 'ui/*.test.mjs'
import { test } from "node:test"
import assert from "node:assert/strict"

import { chipText, chipTitle } from "./caffeine_chip.js"

const off = { active: false, remaining_s: null, label: "", error: null }
const on = (label, remaining_s = null) => ({ active: true, remaining_s, label, error: null })

test("the cup is always shown, with the time left when on", () => {
  assert.equal(chipText(off), "☕")
  assert.equal(chipText(undefined), "☕")
  assert.equal(chipText(on("∞")), "☕ ∞")
  assert.equal(chipText(on("14m", 840)), "☕ 14m")
  assert.equal(chipText(on("40s", 40)), "☕ 40s")
})

test("the tooltip says what a click does", () => {
  assert.match(chipTitle(off), /off.*click to keep it awake/)
  assert.match(chipTitle(on("∞")), /^Caffeine on: .*click to turn off/)
  assert.match(chipTitle(on("14m", 840)), /^Caffeine on for 14m: .*click to turn off/)
})
