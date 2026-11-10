// suzuri UI: split layout, focus, input and painting. The terminal
// state lives in Rust; this only lays out panes and paints their frames.
// Each pane's terminal is a TermView from the view package (ui/view: the
// canvas, keys, IME, paste, the pointer and the wheel); this wires each one
// to the pane_* commands through its TermHost, and keeps the tabs, splits,
// input bar, settings, HUD and the paint worker.
import { Screen, cellMetrics, fontSpec, onStyle, styleState, M_ALT_SCREEN } from "./frame.js"
import { mount } from "./view/index.js"
import { keyFilter, pageKeys, textFilter, pasteFilter, pasteText, writeFrom, writeAfterPaste, pasteInOrder } from "./pane_input.js"
import { clipboardImage, saveClipboardImage } from "./paste_image.js"
import { isOpen as panelOpen } from "./panel.js"
import "./aicontrol.js"
import "./layout_tools.js"
import "./pane_drag.js"
import { neighbor, swapPanes } from "./layout_ops.js"
import { InputBar, PROMPT, barKey, displayPath } from "./bar.js"
import { shortcut, chordLabel } from "./shortcuts.js"
import { Palette } from "./palette.js"
import { Settings, announce } from "./settings.js"
import { loadAppearance, zoom } from "./appearance.js"
import { hostToast } from "./host_toast.js"
import { complete, ghostSuffix, DirCache, PENDING } from "./complete.js"
import { closeLarge } from "./images.js"
import { cleanTitle } from "./activity_state.js"
import { tabTitle, stickyAt, windowTitle, zoomCycle, MAX_TABS, MAX_PANES } from "./tab_rules.js"
import { confirm, isOpen as confirmOpen } from "./confirm.js"
import { FocusReports } from "./focus_report.js"

const { invoke, Channel } = window.__TAURI__.core
const log = (...a) => invoke("ui_log", { line: a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" ") })
window.addEventListener("error", (e) => log("error", e.message, e.filename + ":" + e.lineno))
window.addEventListener("unhandledrejection", (e) => log("rejection", String(e.reason)))
let firstMsg = true
const isMac = navigator.platform.startsWith("Mac")
if (!isMac) document.body.classList.add("win")

const root = document.getElementById("root")
const strip = document.getElementById("tabs")
const hud = document.getElementById("hud")

// Each pane's view has its own hidden textarea; the focused pane's holds
// the keyboard (paste and IME events land there). With no pane focused the
// page keeps the keys, and pageKeys (below) still runs the app shortcuts.
const focusInput = () => focused?.view.focus()

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

let home = ""
// ---- tabs: each has its own split tree, focused pane and element under
// #root. Only the active one is shown; the others keep running.
const tabs = []
let activeTab = null
let nextTabId = 1
const cur = () => activeTab
let focused = null // the active tab's focused pane
const byId = new Map()
const early = new Map() // frames that beat pane_open's reply, by pane id
// DECSET 1004 reports, for pane, tab and window focus (ui/focus_report.js).
const focusReports = new FocusReports({
  panes: () => everyPane(),
  focused: () => focused,
  write: (p, data) => invoke("pane_write", { id: p.id, data }),
  windowFocused: document.hasFocus(),
})
focusReports.listen(window)

// Painting: each pane's view paints its frames in requestAnimationFrame
// (default). RSZ_PAINT=worker hands every canvas to the paint worker
// (paint-worker.js) as an OffscreenCanvas, and the worker owns the frame
// socket: it paints frames as they arrive and acks them itself. On #289's
// benches it was no faster on the Mac or Windows and cost 65-100 MB more
// (a second JS heap and its canvases), so it stays opt-in.
let worker = null
const FONT = ["Gohu", "view/fonts/GohuFontuni14NerdFontMono-Regular.ttf"]
// With the worker, a view only measures: the window's cells (frame.js).
const WINDOW_CELLS = { cellMetrics, fontSpec, setStyle() {}, resetMetrics() {} }

// Flow control: tell the pane its frames up to seq are painted, so it may
// send the next (one frame in flight per pane).
let frameSock = null
function ackFrame(p, seq) {
  if (!p.id) return
  if (worker) {
    // The worker acks socket frames itself; Channel frames ack here.
    if (!frameSock) invoke("pane_ack", { id: p.id, seq: seq >>> 0 })
    return
  }
  if (frameSock && frameSock.readyState === 1) {
    const b = new DataView(new ArrayBuffer(8))
    b.setUint32(0, p.id, true)
    b.setUint32(4, seq >>> 0, true)
    frameSock.send(b.buffer)
  } else {
    invoke("pane_ack", { id: p.id, seq: seq >>> 0 })
  }
}

// The frame socket (main-thread painting): every pane's frames on one
// socket, one message per tick of [u32 pane id][u32 len][frame] records.
function connectFrames(spec) {
  const [port, token] = spec.split("/")
  const sock = new WebSocket(`ws://127.0.0.1:${port}/?t=${token}`)
  sock.binaryType = "arraybuffer"
  frameSock = sock
  sock.onmessage = (e) => {
    const buf = e.data
    const dv = new DataView(buf)
    for (let off = 0; off + 8 <= buf.byteLength;) {
      const id = dv.getUint32(off, true)
      const len = dv.getUint32(off + 4, true)
      const p = byId.get(id)
      if (p) p.enqueue(buf, off + 8)
      else {
        if (!early.has(id)) early.set(id, [])
        early.get(id).push([buf, off + 8])
      }
      off += 8 + len
    }
  }
  sock.onerror = () => log("frame socket error")
}

// The paint worker; resolves once its font is loaded.
function startWorker(boot) {
  worker = new Worker("paint-worker.js", { type: "module" })
  if (boot.ws) frameSock = true // the worker's socket: acks are its job
  worker.onerror = (e) => log("paint worker error", e.message)
  worker.onmessage = (e) => {
    const m = e.data
    if (m.t === "painted") return onPainted(m)
    if (m.t === "log") return log(m.line)
  }
  const ready = new Promise((r) => worker.addEventListener("message", (e) => e.data.t === "ready" && r()))
  worker.postMessage({ t: "init", ws: boot.ws, dpr: window.devicePixelRatio || 1, font: FONT })
  // Theme and font settings (appearance.js) follow into the worker.
  worker.postMessage({ t: "style", ...styleState() })
  onStyle((st) => worker.postMessage({ t: "style", ...st }))
  // A move to a screen with another pixel ratio: new backing stores, and a
  // full frame from every pane to fill them.
  const watchDpr = () => {
    matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`).addEventListener("change", () => {
      worker.postMessage({ t: "dpr", dpr: window.devicePixelRatio || 1 })
      for (const p of everyPane()) if (p.id) invoke("pane_redraw", { id: p.id })
      watchDpr()
    }, { once: true })
  }
  watchDpr()
  return ready
}

// The worker painted these panes' frames, ending at m.at (epoch ms).
function onPainted(m) {
  const now = m.at - performance.timeOrigin
  paint.applyMs += m.ms
  paint.workerLagMs = (paint.workerLagMs || 0) + (performance.now() - now)
  for (const h of m.panes) {
    const p = byId.get(h.id)
    if (!p) continue
    paint.frames += h.frames
    paint.bytes += h.bytes
    p.painted(h, now)
  }
}

// A pane's TermHost: the view's calls as pane_* commands. Nothing reaches
// the core before pane_open has answered (the pane has no id until then).
function paneHost(p) {
  return {
    write: writeFrom(p),
    // null: ⌘V / Ctrl+V / right-click, the clipboard as the core reads it.
    paste: (text) => (text == null ? paste(p) : pasteText(p, text, invoke)),
    // p.size is what the core has: pane_open's size, then each change.
    // Before pane_open answers the view's fit waits in p.fitSize.
    // While a divider is dragged (ui/sash.js) the gate spaces these out.
    resize: (cols, rows) => {
      if (!p.id) return void (p.fitSize = [cols, rows])
      const send = () => {
        if (cols === p.size[0] && rows === p.size[1]) return
        p.size = [cols, rows]
        invoke("pane_resize", { id: p.id, cols, rows })
      }
      window.rszResizeGate ? window.rszResizeGate(p, send) : send()
    },
    // Inline images are sized in cells on the Rust side (card #288). Before
    // pane_open answers it waits in p.cell, and open() sends it.
    cellPx: (w, h) => {
      p.cell = [w, h]
      if (p.id) invoke("pane_cell_px", { id: p.id, w, h })
    },
    scroll: (delta) => { if (p.id) invoke("pane_scroll", { id: p.id, delta }) },
    // The wheel's arrow keys on an alt screen: straight to the PTY, not
    // timed as typed input.
    wheelKeys: (data) => writeAfterPaste(p, () => invoke("pane_write", { id: p.id, data })),
    ack: (seq) => ackFrame(p, seq),
    redraw: () => { if (p.id) invoke("pane_redraw", { id: p.id }) },
    focus: () => {}, // setFocus reports the focused pane (mount's reportFocus: false)
    select: (op, col, row, right) => { if (p.id) invoke("pane_select", { id: p.id, op, col, row, right }) },
    copy: () => copy(p),
    mouse: (ev) => { if (p.id) invoke("pane_mouse", { id: p.id, ev }) },
    link: (col, row) => (p.id ? invoke("pane_link", { id: p.id, col, row }) : Promise.resolve(null)),
    // rszDryOpen: the selftest's hook (selftest.js), so a test click opens nothing.
    openUrl: (url) => {
      if (window.rszDryOpen) window.rszDryOpen(url)
      else invoke("open_url", { url }).then((note) => toast(note || "opened link"), (err) => console.warn(err))
    },
    keyFilter: keyFilter(p, {
      isMac, shortcut, closeLarge,
      run: (id, arg) => run(id, arg),
      now: () => performance.now(),
      copyOrInterrupt,
    }),
    textFilter: textFilter(p),
    pasteFilter: pasteFilter(p, { invoke, log, clipboardImage, saveClipboardImage }),
    onMessage: (msg) => p.onMessage(msg),
    onPaint: (h, stats) => p.onPaint(h, stats),
  }
}

class PaneView {
  constructor(tab) {
    this.tab = tab
    this.titleOverride = "" // Rename pane (or AI control's rename); wins over the program's title
    this.el = document.createElement("div")
    this.el.className = "pane"
    this.head = document.createElement("div")
    this.head.className = "head"
    this.head.innerHTML = `<span class="dot"></span><span class="t">shell</span>`
    this.el.append(this.head)
    // The input bar (#285): the shell's command line, under the screen.
    this.barEl = document.createElement("div")
    this.barEl.className = "inbar"
    this.barEl.hidden = true
    this.barEl.innerHTML = `<div class="cwd"></div><div class="lines"></div><div class="caret"></div>`
    this.bar = new InputBar()
    // Directory listings for Tab and the ghost text (#295), from the shell's cwd.
    this.dirs = new DirCache({
      sep: PATH_SEP,
      fetch: (dir, cwd) => invoke("complete_list", { cwd, dir }),
      onLoad: () => this.renderBar(),
    })
    // The shell as the pane reports it; see ShellStatus in crates/term.
    this.shell = { integrated: false, cwd: "", program: false, prompts: 0 }
    // Set on Enter until the next prompt: the command owns the keys from
    // the moment it is sent, before the shell's start mark arrives.
    this.awaiting = -1
    this.screen = null // the view's Screen (main-thread painting)
    this.queue = [] // the worker's frames that came before pane_open answered
    this.lastModes = 0
    this.title = "" // the program's title; "" until it sets one (tab_rules.js)
    this.id = 0
    this.size = [0, 0] // the core's (pane_open's, then each pane_resize)
    this.fitSize = null // the view's fit before pane_open answered
    this.cell = null // the view's cell pixels (pane_cell_px)
    this.disposed = false
    this.pendingInput = 0 // timestamp of the oldest unpainted input
    this.pendingProbe = 0
    this.keyTime = 0 // the key event whose bytes the view is writing (pane_input.js)
    this.exited = false
    // The terminal: canvas, cursor, IME textarea, pointer, wheel, images.
    // Its textarea sits on the input bar's caret at a prompt.
    this.view = mount(this.el, paneHost(this), {
      isMac,
      reportFocus: false,
      // The pointer acts only once pane_open has answered (the pane has an id).
      ready: () => !!this.id,
      caret: () => (this.barActive() ? this.barEl.querySelector(".caret") : this.cursor),
      paint: worker ? "external" : "view",
      // A worker pane's frames wait in this.queue until pane_open answers;
      // an imgpos waits with them.
      framesPending: () => this.queue.length > 0,
      // paste() below debounces every clipboard paste, the view's chords too.
      debouncePaste: false,
      screen: worker ? () => WINDOW_CELLS : (c) => (this.screen = new Screen(c, window.devicePixelRatio || 1)),
    })
    this.el.append(this.barEl)
    this.body = this.el.querySelector(".rsz-view")
    this.canvas = this.view.canvas
    this.cursor = this.body.querySelector(".cursor")
    this.input = this.body.querySelector("textarea")
    // A press focuses the pane before the pointer acts on it (capture runs
    // ahead of the view's own listeners). Not inside the pane-rename box in
    // the head: focusing the pane would blur it and end the rename.
    this.el.addEventListener("mousedown", (e) => {
      if (e.target.closest?.("input.rename")) return
      setFocus(this)
    }, true)
    this.head.addEventListener("dblclick", () => renamePane(this))
    // The view refits itself; the bar follows the width.
    this.ro = new ResizeObserver(() => { if (!this.barEl.hidden) this.renderBar() })
    this.ro.observe(this.body)
  }

  // The last painted frame's modes (the view's).
  get modes() { return this.view ? this.view.modes : 0 }

  // from: the pane this one splits from; the new shell starts in its cwd.
  // profile: a Settings profile's shell and folder (new tabs).
  async open(cols, rows, from = null, profile = null) {
    const ch = new Channel()
    ch.onmessage = (m) => this.onChannel(m)
    this.size = [cols, rows]
    this.id = await invoke("pane_open", { cols, rows, command: null, from, profile, onFrame: ch })
    byId.set(this.id, this)
    if (worker) {
      const off = this.canvas.transferControlToOffscreen()
      worker.postMessage({ t: "pane", id: this.id, canvas: off }, [off])
      const held = this.queue.splice(0)
      for (const [buf, o] of held) this.enqueue(buf, o)
    }
    for (const [buf, off] of early.get(this.id) || []) this.enqueue(buf, off)
    early.delete(this.id)
    // The view measured before the pane had an id: its cell size, and a fit
    // that came out other than the size it opened at.
    if (this.cell) invoke("pane_cell_px", { id: this.id, w: this.cell[0], h: this.cell[1] })
    const fs = this.fitSize
    this.fitSize = null
    if (fs && (fs[0] !== cols || fs[1] !== rows)) {
      this.size = fs
      invoke("pane_resize", { id: this.id, cols: fs[0], rows: fs[1] })
    }
    invoke("pane_focus", { id: this.id, focused: focused === this })
  }

  // The pane's Channel: frames and messages.
  onChannel(m) {
    if (firstMsg) { firstMsg = false; log("first msg", Object.prototype.toString.call(m), m?.byteLength ?? m?.length) }
    if (m instanceof ArrayBuffer) return this.enqueue(m)
    if (ArrayBuffer.isView(m)) return this.enqueue(m.buffer.slice(m.byteOffset, m.byteOffset + m.byteLength))
    if (Array.isArray(m)) return this.enqueue(new Uint8Array(m).buffer)
    // The view takes title, exit, img, imgdrop, imgpos and progress, then
    // hands every message to onMessage. A closed pane's view is gone, but
    // what the core still sends (an OSC 52 copy, title, shell) is handled.
    if (this.disposed) return this.onMessage(typeof m === "string" ? JSON.parse(m) : m)
    this.view.message(m)
  }

  onMessage(msg) {
    window.rszActivity?.onMessage(this, msg)
    if (msg.t === "title") {
      this.title = cleanTitle(msg.title) || ""
      this.showTitle()
    } else if (msg.t === "exit") {
      this.exited = true
      this.head.insertAdjacentHTML("beforeend", `<span class="exited">exited${msg.code != null ? " " + msg.code : ""}</span>`)
      setTimeout(() => closePane(this, "exit"), 600)
    } else if (msg.t === "clip") {
      navigator.clipboard?.writeText(msg.text).catch(() => {})
    } else if (msg.t === "shell") {
      this.shell = msg
      if (this.awaiting >= 0 && msg.prompts > this.awaiting) this.awaiting = -1
      this.renderBar()
    } else if (msg.t === "progress") {
      this.el.dataset.progress = msg.state ? String(msg.state) : ""
      this.el.style.setProperty("--progress", `${msg.percent}%`)
    }
  }

  // The bar has the keys: a shell with the hooks, at its prompt, on the
  // main screen. A full-screen app, a running command (Claude Code, ssh, a
  // build) or a shell without hooks gets the keys directly.
  barActive() {
    return this.shell.integrated && !this.shell.program && this.awaiting < 0 && !(this.modes & M_ALT_SCREEN) && !this.exited
  }

  barCols() {
    const { cw } = cellMetrics()
    const w = this.el.clientWidth - 2 * BAR_PAD_X
    return Math.max(8, Math.floor(w / cw) - [...PROMPT].length)
  }

  renderBar() {
    const on = this.barActive()
    if (this.barEl.hidden === on) this.barEl.hidden = !on
    this.cursor.style.visibility = on ? "hidden" : ""
    if (!on) return
    const { cw, ch } = cellMetrics()
    const cwd = displayPath(this.shell.cwd, home)
    const cwdEl = this.barEl.querySelector(".cwd")
    cwdEl.textContent = cwd
    cwdEl.hidden = !cwd
    const v = this.bar.visible(this.barCols())
    // The ghost text (#295): dim, after the caret, in the focused pane only.
    const ghost = focused === this ? ghostSuffix(this.bar, this.completionFs()) : ""
    const lines = this.barEl.querySelector(".lines")
    lines.style.setProperty("--ch", `${ch}px`)
    lines.replaceChildren(
      ...v.lines.map((text, i) => {
        const d = document.createElement("div")
        d.className = "row"
        const p = document.createElement("span")
        p.className = "prompt"
        p.textContent = i === 0 ? PROMPT : " ".repeat([...PROMPT].length)
        d.append(p, text)
        if (ghost && i === v.row) {
          const g = document.createElement("span")
          g.className = "ghost"
          g.textContent = ghost
          d.append(g)
        }
        return d
      }),
    )
    const caret = this.barEl.querySelector(".caret")
    caret.style.left = `${BAR_PAD_X + ([...PROMPT].length + v.col) * cw}px`
    caret.style.top = `${lines.offsetTop + v.row * ch}px`
    caret.style.width = `${cw}px`
    caret.style.height = `${ch}px`
    caret.classList.toggle("dim", focused !== this)
    if (focused === this) this.view.placeInput()
  }

  // A key while the bar has the keys. True when it was the bar's.
  barKeydown(e) {
    const r = barKey(this.bar, e, { isMac, cols: this.barCols(), fs: this.completionFs() })
    if (r === null) {
      // Plain typing: the key's character (Option's composed one on macOS).
      const ctl = e.ctrlKey || (isMac && e.metaKey)
      if (ctl || [...e.key].length !== 1) return false
      this.bar.insert(e.key)
    } else if (r.kind === "submit") {
      this.submit(r.line)
    } else if (r.kind === "pty") {
      this.send(r.data)
    } else if (r.kind === "complete") {
      this.tabComplete(r.reverse)
    } else if (r.kind === "scroll") {
      this.view.scrollBy(Math.round(r.pages * this.size[1])) // eased (ui/view/smooth.js)
    }
    this.renderBar()
    return true
  }

  // The completion file system: listings from the shell's cwd.
  completionFs() {
    this.dirs.cwd = this.shell.cwd
    return this.dirs
  }

  // Tab / Shift+Tab. A directory not listed yet is fetched first; the Tab
  // applies once it is, unless the bar changed meanwhile.
  async tabComplete(reverse) {
    for (let tries = 0; tries < 3; tries++) {
      const before = this.bar.text()
      const r = complete(this.bar, this.completionFs(), reverse)
      if (r !== PENDING) break
      await this.dirs.settled()
      if (this.bar.text() !== before || !this.barActive()) return
    }
    this.renderBar()
  }

  // Enter in the bar (#296): the core draws the command block, hides the
  // shell's echo and sends the line, or queues it while the shell is busy
  // ("queued · N"). A sent line hands the keys to the command until the
  // shell's next prompt.
  async submit(line) {
    if (line.trim() !== "") this.awaiting = this.shell.prompts
    if (!this.pendingInput) this.pendingInput = performance.now()
    const r = await invoke("pane_submit", { id: this.id, line }).catch((err) => (log("submit", String(err)), null))
    if (r?.kind === "queued") {
      this.awaiting = -1
      this.queued = r.n
      toast(`queued · ${r.n}`)
      this.renderBar()
      this.pollQueue()
    } else if (r?.kind !== "sent") {
      this.awaiting = -1
      this.renderBar()
    }
  }

  // Queued lines run one at a time once the shell is idle again (back at
  // its prompt and quiet for a moment).
  pollQueue() {
    if (this.queueTimer || !this.queued) return
    this.queueTimer = setInterval(async () => {
      if (this.exited) return this.stopQueue()
      const left = await invoke("pane_queue_flush", { id: this.id }).catch(() => null)
      if (left == null) return
      this.awaiting = this.shell.prompts
      this.queued = left
      toast(left ? `ran queued · ${left} left` : "ran queued")
      this.renderBar()
      if (!left) this.stopQueue()
    }, 300)
  }
  stopQueue() {
    clearInterval(this.queueTimer)
    this.queueTimer = 0
  }

  // Ctrl+C while a command runs also drops the queue (Go suzuri).
  async clearQueue() {
    if (!this.queued) return
    const n = await invoke("pane_queue_clear", { id: this.id }).catch(() => 0)
    this.queued = 0
    this.stopQueue()
    if (n) toast(`cleared ${n} queued`)
  }

  label() { return this.titleOverride || this.title }
  showTitle() {
    this.head.querySelector(".t").textContent = this.label() || "shell"
    if (this.tab.focused === this) renderStrip()
  }

  // A frame is [buffer, byte offset of the frame in it].
  enqueue(buf, off = 0) {
    if (worker && !this.id) {
      // Before pane_open answers: hold it until the worker knows the pane.
      this.queue.push([buf, off])
      return
    }
    if (worker) {
      // A Channel frame: the worker paints it. (Socket frames never come
      // through here; the worker reads the socket itself.)
      const b = off ? buf.slice(off) : buf
      worker.postMessage({ t: "frame", id: this.id, buf: b }, [b])
      return
    }
    // The view paints it on the next animation frame, then acks it.
    this.view.feed(buf, off)
  }

  // The worker painted this pane up to header h, at `now` (this thread's
  // clock): the view moves the cursor and chrome and acks; time pending input.
  painted(h, now) {
    this.view.painted(h)
    // The worker's paint reaches the screen with the next composite: time
    // input to this thread's next animation frame, the same point the
    // main-thread painter measures to (and never earlier than the pixels).
    if (this.pendingInput || this.pendingProbe) requestAnimationFrame(() => settleLatency(this, performance.now()))
  }

  // After the view followed a painted frame, before its ack. stats: the
  // view painted it (main thread), so it counts here and closes latency.
  onPaint(h, stats) {
    focusReports.check(this)
    const altFlip = (this.lastModes ^ h.modes) & M_ALT_SCREEN
    this.lastModes = h.modes
    if (altFlip) {
      this.renderBar()
      if (this.tab.focused === this) renderStrip() // "shell N" / "app N"
    }
    if (!stats) return
    paint.frames += stats.frames
    paint.bytes += stats.bytes
    paint.applyMs += stats.ms
    settleLatency(this, performance.now())
  }

  fit() {
    if (!this.barEl.hidden) this.renderBar()
    this.view.fit()
  }

  send(data, t0) {
    if (!this.id || this.exited) return
    if (!this.pendingInput) this.pendingInput = t0 ?? performance.now()
    window.rszActivity?.onInput(this)
    writeAfterPaste(this, () => invoke("pane_write", { id: this.id, data }))
  }

  // The pane is gone: its view lets go of its DOM, listeners and frames.
  dispose() {
    this.disposed = true
    this.ro.disconnect()
    this.view.unmount()
  }
}

// The separator a completed directory gets when its token has none.
const PATH_SEP = navigator.platform.startsWith("Win") ? "\\" : "/"

// Horizontal padding inside the input bar (matches .inbar in style.css).
const BAR_PAD_X = 8

// A paint of pane p ended at `now`: that closes its pending input samples.
function settleLatency(p, now) {
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

// ---- layout tree per tab: {pane} leaves and {dir: "h"|"v", a, b} splits.
function allPanes(node = activeTab?.tree, out = []) {
  if (!node) return out
  if (node.pane) out.push(node.pane)
  else { allPanes(node.a, out); allPanes(node.b, out) }
  return out
}
function everyPane() {
  const out = []
  for (const t of tabs) allPanes(t.tree, out)
  return out
}

function newTab(at = tabs.length) {
  const el = document.createElement("div")
  el.className = "tab-root"
  root.appendChild(el)
  // sticky: the name it froze at on its first split (tab_rules.js).
  const t = { id: nextTabId++, name: "", sticky: "", tree: null, focused: null, zoomed: null, el }
  tabs.splice(at, 0, t)
  return t
}

// The tab's name on the strip (tab_rules.js tabTitle).
function tabLabel(t) {
  const panes = allPanes(t.tree)
  return tabTitle({
    name: t.name, sticky: t.sticky, panes: panes.map((p) => p.label()),
    focused: t.focused?.label() || "", alt: !!(t.focused?.modes & M_ALT_SCREEN), index: tabs.indexOf(t),
  })
}

function render(tab = activeTab) {
  const build = (node) => {
    if (node.pane) return node.pane.el
    const d = document.createElement("div")
    d.className = `split ${node.dir}`
    // Weight each side by the panes it holds along this direction, so
    // repeated splits come out equal instead of halving.
    const span = (n) => (n.pane || n.dir !== node.dir ? 1 : span(n.a) + span(n.b))
    const a = build(node.a), b = build(node.b)
    // A ratio (set by grow/shrink/equalize, ui/layout_ops.js) wins.
    const wa = node.ratio != null ? node.ratio : span(node.a)
    const wb = node.ratio != null ? 1 - node.ratio : span(node.b)
    a.style.flex = `${wa} 1 0`
    b.style.flex = `${wb} 1 0`
    d.append(a, b)
    return d
  }
  // Moving the panes' elements (build() puts them in new split boxes) takes
  // the focus off a pane's textarea; it goes back after (the one textarea
  // suzuri had never moved). Read before build() moves anything.
  const had = document.activeElement
  // Maximized: only that pane shows; the others stay alive, detached.
  const top = tab.zoomed ? tab.zoomed.el : tab.tree ? build(tab.tree) : ""
  if (tab.zoomed) tab.zoomed.el.style.flex = "1 1 0"
  tab.el.replaceChildren(top)
  if (had && had !== document.activeElement && had.isConnected && had.closest?.(".rsz-view")) had.focus({ preventScroll: true })
  tab.el.classList.toggle("zoomed", !!tab.zoomed)
  // A tab's only pane has no title row (tabs.css).
  const panes = allPanes(tab.tree)
  for (const p of panes) p.el.classList.toggle("solo", panes.length === 1)
  renderStrip()
}

function findParent(node, target, parent = null) {
  if (node === target) return parent
  if (node.pane) return undefined
  return findParent(node.a, target, node) ?? findParent(node.b, target, node)
}
function leafOf(pane, node = pane?.tab?.tree) {
  if (!node) return null
  if (node.pane === pane) return node
  if (node.pane) return null
  return leafOf(pane, node.a) || leafOf(pane, node.b)
}

// Put `p` into `tab` next to `pane` (or as its only pane).
function placePane(p, dir, pane, tab) {
  p.tab = tab
  if (!tab.tree) {
    tab.tree = { pane: p }
    return
  }
  // Its first split freezes the tab's strip name.
  if (tab.tree.pane) tab.sticky = stickyAt(tab.name, tab.sticky, tab.focused?.label() || "shell")
  const leaf = leafOf(pane ?? allPanes(tab.tree).at(-1), tab.tree)
  const old = { pane: leaf.pane }
  delete leaf.pane
  leaf.dir = dir
  leaf.a = old
  leaf.b = { pane: p }
}

// Take `pane` out of its tab's tree. Returns false when it was the only one.
function detachPane(pane) {
  const tab = pane.tab
  const leaf = leafOf(pane)
  if (!leaf) return false
  if (tab.zoomed) tab.zoomed = null
  const parent = findParent(tab.tree, leaf)
  if (!parent) {
    tab.tree = null
    tab.sticky = ""
    return true
  }
  const sibling = parent.a === leaf ? parent.b : parent.a
  for (const k of ["pane", "dir", "a", "b"]) delete parent[k]
  Object.assign(parent, sibling)
  if (tab.focused === pane) tab.focused = allPanes(parent)[0]
  if (tab.tree.pane) tab.sticky = "" // one pane left: follow it again
  return true
}

async function split(dir, pane = focused, opts = {}) {
  const tab = opts.tab ?? pane?.tab ?? activeTab
  if (everyPane().length >= MAX_PANES) return void toast("max panes")
  if (tab.zoomed) tab.zoomed = null
  const p = new PaneView(tab)
  placePane(p, dir, pane, tab)
  render(tab)
  if (tab === activeTab) setFocus(p)
  else tab.focused = tab.focused ?? p
  // Size from the laid-out element so the shell starts at its real size.
  await new Promise((r) => requestAnimationFrame(r))
  const { cw, ch } = cellMetrics()
  const cols = Math.max(2, Math.floor((p.body.clientWidth - 12) / cw) || 80)
  const rows = Math.max(1, Math.floor((p.body.clientHeight - 8) / ch) || 24)
  await p.open(cols, rows, pane?.id ?? null, opts.profile)
  for (const other of allPanes(tab.tree)) other.fit()
  return p
}

// how: "user" (⌘W, the palette), "exit" (its shell ended) or "ai" (AI
// control). The last pane of the last tab asks "Quit suzuri?" when the user
// closes it and quits when its shell ends (Go's closePageAt); AI control
// gets a fresh shell there instead.
async function closePane(pane, how = "user") {
  if (!pane) return
  const last = tabs.length === 1 && allPanes(pane.tab.tree).length === 1 && pane.tab.tree?.pane === pane
  if (last && how === "user") return void ((await askQuit()) ? quit() : focusInput())
  if (last && how === "exit") return quit()
  if (!pane.exited && pane.id) invoke("pane_close", { id: pane.id })
  pane.exited = true
  pane.dispose()
  if (worker && pane.id) worker.postMessage({ t: "drop", id: pane.id })
  const tab = pane.tab
  if (!detachPane(pane)) return
  if (!tab.tree) {
    // Its last pane: the tab goes, and the window always keeps one shell.
    if (tabs.length > 1) return dropTab(tab)
    render(tab)
    split("h", null, { tab })
    return
  }
  render(tab)
  if (tab === activeTab && (focused === pane || !focused)) setFocus(tab.focused)
  if (how === "user") { const n = allPanes(tab.tree).length; toast(n === 1 ? "1 pane" : `${n} panes`) }
}

// Go's confirm-quit modal, and the quit itself (app/src/window.rs).
function askQuit() {
  return confirm({ title: "Quit suzuri?", body: "This is the last tab. Close the window?", yes: "Quit", no: "Cancel" })
}
function quit() {
  invoke("app_quit").catch((err) => log("quit", String(err)))
}

// ---- tabs
// A new tab starts in its profile's folder (else home), not the active
// pane's cwd, and a profile other than Default names the tab: Go suzuri's
// splitOptsFromProfile.
async function openTab(profile) {
  if (tabs.length >= MAX_TABS) return void toast("tab limit")
  // Its first pane would go past the pane limit: no empty tab.
  if (everyPane().length >= MAX_PANES) return void toast("max panes")
  // New tabs go at the end of the strip (Go's addPageWithTab).
  const t = newTab()
  const pname = profile || settingsCfg?.active_profile || ""
  if (pname && pname.toLowerCase() !== "default") t.name = pname
  showTab(t)
  return split("h", null, { tab: t, profile })
}

function showTab(t) {
  if (!t) return
  if (activeTab === t) return void renderStrip()
  activeTab = t
  for (const x of tabs) x.el.hidden = x !== t
  render(t)
  if (t.focused) setFocus(t.focused, true)
}

function switchTab(delta) {
  if (tabs.length < 2) return
  const i = tabs.indexOf(activeTab)
  showTab(tabs[(i + delta + tabs.length) % tabs.length])
}

async function closeTab(t = activeTab) {
  if (tabs.length === 1) return void ((await askQuit()) ? quit() : focusInput())
  for (const p of allPanes(t.tree)) {
    if (!p.exited && p.id) invoke("pane_close", { id: p.id })
    p.exited = true
    p.dispose()
  }
  t.tree = null
  dropTab(t)
  toast(tabs.length === 1 ? "1 tab" : `${tabs.length} tabs`)
}

function dropTab(t) {
  const i = tabs.indexOf(t)
  tabs.splice(i, 1)
  t.el.remove()
  if (activeTab === t) {
    activeTab = null
    showTab(tabs[Math.min(i, tabs.length - 1)])
  } else renderStrip()
}

// Move the focused pane to tab `to` (an index; -1 or tabs.length make a new
// tab at that end). Go suzuri's ⌘⌥⇧←/→ and "Move pane to tab".
function movePaneToTab(pane, to) {
  if (!pane) return
  const from = pane.tab
  if (to >= 0 && to < tabs.length && tabs[to] === from) return
  const alone = allPanes(from.tree).length === 1
  if (alone && (to < 0 || to >= tabs.length)) return // already its own tab
  if ((to < 0 || to >= tabs.length) && tabs.length >= MAX_TABS) return void toast("tab limit")
  let target
  if (to < 0) target = newTab(0)
  else if (to >= tabs.length) target = newTab(tabs.length)
  else target = tabs[to]
  detachPane(pane)
  placePane(pane, "h", target.focused, target)
  target.focused = pane
  if (!from.tree) dropTab(from)
  else render(from)
  activeTab = null
  showTab(target)
  render(target)
  setFocus(pane)
}

// Every pane of tab `t` joins tab `into` (Go's "Merge tab into").
function mergeTab(t, into) {
  if (!into || t === into) return
  for (const p of allPanes(t.tree)) {
    placePane(p, "h", into.focused, into)
    into.focused = into.focused ?? p
  }
  t.tree = null
  dropTab(t)
  activeTab = null
  showTab(into)
}

function togglePaneZoom(tab = activeTab) {
  if (!tab?.focused) return
  if (tab.zoomed || allPanes(tab.tree).length < 2) tab.zoomed = null
  else tab.zoomed = tab.focused
  render(tab)
  setFocus(tab.focused, true)
}

// Inline rename on the strip or a pane head: Enter keeps, Esc drops.
function editInline(host, value, done) {
  const box = document.createElement("input")
  box.className = "rename"
  box.value = value
  box.spellcheck = false
  host.replaceChildren(box)
  box.focus()
  box.select()
  let finished = false
  const finish = (keep) => {
    if (finished) return
    finished = true
    done(keep ? box.value.trim() : null)
    focusInput()
  }
  box.addEventListener("keydown", (e) => {
    e.stopPropagation()
    if (e.key === "Enter") finish(true)
    else if (e.key === "Escape") finish(false)
  })
  box.addEventListener("blur", () => finish(true))
  box.addEventListener("mousedown", (e) => e.stopPropagation())
}

function renameTab(t = activeTab) {
  const el = strip.querySelector(`[data-tab="${t.id}"] .name`)
  if (!el) return
  editInline(el, tabLabel(t), (v) => {
    if (v != null && v !== tabLabel(t)) { t.name = v; toast("renamed") }
    renderStrip()
  })
}

function renamePane(p = focused) {
  if (!p) return
  const el = p.head.querySelector(".t")
  const was = p.label() || "shell"
  editInline(el, was, (v) => {
    // Unchanged (Enter or a blur on the pre-fill) keeps what it had.
    if (v != null && v !== was) p.titleOverride = v === p.title ? "" : v
    el.textContent = p.label() || "shell"
    renderStrip()
  })
}

function renderStrip() {
  const frag = document.createDocumentFragment()
  tabs.forEach((t, i) => {
    const b = document.createElement("div")
    b.className = "tab" + (t === activeTab ? " active" : "")
    b.dataset.tab = t.id
    const n = allPanes(t.tree).length
    b.title = `${tabLabel(t)}${n > 1 ? ` · ${n} panes` : ""}${i < 9 ? ` · ${isMac ? "⌘" : "Ctrl+"}${i + 1}` : ""}`
    b.innerHTML = `<span class="num"></span><span class="name"></span><span class="count"></span><button class="x" title="Close tab">×</button>`
    b.querySelector(".num").textContent = i < 9 ? String(i + 1) : ""
    b.querySelector(".name").textContent = tabLabel(t)
    b.querySelector(".count").textContent = n > 1 ? String(n) : ""
    b.addEventListener("mousedown", (e) => {
      if (e.target.closest(".x") || e.target.closest("input")) return
      if (e.button === 1) { e.preventDefault(); return closeTab(t) }
      showTab(t)
    })
    b.addEventListener("dblclick", (e) => { if (!e.target.closest(".x")) renameTab(t) })
    b.querySelector(".x").addEventListener("click", (e) => { e.stopPropagation(); closeTab(t) })
    frag.appendChild(b)
  })
  // Don't rebuild under an open rename box.
  if (strip.querySelector("input.rename")) return
  strip.replaceChildren(frag)
  document.title = windowTitle(activeTab ? tabLabel(activeTab) : "")
}

function setFocus(p, force = false) {
  if (!p) return
  if (p.tab !== activeTab) showTab(p.tab)
  if (window.rszTrace) log("focus", p.id, allPanes().indexOf(p))
  const prev = focused
  focused = p
  p.tab.focused = p
  for (const q of allPanes()) q.el.classList.toggle("focused", q === p)
  if (prev && prev !== p) prev.renderBar()
  p.renderBar()
  renderStrip()
  p.view.focus()
  if (prev && prev !== p && prev.id && !prev.exited) {
    invoke("pane_focus", { id: prev.id, focused: false })
  }
  if (p.id && (prev !== p || force)) invoke("pane_focus", { id: p.id, focused: true })
  focusReports.sync()
}

// After a layout op (ui/layout_ops.js): rebuild the DOM, keep focus, refit.
function relayout() {
  render()
  if (focused) setFocus(focused)
  for (const p of allPanes()) p.fit()
}

// Trades the focused pane with its neighbour in (dx, dy) (Go's move_pane_*).
function movePane(dx, dy) {
  if (!focused) return
  if (activeTab.zoomed) return void toast("pane is maximized")
  const rects = allPanes().map((p) => {
    const r = p.el.getBoundingClientRect()
    return { pane: p, x: r.left, y: r.top, w: r.width, h: r.height }
  })
  const other = neighbor(rects, focused, dx, dy)
  if (other && swapPanes(activeTab.tree, focused, other)) relayout()
}

function moveFocus(dx, dy) {
  if (!focused) return
  // Maximized: the arrows walk the hidden panes in tree order, zoom following.
  if (activeTab.zoomed) {
    const next = zoomCycle(allPanes(), focused, dx, dy)
    if (!next) return
    activeTab.zoomed = next
    render(activeTab)
    setFocus(next)
    return next.fit()
  }
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

// ---- commands: what the palette lists and the shortcuts run.
let settingsCfg = null // the saved settings, for the profile entries
const overlays = document.getElementById("overlays")
const palette = new Palette(overlays, () => focusInput())
const settings = new Settings(overlays, () => focusInput())
const overlayOpen = () => palette.isOpen || settings.isOpen || confirmOpen()

// Other parts add palette entries here: {id, title, category, hint?, run()}.
const extraCommands = []
window.rsz = { addCommands: (cmds) => extraCommands.push(...cmds), toast: (text) => toast(text) }
// The bell's history (ui/bell.js) reveals a pane by id; false if it's gone.
window.rsz.focusPane = (id) => { const p = byId.get(id); if (p) setFocus(p); return !!p }
// Files dropped on the window (ui/file_drop.js) paste into the focused pane.
window.rsz.pasteFocused = (text) => { if (focused?.id && !focused.disposed) pasteText(focused, text, invoke) }
// Host feedback as a notice card (ui/host_toast.js).
window.rsz.toast = hostToast

const ACTIONS = {
  new_tab: () => openTab(),
  close_tab: () => closeTab(),
  next_tab: () => switchTab(1),
  prev_tab: () => switchTab(-1),
  select_tab: (i) => tabs[i] && showTab(tabs[i]),
  rename_tab: () => renameTab(),
  split_right: () => split("h").then((p) => p && toast("split right")),
  split_down: () => split("v").then((p) => p && toast("split down")),
  close_pane: () => closePane(focused),
  rename_pane: () => renamePane(),
  zoom_pane: () => togglePaneZoom(),
  focus_left: () => moveFocus(-1, 0),
  focus_right: () => moveFocus(1, 0),
  focus_up: () => moveFocus(0, -1),
  focus_down: () => moveFocus(0, 1),
  move_pane_left: () => movePane(-1, 0),
  move_pane_right: () => movePane(1, 0),
  move_pane_up: () => movePane(0, -1),
  move_pane_down: () => movePane(0, 1),
  move_pane_prev_tab: () => movePaneToTab(focused, tabs.indexOf(activeTab) - 1),
  move_pane_next_tab: () => movePaneToTab(focused, tabs.indexOf(activeTab) + 1),
  move_pane_new_tab: () => movePaneToTab(focused, tabs.length),
  move_pane_to: (id) => movePaneToTab(focused, tabs.findIndex((t) => t.id === id)),
  merge_tab_into: (id) => mergeTab(activeTab, tabs.find((t) => t.id === id)),
  new_tab_profile: (name) => openTab(name),
  palette: () => openPalette(),
  settings: () => openSettings(),
  hud: () => toggleHud(),
  new_window: () => invoke("new_window").catch((err) => log("new window failed", String(err))),
  zoom_in: () => zoom(1),
  zoom_out: () => zoom(-1),
  zoom_reset: () => zoom(0),
}

function run(id, arg) {
  if (palette.isOpen) palette.close()
  // A chord for a command another module added (help.js's ⌘/).
  const act = ACTIONS[id] || extraCommands.find((x) => x.id === id)?.run
  act?.(arg)
}

// Go suzuri's DefaultCommands + TabMoveCommands, for what suzuri has.
function commandList() {
  const c = (id, title, category, arg) => ({ id, title, category, arg, hint: chordLabel(id, isMac) })
  const list = [
    c("settings", "Settings", "Appearance"),
    c("new_tab", "New tab", "Tabs"),
    c("new_window", "New window", "Window"),
  ]
  const active = settingsCfg?.active_profile
  for (const p of settingsCfg?.profiles || []) {
    list.push({ id: "new_tab_profile", arg: p.name, category: "Tabs", title: `New tab: ${p.name}${p.name === active ? " ★" : ""}`, hint: p.shell || "" })
  }
  list.push(
    c("close_tab", "Close tab", "Tabs"),
    c("rename_tab", "Rename tab", "Tabs"),
    c("next_tab", "Next tab", "Tabs"),
    c("prev_tab", "Previous tab", "Tabs"),
    c("split_right", "Split right", "Panes"),
    c("split_down", "Split down", "Panes"),
    c("rename_pane", "Rename pane", "Panes"),
    c("close_pane", "Close pane", "Panes"),
    c("focus_left", "Focus pane left", "Panes"),
    c("focus_right", "Focus pane right", "Panes"),
    c("focus_up", "Focus pane up", "Panes"),
    c("focus_down", "Focus pane down", "Panes"),
    c("move_pane_left", "Move pane left", "Panes"),
    c("move_pane_right", "Move pane right", "Panes"),
    c("move_pane_up", "Move pane up", "Panes"),
    c("move_pane_down", "Move pane down", "Panes"),
    c("zoom_pane", "Maximize pane", "Panes"),
    c("move_pane_prev_tab", "Move pane to previous tab", "Panes"),
    c("move_pane_next_tab", "Move pane to next tab", "Panes"),
    c("move_pane_new_tab", "Move pane to new tab", "Panes"),
  )
  tabs.forEach((t, i) => {
    if (t === activeTab) return
    list.push({ id: "move_pane_to", arg: t.id, category: "Panes", title: `Move pane to tab: ${i + 1} ${tabLabel(t)}` })
  })
  tabs.forEach((t, i) => {
    if (t === activeTab) return
    list.push({ id: "merge_tab_into", arg: t.id, category: "Tabs", title: `Merge tab into: ${i + 1} ${tabLabel(t)}` })
  })
  list.push(c("zoom_in", "Zoom in", "Appearance"), c("zoom_out", "Zoom out", "Appearance"), c("zoom_reset", "Reset zoom", "Appearance"))
  list.push(c("hud", "Stats HUD", "Help"))
  for (const x of extraCommands) list.push({ ...x, extra: true })
  return list
}

function openPalette() {
  if (settings.isOpen) return
  palette.open(commandList(), (it) => (it.extra ? it.run() : run(it.id, it.arg)))
}

function openSettings() {
  if (palette.isOpen) palette.close()
  if (!settings.isOpen) settings.open()
}

window.addEventListener("rsz:config", (e) => { settingsCfg = e.detail })

// ---- keys: each pane's view takes its keys, IME and pastes (ui/view),
// after suzuri's own (pane_input.js: shortcuts, the input bar).
document.addEventListener("keydown", (e) => {
  if (window.rszTrace && e.target?.closest?.(".rsz-view")) log("key", e.key, e.code, e.metaKey, e.ctrlKey, e.altKey, e.isComposing)
}, true)

// Ctrl+C with a selection: copy, and ^C to the pane focused by then when
// there was nothing to copy (no key time: a ^C that is not a key's).
function copyOrInterrupt() {
  copy().then((had) => { if (!had) focused?.send("\x03") })
}

// Keys with no pane's textarea focused (pane_input.js pageKeys).
document.addEventListener("keydown", pageKeys({
  isMac, page: [document.body, document.documentElement, null],
  blocked: () => overlayOpen() || panelOpen(),
  pane: () => (focused && !focused.disposed ? focused : null),
  hasSelection: () => !!focused?.view.hasSelection,
  clip: (kind) => (kind === "paste" ? paste() : kind === "copy" ? copy() : copyOrInterrupt()),
  closeLarge, shortcut,
  run: (id, arg) => run(id, arg),
}))

async function copy(pane = focused) {
  if (!pane?.id) return false
  const had = await invoke("pane_copy", { id: pane.id }).catch((err) => (log("copy", String(err)), false))
  if (had) hostToast("copied")
  return had
}
let lastPaste = 0
// ⌘V / Ctrl+V / right-click / the Edit menu: the clipboard into the pane.
async function paste(pane = focused) {
  if (!pane?.id || pane.exited) return
  // One key press can arrive as both a keydown and a menu action (Go
  // suzuri debounces the same way).
  const now = performance.now()
  if (now - lastPaste < 280) return
  lastPaste = now
  try {
    // At the prompt the bar takes the text; otherwise the core reads the
    // clipboard itself (an image on the alt screen becomes a file path).
    if (pane.barActive()) return pasteText(pane, await invoke("clipboard_text"), invoke)
    if (!pane.pendingInput) pane.pendingInput = performance.now()
    // Read off the core's main thread: keys typed meanwhile wait for it.
    await pasteInOrder(pane, () => invoke("pane_paste_clipboard", { id: pane.id }))
  } catch (err) {
    log("paste", String(err))
  }
}
let toastTimer = 0
function toast(text) {
  const t = document.getElementById("toast")
  t.textContent = text
  t.hidden = false
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (t.hidden = true), 900)
}

// macOS: the Edit menu's Copy and Paste are ours (app/src/main.rs
// install_menu): copy the pane's selection, paste through the core.
window.__TAURI__.event.listen("rsz-copy", () => copy())
window.__TAURI__.event.listen("rsz-paste", () => paste())
// Clicks give the keyboard back to the terminal, except inside an overlay,
// a panel (#286) or a rename box.
document.addEventListener("mousedown", (e) => {
  if (overlayOpen() || e.target.closest?.("input.rename")) return
  setTimeout(() => panelOpen() || focusInput(), 0)
})
window.addEventListener("focus", () => { if (!overlayOpen() && !panelOpen()) focusInput() })
window.addEventListener("rsz-panel-closed", () => focusInput())

// For #286's AI control (ui/aicontrol.js): the active tab's tree, every
// pane in every tab, and the commands.
window.rszHost = {
  tree: () => activeTab?.tree,
  focused: () => focused,
  allPanes: () => everyPane(),
  split, closePane: (p) => closePane(p, "ai"), setFocus, relayout,
  tabs: () => tabs, activeTab: () => activeTab, openTab, showTab, movePaneToTab,
}

// Go suzuri's AI-control tab tools (internal/aicontrol/server.go).
window.rszRegisterTool?.("new_tab", async () => {
  if (tabs.length >= MAX_TABS) return [400, { error: "tab limit" }]
  if (everyPane().length >= MAX_PANES) return [400, { error: "max panes" }]
  const p = await openTab()
  if (!p) return [400, { error: "could not open a tab" }]
  return [200, { ok: true, tab_id: p.tab.id, pane_id: p.id }]
})
window.rszRegisterTool?.("move_to_tab", async (a) => {
  const p = everyPane().find((x) => x.id === Number(a.pane_id))
  if (!p) return [404, { error: `no pane ${a.pane_id}` }]
  let to = tabs.length // no tab_id: extract to a new tab
  if (a.tab_id != null) {
    to = tabs.findIndex((t) => t.id === Number(a.tab_id))
    if (to < 0) return [404, { error: `no tab ${a.tab_id}` }]
  }
  if (to === tabs.length && allPanes(p.tab.tree).length === 1) return [400, { error: "the pane is already alone in its tab" }]
  movePaneToTab(p, to)
  return [200, { ok: true, pane_id: p.id, tab_id: p.tab.id }]
})

document.getElementById("new-tab").onclick = () => openTab()
document.getElementById("split-right").onclick = () => split("h")
document.getElementById("split-down").onclick = () => split("v")
document.getElementById("open-palette").onclick = () => openPalette()
document.getElementById("open-settings").onclick = () => openSettings()

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
  const first = await openTab()
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
    app: "suzuri",
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

// After a font change: every pane refits to the new cell size; a theme
// change alone needs only a repaint (Rust sends full frames itself).
// (relayout() above is #293's: after a layout op.)
function refitFont(font = true) {
  if (!font) return
  for (const p of everyPane()) {
    // Main thread: the canvas resizes now. Worker: its CSS size follows at
    // the next paint (the view sees the new cells).
    if (p.screen) p.screen.setSize(p.screen.cols, p.screen.rows)
    p.fit()
    if (p.id) invoke("pane_redraw", { id: p.id })
  }
}

async function start() {
  await loadAppearance(refitFont).catch((e) => log("appearance", String(e)))
  const boot = await invoke("boot")
  window.rszTrace = boot.trace
  home = boot.home
  const canWorker = typeof OffscreenCanvas !== "undefined" && "transferControlToOffscreen" in HTMLCanvasElement.prototype
  if (boot.paint === "worker" && canWorker) await startWorker(boot)
  else if (boot.ws) connectFrames(boot.ws)
  log("paint:", worker ? "worker" : "main", "transport:", boot.ws ? "socket" : "channel")
  settingsCfg = await invoke("config_get").catch(() => null)
  if (settingsCfg) announce(settingsCfg)
  const first = boot.panes > 1 ? await grid(boot.panes) : await openTab()
  if (boot.bench) bench(boot, first)
  if (boot.ui_test) {
    const { run } = await import("./selftest.js")
    run(boot.ui_test, { invoke, log, first, focused: () => focused, split, isMac })
  }
}
start()

// A click on a notice card brings the window up on the pane that asked
// (card #288; the window itself is raised on the Rust side).
window.__TAURI__.event.listen("notice-focus", (e) => {
  const p = byId.get(e.payload)
  if (p) setFocus(p)
})
