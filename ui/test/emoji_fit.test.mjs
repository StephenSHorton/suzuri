// node --test ui/test
// The window Screen (ui/frame.js) re-fits emoji when the window font
// changes: a fit measured for wide cells must not be reused for narrow ones.
import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

// Fonts named "wide" advance 10px a glyph, others 7px. Emoji are 16px wide.
function recorder() {
  const ops = []
  const ctx = {
    ops, fillStyle: "#000", font: "", textBaseline: "",
    setTransform() {}, fillRect() {},
    fillText(t, x, y) { ops.push({ op: "text", t, at: [x, y] }) },
    measureText(s) {
      if (/Emoji/.test(ctx.font)) return { width: 16, actualBoundingBoxAscent: 11, actualBoundingBoxDescent: 3 }
      const w = /wide/.test(ctx.font) ? 10 : 7
      return { width: s.length * w, fontBoundingBoxAscent: 11, fontBoundingBoxDescent: 3 }
    },
    save() { ops.push({ op: "save" }) }, restore() {}, beginPath() {}, rect() {}, clip() {}, translate() {},
    scale(x) { ops.push({ op: "scale", x }) },
  }
  return ctx
}
globalThis.OffscreenCanvas = class {
  constructor(w, h) { this.width = w; this.height = h; this.ctx = recorder() }
  getContext() { return this.ctx }
}
const F = await import("../frame.js")

function load(name) {
  const b = readFileSync(new URL(`../../testdata/frames/${name}`, import.meta.url))
  return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength)
}

test("a window font change re-fits emoji", () => {
  F.setFont("wide", 14)
  const c = new OffscreenCanvas(1, 1)
  const s = new F.Screen(c)
  s.apply(load("clusters.bin"))
  assert.equal(s.cellMetrics().cw, 10)
  assert.ok(!c.ctx.ops.some(o => o.op === "scale"), "16px fits a 20px box unscaled")
  F.setFont("narrow", 14)
  c.ctx.ops.length = 0
  s.apply(load("clusters.bin"))
  assert.equal(s.cellMetrics().cw, 7)
  assert.ok(c.ctx.ops.some(o => o.op === "scale" && o.x < 1), "16px is scaled into a 14px box")
})
