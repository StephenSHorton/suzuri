// rust-suzuri UI: split layout, focus, input and painting. The terminal
// state lives in Rust; this only lays out panes and paints their frames.
import { Screen, cellMetrics, M_ALT_SCREEN, M_ALT_SCROLL, M_APP_CURSOR, M_BRACKETED_PASTE, M_FOCUS } from "./frame.js"
import { encodeKey } from "./keys.js"
import { clipboardImage, saveClipboardImage } from "./paste_image.js"
import { extraShortcut } from "./extras.js"
import { isOpen as panelOpen } from "./panel.js"
import "./aicontrol.js"
import { InputBar, PROMPT, barKey, displayPath } from "./bar.js"
import { shortcut, chordLabel } from "./shortcuts.js"
import { Palette } from "./palette.js"
import { Settings, announce } from "./settings.js"

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
  constructor(tab) {
    this.tab = tab
    this.titleOverride = "" // Rename pane (or AI control's rename); wins over the program's title
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
    // The input bar (#285): the shell's command line, under the screen.
    this.barEl = document.createElement("div")
    this.barEl.className = "inbar"
    this.barEl.hidden = true
    this.barEl.innerHTML = `<div class="cwd"></div><div class="lines"></div><div class="caret"></div>`
    this.el.append(this.head, this.body, this.barEl)
    this.bar = new InputBar()
    // The shell as the pane reports it; see ShellStatus in crates/term.
    this.shell = { integrated: false, cwd: "", program: false, prompts: 0 }
    // Set on Enter until the next prompt: the command owns the keys from
    // the moment it is sent, before the shell's start mark arrives.
    this.awaiting = -1
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
    this.head.addEventListener("dblclick", () => renamePane(this))
    this.body.addEventListener("wheel", (e) => this.onWheel(e), { passive: true })
    this.ro = new ResizeObserver(() => this.fit())
    this.ro.observe(this.body)
  }

  // from: the pane this one splits from; the new shell starts in its cwd.
  // profile: a Settings profile's shell and folder (new tabs).
  async open(cols, rows, from = null, profile = null) {
    const ch = new Channel()
    ch.onmessage = (m) => this.onMessage(m)
    this.size = [cols, rows]
    this.id = await invoke("pane_open", { cols, rows, command: null, from, profile, onFrame: ch })
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
      this.showTitle()
    } else if (msg.t === "exit") {
      this.exited = true
      this.head.insertAdjacentHTML("beforeend", `<span class="exited">exited${msg.code != null ? " " + msg.code : ""}</span>`)
      setTimeout(() => closePane(this), 600)
    } else if (msg.t === "clip") {
      navigator.clipboard?.writeText(msg.text).catch(() => {})
    } else if (msg.t === "shell") {
      this.shell = msg
      if (this.awaiting >= 0 && msg.prompts > this.awaiting) this.awaiting = -1
      this.renderBar()
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
        return d
      }),
    )
    const caret = this.barEl.querySelector(".caret")
    caret.style.left = `${BAR_PAD_X + ([...PROMPT].length + v.col) * cw}px`
    caret.style.top = `${lines.offsetTop + v.row * ch}px`
    caret.style.width = `${cw}px`
    caret.style.height = `${ch}px`
    caret.classList.toggle("dim", focused !== this)
  }

  // A key while the bar has the keys. True when it was the bar's.
  barKeydown(e) {
    const r = barKey(this.bar, e, { isMac, cols: this.barCols() })
    if (r === null) {
      // Plain typing: the key's character (Option's composed one on macOS).
      const ctl = e.ctrlKey || (isMac && e.metaKey)
      if (ctl || [...e.key].length !== 1) return false
      this.bar.insert(e.key)
    } else if (r.kind === "submit") {
      this.submit(r.line)
    } else if (r.kind === "pty") {
      this.send(r.data)
    } else if (r.kind === "scroll") {
      invoke("pane_scroll", { id: this.id, delta: Math.round(r.pages * this.size[1]) })
    }
    this.renderBar()
    return true
  }

  // Enter in the bar: the line and CR in one write. A non-empty line hands
  // the keys to the command until the shell's next prompt.
  submit(line) {
    if (line.trim() === "") return this.send("\r")
    this.awaiting = this.shell.prompts
    this.send(line + "\r")
  }

  label() { return this.titleOverride || this.title }
  showTitle() {
    this.head.querySelector(".t").textContent = this.label()
    if (this.tab.focused === this) renderStrip()
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
    const altFlip = (this.modes ^ h.modes) & M_ALT_SCREEN
    this.modes = h.modes
    this.lastSeq = h.seq
    if (altFlip) this.renderBar()
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
    if (!this.barEl.hidden) this.renderBar()
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

// Horizontal padding inside the input bar (matches .inbar in style.css).
const BAR_PAD_X = 8

// ---- frames: everything queued is painted in one animation frame.
let frameScheduled = false
function scheduleFrame() {
  if (frameScheduled) return
  frameScheduled = true
  requestAnimationFrame(() => {
    frameScheduled = false
    for (const p of everyPane()) {
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
  const t = { id: nextTabId++, name: "", tree: null, focused: null, zoomed: null, el }
  tabs.splice(at, 0, t)
  return t
}

// The tab's name on the strip: its custom name, else its focused pane's.
function tabLabel(t) {
  return t.name || t.focused?.label() || "shell"
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
    a.style.flex = `${span(node.a)} 1 0`
    b.style.flex = `${span(node.b)} 1 0`
    d.append(a, b)
    return d
  }
  // Maximized: only that pane shows; the others stay alive, detached.
  const top = tab.zoomed ? tab.zoomed.el : tab.tree ? build(tab.tree) : ""
  if (tab.zoomed) tab.zoomed.el.style.flex = "1 1 0"
  tab.el.replaceChildren(top)
  tab.el.classList.toggle("zoomed", !!tab.zoomed)
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
    return true
  }
  const sibling = parent.a === leaf ? parent.b : parent.a
  for (const k of ["pane", "dir", "a", "b"]) delete parent[k]
  Object.assign(parent, sibling)
  if (tab.focused === pane) tab.focused = allPanes(parent)[0]
  return true
}

async function split(dir, pane = focused, opts = {}) {
  const tab = opts.tab ?? pane?.tab ?? activeTab
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

function closePane(pane) {
  if (!pane) return
  if (!pane.exited && pane.id) invoke("pane_close", { id: pane.id })
  pane.exited = true
  pane.ro.disconnect()
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
}

// ---- tabs
async function openTab(profile) {
  const t = newTab(activeTab ? tabs.indexOf(activeTab) + 1 : tabs.length)
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

function closeTab(t = activeTab) {
  for (const p of allPanes(t.tree)) {
    if (!p.exited && p.id) invoke("pane_close", { id: p.id })
    p.exited = true
    p.ro.disconnect()
  }
  t.tree = null
  if (tabs.length === 1) {
    render(t)
    split("h", null, { tab: t })
    return
  }
  dropTab(t)
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
    input.focus()
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
    if (v != null) t.name = v === t.focused?.label() ? t.name : v
    renderStrip()
  })
}

function renamePane(p = focused) {
  if (!p) return
  const el = p.head.querySelector(".t")
  editInline(el, p.label(), (v) => {
    if (v != null) p.titleOverride = v === p.title ? "" : v
    el.textContent = p.label()
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
  const label = activeTab ? tabLabel(activeTab) : "suzuri"
  document.title = label
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
  input.focus()
  if (prev && prev !== p && prev.id && !prev.exited) {
    invoke("pane_focus", { id: prev.id, focused: false })
    if (prev.modes & M_FOCUS) invoke("pane_write", { id: prev.id, data: "\x1b[O" })
  }
  if (p.id && (prev !== p || force)) {
    invoke("pane_focus", { id: p.id, focused: true })
    if (prev !== p && p.modes & M_FOCUS) invoke("pane_write", { id: p.id, data: "\x1b[I" })
  }
}

function moveFocus(dx, dy) {
  if (!focused || activeTab.zoomed) return
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
const palette = new Palette(overlays, () => input.focus())
const settings = new Settings(overlays, () => input.focus())
const overlayOpen = () => palette.isOpen || settings.isOpen

// Other parts add palette entries here: {id, title, category, hint?, run()}.
const extraCommands = []
window.rsz = { addCommands: (cmds) => extraCommands.push(...cmds) }

const ACTIONS = {
  new_tab: () => openTab(),
  close_tab: () => closeTab(),
  next_tab: () => switchTab(1),
  prev_tab: () => switchTab(-1),
  select_tab: (i) => tabs[i] && showTab(tabs[i]),
  rename_tab: () => renameTab(),
  split_right: () => split("h"),
  split_down: () => split("v"),
  close_pane: () => closePane(focused),
  rename_pane: () => renamePane(),
  zoom_pane: () => togglePaneZoom(),
  focus_left: () => moveFocus(-1, 0),
  focus_right: () => moveFocus(1, 0),
  focus_up: () => moveFocus(0, -1),
  focus_down: () => moveFocus(0, 1),
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
}

function run(id, arg) {
  if (palette.isOpen) palette.close()
  ACTIONS[id]?.(arg)
}

// Go suzuri's DefaultCommands + TabMoveCommands, for what rust-suzuri has.
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

// ---- keys
input.addEventListener("keydown", (e) => {
  if (window.rszTrace) log("key", e.key, e.code, e.metaKey, e.ctrlKey, e.altKey, e.isComposing)
  if (e.isComposing) return
  const sc = shortcut(e, isMac)
  if (sc) { e.preventDefault(); run(sc.id, sc.arg); return }
  if (extraShortcut(e, isMac)) { e.preventDefault(); return }
  if (!focused) return
  if (focused.barActive()) {
    if (focused.barKeydown(e)) e.preventDefault()
    return
  }
  const data = encodeKey(e, focused.modes, isMac)
  if (data == null) return
  e.preventDefault()
  focused.send(data, e.timeStamp || performance.now())
})
// Text that arrives without a usable keydown (IME commit, dead keys).
input.addEventListener("input", (e) => {
  const v = input.value
  input.value = ""
  if (!v || !focused || e.isComposing) return
  if (focused.barActive()) focused.bar.insert(v), focused.renderBar()
  else focused.send(v)
})
input.addEventListener("compositionend", () => {
  const v = input.value
  input.value = ""
  if (!v || !focused) return
  if (focused.barActive()) focused.bar.insert(v), focused.renderBar()
  else focused.send(v)
})
function pasteText(pane, text) {
  // At the prompt, a paste goes into the input bar (newlines kept).
  if (pane.barActive()) return pane.bar.insert(text.replace(/\r\n?/g, "\n")), pane.renderBar()
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
// Clicks give the keyboard back to the terminal, except inside an overlay,
// a panel (#286) or a rename box.
document.addEventListener("mousedown", (e) => {
  if (overlayOpen() || e.target.closest?.("input.rename")) return
  setTimeout(() => panelOpen() || input.focus(), 0)
})
window.addEventListener("focus", () => { if (!overlayOpen() && !panelOpen()) input.focus() })
window.addEventListener("rsz-panel-closed", () => input.focus())

// For #286's AI control (ui/aicontrol.js): the active tab's tree, every
// pane in every tab, and the commands.
window.rszHost = {
  tree: () => activeTab?.tree,
  focused: () => focused,
  allPanes: () => everyPane(),
  split, closePane, setFocus,
  tabs: () => tabs, activeTab: () => activeTab, openTab, showTab, movePaneToTab,
}

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
  home = boot.home
  if (boot.ws) connectFrames(boot.ws)
  settingsCfg = await invoke("config_get").catch(() => null)
  if (settingsCfg) announce(settingsCfg)
  const first = boot.panes > 1 ? await grid(boot.panes) : await openTab()
  if (boot.bench) bench(boot, first)
}
start()
