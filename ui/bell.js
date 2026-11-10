// Notification bell: Go suzuri's 🔔 chip just left of the ☕ cup, and its
// session history (notice_history.go). Rust keeps the last 40 notices and
// the unread count (app/src/notices.rs); this shows the count in the bar,
// lists the history under the bell (also "Notifications" in the palette),
// and a click on an entry focuses the pane that posted it. Opening the
// list marks everything read, as in Go.
//
// The view is chrome/Bell.jsx (an Astryx Popover anchored on the title bar's
// bell button), mounted through chrome/overlays.js; this keeps the state.
import { createElement as h } from "react"
import { chrome } from "./chrome/store.js"
import { mountOverlay } from "./chrome/overlays.js"
import { BellPopover } from "./chrome/Bell.jsx"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

function setUnread(n) {
  chrome.set({ unread: Math.max(0, Number(n) || 0) })
}
setUnread(0)

let shown = false
let items = []
let index = 0

const isOpen = () => shown

// The popover stays mounted (it owns the bell button's click), open or not.
function render() {
  mountOverlay(
    "bell",
    h(BellPopover, {
      isOpen: shown,
      items,
      index,
      onOpenChange: (next) => (next ? open() : close()),
      onIndex: (i) => {
        index = i
        render()
      },
      onChoose: choose,
      onClose: () => close(),
    }),
  )
}

async function open() {
  if (!shown) {
    index = 0
    shown = true
    chrome.set({ bellOpen: true })
    render()
  }
  await refresh()
}

async function refresh() {
  const res = await invoke("notice_history").catch(() => ({ items: [], unread: 0 }))
  items = res.items || []
  if (index >= items.length) index = Math.max(0, items.length - 1)
  render()
  if (res.unread) invoke("notice_read")
}

// Runs after the popover's own focus restore, so what app.js does with the
// keyboard (back on the focused pane, or on the chosen notice's pane) wins.
const later = (f) => setTimeout(f, 0)

function close(refocus = true) {
  if (!shown) return
  shown = false
  chrome.set({ bellOpen: false })
  render()
  // app.js puts the keyboard back on the focused pane.
  if (refocus) later(() => window.dispatchEvent(new Event("rsz-panel-closed")))
}

// Go ActionFocusNoticePane: reveal the pane that posted it, if it's still open.
function choose(it) {
  if (!it) return
  close(false)
  later(() => {
    const revealed = it.pane != null && window.rsz?.focusPane?.(it.pane)
    if (!revealed) window.dispatchEvent(new Event("rsz-panel-closed"))
  })
}

// app.js refocuses the pane when the window comes back, so close on the way out.
window.addEventListener("blur", () => close(false))

chrome.actions.toggleBell = () => (isOpen() ? close() : open())

// The count; refresh an open list as notices arrive (that marks them read).
listen("notice-bell", (e) => {
  setUnread(e.payload)
  if (isOpen() && e.payload > 0) refresh()
})
invoke("notice_history")
  .then((res) => setUnread(res.unread))
  .catch(() => setUnread(0))

render()

window.rsz?.addCommands([
  { id: "notifications", title: "Notifications", category: "System", hint: "Session history · 🔔", run: () => open() },
])
