// Pane-tree operations, after Go suzuri's split.go and page_move.go. The tree
// is app.js's: {pane} leaves and {dir: "h"|"v", a, b, ratio?} splits ("h" puts
// a left of b, "v" puts a above b). ratio is a's share; without one, render()
// weighs the sides by how many panes they hold. Every operation changes the
// tree in place and returns whether it did anything; none of them touch the
// DOM, so they are tested under node (ui/test/layout_ops.test.mjs).

export const RATIO_MIN = 0.12
export const RATIO_MAX = 0.88
const clamp = (r) => Math.min(RATIO_MAX, Math.max(RATIO_MIN, r))

export function leafOf(tree, pane) {
  if (!tree) return null
  if (tree.pane) return tree.pane === pane ? tree : null
  return leafOf(tree.a, pane) || leafOf(tree.b, pane)
}

// The split directly holding pane's leaf, or null for a lone pane.
export function parentOf(tree, pane, node = tree) {
  if (!node || node.pane) return null
  if (node.a.pane === pane || node.b.pane === pane) return node
  return parentOf(tree, pane, node.a) || parentOf(tree, pane, node.b)
}

function panesUnder(node, out = []) {
  if (!node) return out
  if (node.pane) out.push(node.pane)
  else { panesUnder(node.a, out); panesUnder(node.b, out) }
  return out
}

// a's current share of a split: its ratio, or the pane-count weight render()
// uses when it has none.
export function shareOf(node) {
  if (node.ratio != null) return node.ratio
  const span = (n) => (n.pane || n.dir !== node.dir ? 1 : span(n.a) + span(n.b))
  return span(node.a) / (span(node.a) + span(node.b))
}

// Trades two panes' places (Go's swapPanes).
export function swapPanes(tree, a, b) {
  if (a === b) return false
  const la = leafOf(tree, a), lb = leafOf(tree, b)
  if (!la || !lb) return false
  la.pane = b
  lb.pane = a
  return true
}

// Takes pane's leaf out, folding its sibling into the parent (Go's
// removeLeaf). Returns the new root: null when pane was the only one.
export function removePane(tree, pane) {
  const leaf = leafOf(tree, pane)
  if (!leaf) return tree
  const parent = parentOf(tree, pane)
  if (!parent) return null
  const sibling = parent.a === leaf ? parent.b : parent.a
  for (const k of ["pane", "dir", "a", "b", "ratio"]) delete parent[k]
  Object.assign(parent, sibling)
  return tree
}

// Moves pane next to target on one edge (left, right, top/up, bottom/down),
// as dragging a pane's title onto another pane does (Go's movePaneBeside /
// dockExisting). Returns the new root, or null when the move isn't possible.
export function dockBeside(tree, pane, target, edge) {
  const e = { left: "left", right: "right", top: "top", up: "top", bottom: "bottom", down: "bottom" }[String(edge).toLowerCase()]
  if (!e || pane === target || !leafOf(tree, pane) || !leafOf(tree, target)) return null
  tree = removePane(tree, pane)
  const t = leafOf(tree, target)
  const old = { pane: t.pane }
  delete t.pane
  t.dir = e === "left" || e === "right" ? "h" : "v"
  const moved = { pane }
  if (e === "left" || e === "top") { t.a = moved; t.b = old } else { t.a = old; t.b = moved }
  return tree
}

// Grows pane's side of its split by delta (shrinks with a negative one).
export function adjustRatio(tree, pane, delta) {
  const n = parentOf(tree, pane)
  if (!n) return false
  const inA = panesUnder(n.a).includes(pane)
  n.ratio = clamp(shareOf(n) + (inA ? delta : -delta))
  return true
}

export function equalize(tree, pane) {
  const n = parentOf(tree, pane)
  if (!n) return false
  n.ratio = 0.5
  return true
}

// Turns pane's split from side by side to stacked, or back.
export function rotate(tree, pane) {
  const n = parentOf(tree, pane)
  if (!n) return false
  n.dir = n.dir === "h" ? "v" : "h"
  return true
}

// Swaps the two sides of pane's split.
export function swapSides(tree, pane) {
  const n = parentOf(tree, pane)
  if (!n) return false
  const share = shareOf(n)
  ;[n.a, n.b] = [n.b, n.a]
  n.ratio = clamp(1 - share)
  return true
}

// The pane next to `from` in direction (dx, dy), from their screen rects
// ({pane, x, y, w, h}); null at the edge. Same scoring app.js uses to move
// focus, so "move" and "focus" agree on who the neighbour is.
export function neighbor(rects, from, dx, dy) {
  const r = rects.find((q) => q.pane === from)
  if (!r) return null
  const cx = r.x + r.w / 2, cy = r.y + r.h / 2
  let best = null, bestD = Infinity
  for (const q of rects) {
    if (q.pane === from) continue
    const ddx = q.x + q.w / 2 - cx, ddy = q.y + q.h / 2 - cy
    if (dx && (Math.sign(ddx) !== dx || Math.abs(ddx) < 1)) continue
    if (dy && (Math.sign(ddy) !== dy || Math.abs(ddy) < 1)) continue
    const d = Math.abs(ddx) + Math.abs(ddy) + (dx ? Math.abs(ddy) : Math.abs(ddx)) * 2
    if (d < bestD) { bestD = d; best = q.pane }
  }
  return best
}

// Dragging a pane's title onto another pane (Go's pane_drag.go): the edge of
// rect ({x, y, w, h}) nearest the pointer, by which axis it is further off
// centre along.
export function dropEdge(rect, px, py) {
  if (rect.w < 1 || rect.h < 1) return ""
  const fx = (px - rect.x + 0.5) / rect.w - 0.5
  const fy = (py - rect.y + 0.5) / rect.h - 0.5
  if (Math.abs(fx) >= Math.abs(fy)) return fx < 0 ? "left" : "right"
  return fy < 0 ? "top" : "bottom"
}

export function cloneTree(n) {
  if (!n) return n
  return n.pane ? { ...n } : { ...n, a: cloneTree(n.a), b: cloneTree(n.b) }
}

export function sameShape(a, b) {
  if (!a || !b) return a === b
  if (a.pane || b.pane) return a.pane === b.pane
  return a.dir === b.dir && sameShape(a.a, b.a) && sameShape(a.b, b.b)
}

// Whether dropping pane on target's edge would change the layout (Go hides
// the drop highlight, and ignores the drop, when it wouldn't).
export function dropChanges(tree, pane, target, edge) {
  const out = dockBeside(cloneTree(tree), pane, target, edge)
  return !!out && !sameShape(out, tree)
}
