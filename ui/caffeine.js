// Caffeine (#294): keep the machine awake, as Go suzuri's ☕ strip does.
// The hold lives in Rust (app/src/caffeine.rs); this adds the palette
// entries and the ☕ label in the bar ("∞", "12m"; click turns it off).
const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

const el = document.createElement("button")
el.id = "caffeine"
el.hidden = true
el.title = "Caffeine: the computer stays awake (click to turn off)"
const bar = document.getElementById("bar")
bar.insertBefore(el, document.getElementById("split-right") || null)

let tick = 0
function show(s) {
  clearTimeout(tick)
  el.hidden = !s.active
  el.textContent = s.active ? `☕ ${s.label}` : ""
  if (s.error) window.rsz?.toast?.(`Caffeine: ${s.error}`)
  // Keep a timed label current (Go repaints the strip every second).
  if (s.active && s.remaining_s != null) tick = setTimeout(() => invoke("caffeine_status").then(show), 1000)
}
el.addEventListener("click", () => invoke("caffeine_off").then(show))
listen("caffeine", (e) => show(e.payload))
invoke("caffeine_status").then(show)

window.rsz?.addCommands([
  { id: "caffeine_toggle", title: "Toggle caffeine", category: "System", hint: "☕ prevent sleep", run: () => invoke("caffeine_toggle").then(show) },
  { id: "caffeine_15", title: "Caffeine 15 minutes", category: "System", hint: "Stay awake 15m", run: () => invoke("caffeine_on", { minutes: 15 }).then(show) },
  { id: "caffeine_1h", title: "Caffeine 1 hour", category: "System", hint: "Stay awake 1h", run: () => invoke("caffeine_on", { minutes: 60 }).then(show) },
  { id: "caffeine_off", title: "Caffeine off", category: "System", hint: "Allow sleep", run: () => invoke("caffeine_off").then(show) },
])
