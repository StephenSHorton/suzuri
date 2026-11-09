// node --test ui/test
// The frame decoder against frames that rsz-term's encoder wrote
// (crates/term/tests/frame_fixtures.rs → testdata/frames): the two sides of
// the wire format are kept in step here.
import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

// cellMetrics() measures with an OffscreenCanvas; node has none. Every
// glyph is 8px wide, ascent 11 and descent 3: cells are 8x15, baseline 11.
// A test can set ctx.measure(s) to answer (and record) measureText itself.
function recorder() {
  const ops = []
  const rec = (op) => (...a) => { ops.push({ op, a }) }
  const ctx = {
    ops,
    fillStyle: "#000",
    font: "",
    textBaseline: "",
    textAlign: "",
    measure: null,
    setTransform() {},
    fillRect(x, y, w, h) { ops.push({ op: "rect", style: ctx.fillStyle, r: [x, y, w, h] }) },
    fillText(t, x, y) { ops.push({ op: "text", style: ctx.fillStyle, font: ctx.font, t, at: [x, y] }) },
    measureText(s) {
      if (ctx.measure) { ops.push({ op: "measure", t: s }); return ctx.measure(s) }
      return { width: s.length * 8, fontBoundingBoxAscent: 11, fontBoundingBoxDescent: 3 }
    },
    save: rec("save"), restore: rec("restore"), beginPath: rec("beginPath"), rect: rec("pathRect"),
    clip: rec("clip"), translate: rec("translate"), scale: rec("scale"),
  }
  return ctx
}
globalThis.OffscreenCanvas = class {
  constructor(w, h) { this.width = w; this.height = h; this.ctx = recorder() }
  getContext() { return this.ctx }
}
const { Screen, DEFAULT_BG, FORMAT_VERSION } = await import("../frame.js")

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
    { version: h.version, full: h.full, cols: h.cols, rows: h.rows, cx: h.cx, cy: h.cy, shape: h.shape, seq: h.seq, nrows: h.nrows, history: h.history },
    { version: FORMAT_VERSION, full: true, cols: 10, rows: 3, cx: 2, cy: 1, shape: 3, seq: 7, nrows: 3, history: 4 },
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

test("a frame in another format version is refused, not drawn", () => {
  const { s, ops } = screen()
  const buf = load("full.bin")
  new Uint8Array(buf)[11] = FORMAT_VERSION + 1
  assert.throws(() => s.apply(buf), new RegExp(`terminal format v${FORMAT_VERSION + 1}, this app knows v${FORMAT_VERSION}`))
  assert.equal(ops.filter((o) => o.op === "text").length, 0)
})

// A one-row frame built here: runs [{col, attrs, cps}] and cluster table
// entries [{col, w, cps}], as frame.rs lays them out.
function build(cols, runs, clusters = []) {
  const b = []
  const u8 = (v) => b.push(v & 0xff)
  const u16 = (v) => { u8(v); u8(v >> 8) }
  const u32 = (v) => { u16(v & 0xffff); u16(v >>> 16) }
  u8(70); u8(1); u16(cols); u16(1); u16(0); u16(0); u8(1); u8(FORMAT_VERSION)
  u32(0); u32(0); u32(0); u32(1); u16(1); u16(clusters.length)
  u16(0); u16(runs.length); u16(0); u16(cols)
  for (const r of runs) {
    u16(r.col); u16(r.cps.length); u32(0xdddddd); u32(DEFAULT_BG); u16(r.attrs || 0); u16(0)
    r.cps.forEach(u32)
  }
  for (const c of clusters) { u16(0); u16(c.col); u8(c.w); u8(c.cps.length); c.cps.forEach(u32) }
  return new Uint8Array(b).buffer
}
const cps = (s) => [...s].map((c) => c.codePointAt(0))
// measureText for the emoji path: `width` wide, ascent 11, descent 3.
const fits = (width) => () => ({ width, actualBoundingBoxAscent: 11, actualBoundingBoxDescent: 3 })

test("a frame's clusters draw whole: emoji in the emoji faces, é in the run's", () => {
  const { s, ops } = screen()
  s.ctx.measure = fits(2 * CW)
  const h = s.apply(load("clusters.bin"))
  assert.equal(h.nclusters, 4)
  assert.deepEqual(texts(ops), [
    ["👍🏽", 0, BASE],
    ["é", 2 * CW, BASE],
    ["🇯🇵", 3 * CW, BASE],
    ["👨‍👩‍👧", 5 * CW, BASE],
  ])
  const font = (t) => ops.find((o) => o.op === "text" && o.t === t).font
  assert.match(font("👍🏽"), /"Apple Color Emoji".*"Segoe UI Emoji"/)
  assert.doesNotMatch(font("é"), /Emoji/)
  assert.ok(!ops.some((o) => o.op === "text" && o.t === "👍"), "no lone base")
  // They fit: no clip, no scale.
  assert.ok(!ops.some((o) => o.op === "save"))
})

test("an emoji wider than its cells is clipped and scaled down", () => {
  const { s, ops } = screen()
  s.ctx.measure = fits(30)
  s.apply(load("clusters.bin"))
  const i = ops.findIndex((o) => o.op === "text" && o.t === "👍🏽")
  assert.deepEqual(ops.slice(i - 6, i + 2).map((o) => o.op), ["save", "beginPath", "pathRect", "clip", "translate", "scale", "text", "restore"])
  assert.deepEqual(ops[i - 4].a, [0, 0, 2 * CW, CH])
  assert.deepEqual(ops[i - 2].a, [CW, BASE])
  assert.ok(ops[i - 1].a[0] < 1 && Math.abs(ops[i - 1].a[0] - 16 / 30) < 1e-9)
  assert.deepEqual(ops[i].at, [-15, 0])
  // e + U+0301 is not an emoji: drawn plainly.
  assert.ok(ops.some((o) => o.op === "text" && o.t === "é" && o.at[0] === 2 * CW))
})

test("an emoji is measured once, across frames", () => {
  const { s, ops } = screen()
  s.ctx.measure = fits(2 * CW)
  s.apply(load("clusters.bin"))
  s.apply(load("clusters.bin"))
  assert.equal(ops.filter((o) => o.op === "measure" && o.t === "👍🏽").length, 1)
  // A font change measures again.
  s.setStyle({ px: 15 })
  s.apply(load("clusters.bin"))
  assert.equal(ops.filter((o) => o.op === "measure" && o.t === "👍🏽").length, 2)
})

test("the plain fixtures take no cluster path", () => {
  const { s, ops } = screen()
  s.ctx.measure = fits(2 * CW)
  assert.equal(s.apply(load("full.bin")).nclusters, 0)
  s.apply(load("partial.bin"))
  assert.ok(!ops.some((o) => ["measure", "save", "scale"].includes(o.op)), "漢 is not an emoji")
  assert.ok(ops.filter((o) => o.op === "text").every((o) => !/Emoji/.test(o.font)))
})

test("a wide one-codepoint pictograph takes the emoji path; a narrow © does not", () => {
  const { s, ops } = screen()
  s.ctx.measure = fits(2 * CW)
  s.apply(build(6, [{ col: 0, cps: [0x1F600, 0, 0xA9, 0x20, 0x20, 0x20] }]))
  const t = ops.filter((o) => o.op === "text")
  assert.deepEqual(t.map((o) => [o.t, ...o.at]), [["😀", 0, BASE], ["©", 2 * CW, BASE]])
  assert.match(t[0].font, /Apple Color Emoji/)
  assert.doesNotMatch(t[1].font, /Emoji/)
})

test("a cluster on a space or with a VS15 draws as text", () => {
  const { s, ops } = screen()
  s.ctx.measure = fits(2 * CW)
  const frame = build(6, [{ col: 0, cps: [0x20, 0x2764, 0, 0x20, 0x20, 0x20] }], [
    { col: 0, w: 1, cps: [0x20, 0x301] },
    { col: 1, w: 2, cps: [0x2764, 0xFE0E] },
  ])
  s.apply(frame)
  const t = ops.filter((o) => o.op === "text")
  assert.deepEqual(t.map((o) => o.t), [" ́", "❤︎"])
  assert.ok(t.every((o) => !/Emoji/.test(o.font)))
})

test("bold on the bundled face: emoji get no double strike, é does", async () => {
  // Windows draws bundled bold as a 1px double strike (frame.js BOLD_FACE).
  const nav = Object.getOwnPropertyDescriptor(globalThis, "navigator")
  Object.defineProperty(globalThis, "navigator", { value: { platform: "Win32" }, configurable: true })
  const win = await import("../view/frame.js?win")
  if (nav) Object.defineProperty(globalThis, "navigator", nav)
  else delete globalThis.navigator
  const canvas = new OffscreenCanvas(1, 1)
  canvas.ctx.measure = fits(2 * CW)
  const s = new win.Screen(canvas)
  const buf = load("clusters.bin")
  new DataView(buf).setUint16(32 + 8 + 12, 1, true) // the run's attrs: bold
  s.apply(buf)
  const n = (t) => canvas.ctx.ops.filter((o) => o.op === "text" && o.t === t).length
  assert.equal(n("👍🏽"), 1)
  assert.equal(n("é"), 2)
})
