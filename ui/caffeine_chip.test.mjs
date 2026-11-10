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

test("each caffeine change says what it did (Go's toasts)", async () => {
  const { caffeineToast } = await import("./caffeine_chip.js")
  assert.equal(caffeineToast("toggle", off, on("∞")), "caffeine on · sleep prevented")
  assert.equal(caffeineToast("toggle", on("∞"), off), "caffeine off")
  assert.equal(caffeineToast("on", off, on("15m", 900), 15), "caffeine on · 15m")
  assert.equal(caffeineToast("on", off, on("1h", 3600), 60), "caffeine on · 1h")
  assert.equal(caffeineToast("off", on("∞"), off), "caffeine off")
  assert.equal(caffeineToast("off", off, off), "caffeine already off")
  assert.equal(caffeineToast("toggle", off, { ...off, error: "denied" }), "caffeine failed: denied")
})

test("caffeine is quiet at launch and on label refreshes", async () => {
  const { caffeineToast } = await import("./caffeine_chip.js")
  assert.equal(caffeineToast(null, null, on("∞")), "")
  assert.equal(caffeineToast(null, on("14m", 840), on("13m", 780)), "")
  // A launch failure is said once.
  const err = { ...off, error: "denied" }
  assert.equal(caffeineToast(null, null, err), "caffeine failed: denied")
  assert.equal(caffeineToast(null, err, err), "")
})

test("a timed hold running out says so; turning it off early doesn't", async () => {
  const { caffeineToast } = await import("./caffeine_chip.js")
  assert.equal(caffeineToast(null, on("1s", 1), off), "caffeine off · timer ended")
  assert.equal(caffeineToast(null, on("∞"), off), "")
  // The label refresh lagged (a background window): the due time decides.
  const due = { ...on("5m", 300), ends: 10_000 }
  assert.equal(caffeineToast(null, due, off, undefined, 9_500), "caffeine off · timer ended")
  assert.equal(caffeineToast(null, due, off, undefined, 1_000), "")
})
