// The paint worker: every pane's canvas is an OffscreenCanvas painted here,
// off the UI thread, as soon as its frames arrive (no requestAnimationFrame
// wait). With the frame socket the worker owns the socket too: frames come
// in, get painted, and their acks go straight back, without the UI thread.
// After each batch it tells the UI thread what it painted (cursor, modes,
// scroll state, size) so the DOM around the canvas can follow.
//
// UI -> worker:
//   {t: "init", ws: "port/token" | "", dpr, font: [family, url]}
//   {t: "pane", id, canvas}      a pane's OffscreenCanvas (transferred)
//   {t: "drop", id}              the pane closed
//   {t: "dpr", dpr}              the window moved to another screen
//   {t: "frame", id, buf}        a frame from a Tauri Channel (no socket)
//   {t: "style", void, selection, font}  theme and font (frame.js styleState)
// worker -> UI:
//   {t: "painted", at, panes: [{id, seq, cols, rows, cx, cy, shape, modes,
//     offset, history, frames, bytes}], ms}
//     `at` is when painting ended, in ms since the epoch (performance
//     timeOrigin + now), so the UI can set it against its own key times.
//   {t: "ready"} once the font is loaded and frames can be painted.
import { Screen, resetMetrics, applyStyle } from "./frame.js"

const screens = new Map() // pane id -> Screen
const early = new Map() // frames for panes the UI hasn't sent yet
let sock = null
let dpr = 1
let ready = false
const backlog = [] // [id, buf, off] that arrived before the font

function ack(id, seq) {
  if (!sock || sock.readyState !== 1) return
  const b = new DataView(new ArrayBuffer(8))
  b.setUint32(0, id, true)
  b.setUint32(4, seq >>> 0, true)
  sock.send(b.buffer)
}

// Paint a batch of [id, buf, off] frames and report them. Only the last
// frame's header per pane matters to the UI.
function paint(batch) {
  if (!ready) { backlog.push(...batch); return }
  const t = performance.now()
  const done = new Map()
  for (const [id, buf, off] of batch) {
    const s = screens.get(id)
    if (!s) {
      if (!early.has(id)) early.set(id, [])
      early.get(id).push([id, buf, off])
      continue
    }
    const h = s.apply(buf, off)
    const d = done.get(id) || { frames: 0, bytes: 0 }
    Object.assign(d, h, { id, frames: d.frames + 1, bytes: d.bytes + buf.byteLength - off })
    done.set(id, d)
  }
  if (!done.size) return
  const ms = performance.now() - t
  const panes = []
  for (const d of done.values()) {
    if (sock) ack(d.id, d.seq)
    panes.push({
      id: d.id, seq: d.seq, cols: d.cols, rows: d.rows, cx: d.cx, cy: d.cy, shape: d.shape,
      modes: d.modes, offset: d.offset, history: d.history, frames: d.frames, bytes: d.bytes,
    })
  }
  postMessage({ t: "painted", at: performance.timeOrigin + performance.now(), ms, panes })
}

function connect(spec) {
  const [port, token] = spec.split("/")
  sock = new WebSocket(`ws://127.0.0.1:${port}/?t=${token}`)
  sock.binaryType = "arraybuffer"
  // One message per tick: [u32 pane id][u32 len][frame] records.
  sock.onmessage = (e) => {
    const buf = e.data
    const dv = new DataView(buf)
    const batch = []
    for (let p = 0; p + 8 <= buf.byteLength;) {
      const id = dv.getUint32(p, true)
      const len = dv.getUint32(p + 4, true)
      batch.push([id, buf, p + 8])
      p += 8 + len
    }
    paint(batch)
  }
  sock.onerror = () => postMessage({ t: "log", line: "frame socket error" })
}

onmessage = async (e) => {
  const m = e.data
  switch (m.t) {
    case "init": {
      dpr = m.dpr || 1
      if (m.ws) connect(m.ws)
      if (m.font && self.fonts) {
        try {
          const ff = new FontFace(m.font[0], `url(${m.font[1]})`)
          self.fonts.add(ff)
          await ff.load()
          resetMetrics() // measured with the fallback font if asked before
        } catch (err) {
          postMessage({ t: "log", line: "worker font: " + err })
        }
      }
      ready = true
      postMessage({ t: "ready" })
      paint(backlog.splice(0))
      break
    }
    case "pane": {
      screens.set(m.id, new Screen(m.canvas, dpr))
      const q = early.get(m.id)
      early.delete(m.id)
      if (q) paint(q)
      break
    }
    case "drop":
      screens.delete(m.id)
      early.delete(m.id)
      break
    case "dpr":
      dpr = m.dpr
      for (const s of screens.values()) s.wantDpr = dpr
      break
    case "frame":
      paint([[m.id, m.buf, 0]])
      break
    case "style":
      // New metrics resize each canvas at its next frame; the UI asks the
      // panes for full frames after a font change.
      applyStyle(m)
      break
  }
}
