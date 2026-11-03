import { test } from "node:test"
import assert from "node:assert/strict"
import * as L from "../layout_ops.js"

// A | (B over C)
const mk = () => ({ dir: "h", a: { pane: "A" }, b: { dir: "v", a: { pane: "B" }, b: { pane: "C" } } })
const shape = (n) => (n.pane ? n.pane : `${n.dir}(${shape(n.a)},${shape(n.b)})`)

test("swap and the neighbour that move uses", () => {
  const t = mk()
  const rects = [
    { pane: "A", x: 0, y: 0, w: 50, h: 100 },
    { pane: "B", x: 50, y: 0, w: 50, h: 50 },
    { pane: "C", x: 50, y: 50, w: 50, h: 50 },
  ]
  assert.equal(L.neighbor(rects, "A", 1, 0), "B")
  assert.equal(L.neighbor(rects, "C", 0, -1), "B")
  assert.equal(L.neighbor(rects, "A", -1, 0), null)
  assert.ok(L.swapPanes(t, "A", L.neighbor(rects, "A", 1, 0)))
  assert.equal(shape(t), "h(B,v(A,C))")
  assert.equal(L.swapPanes(t, "A", "A"), false)
})

test("remove folds the sibling up", () => {
  let t = mk()
  t = L.removePane(t, "B")
  assert.equal(shape(t), "h(A,C)")
  t = L.removePane(t, "A")
  assert.equal(shape(t), "C")
  assert.equal(L.removePane(t, "C"), null)
})

test("dock beside on each edge", () => {
  assert.equal(shape(L.dockBeside(mk(), "A", "C", "right")), "v(B,h(C,A))")
  assert.equal(shape(L.dockBeside(mk(), "C", "A", "top")), "h(v(C,A),B)")
  assert.equal(shape(L.dockBeside(mk(), "B", "A", "left")), "h(h(B,A),C)")
  assert.equal(L.dockBeside(mk(), "A", "A", "left"), null)
  assert.equal(L.dockBeside(mk(), "A", "B", "middle"), null)
})

test("grow, shrink, equalize, rotate, swap sides", () => {
  const t = mk()
  // Root weighs A 1 : (B,C) 1 (different dir), so A starts at 0.5.
  assert.ok(L.adjustRatio(t, "A", 0.08))
  assert.equal(t.ratio.toFixed(2), "0.58")
  assert.ok(L.adjustRatio(t.b.a.pane === "B" ? t : t, "B", 0.08)) // B is a of the inner split
  assert.equal(t.b.ratio.toFixed(2), "0.58")
  L.adjustRatio(t, "C", 1)
  assert.equal(t.b.ratio, L.RATIO_MIN)
  assert.ok(L.equalize(t, "C"))
  assert.equal(t.b.ratio, 0.5)
  assert.ok(L.rotate(t, "B"))
  assert.equal(t.b.dir, "h")
  t.ratio = 0.7
  assert.ok(L.swapSides(t, "A"))
  assert.equal(shape(t), "h(h(B,C),A)")
  assert.equal(t.ratio.toFixed(2), "0.30")
  assert.equal(L.equalize({ pane: "Z" }, "Z"), false)
})

test("drop edge and no-op drops", () => {
  const r = { x: 0, y: 0, w: 100, h: 100 }
  assert.equal(L.dropEdge(r, 5, 50), "left")
  assert.equal(L.dropEdge(r, 95, 40), "right")
  assert.equal(L.dropEdge(r, 50, 2), "top")
  assert.equal(L.dropEdge(r, 50, 98), "bottom")
  const t = { dir: "h", a: { pane: "A" }, b: { pane: "B" } }
  assert.equal(L.dropChanges(t, "A", "B", "left"), false) // A is already left of B
  assert.equal(L.dropChanges(t, "A", "B", "right"), true)
  assert.equal(L.dropChanges(t, "A", "B", "top"), true)
  assert.equal(shape(t), "h(A,B)") // untouched
})
