// Drag the divider between two panes to resize them (Go suzuri's sash drag,
// split.go hitSash / applySashDrag). Hovering a divider shows the resize
// cursor; dragging sets the split's ratio live; a double-click evens the run
// of panes out again. The divider is the split's gap: render() (app.js)
// builds each split as a box holding its two sides, so the boxes under the
// tab mirror the tree. PTY resizes during a drag go through a gate that sends
// at most one per pane every 80 ms, and the last at release.
import { sashRect, hitSash, ratioAt, equalizeRun, resizeGate, MIN_W, MIN_H } from "./sash_ops.js"

const host = () => window.rszHost
const gate = resizeGate(80)
// app.js's paneHost.resize sends through this.
window.rszResizeGate = (key, send) => gate.run(key, send)

const rectOf = (el) => {
  const r = el.getBoundingClientRect()
  return { x: r.left, y: r.top, w: r.width, h: r.height }
}

// Every divider in the active tab: {node, dir, rect, el, a, b}.
function sashes() {
  const tab = host()?.activeTab?.()
  if (!tab?.tree || tab.zoomed) return []
  const out = []
  const walk = (node, el) => {
    if (!el || node.pane) return
    const [a, b] = el.children
    if (!a || !b || !el.classList.contains("split")) return
    out.push({ node, dir: node.dir, rect: sashRect(node.dir, rectOf(a), rectOf(b)), el, a, b })
    walk(node.a, a)
    walk(node.b, b)
  }
  walk(tab.tree, tab.el?.firstElementChild)
  return out
}

let drag = null // {s}: the divider being dragged
let hover = ""

function setCursor(dir) {
  const c = dir === "h" ? "sash-col" : dir === "v" ? "sash-row" : ""
  if (c === hover) return
  if (hover) document.body.classList.remove(hover)
  if (c) document.body.classList.add(c)
  hover = c
}

// Our own UI on top (overlays, panels, the tab strip) keeps its pointer.
const onPanes = (e) => !!e.target.closest?.("#root")

window.addEventListener("mousemove", (e) => {
  if (drag) {
    e.stopPropagation()
    const { s } = drag
    const box = rectOf(s.el)
    const h = s.dir === "h"
    const r = h
      ? ratioAt(box.x, box.w, s.rect.w, e.clientX, MIN_W)
      : ratioAt(box.y, box.h, s.rect.h, e.clientY, MIN_H)
    if (r == null || r === s.node.ratio) return
    s.node.ratio = r
    s.a.style.flex = `${r} 1 0`
    s.b.style.flex = `${1 - r} 1 0`
    return
  }
  // Only a free pointer hovers (not mid-selection or mid pane-drag).
  if (e.buttons || !onPanes(e)) return setCursor("")
  setCursor(hitSash(sashes(), e.clientX, e.clientY)?.dir || "")
}, true)

// Capture, ahead of the pane's focus/selection and pane_drag.js.
window.addEventListener("mousedown", (e) => {
  if (e.button !== 0 || !onPanes(e)) return
  const s = hitSash(sashes(), e.clientX, e.clientY)
  if (!s) return
  e.preventDefault()
  e.stopPropagation()
  // The second press of a double-click starts no drag; dblclick evens it.
  if (e.detail >= 2) return
  drag = { s }
  gate.hold()
  setCursor(s.dir)
}, true)

// A double-click on a divider evens the run out. Swallow it here, since the
// divider's hit area overlaps the lower pane's title bar (rename on dblclick).
window.addEventListener("dblclick", (e) => {
  if (e.button !== 0 || !onPanes(e)) return
  const s = hitSash(sashes(), e.clientX, e.clientY)
  if (!s) return
  e.preventDefault()
  e.stopPropagation()
  if (equalizeRun(s.node)) host().relayout()
}, true)

window.addEventListener("mouseup", (e) => {
  if (!drag) return
  e.stopPropagation()
  drag = null
  gate.release()
  setCursor("")
}, true)

// Losing the window mid-drag (alt-tab) ends it as a release would.
window.addEventListener("blur", () => {
  if (!drag) return
  drag = null
  gate.release()
  setCursor("")
})
