// node --test ui/view/test
// Style and metrics are per Screen in the package; suzuri's frame.js shim
// keeps one window style that its Screens follow.
import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

function recorder() {
  const ops = []
  const ctx = {
    ops, fillStyle: "#000", font: "14px x", textBaseline: "",
    setTransform() {},
    fillRect(x, y, w, h) { ops.push({ op: "rect", style: ctx.fillStyle, r: [x, y, w, h] }) },
    fillText(t, x, y) { ops.push({ op: "text", t, at: [x, y] }) },
    measureText(s) {
      const px = Number(/(\d+)px/.exec(ctx.font)?.[1] || 14)
      return { width: s.length * Math.round(px * 4 / 7), fontBoundingBoxAscent: Math.round(px * 0.8), fontBoundingBoxDescent: Math.round(px * 0.2) }
    },
  }
  return ctx
}
globalThis.OffscreenCanvas = class {
  constructor(w, h) { this.width = w; this.height = h; this.ctx = recorder() }
  getContext() { return this.ctx }
}
const view = await import("../frame.js")
const shim = await import("../../frame.js")

const load = (name) => {
  const b = readFileSync(new URL(`../../../testdata/frames/${name}`, import.meta.url))
  return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength)
}

test("two Screens with different styles paint with their own", () => {
  const a = new view.Screen(new OffscreenCanvas(1, 1))
  const b = new view.Screen(new OffscreenCanvas(1, 1), 1, { ...view.DEFAULT_STYLE, void: "#222222", selection: "red" })
  b.setStyle(view.fontStyle("Mono", 21))
  assert.deepEqual([a.cellMetrics().cw, a.cellMetrics().ch], [8, 15])
  assert.deepEqual([b.cellMetrics().cw, b.cellMetrics().ch], [12, 22])
  a.apply(load("full.bin"))
  b.apply(load("full.bin"))
  assert.equal(a.canvas.ctx.ops[0].style, view.DEFAULT_STYLE.void)
  assert.equal(b.canvas.ctx.ops[0].style, "#222222")
  assert.ok(b.canvas.ctx.ops.some((o) => o.style === "red"), "b's selection colour")
  assert.ok(!a.canvas.ctx.ops.some((o) => o.style === "red"))
})

test("a partial style takes the rest from DEFAULT_STYLE", () => {
  const s = new view.Screen(new OffscreenCanvas(1, 1), 1, { void: "#000000" })
  assert.equal(s.style.void, "#000000")
  assert.equal(s.style.family, view.DEFAULT_STYLE.family)
  const m = s.cellMetrics()
  assert.deepEqual([m.cw, m.ch], [8, 15])
})

test("a Screen keeps its metrics object until its font changes", () => {
  const s = new view.Screen(new OffscreenCanvas(1, 1))
  const m = s.cellMetrics()
  s.setStyle({ void: "#000000", selection: "blue" })
  assert.equal(s.cellMetrics(), m, "colours don't re-measure")
  s.setStyle({ px: 14 })
  assert.equal(s.cellMetrics(), m, "the same font doesn't re-measure")
  s.setStyle({ px: 16 })
  assert.notEqual(s.cellMetrics(), m)
})

test("suzuri's Screens follow the window style without clearing on every frame", () => {
  const s = new shim.Screen(new OffscreenCanvas(1, 1))
  s.apply(load("full.bin"))
  const ops = s.canvas.ctx.ops
  ops.length = 0
  s.apply(load("partial.bin"))
  // A partial frame clears only its dirty columns: no full-canvas resize.
  assert.deepEqual(ops.filter((o) => o.op === "rect")[0].r, [4 * 8, 15, 3 * 8, 15])

  shim.setVoid("#123456")
  ops.length = 0
  s.apply(load("partial.bin"))
  assert.equal(ops[0].style, "#123456", "the new void, same size")
  assert.deepEqual(ops[0].r, [4 * 8, 15, 3 * 8, 15])

  shim.setFont("Mono", 21)
  assert.deepEqual([shim.cellMetrics().cw, shim.cellMetrics().ch], [12, 22])
  // refitFont's setSize picks up the font before any frame.
  assert.equal(s.setSize(10, 3), true)
  assert.equal(s.canvas.width, 10 * 12)
  assert.equal(s.cellMetrics(), shim.cellMetrics())
  assert.equal(s.setSize(10, 3), false)
})

test("styleState round-trips into Screen fields (the paint worker's copy)", () => {
  shim.setSelection("rgba(1, 2, 3, 0.38)")
  const st = shim.styleState()
  const w = new view.Screen(new OffscreenCanvas(1, 1))
  w.setStyle(shim.styleFields(st))
  assert.equal(w.style.selection, "rgba(1, 2, 3, 0.38)")
  assert.equal(w.style.void, "#123456")
  assert.deepEqual(w.cellMetrics(), shim.cellMetrics())
})

test("measureCell without OffscreenCanvas measures in the document it is given, not a global", () => {
  const saved = globalThis.OffscreenCanvas
  delete globalThis.OffscreenCanvas
  try {
    assert.equal(globalThis.document, undefined)
    const made = []
    const doc = { createElement: (tag) => { made.push(tag); return { getContext: () => recorder() } } }
    const m = view.measureCell({ ...view.DEFAULT_STYLE, face: "" }, doc)
    assert.deepEqual(made, ["canvas"])
    assert.deepEqual([m.cw, m.ch], [8, 15])
    // A main-thread Screen measures in its canvas's document.
    const canvas = { ownerDocument: doc, getContext: () => recorder(), style: {} }
    new view.Screen(canvas).cellMetrics()
    assert.deepEqual(made, ["canvas", "canvas"])
    assert.throws(() => view.measureCell(view.DEFAULT_STYLE), /no OffscreenCanvas and no document/)
  } finally {
    globalThis.OffscreenCanvas = saved
  }
})
