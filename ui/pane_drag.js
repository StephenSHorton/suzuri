// Drag a pane by its title onto another pane to dock it on that pane's edge
// (Go suzuri's pane_drag.go). The drag starts past a 6 px threshold; while it
// is over a pane, the half it would take is lit; dropping where the layout
// wouldn't change does nothing. A plain click or double-click on the title
// (rename) never moves anything.
import { dropEdge, dropChanges, dockBeside } from "./layout_ops.js"

const THRESHOLD = 6
const host = () => window.rszHost

let drag = null // {pane, x, y, active}
const hint = document.createElement("div")
Object.assign(hint.style, {
  position: "fixed", pointerEvents: "none", zIndex: 50, display: "none",
  background: "rgba(200, 160, 255, 0.16)", border: "2px solid rgba(200, 160, 255, 0.7)", borderRadius: "6px",
})
document.body.append(hint)

function panesInTab() {
  const out = []
  const walk = (n) => n && (n.pane ? out.push(n.pane) : (walk(n.a), walk(n.b)))
  walk(host().tree())
  return out
}

function paneAt(x, y) {
  for (const p of panesInTab()) {
    const r = p.el.getBoundingClientRect()
    if (x >= r.left && x < r.right && y >= r.top && y < r.bottom) return [p, r]
  }
  return [null, null]
}

// Where a drop at (x, y) would put the dragged pane: {target, edge, rect},
// or null off a pane, on itself, or where nothing would change.
function dropAt(x, y) {
  const [p, r] = paneAt(x, y)
  if (!p || p === drag.pane) return null
  const edge = dropEdge({ x: r.left, y: r.top, w: r.width, h: r.height }, x, y)
  return edge && dropChanges(host().tree(), drag.pane, p, edge) ? { target: p, edge, rect: r } : null
}

function showHint(r, edge) {
  const half = { left: [0, 0, 0.5, 1], right: [0.5, 0, 0.5, 1], top: [0, 0, 1, 0.5], bottom: [0, 0.5, 1, 0.5] }[edge]
  Object.assign(hint.style, {
    display: "block",
    left: `${r.left + r.width * half[0]}px`, top: `${r.top + r.height * half[1]}px`,
    width: `${r.width * half[2]}px`, height: `${r.height * half[3]}px`,
  })
}

document.addEventListener("mousedown", (e) => {
  if (e.button !== 0) return
  const head = e.target.closest?.(".head")
  if (!head) return
  const pane = panesInTab().find((p) => p.head === head)
  if (!pane || host().activeTab?.()?.zoomed) return
  drag = { pane, x: e.clientX, y: e.clientY, active: false }
})

window.addEventListener("mousemove", (e) => {
  if (!drag) return
  if (!drag.active) {
    const dx = e.clientX - drag.x, dy = e.clientY - drag.y
    if (dx * dx + dy * dy < THRESHOLD * THRESHOLD) return
    drag.active = true
    document.body.style.cursor = "grabbing"
  }
  const d = dropAt(e.clientX, e.clientY)
  if (d) showHint(d.rect, d.edge)
  else hint.style.display = "none"
})

window.addEventListener("mouseup", (e) => {
  if (!drag) return
  const d = drag.active ? dropAt(e.clientX, e.clientY) : null
  const pane = drag.pane
  drag = null
  hint.style.display = "none"
  document.body.style.cursor = ""
  if (!d) return
  if (dockBeside(host().tree(), pane, d.target, d.edge)) {
    host().relayout()
    host().setFocus(pane)
  }
})
