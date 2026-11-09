import { test } from "node:test"
import assert from "node:assert/strict"
import * as S from "../aicontrol_shape.js"

test("split axis words and aliases, as Go's", () => {
  for (const a of [undefined, "", "right", "Vertical", "v", " RIGHT "]) assert.equal(S.axisOf(a), "h", String(a))
  for (const a of ["down", "below", "horizontal", "H"]) assert.equal(S.axisOf(a), "v", a)
  for (const a of ["left", "up", "sideways", 3]) assert.equal(S.axisOf(a), null, String(a))
})

test("report_agent states; bad ones are refused", () => {
  assert.equal(S.agentStateOf("Working"), "working")
  assert.equal(S.agentStateOf("done"), "done")
  for (const s of ["", "clear", "unknown", " CLEAR "]) assert.equal(S.agentStateOf(s), "")
  assert.equal(S.agentStateOf("asleep"), null)
})

test("activity names follow Go's", () => {
  assert.equal(S.activityName("busy"), "working")
  assert.equal(S.activityName("blocked"), "blocked")
  assert.equal(S.activityName("idle"), "idle")
  assert.equal(S.activityName(undefined), "idle")
  assert.equal(S.activityName("busy", true), "dead")
})

const pane = (id, more = {}) => ({ id, title: `p${id}`, size: [80, 24], shell: { cwd: "/tmp" }, ...more })

test("pane JSON has Go's fields and the old ones", () => {
  const p = pane(3, { titleOverride: "build", agentState: "blocked" })
  const j = S.paneJSON(p, { tail: ["a", "b"], live: ["$ ls"] }, { focused: true, activity: "blocked" })
  assert.deepEqual(
    { id: j.id, title: j.title, kind: j.kind, cwd: j.cwd, cols: j.cols, rows: j.rows, agent_state: j.agent_state, activity: j.activity, tail: j.tail, live: j.live },
    { id: 3, title: "build", kind: "terminal", cwd: "/tmp", cols: 80, rows: 24, agent_state: "blocked", activity: "blocked", tail: ["a", "b"], live: ["$ ls"] },
  )
  assert.equal(j.guest_url, null)
  assert.equal(j.focused, true)
  assert.equal(j.alive, true)
  assert.equal(j.text, "a\nb\n$ ls")
  const dead = S.paneJSON(pane(4, { exited: true }), null)
  assert.deepEqual([dead.tail, dead.live, dead.activity, dead.alive], [[], [], "dead", false])
})

test("layout in Go's shape: ids in the tree, ratio, panes per tab", () => {
  const A = pane(1), B = pane(2), C = pane(3)
  const t1 = { id: 1, name: "", tree: { dir: "h", ratio: 0.3, a: { pane: A }, b: { dir: "v", a: { pane: B }, b: { pane: C } } }, focused: B }
  const t2 = { id: 5, name: "logs", tree: { pane: pane(9) }, focused: null }
  const out = S.layoutJSON([t1, t2], t1, (t) => (t === t1 ? ["x"] : []))
  assert.equal(out.focus_pane, 2)
  assert.equal(out.active_tab, 1)
  assert.equal(out.windows[0].surface, 0)
  const [a, b] = out.windows[0].tabs
  assert.deepEqual(a.tree, {
    axis: "right", split: "right", ratio: 0.3, a: { pane: 1 },
    b: { axis: "down", split: "down", ratio: 0.5, a: { pane: 2 }, b: { pane: 3 } },
  })
  assert.equal(a.title, "p2")
  assert.equal(a.active, true)
  assert.equal(a.focus_pane, 2)
  assert.equal(a.focused_pane, 2)
  assert.deepEqual(a.panes, ["x"])
  assert.deepEqual([b.title, b.active, b.focus_pane, b.tree], ["logs", false, 0, { pane: 9 }])
  assert.deepEqual(S.treeJSON(null), { pane: 0 })
})
