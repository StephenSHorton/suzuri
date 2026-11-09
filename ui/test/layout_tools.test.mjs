import { test } from "node:test"
import assert from "node:assert/strict"
import * as L from "../layout_ops.js"

// AI control's move tool against a small stand-in for app.js's rszHost: tabs
// with their own trees and focus, and showTab/setFocus/movePaneToTab that
// behave as app.js's do (showTab of the active tab does nothing; setFocus
// shows the pane's tab; movePaneToTab shows the target and focuses the pane).
let onEvent
const replies = []
globalThis.window = {
  __TAURI__: {
    core: { invoke: async (cmd, a) => (cmd === "aicontrol_reply" ? replies.push(a) : null) },
    event: { listen: (_, fn) => { onEvent = fn } },
  },
}

function fakeHost() {
  const h = { tabs: [], active: null, focused: null }
  const leaves = (n) => (!n ? [] : n.pane ? [n.pane] : [...leaves(n.a), ...leaves(n.b)])
  const panes = (t) => leaves(t.tree)
  const tab = (...ids) => {
    const t = { id: h.tabs.length + 1, name: "", focused: null }
    const ps = ids.map((id) => ({ id, tab: t }))
    t.tree = ps.slice(1).reduce((n, p) => ({ dir: "h", a: n, b: { pane: p } }), { pane: ps[0] })
    t.focused = ps[0]
    h.tabs.push(t)
    return t
  }
  const showTab = (t) => {
    if (h.active === t) return
    h.active = t
    if (t.focused) setFocus(t.focused)
  }
  const setFocus = (p) => {
    if (p.tab !== h.active) showTab(p.tab)
    h.focused = p
    p.tab.focused = p
  }
  window.rszHost = {
    tree: () => h.active?.tree,
    focused: () => h.focused,
    allPanes: () => h.tabs.flatMap(panes),
    tabs: () => h.tabs,
    activeTab: () => h.active,
    showTab, setFocus,
    relayout: () => h.focused && setFocus(h.focused),
    movePaneToTab: (p, to) => {
      const from = p.tab, target = h.tabs[to]
      from.tree = L.removePane(from.tree, p)
      if (from.focused === p) from.focused = from.tree && panes(from)[0]
      target.tree = { dir: "h", a: target.tree, b: { pane: p } }
      p.tab = target
      target.focused = p
      if (!from.tree) h.tabs.splice(h.tabs.indexOf(from), 1)
      h.active = null
      showTab(target)
      setFocus(p)
    },
  }
  return { h, tab, pane: (id) => window.rszHost.allPanes().find((p) => p.id === id) }
}

await import("../layout_tools.js")
const move = async (args) => {
  await onEvent({ payload: { id: 1, tool: "move", args } })
  return replies.pop()
}

test("moving another tab's pane beside yours keeps your focus", async () => {
  const { h, tab, pane } = fakeHost()
  const mine = tab(1, 2), theirs = tab(3, 4)
  window.rszHost.setFocus(pane(2))
  const r = await move({ pane_id: 3, target_pane_id: 1, edge: "left" })
  assert.equal(r.status, 200)
  assert.equal(pane(3).tab, mine)
  assert.equal(h.active, mine)
  assert.equal(h.focused, pane(2))
  assert.equal(theirs.focused, pane(4))
})

test("moving your pane into another tab leaves you on yours", async () => {
  const { h, tab, pane } = fakeHost()
  const mine = tab(1, 2), theirs = tab(3, 4)
  window.rszHost.setFocus(pane(1))
  theirs.focused = pane(4)
  const r = await move({ pane_id: 2, target_pane_id: 3, edge: "bottom" })
  assert.equal(r.status, 200)
  assert.equal(pane(2).tab, theirs)
  assert.equal(h.active, mine)
  assert.equal(h.focused, pane(1))
  assert.equal(theirs.focused, pane(4)) // the target tab keeps its own focus
})

test("moving the focused pane away leaves you on your tab", async () => {
  const { h, tab, pane } = fakeHost()
  const mine = tab(1, 2), theirs = tab(3)
  window.rszHost.setFocus(pane(2))
  await move({ pane_id: 2, target_pane_id: 3, edge: "right" })
  assert.equal(pane(2).tab, theirs)
  assert.equal(h.active, mine)
  assert.equal(h.focused, pane(1))
})
