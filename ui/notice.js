// The notice panel (card #288; app/src/notices.rs holds the notices and
// sizes and places this window). Left click: focus the pane that asked.
// Right click: dismiss. Hovering holds every card.
const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

const stack = document.getElementById("stack")
const sounds = {}
for (const name of ["success", "fail", "published"]) {
  const a = new Audio(`sounds/${name}.wav`)
  a.preload = "auto"
  sounds[name] = a
}

const COLS = ["left", "center", "right"]
const ROWS = ["bottom", "middle", "top"]
let shown = new Map() // key -> element

function render(state) {
  document.body.className = `${COLS[state.anchor % 3]} ${ROWS[Math.floor(state.anchor / 3)]}`
  const keep = new Set(state.cards.map((c) => c.key))
  for (const [key, el] of shown) {
    if (!keep.has(key)) {
      el.remove()
      shown.delete(key)
    }
  }
  // Oldest at the stack's base: append in order (the stack's direction
  // puts the base at the anchored edge).
  for (const c of state.cards) {
    let el = shown.get(c.key)
    if (!el) {
      el = document.createElement("div")
      el.innerHTML = `<div class="t"></div><div class="b"></div>`
      el.addEventListener("click", () => invoke("notice_click", { key: c.key }))
      el.addEventListener("contextmenu", (e) => {
        e.preventDefault()
        invoke("notice_dismiss", { key: c.key })
      })
      shown.set(c.key, el)
    }
    el.className = `card ${c.tone}`
    el.querySelector(".t").textContent = c.title
    el.querySelector(".b").textContent = c.body
    stack.append(el)
  }
  if (state.sound && sounds[state.sound]) {
    const a = sounds[state.sound]
    a.currentTime = 0
    a.play().catch(() => {})
  }
}

listen("notices", (e) => render(e.payload))
document.addEventListener("mouseenter", () => invoke("notice_hover", { hover: true }))
document.addEventListener("mouseleave", () => invoke("notice_hover", { hover: false }))
