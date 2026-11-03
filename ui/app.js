// rust-suzuri UI: split layout, focus, input and painting. The terminal
// state lives in Rust; this only lays out panes and paints their frames.
import { Screen, cellMetrics, M_ALT_SCREEN, M_ALT_SCROLL, M_APP_CURSOR, M_BRACKETED_PASTE, M_FOCUS } from "./frame.js"
import { encodeKey } from "./keys.js"
import { clipboardImage, saveClipboardImage } from "./paste_image.js"

const { invoke, Channel } = window.__TAURI__.core
const log = (...a) => invoke("ui_log", { line: a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" ") })
window.addEventListener("error", (e) => log("error", e.message, e.filename + ":" + e.lineno))
window.addEventListener("unhandledrejection", (e) => log("rejection", String(e.reason)))
let firstMsg = true
const isMac = navigator.platform.startsWith("Mac")
if (!isMac) document.body.classList.add("win")

const root = document.getElementById("root")
const titleEl = document.getElementById("title")
const hud = document.getElementById("hud")

// One hidden textarea holds keyboard focus for every pane, so paste and IME
// events have somewhere to land.
const input = document.createElement("textarea")
input.setAttribute("autocapitalize", "off")
input.setAttribute("autocomplete", "off")
input.spellcheck = false
Object.assign(input.style, { position: "fixed", left: "-1000px", top: "0", width: "10px", height: "10px", opacity: "0" })
document.body.appendChild(input)

// ---- latency: oldest unpainted input -> end of the frame that paints it.
// Samples over 250 ms are dropped (oak's definition on #255, shared with
// Go suzuri's telemetry).
const lat = { samples: [], probe: [] }
function pct(arr, p) {
  if (!arr.length) return 0
  const s = [...arr].sort((a, b) => a - b)
  return s[Math.min(s.length - 1, Math.ceil((p / 100) * s.length) - 1)]
}
const paint = { frames: 0, bytes: 0, applyMs: 0 }

let focused = null
let tree = null
const byId = new Map()
const early = new Map() // frames that beat pane_open's reply, by pane id

// Flow control: tell the pane its frames up to lastSeq are painted, so it
// may send the next (one frame in flight per pane).
let frameSock = null
function ackFrame(p) {
  if (!p.id) return
  if (frameSock && frameSock.readyState === 1) {
    const b = new DataView(new ArrayBuffer(8))
    b.setUint32(0, p.id, true)
    b.setUint32(4, p.lastSeq >>> 0, true)
    frameSock.send(b.buffer)
  } else {
    invoke("pane_ack", { id: p.id, seq: p.lastSeq >>> 0 })
  }
}

// The frame socket: every pane's frames on one socket, [u32 pane id][frame].
function connectFrames(spec) {
  const [port, token] = spec.split("/")
  const sock = new WebSocket(`ws://127.0.0.1:${port}/?t=${token}`)
  sock.binaryType = "arraybuffer"
  frameSock = sock
  sock.onmessage = (e) => {
    const id = new DataView(e.data).getUint32(0, true)
    const p = byId.get(id)
    if (p) p.enqueue(e.data, 4)
    else {
      if (!early.has(id)) early.set(id, [])
      early.get(id).push([e.data, 4])
    }
  }
  sock.onerror = () => log("frame socket error")
}

class PaneView {
  constructor() {
    this.el = document.createElement("div")
    this.el.className = "pane"
    this.head = document.createElement("div")
    this.head.className = "head"
    this.head.innerHTML = `<span class="dot"></span><span class="t">shell</span>`
    this.body = document.createElement("div")
    this.body.className = "body"
    this.canvas = document.createElement("canvas")
    this.cursor = document.createElement("div")
    this.cursor.className = "cursor"
    this.scrolled = document.createElement("div")
    this.scrolled.className = "scrolled"
    this.scrolled.hidden = true
    this.body.append(this.canvas, this.cursor, this.scrolled)
    this.el.append(this.head, this.body)
    this.screen = new Screen(this.canvas)
    this.queue = []
    this.modes = 0
    this.title = "shell"
    this.id = 0
    this.size = [0, 0]
    this.pendingInput = 0 // timestamp of the oldest unpainted input
    this.pendingProbe = 0
    this.exited = false
    this.wheel = 0
    this.el.addEventListener("mousedown", () => setFocus(this))
    this.body.addEventListener("wheel", (e) => this.onWheel(e), { passive: true })
    this.ro = new ResizeObserver(() => this.fit())
    this.ro.observe(this.body)
  }

  // from: the pane this one splits from; the new shell starts in its cwd.
  async open(cols, rows, from = null) {
    const ch = new Channel()
    ch.onmessage = (m) => this.onMessage(m)
    this.size = [cols, rows]
    this.id = await invoke("pane_open", { cols, rows, command: null, from, onFrame: ch })
    byId.set(this.id, this)
    for (const [buf, off] of early.get(this.id) || []) this.enqueue(buf, off)
    early.delete(this.id)
    invoke("pane_focus", { id: this.id, focused: focused === this })
  }

  onMessage(m) {
    if (firstMsg) { firstMsg = false; log("first msg", Object.prototype.toString.call(m), m?.byteLength ?? m?.length) }
    if (m instanceof ArrayBuffer) return this.enqueue(m)
    if (ArrayBuffer.isView(m)) return this.enqueue(m.buffer.slice(m.byteOffset, m.byteOffset + m.byteLength))
    if (Array.isArray(m)) return this.enqueue(new Uint8Array(m).buffer)
    const msg = typeof m === "string" ? JSON.parse(m) : m
    if (msg.t === "title") {
      this.title = msg.title || "shell"
      this.head.querySelector(".t").textContent = this.title
      if (focused === this) titleEl.textContent = this.title
    } else if (msg.t === "exit") {
      this.exited = true
      this.head.insertAdjacentHTML("beforeend", `<span class="exited">exited${msg.code != null ? " " + msg.code : ""}</span>`)
      setTimeout(() => closePane(this), 600)
    } else if (msg.t === "clip") {
      navigator.clipboard?.writeText(msg.text).catch(() => {})
    }
  }

  // A frame is [buffer, byte offset of the frame in it].
  enqueue(buf, off = 0) {
    this.queue.push([buf, off])
    scheduleFrame()
  }

  // Apply queued frames; returns true if something was painted.
  flush() {
    if (!this.queue.length) return false
    const t = performance.now()
    let h
    for (const [buf, off] of this.queue) {
      h = this.screen.apply(buf, off)
      paint.bytes += buf.byteLength - off
      paint.frames++
    }
    this.queue.length = 0
    paint.applyMs += performance.now() - t
    this.modes = h.modes
    this.lastSeq = h.seq
    const { cw, ch } = cellMetrics()
    const cs = this.cursor.style
    this.cursor.hidden = h.shape === 0
    this.cursor.className = "cursor " + (["", "", "underline", "beam", "hollow"][h.shape] || "")
    cs.left = `${6 + h.cx * cw}px`
    cs.top = `${4 + h.cy * ch}px`
    cs.width = `${cw}px`
    cs.height = `${ch}px`
    cs.setProperty("--ch", `${ch}px`)
    this.scrolled.hidden = h.offset === 0
    if (h.offset) this.scrolled.textContent = `↑ ${h.offset} / ${h.history}`
    return true
  }

  fit() {
    const { cw, ch } = cellMetrics()
    const w = this.body.clientWidth - 12
    const hgt = this.body.clientHeight - 8
    if (w <= 0 || hgt <= 0) return
    const cols = Math.max(2, Math.floor(w / cw))
    const rows = Math.max(1, Math.floor(hgt / ch))
    if (cols === this.size[0] && rows === this.size[1]) return
    this.size = [cols, rows]
    if (this.id) invoke("pane_resize", { id: this.id, cols, rows })
  }

  send(data, t0) {
    if (!this.id || this.exited) return
    if (!this.pendingInput) this.pendingInput = t0 ?? performance.now()
    invoke("pane_write", { id: this.id, data })
  }

  onWheel(e) {
    const { ch } = cellMetrics()
    this.wheel += e.deltaY / ch
    const lines = Math.trunc(this.wheel)
    if (!lines) return
    this.wheel -= lines
    if (this.modes & M_ALT_SCREEN) {
      if (this.modes & M_ALT_SCROLL) {
        const seq = (this.modes & M_APP_CURSOR ? "\x1bO" : "\x1b[") + (lines < 0 ? "A" : "B")
        invoke("pane_write", { id: this.id, data: seq.repeat(Math.abs(lines)) })
      }
      return
    }
    invoke("pane_scroll", { id: this.id, delta: -lines })
  }
}

// ---- frames: everything queued is painted in one animation frame.
let frameScheduled = false
function scheduleFrame() {
  if (frameScheduled) return
  frameScheduled = true
  requestAnimationFrame(() => {
    frameScheduled = false
    for (const p of allPanes()) {
      if (!p.flush()) continue
      ackFrame(p)
      const now = performance.now()
      if (p.pendingInput) {
        const d = now - p.pendingInput
        if (d <= 250) lat.samples.push(d)
        p.pendingInput = 0
      }
      if (p.pendingProbe) {
        const d = now - p.pendingProbe
        if (d <= 250) lat.probe.push(d)
        p.pendingProbe = 0
      }
    }
  })
}

// ---- layout tree: {pane} leaves and {dir: "h"|"v", a, b} splits.
function allPanes(node = tree, out = []) {
  if (!node) return out
  if (node.pane) out.push(node.pane)
  else { allPanes(node.a, out); allPanes(node.b, out) }
  return out
}

function render() {
  const build = (node) => {
    if (node.pane) return node.pane.el
    const d = document.createElement("div")
    d.className = `split ${node.dir}`
    // Weight each side by the panes it holds along this direction, so
    // repeated splits come out equal instead of halving.
    const span = (n) => (n.pane || n.dir !== node.dir ? 1 : span(n.a) + span(n.b))
    const a = build(node.a), b = build(node.b)
    a.style.flex = `${span(node.a)} 1 0`
    b.style.flex = `${span(node.b)} 1 0`
    d.append(a, b)
    return d
  }
  root.replaceChildren(tree ? build(tree) : "")
}

function findParent(node, target, parent = null) {
  if (node === target) return parent
  if (node.pane) return undefined
  return findParent(node.a, target, node) ?? findParent(node.b, target, node)
}
function leafOf(pane, node = tree) {
  if (!node) return null
  if (node.pane === pane) return node
  if (node.pane) return null
  return leafOf(pane, node.a) || leafOf(pane, node.b)
}

async function split(dir, pane = focused) {
  const p = new PaneView()
  if (!tree) {
    tree = { pane: p }
  } else {
    const leaf = leafOf(pane)
    const old = { pane: leaf.pane }
    delete leaf.pane
    leaf.dir = dir
    leaf.a = old
    leaf.b = { pane: p }
  }
  render()
  setFocus(p)
  // Size from the laid-out element so the shell starts at its real size.
  await new Promise((r) => requestAnimationFrame(r))
  const { cw, ch } = cellMetrics()
  const cols = Math.max(2, Math.floor((p.body.clientWidth - 12) / cw))
  const rows = Math.max(1, Math.floor((p.body.clientHeight - 8) / ch))
  await p.open(cols, rows, pane?.id ?? null)
  for (const other of allPanes()) other.fit()
  return p
}

function closePane(pane) {
  if (!pane) return
  if (!pane.exited && pane.id) invoke("pane_close", { id: pane.id })
  pane.exited = true
  pane.ro.disconnect()
  const leaf = leafOf(pane)
  if (!leaf) return
  const parent = findParent(tree, leaf)
  if (!parent) {
    tree = null
    render()
    split("h") // the window always keeps one shell
    return
  }
  const sibling = parent.a === leaf ? parent.b : parent.a
  for (const k of ["pane", "dir", "a", "b"]) delete parent[k]
  Object.assign(parent, sibling)
  render()
  if (focused === pane) setFocus(allPanes(parent)[0])
}

function setFocus(p) {
  if (!p) return
  if (window.rszTrace) log("focus", p.id, allPanes().indexOf(p))
  const prev = focused
  focused = p
  for (const q of allPanes()) q.el.classList.toggle("focused", q === p)
  titleEl.textContent = p.title
  input.focus()
  if (prev && prev !== p && prev.id) {
    invoke("pane_focus", { id: prev.id, focused: false })
    if (prev.modes & M_FOCUS) invoke("pane_write", { id: prev.id, data: "\x1b[O" })
  }
  if (p.id && prev !== p) {
    invoke("pane_focus", { id: p.id, focused: true })
    if (p.modes & M_FOCUS) invoke("pane_write", { id: p.id, data: "\x1b[I" })
  }
}

function moveFocus(dx, dy) {
  if (!focused) return
  const r = focused.el.getBoundingClientRect()
  const cx = r.left + r.width / 2, cy = r.top + r.height / 2
  let best = null, bestD = Infinity
  for (const p of allPanes()) {
    if (p === focused) continue
    const q = p.el.getBoundingClientRect()
    const qx = q.left + q.width / 2, qy = q.top + q.height / 2
    const ddx = qx - cx, ddy = qy - cy
    if (dx && Math.sign(ddx) !== dx) continue
    if (dy && Math.sign(ddy) !== dy) continue
    if (dx && Math.abs(ddx) < 1) continue
    if (dy && Math.abs(ddy) < 1) continue
    const d = Math.abs(ddx) + Math.abs(ddy) * 1.0 + (dx ? Math.abs(ddy) : Math.abs(ddx)) * 2
    if (d < bestD) { bestD = d; best = p }
  }
  if (best) setFocus(best)
}

// ---- keys
function appShortcut(e) {
  const k = e.key.toLowerCase()
  const arrows = { arrowleft: [-1, 0], arrowright: [1, 0], arrowup: [0, -1], arrowdown: [0, 1] }
  if (isMac ? e.metaKey : e.ctrlKey && e.shiftKey) {
    if (k === "d" && (isMac ? !e.shiftKey : true)) return split("h"), true
    if ((isMac && k === "d" && e.shiftKey) || (!isMac && k === "e")) return split("v"), true
    if (k === "w") return closePane(focused), true
    if (k === "h") return toggleHud(), true
    if (isMac && e.altKey && arrows[k]) return moveFocus(...arrows[k]), true
    if (isMac && (k === "v" || k === "c" || k === "q")) return false // native paste/copy/quit
  }
  if (!isMac && e.altKey && !e.ctrlKey && arrows[k]) return moveFocus(...arrows[k]), true
  return false
}

input.addEventListener("keydown", (e) => {
  if (window.rszTrace) log("key", e.key, e.code, e.metaKey, e.ctrlKey, e.altKey, e.isComposing)
  if (e.isComposing) return
  if (appShortcut(e)) { e.preventDefault(); return }
  if (!focused) return
  const data = encodeKey(e, focused.modes, isMac)
  if (data == null) return
  e.preventDefault()
  focused.send(data, e.timeStamp || performance.now())
})
// Text that arrives without a usable keydown (IME commit, dead keys).
input.addEventListener("input", (e) => {
  const v = input.value
  input.value = ""
  if (v && focused && !e.isComposing) focused.send(v)
})
input.addEventListener("compositionend", () => {
  const v = input.value
  input.value = ""
  if (v && focused) focused.send(v)
})
function pasteText(pane, text) {
  const body = text.replace(/\r?\n/g, "\r")
  pane.send(pane.modes & M_BRACKETED_PASTE ? `\x1b[200~${body}\x1b[201~` : body)
}
input.addEventListener("paste", (e) => {
  e.preventDefault()
  if (!focused) return
  const pane = focused
  const text = e.clipboardData?.getData("text/plain")
  // A full-screen app (Claude Code, Grok) gets a pasted image as a file
  // path it can attach, ahead of any text (ui/paste_image.js).
  const image = pane.modes & M_ALT_SCREEN ? clipboardImage(e.clipboardData) : null
  if (image) {
    saveClipboardImage(image).then(
      (path) => pasteText(pane, path),
      (err) => { log("image paste failed", String(err)); if (text) pasteText(pane, text) },
    )
    return
  }
  if (text) pasteText(pane, text)
})
document.addEventListener("mousedown", () => setTimeout(() => input.focus(), 0))
window.addEventListener("focus", () => input.focus())

document.getElementById("split-right").onclick = () => split("h")
document.getElementById("split-down").onclick = () => split("v")
document.getElementById("close-pane").onclick = () => closePane(focused)

// ---- HUD: Cmd/Ctrl+Shift+H
let hudTimer = 0
function toggleHud() {
  hud.hidden = !hud.hidden
  clearInterval(hudTimer)
  if (!hud.hidden) hudTimer = setInterval(updateHud, 500)
}
async function updateHud() {
  const st = await invoke("stats")
  const inBytes = st.reduce((a, s) => a + s.bytes_in, 0)
  const feed = st.reduce((a, s) => a + s.feed_ms, 0)
  hud.textContent =
    `panes ${st.length}  in ${(inBytes / 1e6).toFixed(1)} MB  feed ${feed.toFixed(0)} ms\n` +
    `frames ${paint.frames}  ${(paint.bytes / 1e6).toFixed(1)} MB  paint ${paint.applyMs.toFixed(0)} ms\n` +
    `input p50 ${pct(lat.samples, 50).toFixed(1)}  p99 ${pct(lat.samples, 99).toFixed(1)} ms  n=${lat.samples.length}`
}

// ---- bench: RSZ_PANES opens N panes; RSZ_BENCH=1 runs the latency probe
// in the first pane (focused) and reports after RSZ_BENCH_SECS.
async function grid(n) {
  // Columns first, then rows: 8 panes = 4 x 2.
  const first = await split("h")
  // Let the first pane's program start before the rest (the bench's quiet
  // probe pane is whichever fakeagent starts first).
  await new Promise((r) => setTimeout(r, 500))
  const cols = Math.min(n, n >= 4 ? Math.ceil(n / 2) : n)
  const colPanes = [first]
  for (let i = 1; i < cols; i++) colPanes.push(await split("h", colPanes[i - 1]))
  let left = n - cols
  for (const c of colPanes) {
    if (left-- <= 0) break
    await split("v", c)
  }
  setFocus(first)
  return first
}

// In-app latency: key event (e.timeStamp) to the end of the paint that
// follows it, oak's definition (#255), samples over 250 ms dropped. With the
// internal probe the UI types "x"/Backspace itself every 100 ms (no OS input
// path); with the external probe real keys come from outside.
async function bench(boot, target) {
  await new Promise((r) => setTimeout(r, 15_000))
  lat.probe.length = 0
  lat.samples.length = 0
  const t0 = performance.now()
  const statsStart = await invoke("stats")
  const paintStart = { ...paint }
  let i = 0
  await new Promise((resolve) => {
    const iv = setInterval(() => {
      if (performance.now() - t0 > boot.bench_secs * 1000) { clearInterval(iv); resolve(); return }
      if (boot.bench_probe !== "internal") return
      const data = i++ % 2 ? "\x7f" : "x"
      const now = performance.now()
      if (!target.pendingProbe) target.pendingProbe = now
      target.send(data, now)
    }, 100)
  })
  const secs = (performance.now() - t0) / 1000
  const st = await invoke("stats")
  const sum = (a, k) => a.reduce((x, s) => x + s[k], 0)
  const summarize = (p) => ({
    n: p.length,
    p50: pct(p, 50), p90: pct(p, 90), p99: pct(p, 99), max: Math.max(0, ...p),
    over16: p.filter((x) => x > 16).length, over100: p.filter((x) => x > 100).length,
  })
  const report = {
    app: "rust-suzuri",
    platform: boot.platform,
    probe: boot.bench_probe,
    panes: st.length,
    secs: Math.round(secs),
    internal_probe_ms: summarize(lat.probe),
    key_event_ms: summarize(lat.samples),
    pty_mb_s: (sum(st, "bytes_in") - sum(statsStart, "bytes_in")) / 1e6 / secs,
    feed_ms_per_s: (sum(st, "feed_ms") - sum(statsStart, "feed_ms")) / secs,
    snapshot_ms_per_s: (sum(st, "snapshot_ms") - sum(statsStart, "snapshot_ms")) / secs,
    frames_per_s: (paint.frames - paintStart.frames) / secs,
    frame_mb_s: (paint.bytes - paintStart.bytes) / 1e6 / secs,
    paint_ms_per_s: (paint.applyMs - paintStart.applyMs) / secs,
  }
  await invoke("bench_report", { report, exit: true })
}

async function start() {
  await document.fonts.load(`14px "Gohu"`).catch(() => {})
  const boot = await invoke("boot")
  window.rszTrace = boot.trace
  if (boot.ws) connectFrames(boot.ws)
  const first = boot.panes > 1 ? await grid(boot.panes) : await split("h")
  if (boot.bench) bench(boot, first)
}
start()
