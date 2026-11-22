// Tabs and splits across a restart (card hearth#913). Shells run in their
// own keeper hosts (app/src/keeper.rs) and outlive the app; the UI saves
// its layout by pane id on every change (layout_save), and the next start
// puts each kept shell back where it was (kept_list, pane_attach). Pure, so
// node tests it (test/kept_layout.test.mjs).

// The layout to save: each tab's tree with {id, title} leaves. A pane with
// no shell yet (or an ended one) is left out, and its split with it; so is
// one showing another program's shell (open_kept): that one is not ours to
// put back or bring back after a reboot.
export function layoutOf(tabs, active) {
  const leaf = (p) => (p && p.id && !p.exited && !p.borrowed ? { id: p.id, title: p.titleOverride || "" } : null)
  const ser = (n) => {
    if (!n) return null
    if (n.pane) return leaf(n.pane)
    const a = ser(n.a), b = ser(n.b)
    if (!a || !b) return a || b
    const out = { dir: n.dir, a, b }
    if (n.ratio != null) out.ratio = n.ratio
    return out
  }
  const saved = tabs
    .map((t) => ({ name: t.name || "", sticky: t.sticky || "", focused: t.focused?.id || 0, zoomed: t.zoomed?.id || 0, tree: ser(t.tree), active: t === active }))
    .filter((t) => t.tree)
  const at = saved.findIndex((t) => t.active)
  for (const t of saved) delete t.active
  return { v: 1, active: Math.max(0, at), tabs: saved }
}

// What to build at start: tabs from the saved layouts (newest first),
// keeping only shells that are still kept, each once; then a tab for each
// kept shell no layout had. Leaves are {id, title}.
export function restorePlan(layouts, shells) {
  const live = new Set(shells.map((h) => h.id))
  const used = new Set()
  const prune = (n) => {
    if (!n) return null
    if (n.id != null) {
      if (!live.has(n.id) || used.has(n.id)) return null
      used.add(n.id)
      return { id: n.id, title: n.title || "" }
    }
    const a = prune(n.a), b = prune(n.b)
    if (!a || !b) return a || b
    const out = { dir: n.dir === "v" ? "v" : "h", a, b }
    if (typeof n.ratio === "number") out.ratio = n.ratio
    return out
  }
  const ids = (n, out = []) => {
    if (n.id != null) out.push(n.id)
    else { ids(n.a, out); ids(n.b, out) }
    return out
  }
  const tabs = []
  let active = 0
  for (const [li, l] of (layouts || []).entries()) {
    for (const [ti, t] of (l?.tabs || []).entries()) {
      const tree = prune(t.tree)
      if (!tree) continue
      const has = ids(tree)
      if (li === 0 && ti === (l.active || 0)) active = tabs.length
      tabs.push({
        name: t.name || "",
        sticky: has.length > 1 ? t.sticky || "" : "",
        tree,
        focused: has.includes(t.focused) ? t.focused : has[0],
        zoomed: has.length > 1 && has.includes(t.zoomed) ? t.zoomed : 0,
      })
    }
  }
  for (const h of shells) {
    if (used.has(h.id)) continue
    used.add(h.id)
    tabs.push({ name: "", sticky: "", tree: { id: h.id, title: "" }, focused: h.id, zoomed: 0 })
  }
  return { tabs, active: Math.min(active, Math.max(0, tabs.length - 1)) }
}
