import { test } from "node:test"
import assert from "node:assert/strict"
import { layoutOf, restorePlan } from "../kept_layout.js"

const pane = (id, extra = {}) => ({ id, exited: false, titleOverride: "", ...extra })

test("the saved layout keeps tabs, splits, ratios, names and focus by id", () => {
  const p1 = pane(1), p2 = pane(2, { titleOverride: "logs" }), p3 = pane(3)
  const tabs = [
    { name: "", sticky: "zsh", tree: { dir: "h", ratio: 0.3, a: { pane: p1 }, b: { dir: "v", a: { pane: p2 }, b: { pane: p3 } } }, focused: p2, zoomed: null },
    { name: "work", sticky: "", tree: { pane: pane(9) }, focused: null, zoomed: null },
  ]
  const l = layoutOf(tabs, tabs[1])
  assert.equal(l.active, 1)
  assert.deepEqual(l.tabs[0].tree, { dir: "h", ratio: 0.3, a: { id: 1, title: "" }, b: { dir: "v", a: { id: 2, title: "logs" }, b: { id: 3, title: "" } } })
  assert.equal(l.tabs[0].focused, 2)
  assert.equal(l.tabs[1].name, "work")
})

test("a pane with no shell yet, or an ended one, is left out with its split", () => {
  const tabs = [{ name: "", sticky: "", tree: { dir: "h", a: { pane: pane(1) }, b: { pane: pane(0) } } }, { tree: { pane: pane(5, { exited: true }) } }]
  const l = layoutOf(tabs, tabs[0])
  assert.deepEqual(l.tabs, [{ name: "", sticky: "", focused: 0, zoomed: 0, tree: { id: 1, title: "" } }])
})

test("restore puts kept shells back where they were and drops the ended ones", () => {
  const saved = layoutOf(
    [
      { name: "", sticky: "s", tree: { dir: "h", a: { pane: pane(1) }, b: { dir: "v", a: { pane: pane(2) }, b: { pane: pane(3) } } }, focused: pane(3), zoomed: pane(3) },
      { name: "two", sticky: "", tree: { pane: pane(4) } },
    ],
    null,
  )
  saved.active = 1
  // Shell 2 ended while the app was gone; 7 was in no layout.
  const plan = restorePlan([saved], [{ id: 1 }, { id: 3 }, { id: 4 }, { id: 7 }])
  assert.equal(plan.tabs.length, 3)
  assert.deepEqual(plan.tabs[0].tree, { dir: "h", a: { id: 1, title: "" }, b: { id: 3, title: "" } })
  assert.equal(plan.tabs[0].focused, 3)
  assert.equal(plan.tabs[0].zoomed, 3)
  assert.equal(plan.tabs[1].name, "two")
  assert.deepEqual(plan.tabs[2].tree, { id: 7, title: "" })
  assert.equal(plan.active, 1)
})

test("a shell is restored once, even when two layouts name it", () => {
  const l = { v: 1, active: 0, tabs: [{ tree: { id: 1 } }] }
  const plan = restorePlan([l, l], [{ id: 1 }])
  assert.equal(plan.tabs.length, 1)
})

test("nothing kept: nothing to restore", () => {
  assert.deepEqual(restorePlan([{ tabs: [{ tree: { id: 1 } }] }], []), { tabs: [], active: 0 })
})
