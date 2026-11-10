// The notice panel (card #288; app/src/notices.rs holds the notices and
// sizes and places this window). Its own page, so its own React root: the
// Astryx stylesheets in main.jsx's cascade order, the Neutral theme in dark
// mode, and the cards (chrome/Notices.jsx). Left click: focus the pane that
// asked. Right click or the close button: dismiss. Hovering holds every card.
import "@astryxdesign/core/reset.css"
import "@astryxdesign/core/astryx.css"
import "@astryxdesign/theme-neutral/theme.css"
import "./chrome/notices.css"

import { createElement as h } from "react"
import { createRoot } from "react-dom/client"
import { Theme } from "@astryxdesign/core/theme"
import { neutralTheme } from "@astryxdesign/theme-neutral/built"
import { Notices } from "./chrome/Notices.jsx"
// Imported so Vite copies the files into dist (a bare "sounds/x.wav" string
// never reached the build).
import successWav from "./sounds/success.wav"
import failWav from "./sounds/fail.wav"
import publishedWav from "./sounds/published.wav"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

const sounds = {}
for (const [name, url] of [["success", successWav], ["fail", failWav], ["published", publishedWav]]) {
  const a = new Audio(url)
  a.preload = "auto"
  sounds[name] = a
}

const COLS = ["left", "center", "right"]
const ROWS = ["bottom", "middle", "top"]

const actions = {
  click: (key) => invoke("notice_click", { key }),
  dismiss: (key) => invoke("notice_dismiss", { key }),
}

const root = createRoot(document.getElementById("root"))

// notices.rs sends the whole panel each time: the cards (oldest first), the
// anchor (0..8: column + 3 * row) and a sound to play now. Its `colors` are
// the terminal theme's; the cards use Astryx tokens instead.
function render(state) {
  document.body.className = `${COLS[state.anchor % 3]} ${ROWS[Math.floor(state.anchor / 3)]}`
  root.render(
    h(Theme, { theme: neutralTheme, mode: "dark" }, h(Notices, { cards: state.cards, ...actions })),
  )
  if (state.sound && sounds[state.sound]) {
    const a = sounds[state.sound]
    a.currentTime = 0
    a.play().catch(() => {})
  }
}

listen("notices", (e) => render(e.payload)).then(() => invoke("notice_sync"))
document.addEventListener("mouseenter", () => invoke("notice_hover", { hover: true }))
document.addEventListener("mouseleave", () => invoke("notice_hover", { hover: false }))
