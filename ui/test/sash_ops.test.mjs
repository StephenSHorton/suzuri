import { test } from "node:test"
import assert from "node:assert/strict"
import * as S from "../sash_ops.js"

test("the divider is the gap between the two sides", () => {
  assert.deepEqual(S.sashRect("h", { x: 0, y: 10, w: 100, h: 50 }, { x: 106, y: 10, w: 94, h: 50 }), { x: 100, y: 10, w: 6, h: 50 })
  assert.deepEqual(S.sashRect("v", { x: 0, y: 0, w: 80, h: 40 }, { x: 0, y: 46, w: 80, h: 40 }), { x: 0, y: 40, w: 80, h: 6 })
})

test("hits are padded across the divider only", () => {
  const col = { dir: "h", rect: { x: 100, y: 0, w: 6, h: 50 } }
  const row = { dir: "v", rect: { x: 0, y: 40, w: 80, h: 6 } }
  const all = [col, row]
  assert.equal(S.hitSash(all, 103, 20), col)
  assert.equal(S.hitSash(all, 96, 20), col) // the pad
  assert.equal(S.hitSash(all, 95, 20), null)
  assert.equal(S.hitSash(all, 109, 20), col)
  assert.equal(S.hitSash(all, 110, 20), null)
  assert.equal(S.hitSash(all, 103, 60), null) // past its length: no pad along it
  assert.equal(S.hitSash(all, 40, 37), row)
  assert.equal(S.hitSash(all, 40, 49), row)
  assert.equal(S.hitSash(all, 40, 50), null)
})

test("the ratio puts the divider's centre on the pointer, clamped", () => {
  // 0..206 with a 6 px gap: 200 px to share.
  assert.equal(S.ratioAt(0, 206, 6, 103), 0.5)
  assert.equal(S.ratioAt(0, 206, 6, 63), 0.3)
  assert.equal(S.ratioAt(50, 206, 6, 153), 0.5) // the split's own offset
  assert.equal(S.ratioAt(0, 206, 6, -40), 0.12) // Go's clamps
  assert.equal(S.ratioAt(0, 206, 6, 400), 0.88)
  // A min of 60 px a side beats 0.12 here.
  assert.equal(S.ratioAt(0, 206, 6, 0, 60), 0.3)
  assert.equal(S.ratioAt(0, 206, 6, 206, 60), 0.7)
  // Too small for two minimum sides: even.
  assert.equal(S.ratioAt(0, 106, 6, 10, 60), 0.5)
  assert.equal(S.ratioAt(0, 6, 6, 3), null)
})

test("double-click evens the run along the divider's direction", () => {
  // A | (B | C), with ratios, and a stacked split under C that keeps its own.
  const inner = { dir: "v", a: { pane: "C" }, b: { pane: "D" }, ratio: 0.3 }
  const right = { dir: "h", a: { pane: "B" }, b: inner, ratio: 0.7 }
  const top = { dir: "h", a: { pane: "A" }, b: right, ratio: 0.2 }
  assert.ok(S.equalizeRun(top))
  assert.equal(top.ratio, undefined)
  assert.equal(right.ratio, undefined)
  assert.equal(inner.ratio, 0.3)
  assert.equal(S.equalizeRun(top), false)
  assert.equal(S.equalizeRun({ pane: "A" }), false)
})

test("the resize gate passes through until held, then spaces sends out", () => {
  let t = 0
  const timers = []
  const g = S.resizeGate(80, () => t, (fn, ms) => timers.push({ fn, at: t + ms }))
  const sent = []
  g.run("a", () => sent.push("a1"))
  assert.deepEqual(sent, ["a1"])
  g.hold()
  g.run("a", () => sent.push("a2"))
  g.run("a", () => sent.push("a3")) // replaces a2
  g.run("b", () => sent.push("b1"))
  assert.deepEqual(sent, ["a1"])
  assert.equal(timers.length, 1)
  assert.equal(timers[0].at, 80)
  t = 80
  timers.shift().fn()
  assert.deepEqual(sent, ["a1", "a3", "b1"])
  g.run("a", () => sent.push("a4"))
  g.release()
  assert.deepEqual(sent, ["a1", "a3", "b1", "a4"])
  g.run("a", () => sent.push("a5"))
  assert.deepEqual(sent, ["a1", "a3", "b1", "a4", "a5"])
})
