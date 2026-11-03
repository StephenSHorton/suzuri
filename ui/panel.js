// A small modal panel over the panes, shared by transfer and workspace
// (#286). Interim UI until the palette (#283) and notices (#288) land; those
// cards can open these panels from their own entry points.

let current = null

export function isOpen() {
  return current != null
}

export function closePanel() {
  if (!current) return
  const p = current
  current = null
  p.el.remove()
  p.onClose?.()
  window.dispatchEvent(new Event("rsz-panel-closed"))
}

// Open a panel. build(body, panel) fills it; returns the panel.
export function openPanel(title, build, { onClose, wide } = {}) {
  closePanel()
  const el = document.createElement("div")
  el.className = "rsz-panel" + (wide ? " wide" : "")
  // The terminal's hidden textarea steals focus on any mousedown; keep it here.
  el.addEventListener("mousedown", (e) => e.stopPropagation())
  el.addEventListener("keydown", (e) => {
    e.stopPropagation()
    if (e.key === "Escape") {
      e.preventDefault()
      closePanel()
    }
  })
  const head = document.createElement("div")
  head.className = "rsz-panel-head"
  const h = document.createElement("span")
  h.textContent = title
  const x = document.createElement("button")
  x.textContent = "✕"
  x.title = "Close (Esc)"
  x.onclick = closePanel
  head.append(h, x)
  const body = document.createElement("div")
  body.className = "rsz-panel-body"
  el.append(head, body)
  document.body.appendChild(el)
  current = { el, body, onClose }
  build(body, current)
  const first = el.querySelector("input, textarea, button.primary")
  if (first) setTimeout(() => first.focus(), 0)
  return current
}

export function h(tag, props = {}, ...kids) {
  const el = document.createElement(tag)
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") el.className = v
    else if (k.startsWith("on")) el[k] = v
    else if (v != null) el.setAttribute(k, v)
  }
  for (const c of kids.flat()) if (c != null) el.append(c)
  return el
}
