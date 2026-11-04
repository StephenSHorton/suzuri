// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { drawCellGlyph } from "../boxdraw.js"

function rec() {
  const rects = []
  return { rects, fillStyle: "#fff", fillRect: (x, y, w, h) => rects.push([x, y, w, h]) }
}
const inside = (r, x, y, cw, ch) => r.every(([rx, ry, w, h]) => rx >= x && ry >= y && rx + w <= x + cw && ry + h <= y + ch)

test("lines run edge to edge, as Go's boxdraw", () => {
  const c = rec()
  assert.ok(drawCellGlyph(c, 0x2502, 16, 34, 8, 17)) // │
  assert.deepEqual(c.rects, [[20, 34, 1, 17]])
  const h = rec()
  drawCellGlyph(h, 0x2500, 16, 34, 8, 17) // ─
  assert.deepEqual(h.rects, [[16, 42, 8, 1]])
})

test("every handled glyph stays inside its cell", () => {
  for (let cp = 0x2500; cp <= 0x259f; cp++) {
    for (const [cw, ch] of [[8, 17], [9, 19], [13, 27]]) {
      const c = rec()
      if (!drawCellGlyph(c, cp, 100, 200, cw, ch)) continue
      assert.ok(inside(c.rects, 100, 200, cw, ch), `U+${cp.toString(16)} at ${cw}x${ch}`)
    }
  }
})

test("shades are Go's checkerboards; other text is left to the font", () => {
  const s = rec()
  drawCellGlyph(s, 0x2593, 0, 0, 8, 16) // ▓ step 1
  assert.equal(s.rects.length, 64)
  assert.equal(drawCellGlyph(rec(), 0x41, 0, 0, 8, 16), false)
  assert.equal(drawCellGlyph(rec(), 0x2571, 0, 0, 8, 16), false) // ╱ not in Go's set
  assert.ok(drawCellGlyph(rec(), 0xe0b0, 0, 0, 8, 16)) // powerline wedge
})

test("Windows cells come from Go's GDI numbers (#284 probe)", async () => {
  const { gdiCell } = await import("../frame.js")
  assert.deepEqual(gdiCell("Cascadia Mono", 14), [8, 19, 15])
  assert.deepEqual(gdiCell("Courier New", 14), [8, 18, 13])
  assert.deepEqual(gdiCell("GohuFont uni14 Nerd Font Mono", 22), [13, 25, 19])
  assert.equal(gdiCell("JetBrains Mono", 14), null) // not on the probe PC: canvas metrics
  assert.equal(gdiCell("Consolas", 40), null)
})
