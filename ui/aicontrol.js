// AI control (#286): agents in a pane drive the layout over loopback HTTP
// (app/src/bridge.rs, Go suzuri's aicontrol API). Rust forwards each request
// here as an `aicontrol` event; we answer with aicontrol_reply.
// Uses window.rszHost from app.js (the pane tree and its commands).

import { axisOf, agentStateOf, paneJSON, layoutJSON } from "./aicontrol_shape.js"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

const host = () => window.rszHost
const byPaneId = (id) => host().allPanes().find((p) => p.id === Number(id))

// Go's paneJSON: tail is scrollback above the screen, live the screen.
async function paneInfo(p, lines = 80) {
  const got = p.id ? await invoke("pane_lines", { id: p.id, lines }).catch(() => null) : null
  return paneJSON(p, got, { focused: p === host().focused(), activity: window.rszActivity?.of(p) })
}

const leaves = (n, out = []) => {
  if (n?.pane) out.push(n.pane)
  else if (n) { leaves(n.a, out); leaves(n.b, out) }
  return out
}

// Every tab (#283), in Go's layoutJSON shape (12 lines a pane, as Go).
export async function layout() {
  const tabs = host().tabs()
  const panes = await Promise.all(tabs.map((t) => Promise.all(leaves(t.tree).map((p) => paneInfo(p, 12)))))
  return layoutJSON(tabs, host().activeTab(), (t) => panes[tabs.indexOf(t)])
}

function setTitle(p) {
  const t = p.titleOverride || p.title || "shell"
  const el = p.head?.querySelector(".t")
  // An open rename box stays (ui/app.js showTitle); its finish shows the label.
  if (el && !el.querySelector("input.rename")) el.textContent = t
}

const tools = {
  layout: async () => [200, await layout()],
  pane: async (a) => {
    const p = byPaneId(a.pane_id)
    if (a.pane_id == null) return [400, { error: "pane_id required" }]
    return p ? [200, await paneInfo(p, Math.min(Number(a.lines) || 80, 5000))] : [404, { error: `no pane ${a.pane_id}` }]
  },
  split: async (a) => {
    const from = a.pane_id ? byPaneId(a.pane_id) : host().focused()
    if (!from) return [404, { error: `no pane ${a.pane_id}` }]
    const axis = axisOf(a.axis)
    if (!axis) return [400, { error: "axis must be right|down" }]
    // Go focuses the pane (and so its tab) first; a hidden tab has no size.
    host().setFocus(from)
    const p = await host().split(axis, from)
    if (!p) return [400, { error: "max panes" }]
    return [200, { ok: true, pane_id: p.id }]
  },
  focus: async (a) => {
    const p = byPaneId(a.pane_id)
    if (!p) return [404, { error: `no pane ${a.pane_id}` }]
    host().setFocus(p)
    return [200, { ok: true, pane_id: p.id }]
  },
  close: async (a) => {
    const p = a.pane_id ? byPaneId(a.pane_id) : host().focused()
    if (!p) return [404, { error: `no pane ${a.pane_id}` }]
    host().closePane(p)
    return [200, { ok: true, pane_id: p.id }]
  },
  rename: async (a) => {
    const p = byPaneId(a.pane_id)
    if (!p) return [404, { error: `no pane ${a.pane_id}` }]
    p.titleOverride = String(a.title || "")
    setTitle(p)
    return [200, { ok: true, pane_id: p.id, title: p.titleOverride }]
  },
  report_agent: async (a) => {
    const p = byPaneId(a.pane_id)
    if (!p) return [404, { error: `no pane ${a.pane_id}` }]
    if (a.state == null) return [400, { error: "state required" }]
    // clear (or unknown) hands the pane back to the automatic guess.
    const state = agentStateOf(a.state)
    if (state == null) return [400, { error: `state must be idle|working|blocked|done|clear, not ${a.state}` }]
    p.agentState = state
    if (p.el) p.el.dataset.agent = p.agentState
    window.rszActivity?.report(p, p.agentState)
    return [200, { ok: true, pane_id: p.id, state: p.agentState || "clear" }]
  },
}

// Tools that change something: their replies carry the new layout.
const CHANGES = new Set(["split", "focus", "close", "rename", "report_agent", "new_tab", "move_to_tab"])

// Other cards add their tools here (move/swap: #293, new_tab/move_to_tab: #283).
export function registerTool(name, fn) {
  tools[name] = fn
}
window.rszRegisterTool = registerTool

listen("aicontrol", async ({ payload: { id, tool, args } }) => {
  let status = 200
  let body
  try {
    const fn = tools[tool]
    if (!fn) [status, body] = [501, { error: `${tool || "(none)"} is not in suzuri yet; GET /tools lists what is` }]
    else [status, body] = await fn(args || {})
    // Go answers every change with the whole layout; keep our own fields too.
    if (status === 200 && CHANGES.has(tool) && !body?.windows) body = { ...body, ...(await layout()) }
  } catch (e) {
    ;[status, body] = [500, { error: String(e) }]
  }
  invoke("aicontrol_reply", { id, status, body })
})
