// Notification bell: Go suzuri's 🔔 chip just left of the ☕ cup, and its
// session history (notice_history.go). Rust keeps the last 40 notices and
// the unread count (app/src/notices.rs); this shows the count in the bar,
// lists the history under the bell (also "Notifications" in the palette),
// and a click on an entry focuses the pane that posted it. Opening the
// list marks everything read, as in Go.
import { bellLabel, noticeTime, noticeText, moveIndex } from "./notice_list.js"
import { icon } from "./icons.js"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

const el = document.createElement("button")
el.id = "bell"
el.title = "Notifications"
const bar = document.getElementById("bar")
// In the bar's chip slot, before the cup if caffeine.js got here first
// (else caffeine.js appends the cup after us).
const chips = document.getElementById("chips") || bar
chips.insertBefore(el, document.getElementById("caffeine"))

function setUnread(n) {
  const count = bellLabel(n)
  el.innerHTML = icon(n > 0 ? "bellDot" : "bell") + (count ? `<span class="count">${count}</span>` : "")
  el.classList.toggle("unread", n > 0)
  el.title = n > 0 ? `Notifications (${n} unread)` : "Notifications"
}
setUnread(0)

// The popover.
const pop = document.createElement("div")
pop.className = "bell-pop"
pop.tabIndex = -1
pop.hidden = true
pop.innerHTML = `<div class="head">Notifications</div><div class="list" role="listbox"></div>`
const list = pop.querySelector(".list")
;(document.getElementById("overlays") || document.body).appendChild(pop)
let items = []
let index = 0

const isOpen = () => !pop.hidden

async function open() {
  if (!isOpen()) {
    index = 0
    pop.hidden = false
    place()
    pop.focus({ preventScroll: true })
  }
  await refresh()
}

async function refresh() {
  const h = await invoke("notice_history").catch(() => ({ items: [], unread: 0 }))
  items = h.items || []
  if (index >= items.length) index = Math.max(0, items.length - 1)
  paint()
  if (h.unread) invoke("notice_read")
}

function close(refocus = true) {
  if (!isOpen()) return
  pop.hidden = true
  // app.js puts the keyboard back on the focused pane.
  if (refocus) window.dispatchEvent(new Event("rsz-panel-closed"))
}

function place() {
  const r = el.getBoundingClientRect()
  const w = pop.offsetWidth
  pop.style.top = `${Math.round(r.bottom + 4)}px`
  pop.style.left = `${Math.round(Math.max(12, Math.min(r.right - w, window.innerWidth - w - 12)))}px`
}

function paint() {
  if (!items.length) {
    const d = document.createElement("div")
    d.className = "empty"
    d.textContent = "No notifications this session"
    list.replaceChildren(d)
    return
  }
  const frag = document.createDocumentFragment()
  items.forEach((it, i) => {
    const row = document.createElement("div")
    row.className = "row" + (i === index ? " sel" : "") + (it.pane == null ? " gone" : "")
    row.setAttribute("role", "option")
    row.innerHTML = `<span class="when"></span><span class="text"></span>`
    row.querySelector(".when").textContent = noticeTime(it.at)
    row.querySelector(".text").textContent = noticeText(it)
    row.title = noticeText(it)
    row.addEventListener("mousemove", () => { if (index !== i) { index = i; paint() } })
    row.addEventListener("mousedown", (e) => { e.preventDefault(); choose(it) })
    frag.appendChild(row)
  })
  list.replaceChildren(frag)
  list.querySelector(".sel")?.scrollIntoView({ block: "nearest" })
}

// Go ActionFocusNoticePane: reveal the pane that posted it, if it's still open.
function choose(it) {
  if (!it) return
  close(false)
  const shown = it.pane != null && window.rsz?.focusPane?.(it.pane)
  if (!shown) window.dispatchEvent(new Event("rsz-panel-closed"))
}

pop.addEventListener("keydown", (e) => {
  e.stopPropagation()
  if (e.isComposing) return
  if (e.key === "Escape") { e.preventDefault(); return close() }
  if (e.key === "Enter") { e.preventDefault(); return choose(items[index]) }
  const next = moveIndex(index, e.key, items.length)
  if (next !== index || ["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key)) {
    e.preventDefault()
    index = next
    paint()
  }
})
// A click anywhere else closes it (the bell itself toggles).
document.addEventListener("mousedown", (e) => {
  if (isOpen() && !pop.contains(e.target) && !el.contains(e.target)) close()
}, true)
window.addEventListener("resize", () => { if (isOpen()) place() })
// The terminal's hidden textarea steals focus on any mousedown; keep it here.
pop.addEventListener("mousedown", (e) => e.stopPropagation())
// app.js refocuses the pane when the window comes back, so close on the way out.
window.addEventListener("blur", () => close(false))

el.addEventListener("mousedown", (e) => e.preventDefault())
el.addEventListener("click", () => (isOpen() ? close() : open()))

// The count; refresh an open list as notices arrive (that marks them read).
listen("notice-bell", (e) => {
  setUnread(e.payload)
  if (isOpen() && e.payload > 0) refresh()
})
invoke("notice_history").then((h) => setUnread(h.unread)).catch(() => setUnread(0))

window.rsz?.addCommands([
  { id: "notifications", title: "Notifications", category: "System", hint: "Session history · 🔔", run: () => open() },
])
