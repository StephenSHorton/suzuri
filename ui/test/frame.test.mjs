// node --test ui/test
// The frame decoder against frames that rsz-term's encoder wrote
// (crates/term/tests/frame_fixtures.rs → testdata/frames): the two sides of
// the wire format are kept in step here.
import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

// cellMetrics() measures with an OffscreenCanvas; node has none. Every
// glyph is 8px wide, ascent 11 and descent 3: cells are 8x15, baseline 11.
function recorder() {
  const ops = []
  const ctx = {
    ops,
    fillStyle: "#000",
    font: "",
    textBaseline: "",
    setTransform() {},
    fillRect(x, y, w, h) { ops.push({ op: "rect", style: ctx.fillStyle, r: [x, y, w, h] }) },
    fillText(t, x, y) { ops.push({ op: "text", style: ctx.fillStyle, font: ctx.font, t, at: [x, y] }) },
    measureText(s) { return { width: s.length * 8, fontBoundingBoxAscent: 11, fontBoundingBoxDescent: 3 } },
  }
  return ctx
}
globalThis.OffscreenCanvas = class {
  constructor(w, h) { this.width = w; this.height = h; this.ctx = recorder() }
  getContext() { return this.ctx }
}
const { Screen, DEFAULT_BG } = await import("../frame.js")

const CW = 8, CH = 15, BASE = 11
const SELECTION = "rgba(184, 160, 200, 0.38)"
const load = (name) => {
  const b = readFileSync(new URL(`../../testdata/frames/${name}`, import.meta.url))
  return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength)
}
const texts = (ops) => ops.filter((o) => o.op === "text").map((o) => [o.t, ...o.at])
const rects = (ops, style) => ops.filter((o) => o.op === "rect" && o.style === style).map((o) => o.r)

function screen() {
  const canvas = new OffscreenCanvas(1, 1)
  return { s: new Screen(canvas), ops: canvas.ctx.ops }
}

test("the default background is the encoder's", () => {
  assert.equal(DEFAULT_BG, 0x01000000)
})

test("a full frame decodes to its header, text, colours and selection", () => {
  const { s, ops } = screen()
  const h = s.apply(load("full.bin"))
  assert.deepEqual(
    { full: h.full, cols: h.cols, rows: h.rows, cx: h.cx, cy: h.cy, shape: h.shape, seq: h.seq, nrows: h.nrows, history: h.history },
    { full: true, cols: 10, rows: 3, cx: 2, cy: 1, shape: 3, seq: 7, nrows: 3, history: 4 },
  )
  assert.equal(s.cols, 10)
  assert.equal(s.rows, 3)
  assert.deepEqual(texts(ops), [
    ["ab", 0, BASE],
    ["cd", 2 * CW, BASE],
    ["$ ", 0, CH + BASE],
    ["sel", 2 * CW, CH + BASE],
    ["漢", 0, 2 * CH + BASE],
    ["u", 3 * CW, 2 * CH + BASE],
  ])
  // The red and the white-on-blue runs keep their colours.
  assert.equal(ops.find((o) => o.op === "text" && o.t === "ab").style, "#cc3333")
  assert.deepEqual(rects(ops, "#3355cc"), [[2 * CW, 0, 2 * CW, CH]])
  // The selection starts at its own column, not the row's left edge.
  assert.deepEqual(rects(ops, SELECTION), [[2 * CW, CH, 3 * CW, CH]])
  // The box line is drawn into its cell; the underline sits under "u".
  const white = rects(ops, "#dddddd")
  assert.ok(white.some(([x, y, w]) => x >= 2 * CW && x < 3 * CW && y >= 2 * CH && w <= CW), JSON.stringify(white))
  assert.ok(white.some((r) => r.join() === [3 * CW, 3 * CH - 2, CW, 1].join()), JSON.stringify(white))
})

test("a partial frame repaints only its dirty columns", () => {
  const { s, ops } = screen()
  s.apply(load("full.bin"))
  ops.length = 0
  const h = s.apply(load("partial.bin"))
  assert.equal(h.full, false)
  assert.equal(h.seq, 8)
  const cleared = ops.filter((o) => o.op === "rect")[0]
  assert.deepEqual(cleared.r, [4 * CW, CH, 3 * CW, CH])
  assert.deepEqual(texts(ops), [["xyz", 4 * CW, CH + BASE]])
  assert.match(ops.find((o) => o.op === "text").font, /^italic /)
})
