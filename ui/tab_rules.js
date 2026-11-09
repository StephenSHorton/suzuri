// Tab and pane rules from Go suzuri (internal/ui/split.go,
// internal/chrome/model.go tabLabel, ui_common.go): the strip label, the
// window title, the limits and the zoom cycle. No DOM, so node tests them
// (ui/test/tab_rules.test.mjs).

// Go's maxTabs and maxPanesTotal.
export const MAX_TABS = 16
export const MAX_PANES = 24

// The strip label. In order: the tab's own name (Rename tab), the sticky
// name it froze at on its first split (split tabs only), the focused pane's
// title, any pane's title, then "shell N" ("app N" on the alt screen).
// Windows' "Administrator: " prefix goes.
//   t: { name, sticky, panes, focused, alt, index }
//   focused / panes: the panes' labels ("" when a pane has no title yet)
export function tabTitle(t) {
  let s = (t.name || "").trim()
  if (!s && t.panes.length > 1) s = (t.sticky || "").trim()
  if (!s) s = (t.focused || "").trim()
  if (!s) s = (t.panes.find((x) => (x || "").trim()) || "").trim()
  if (!s) s = `${t.alt ? "app" : "shell"} ${t.index + 1}`
  return s.startsWith("Administrator: ") ? s.slice("Administrator: ".length) : s
}

// The name a tab freezes at when it is split: its focused pane's title
// (app.js passes "shell" for one with none yet, as Go's title() did). A
// renamed or already frozen tab keeps what it has.
export function stickyAt(name, sticky, focused) {
  if ((name || "").trim() || (sticky || "").trim()) return sticky || ""
  return (focused || "").trim()
}

// The OS window title (Go's hostWindowTitle).
export function windowTitle(label) {
  return label ? `suzuri — ${label}` : "suzuri"
}

// While a pane is maximized the focus arrows walk the panes in tree order:
// left/up back, right/down on. Returns the pane to zoom to, or null.
export function zoomCycle(panes, current, dx, dy) {
  const n = panes.length
  if (n < 2) return null
  const i = Math.max(0, panes.indexOf(current))
  const step = dx < 0 || dy < 0 ? -1 : 1
  return panes[(i + step + n) % n]
}
