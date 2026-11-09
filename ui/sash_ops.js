// The geometry behind dragging a split's divider (Go suzuri's hitSash and
// applySashDrag, split.go). No DOM here, so node tests it
// (ui/test/sash_ops.test.mjs); ui/sash.js does the events.
import { RATIO_MIN, RATIO_MAX } from "./layout_ops.js"

// Hit pad past each side of the gap, so a thin divider is easy to grab.
export const SASH_PAD = 4
// The smallest a side may get by dragging, in px (about a dozen columns, or
// the title bar and a couple of rows).
export const MIN_W = 100
export const MIN_H = 60

// The divider of a split ("h": side by side, "v": stacked) between its two
// sides' rects ({x, y, w, h}): the gap strip, as a rect.
export function sashRect(dir, ra, rb) {
  if (dir === "h") {
    const x = ra.x + ra.w
    return { x, y: ra.y, w: Math.max(0, rb.x - x), h: ra.h }
  }
  const y = ra.y + ra.h
  return { x: ra.x, y, w: ra.w, h: Math.max(0, rb.y - y) }
}

// The first sash ({dir, rect}) under (px, py), padded across its thin axis
// only, or null.
export function hitSash(sashes, px, py, pad = SASH_PAD) {
  for (const s of sashes) {
    const r = s.rect
    if (s.dir === "h") {
      if (px >= r.x - pad && px < r.x + r.w + pad && py >= r.y && py < r.y + r.h) return s
    } else if (py >= r.y - pad && py < r.y + r.h + pad && px >= r.x && px < r.x + r.w) return s
  }
  return null
}

// a's share when the divider's centre sits at `at` along a split that starts
// at `start` and is `len` long with a `gap` between the sides. Clamped to
// Go's 0.12..0.88 and to `min` px a side; null when there's no room.
export function ratioAt(start, len, gap, at, min = 0) {
  const room = len - gap
  if (!(room > 1)) return null
  const lo = Math.max(RATIO_MIN, min / room)
  const hi = Math.min(RATIO_MAX, 1 - min / room)
  if (lo > hi) return 0.5
  const r = (at - start - gap / 2) / room
  return Math.min(hi, Math.max(lo, r))
}

// Double-clicking a divider: drop the ratios of the split and of the splits
// it runs into along the same direction, so render() weighs the panes in the
// run equally again (three columns come out a third each).
export function equalizeRun(node) {
  if (!node || node.pane) return false
  const dir = node.dir
  let changed = false
  const walk = (n) => {
    if (n.pane || n.dir !== dir) return
    if (n.ratio != null) { delete n.ratio; changed = true }
    walk(n.a)
    walk(n.b)
  }
  walk(node)
  return changed
}

// Holds PTY resizes back while a divider is being dragged: run(key, send)
// sends at once when nothing is held, else keeps the newest send per key
// and lets one through every `ms` (and the rest at release()).
export function resizeGate(ms, now = () => Date.now(), later = setTimeout) {
  let held = false
  const pending = new Map()
  let last = 0, timer = null
  const flush = () => {
    timer = null
    last = now()
    const sends = [...pending.values()]
    pending.clear()
    for (const send of sends) send()
  }
  return {
    get held() { return held },
    hold() { held = true; last = now() },
    release() { held = false; flush() },
    run(key, send) {
      if (!held) return send()
      pending.set(key, send)
      if (timer) return
      const wait = Math.max(0, last + ms - now())
      timer = later(flush, wait)
    },
  }
}
