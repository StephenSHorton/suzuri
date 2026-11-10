// Caffeine (#294): keep the machine awake, as Go suzuri's ☕ strip does.
// The hold lives in Rust (app/src/caffeine.rs) and is on from launch; this
// adds the palette entries and the ☕ button at the right of the bar (dim when
// off, "☕ ∞" / "☕ 12m" when on; a click toggles).
import { caffeineToast } from "./caffeine_chip.js"
import { chrome } from "./chrome/store.js"
import { hostToast } from "./host_toast.js"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

// The cup button is the React title bar's (chrome/TitleBar.jsx); this owns
// the status it shows and the toasts.

let tick = 0
let last = null
// action and prev: what the person asked for, and the status before it
// (Go toasts every change).
function show(s, action = null, prev = last, minutes) {
  hostToast(caffeineToast(action, prev, s, minutes))
  // When a timed hold is due to end: the label refresh slows down in a
  // background window, so remaining_s alone can be stale.
  last = s?.active && s.remaining_s != null ? { ...s, ends: Date.now() + s.remaining_s * 1000 } : s
  clearTimeout(tick)
  chrome.set({ caffeine: s })
  // Keep a timed label current (Go repaints the strip every second).
  if (s?.active && s.remaining_s != null) tick = setTimeout(() => invoke("caffeine_status").then((s) => show(s)), 1000)
}
show(null)
// Run a caffeine command and toast what it did.
function act(cmd, action, minutes) {
  const prev = last
  return invoke(cmd, minutes ? { minutes } : {}).then((s) => show(s, action, prev, minutes))
}
chrome.actions.toggleCaffeine = () => act("caffeine_toggle", "toggle")
listen("caffeine", (e) => show(e.payload))
invoke("caffeine_status").then((s) => show(s))

window.rsz?.addCommands([
  { id: "caffeine_toggle", title: "Toggle caffeine", category: "System", hint: "☕ prevent sleep", run: () => act("caffeine_toggle", "toggle") },
  { id: "caffeine_15", title: "Caffeine 15 minutes", category: "System", hint: "Stay awake 15m", run: () => act("caffeine_on", "on", 15) },
  { id: "caffeine_1h", title: "Caffeine 1 hour", category: "System", hint: "Stay awake 1h", run: () => act("caffeine_on", "on", 60) },
  { id: "caffeine_off", title: "Caffeine off", category: "System", hint: "Allow sleep", run: () => act("caffeine_off", "off") },
])
