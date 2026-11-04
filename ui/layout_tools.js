// AI control's layout tools (#293), registered into #286's ui/aicontrol.js:
// move, swap, rotate, grow, shrink and equalize, with Go suzuri's arguments
// and replies (internal/ui/ai_apply.go): each answers with the new layout.
import { registerTool, layout } from "./aicontrol.js"
import * as L from "./layout_ops.js"

const host = () => window.rszHost
const byPaneId = (id) => host().allPanes().find((p) => p.id === Number(id))

// Go's paneOrFocus: pane_id names (and focuses) a pane, else the focused one.
function paneOrFocus(a) {
  if (a.pane_id != null) {
    const p = byPaneId(a.pane_id)
    if (!p) return [null, "no such pane"]
    host().setFocus(p)
    return [p, ""]
  }
  const f = host().focused()
  return f ? [f, ""] : [null, "no pane"]
}

// A tool that works on the split around a pane.
function around(op) {
  return async (a) => {
    const [p, err] = paneOrFocus(a)
    if (!p) return [400, { error: err }]
    if (!op(host().tree(), p)) return [400, { error: "no split" }]
    host().relayout()
    return [200, await layout()]
  }
}

registerTool("grow", around((t, p) => L.adjustRatio(t, p, 0.08)))
registerTool("shrink", around((t, p) => L.adjustRatio(t, p, -0.08)))
registerTool("equalize", around(L.equalize))
registerTool("rotate", around(L.rotate))
registerTool("swap", around(L.swapSides))

registerTool("move", async (a) => {
  if (a.pane_id == null) return [400, { error: "pane_id required" }]
  if (a.target_pane_id == null) return [400, { error: "target_pane_id required" }]
  const p = byPaneId(a.pane_id), t = byPaneId(a.target_pane_id)
  if (!p || !t) return [400, { error: "move failed" }]
  if (!["left", "right", "top", "bottom", "up", "down"].includes(String(a.edge).toLowerCase()))
    return [400, { error: "edge must be left|right|top|bottom" }]
  if (!L.dockBeside(host().tree(), p, t, a.edge)) return [400, { error: "move failed" }]
  host().relayout()
  host().setFocus(p)
  return [200, await layout()]
})
