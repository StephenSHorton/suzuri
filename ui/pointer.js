// The pointer over a pane: text selection (drag, double-click word,
// triple-click line, Shift+click extends, drags past the edge scroll the
// history), mouse reports to apps that asked for them (Shift bypasses, as in
// xterm), right-click paste, and links that open on Cmd/Ctrl+click. The
// core keeps the selection and finds links (crates/term); this only turns
// pixels into cells. Go suzuri: selection.go, mouse_pty.go, links.go.
import { cellMetrics, M_MOUSE_CLICK, M_MOUSE_DRAG, M_MOUSE_MOTION } from "./frame.js"

const MOUSE_MODES = M_MOUSE_CLICK | M_MOUSE_DRAG | M_MOUSE_MOTION

export function installPointer(pane, { invoke, isMac, focus, paste }) {
  const body = pane.body
  const canvas = pane.canvas
  const hover = document.createElement("div")
  hover.className = "link-hover"
  hover.hidden = true
  body.append(hover)

  let held = null // the button the app was told is down
  let selecting = false
  let edgeTimer = 0
  let lastCell = ""
  let link = null // the link under the pointer: {url, row, x0, x1}
  let linkSeq = 0

  // Pixels to a visible cell; `right` is the cell's right half. Outside
  // the grid it clamps, and `dy` says which edge it is past.
  function cellAt(e) {
    const { cw, ch } = cellMetrics()
    const r = canvas.getBoundingClientRect()
    const cols = Math.max(1, pane.size[0]), rows = Math.max(1, pane.size[1])
    const fx = (e.clientX - r.left) / cw
    const fy = (e.clientY - r.top) / ch
    const col = Math.min(cols - 1, Math.max(0, Math.floor(fx)))
    const row = Math.min(rows - 1, Math.max(0, Math.floor(fy)))
    const dy = fy < 0 ? -1 : fy >= rows ? 1 : 0
    return { col, row, right: fx - Math.floor(fx) >= 0.5 || fx >= cols, dy }
  }

  const appMouse = (e) => (pane.modes & MOUSE_MODES) !== 0 && !e.shiftKey
  const mods = (e) => ({ shift: e.shiftKey, alt: e.altKey, ctrl: e.ctrlKey })
  const report = (kind, button, c, e, held = null) =>
    invoke("pane_mouse", { id: pane.id, ev: { kind, button, held, col: c.col, row: c.row, ...mods(e) } })
  const linkMod = (e) => (isMac ? e.metaKey : e.ctrlKey)

  body.addEventListener("contextmenu", (e) => e.preventDefault())

  body.addEventListener("mousedown", (e) => {
    if (!pane.id || pane.exited) return
    focus(pane)
    const c = cellAt(e)
    if (e.button === 0 && linkMod(e) && link && link.row === c.row && c.col >= link.x0 && c.col < link.x1) {
      e.preventDefault()
      if (window.rszDryOpen) window.rszDryOpen(link.url)
      else invoke("open_url", { url: link.url }).catch((err) => console.warn(err))
      return
    }
    if (appMouse(e)) {
      e.preventDefault()
      held = e.button
      report("press", e.button, c, e)
      return
    }
    if (e.button === 2) {
      // Right-click pastes (Go suzuri, Windows muscle memory).
      e.preventDefault()
      paste()
      return
    }
    if (e.button !== 0) return
    e.preventDefault()
    const op = e.shiftKey ? "extend" : e.detail >= 3 ? "line" : e.detail === 2 ? "word" : "cell"
    // Shift+click with nothing selected starts a selection there.
    invoke("pane_select", { id: pane.id, op: op === "extend" && !pane.hasSelection ? "cell" : op, ...c })
    pane.hasSelection = op !== "cell"
    selecting = true
  })

  function drag(e) {
    const c = cellAt(e)
    invoke("pane_select", { id: pane.id, op: "extend", ...c })
    pane.hasSelection = true
    // Past the top or bottom edge: scroll the history and keep extending.
    clearInterval(edgeTimer)
    if (c.dy) {
      edgeTimer = setInterval(() => {
        invoke("pane_scroll", { id: pane.id, delta: -c.dy })
        invoke("pane_select", { id: pane.id, op: "extend", ...c })
      }, 60)
    }
  }

  window.addEventListener("mousemove", (e) => {
    if (!pane.id) return
    if (selecting) return drag(e)
    const inside = e.target instanceof Node && body.contains(e.target)
    if (held != null) {
      const c = cellAt(e)
      const key = `${c.col},${c.row}`
      if (key !== lastCell) {
        lastCell = key
        report("move", 0, c, e, held)
      }
      return
    }
    if (!inside) return clearLink()
    const c = cellAt(e)
    const key = `${c.col},${c.row}`
    if (key === lastCell) return
    lastCell = key
    if ((pane.modes & M_MOUSE_MOTION) && !e.shiftKey) report("move", 0, c, e, null)
    updateLink(c)
  })
  body.addEventListener("mouseleave", () => { if (!selecting && held == null) { lastCell = ""; clearLink() } })

  window.addEventListener("mouseup", (e) => {
    if (selecting && e.button === 0) {
      selecting = false
      clearInterval(edgeTimer)
    }
    if (held != null && e.button === held) {
      report("release", held, cellAt(e), e)
      held = null
    }
  })

  // The wheel: to the app when it reports the mouse, else the caller's
  // scrolling (history, or arrow keys on the alt screen).
  pane.wheelToApp = (e, lines) => {
    if (!appMouse(e)) return false
    const c = cellAt(e)
    const n = Math.min(32, Math.abs(lines))
    for (let i = 0; i < n; i++) report(lines < 0 ? "wheelup" : "wheeldown", 0, c, e)
    return true
  }

  // Hover: underline the link under the pointer and show a hand.
  async function updateLink(c) {
    const seq = ++linkSeq
    const l = await invoke("pane_link", { id: pane.id, col: c.col, row: c.row })
    if (seq !== linkSeq) return
    if (!l) return clearLink()
    link = l
    const { cw, ch } = cellMetrics()
    const r = canvas.getBoundingClientRect(), b = body.getBoundingClientRect()
    Object.assign(hover.style, {
      left: `${r.left - b.left + l.x0 * cw}px`,
      top: `${r.top - b.top + l.row * ch}px`,
      width: `${(l.x1 - l.x0) * cw}px`,
      height: `${ch}px`,
    })
    hover.title = `${l.url}\n${isMac ? "⌘" : "Ctrl"}+click to open`
    hover.hidden = false
    body.classList.add("on-link")
  }
  function clearLink() {
    linkSeq++
    link = null
    hover.hidden = true
    body.classList.remove("on-link")
  }
  // Frames move text under a still pointer: look again after one lands.
  pane.onFrame = () => { if (link) { lastCell = "" } }
}
