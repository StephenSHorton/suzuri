// Activity marks (Go suzuri's strip spinner, pane title marks and idle
// ring): a spinner while a pane works, "?" while it waits on you, a hollow
// ring on resting panes in a split tab, which also sink back. And a
// "command finished" notice for a long command in a pane you aren't
// looking at. The rules are in activity_state.js; this feeds it and paints.
// app.js calls window.rszActivity.onMessage / onInput; aicontrol.js calls
// report.
import { chrome } from "./chrome/store.js"
import { newActivity, step, activity, rollUp, SPIN } from "./activity_state.js"
import { M_ALT_SCREEN } from "./frame.js"

const { invoke } = window.__TAURI__.core

const host = () => window.rszHost
const states = new WeakMap()
const stateOf = (p) => {
  let s = states.get(p)
  if (!s) states.set(p, (s = newActivity(performance.now())))
  return s
}

// You can see this pane: the window has focus and it's the focused pane.
const looking = (p) => document.hasFocus() && !document.hidden && host()?.focused() === p
const ctx = (p) => ({ looking: looking(p), alt: !!(p.modes & M_ALT_SCREEN) })

function apply(p, ev) {
  const note = step(stateOf(p), ev, performance.now(), ctx(p))
  if (note && p.id) invoke("activity_notice", { pane: p.id, ...note }).catch(() => {})
  schedule()
}

const KINDS = new Set(["title", "shell", "done", "progress", "bell", "notice", "exit"])

window.rszActivity = {
  onMessage(p, msg) {
    if (!KINDS.has(msg.t)) return
    if (msg.t === "shell") apply(p, { t: "shell", integrated: msg.integrated, program: msg.program, prompts: msg.prompts })
    else apply(p, msg)
  },
  onInput(p) {
    // Per key, so no repaint unless it clears a "?".
    const s = stateOf(p)
    const had = s.attention
    step(s, { t: "input" }, performance.now(), ctx(p))
    if (had) schedule()
  },
  report(p, state) { apply(p, { t: "agent", state }) },
  // For AI control's layout and tests.
  of(p) { return activity(stateOf(p), performance.now(), ctx(p)) },
}

// Output times from the core: ms since each pane's PTY last printed. Only
// output since the last poll is new.
let polledAt = 0
async function poll() {
  const h = host()
  if (!h) return
  let quiet
  try { quiet = await invoke("pane_activity") } catch { return }
  const now = performance.now()
  const since = now - polledAt
  polledAt = now
  for (const p of h.allPanes()) {
    const ms = quiet?.[p.id]
    if (ms == null || ms > since) continue
    step(stateOf(p), { t: "output", at: now - ms }, now, ctx(p))
  }
}

let lastSeen = null
function paint() {
  const h = host()
  if (!h) return false
  const now = performance.now()
  const f = h.focused()
  const seen = document.hasFocus() && !document.hidden ? f : null
  if (seen && seen !== lastSeen) step(stateOf(seen), { t: "seen" }, now)
  lastSeen = seen
  let spinning = false
  const byTab = new Map()
  for (const p of h.allPanes()) {
    const a = activity(stateOf(p), now, ctx(p))
    if (a === "busy") spinning = true
    if (!byTab.has(p.tab)) byTab.set(p.tab, [])
    byTab.get(p.tab).push(a)
    paintPane(p, a)
  }
  for (const [t, list] of byTab) {
    for (const p of h.allPanes().filter((q) => q.tab === t)) p.el.classList.toggle("multi", list.length > 1)
  }
  paintStrip(byTab)
  return spinning
}

function paintPane(p, a) {
  if (p.el.dataset.activity !== a) p.el.dataset.activity = a
  const head = p.head
  if (!head) return
  let mark = head.querySelector(".act")
  if (!mark) {
    mark = document.createElement("span")
    mark.className = "act"
    head.querySelector(".dot")?.after(mark)
    const ring = document.createElement("span")
    ring.className = "ring"
    ring.title = "Idle"
    head.append(ring)
  }
  setMark(mark, a)
}

function setMark(el, a) {
  el.dataset.a = a === "busy" || a === "blocked" ? a : ""
  const text = a === "busy" ? SPIN[frame] : a === "blocked" ? "?" : a === "dead" ? "⠁" : ""
  if (el.textContent !== text) el.textContent = text
  el.title = a === "blocked" ? "Waiting on you" : a === "busy" ? "Working" : ""
}

// Each tab's mark on the strip, through the React title bar's store
// (chrome/TitleBar.jsx draws it in place of the tab's icon).
function tabMark(a) {
  if (a === "busy") return { kind: "busy", text: SPIN[frame], title: "Working" }
  if (a === "blocked") return { kind: "blocked", text: "?", title: "Waiting on you" }
  if (a === "dead") return { kind: "dead", text: "⠁", title: "" }
  return null
}
function paintStrip(byTab) {
  const h = host()
  const marks = new Map()
  for (const t of h.tabs()) {
    const list = byTab.get(t) || []
    // The shown split tab's pane heads already carry the marks.
    marks.set(t.id, tabMark(t === h.activeTab() && list.length > 1 ? "idle" : rollUp(list)))
  }
  const same = (x, y) => x?.kind === y?.kind && x?.text === y?.text
  chrome.set((s) => {
    if (s.tabs.every((x) => same(x.activity, marks.get(x.id) ?? null))) return {}
    return { tabs: s.tabs.map((x) => ({ ...x, activity: marks.get(x.id) ?? null })) }
  })
}

// One timer: the spinner at ~80 ms a frame while anything works, else a
// slow tick for the time-based edges (output going quiet, stale titles).
// An event repaints at once.
const SPIN_MS = 80
// Settings' animate_unfocused (Go's "Bg anim", default on): off, the
// spinner rests while another app is in front.
let animateUnfocused = true
const animating = () => !document.hidden && (animateUnfocused || document.hasFocus())
let frame = 0
let frameAt = 0
let timer = 0
let running = false
async function tick() {
  timer = 0
  if (running) return
  running = true
  const now = performance.now()
  if (now - polledAt >= 500) await poll()
  const spinning = paint()
  const spin = spinning && animating()
  if (spin && now - frameAt >= SPIN_MS) { frame = (frame + 1) % SPIN.length; frameAt = now }
  running = false
  if (!timer) timer = setTimeout(tick, spin ? SPIN_MS : 500)
}
function schedule() {
  clearTimeout(timer)
  timer = setTimeout(tick, 0)
}
schedule()

// renderStrip rebuilds the tabs; put the marks back right away.
if (strip) new MutationObserver(() => paint()).observe(strip, { childList: true })
window.addEventListener("focus", schedule)
window.addEventListener("blur", schedule)
const animSetting = (cfg) => { animateUnfocused = cfg?.animate_unfocused !== false }
invoke("config_get").then(animSetting).catch(() => {})
window.addEventListener("rsz:config", (e) => { animSetting(e.detail); schedule() })
document.addEventListener("visibilitychange", schedule)
