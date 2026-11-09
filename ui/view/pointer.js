// The pointer over a pane: text selection (drag, double-click word,
// triple-click line, Shift+click extends, drags past the edge scroll the
// history), mouse reports to apps that asked for them (Shift bypasses, as in
// xterm), right-click paste, and links that open on Cmd/Ctrl+click (Ctrl
// too on macOS). On an alt screen that reports the mouse (Claude, Grok), a
// double or triple click still selects a word or line here. The
// core keeps the selection and finds links (crates/term); this only turns
// pixels into cells. Go suzuri: selection.go, mouse_pty.go, links.go.
//
// pane: {body, canvas, size: [cols, rows], modes, exited, hasSelection};
// this sets pane.hasSelection, pane.wheelToApp(e, lines) and pane.onFrame().
// opts: what the pointer does, as callbacks (no IPC here):
//   metrics() -> {cw, ch}       the pane's cell size
//   live() -> bool              the pane has a terminal behind it
//   focus(pane), paste()        a click focuses; right-click pastes
//   select(op, {col, row, right})  selection ops: cell, word, line, extend
//   mouse(ev)                   a mouse report {kind, button, held, col, row, shift, alt, ctrl}
//   scroll(delta)               history lines (a drag past the edge)
//   link(col, row) -> Promise<{url, row, x0, x1} | null>
//   openUrl(url), isMac
// Returns dispose(), which removes every listener and timer it added.
import { M_ALT_SCREEN, M_MOUSE_CLICK, M_MOUSE_DRAG, M_MOUSE_MOTION } from "./frame.js"

const MOUSE_MODES = M_MOUSE_CLICK | M_MOUSE_DRAG | M_MOUSE_MOTION

export function installPointer(pane, opts) {
  const { isMac, focus, paste, metrics } = opts
  const live = opts.live || (() => !!pane.id)
  const body = pane.body
  const canvas = pane.canvas
  const doc = body.ownerDocument
  const win = doc.defaultView || globalThis
  const hover = doc.createElement("div")
  hover.className = "link-hover"
  hover.hidden = true
  body.append(hover)

  let held = null // the button the app was told is down
  let selecting = false
  let edgeTimer = 0
  let lastCell = ""
  let link = null // the link under the pointer: {url, row, x0, x1}
  let linkSeq = 0
  const added = [] // [target, type, fn] to remove on dispose
  const on = (t, type, fn) => { t.addEventListener(type, fn); added.push([t, type, fn]) }

  // Pixels to a visible cell; `right` is the cell's right half. Outside
  // the grid it clamps, and `dy` says which edge it is past.
  function cellAt(e) {
    const { cw, ch } = metrics()
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
    opts.mouse({ kind, button, held, col: c.col, row: c.row, ...mods(e) })
  // Go suzuri opened links on ⌘-click or Ctrl-click on macOS.
  const linkMod = (e) => (isMac ? e.metaKey || e.ctrlKey : e.ctrlKey)

  on(body, "contextmenu", (e) => e.preventDefault())

  on(body, "mousedown", (e) => {
    if (!live() || pane.exited) return
    focus(pane)
    const c = cellAt(e)
    if (e.button === 0 && linkMod(e) && link && link.row === c.row && c.col >= link.x0 && c.col < link.x1) {
      e.preventDefault()
      opts.openUrl(link.url)
      return
    }
    // Go suzuri: on the alt screen a double or triple click is the host's
    // word or line selection; single clicks still go to the app.
    const multi = e.button === 0 && e.detail >= 2 && (pane.modes & M_ALT_SCREEN) !== 0
    if (appMouse(e) && !multi) {
      e.preventDefault()
      if (pane.hasSelection) {
        opts.select("clear", c)
        pane.hasSelection = false
      }
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
    opts.select(op === "extend" && !pane.hasSelection ? "cell" : op, c)
    pane.hasSelection = op !== "cell"
    selecting = true
  })

  function drag(e) {
    const c = cellAt(e)
    opts.select("extend", c)
    pane.hasSelection = true
    // Past the top or bottom edge: scroll the history and keep extending.
    clearInterval(edgeTimer)
    if (c.dy) {
      edgeTimer = setInterval(() => {
        opts.scroll(-c.dy)
        opts.select("extend", c)
      }, 60)
    }
  }

  on(win, "mousemove", (e) => {
    if (!live()) return
    if (selecting) return drag(e)
    const inside = !!e.target && e.target.nodeType != null && body.contains(e.target)
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
  on(body, "mouseleave", () => { if (!selecting && held == null) { lastCell = ""; clearLink() } })

  on(win, "mouseup", (e) => {
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
    const l = await opts.link(c.col, c.row)
    if (seq !== linkSeq) return
    if (!l) return clearLink()
    link = l
    const { cw, ch } = metrics()
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

  return function dispose() {
    for (const [t, type, fn] of added) t.removeEventListener(type, fn)
    added.length = 0
    clearInterval(edgeTimer)
    clearLink()
    selecting = false
    held = null
    hover.remove()
    pane.wheelToApp = null
    pane.onFrame = null
  }
}
