// AI control (#286): agents in a pane drive the layout over loopback HTTP
// (app/src/bridge.rs, Go suzuri's aicontrol API). Rust forwards each request
// here as an `aicontrol` event; we answer with aicontrol_reply.
// Uses window.rszHost from app.js (the pane tree and its commands).

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

const host = () => window.rszHost
const byPaneId = (id) => host().allPanes().find((p) => p.id === Number(id))

async function paneInfo(p, lines = 20) {
  const tail = (await invoke("pane_text", { id: p.id, lines })) ?? ""
  return {
    id: p.id,
    title: p.titleOverride || p.title,
    kind: "shell",
    cols: p.size?.[0] ?? 0,
    rows: p.size?.[1] ?? 0,
    focused: p === host().focused(),
    agent_state: p.agentState || "",
    live: !p.exited,
    tail,
  }
}

async function treeOf(node) {
  if (!node) return null
  if (node.pane) return { pane: await paneInfo(node.pane) }
  return { split: node.dir === "h" ? "right" : "down", a: await treeOf(node.a), b: await treeOf(node.b) }
}

// Every tab (#283), with its own tree and focused pane.
export async function layout() {
  const tabs = await Promise.all(host().tabs().map(async (t) => ({
    id: t.id,
    title: t.name || t.focused?.titleOverride || t.focused?.title || "",
    active: t === host().activeTab(),
    focused_pane: t.focused?.id ?? 0,
    tree: await treeOf(t.tree),
  })))
  return { windows: [{ id: 1, tabs }] }
}

function setTitle(p) {
  const t = p.titleOverride || p.title
  const el = p.head?.querySelector(".t")
  if (el) el.textContent = t
}

const tools = {
  layout: async () => [200, await layout()],
  pane: async (a) => {
    const p = byPaneId(a.pane_id)
    return p ? [200, await paneInfo(p, Math.min(Number(a.lines) || 200, 5000))] : [404, { error: `no pane ${a.pane_id}` }]
  },
  split: async (a) => {
    const from = a.pane_id ? byPaneId(a.pane_id) : host().focused()
    if (!from) return [404, { error: `no pane ${a.pane_id}` }]
    const axis = a.axis === "down" ? "v" : "h"
    const p = await host().split(axis, from)
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
    const state = String(a.state || "")
    p.agentState = ["idle", "working", "blocked", "done"].includes(state) ? state : ""
    if (p.el) p.el.dataset.agent = p.agentState
    return [200, { ok: true, pane_id: p.id, state: p.agentState || "clear" }]
  },
}

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
    if (!fn) [status, body] = [501, { error: `${tool || "(none)"} is not in rust-suzuri yet; GET /tools lists what is` }]
    else [status, body] = await fn(args || {})
  } catch (e) {
    ;[status, body] = [500, { error: String(e) }]
  }
  invoke("aicontrol_reply", { id, status, body })
})
