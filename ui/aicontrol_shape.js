// AI control's JSON in Go suzuri's shape (internal/ui/ai_apply.go layoutJSON,
// paneJSON, treeJSON), plus its argument parsing. No DOM or Tauri here, so
// it is tested under node (ui/test/aicontrol_shape.test.mjs).
import { shareOf } from "./layout_ops.js"

// Go's split axis words: "right" puts the new pane beside, "down" below.
// horizontal/h name the divider, so they mean down. Returns app.js's split
// dir ("h" side by side, "v" stacked), or null for a bad axis.
export function axisOf(axis) {
  const s = String(axis ?? "").toLowerCase().trim()
  if (["", "right", "vertical", "v"].includes(s)) return "h"
  if (["down", "below", "horizontal", "h"].includes(s)) return "v"
  return null
}

// report_agent's states; "", clear and unknown hand the pane back to the
// automatic guess. Returns the state to store ("" for clear), or null.
export function agentStateOf(state) {
  const s = String(state ?? "").toLowerCase().trim()
  if (["idle", "working", "blocked", "done"].includes(s)) return s
  if (["", "clear", "unknown"].includes(s)) return ""
  return null
}

// activity.js says busy; Go's API says working.
export function activityName(a, exited) {
  if (exited) return "dead"
  return a === "busy" ? "working" : a || "idle"
}

// {pane: id} leaves, {axis, ratio, a, b} splits. split is the old name of
// axis, kept for scripts written against earlier suzuri builds.
export function treeJSON(node) {
  if (!node) return { pane: 0 }
  if (node.pane) return { pane: node.pane.id ?? 0 }
  const axis = node.dir === "h" ? "right" : "down"
  return { axis, split: axis, ratio: shareOf(node), a: treeJSON(node.a), b: treeJSON(node.b) }
}

// One pane from its fields and the core's {tail, live} lines.
export function paneJSON(p, lines, extra = {}) {
  const tail = lines?.tail ?? []
  const live = lines?.live ?? []
  return {
    id: p.id,
    title: p.titleOverride || p.title || "shell",
    kind: "terminal",
    cwd: p.shell?.cwd || "",
    cols: p.size?.[0] ?? 0,
    rows: p.size?.[1] ?? 0,
    guest_url: null,
    agent_state: p.agentState || "",
    activity: activityName(extra.activity, p.exited),
    tail,
    live,
    // Older suzuri builds: focused, alive (was live) and the joined text.
    focused: !!extra.focused,
    alive: !p.exited,
    text: [...tail, ...live].join("\n"),
  }
}

// The whole window. tabs: app.js's tab objects; panesOf(tab) gives each
// tab's paneJSON list. pid is filled in by app/src/bridge.rs.
export function layoutJSON(tabs, active, panesOf) {
  const out = tabs.map((t) => ({
    id: t.id,
    title: t.name || t.focused?.titleOverride || t.focused?.title || "",
    active: t === active,
    focus_pane: t.focused?.id ?? 0,
    focused_pane: t.focused?.id ?? 0,
    tree: treeJSON(t.tree),
    panes: panesOf(t),
  }))
  return {
    focus_pane: active?.focused?.id ?? 0,
    active_tab: active?.id ?? 0,
    windows: [{ surface: 0, id: 1, tabs: out }],
  }
}
