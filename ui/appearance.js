// Appearance: Go suzuri's 16 themes, ANSI map and font settings. The
// catalogue lives in Rust (palette.rs) and the choice in the settings file
// (config.rs); Rust recolours the panes itself. This paints the chrome from
// the theme's roles and sets the terminal font whenever the settings change
// (the `rsz:config` event from settings.js), and runs font zoom.
import { setFont, setSelection, setVoid } from "./frame.js"
import { announce } from "./settings.js"

const { invoke } = window.__TAURI__.core

let cat = null // { themes, default_font_face, default_font_size_px }
let cur = { theme: "", face: "", px: 0 }
let onFont = () => {}

// Go's face names → CSS families. The default face is the bundled Gohu.
function family(face) {
  const q = (s) => `"${s.replace(/"/g, "")}"`
  const head = face === cat.default_font_face ? q("Gohu")
    : face === "SF Mono" ? `${q("SF Mono")}, ui-monospace`
    : q(face)
  return `${head}, "Gohu", ui-monospace, Menlo, Consolas, monospace`
}

function paintChrome(id) {
  const t = cat.themes.find((x) => x.id === id) || cat.themes[0]
  const root = document.documentElement.style
  for (const [k, v] of Object.entries(t.chrome)) root.setProperty(`--${k}`, v)
  setVoid(t.chrome["term-bg"])
  const h = t.chrome.primary
  setSelection(`rgba(${parseInt(h.slice(1, 3), 16)}, ${parseInt(h.slice(3, 5), 16)}, ${parseInt(h.slice(5, 7), 16)}, 0.38)`)
  document.documentElement.dataset.theme = t.id
}

async function apply(cfg) {
  if (!cat || !cfg) return
  if (cfg.theme !== cur.theme) {
    cur.theme = cfg.theme
    paintChrome(cfg.theme)
  }
  if (cfg.font_face !== cur.face || cfg.font_size_px !== cur.px) {
    const first = !cur.face
    cur.face = cfg.font_face
    cur.px = cfg.font_size_px
    const fam = family(cur.face)
    await document.fonts.load(`${cur.px}px ${fam}`).catch(() => {})
    setFont(fam, cur.px, cur.face === cat.default_font_face, cur.face)
    if (!first) onFont()
  }
}

export async function loadAppearance(relayout) {
  onFont = relayout
  cat = await invoke("appearance")
  await apply(await invoke("config_get"))
  window.addEventListener("rsz:config", (e) => apply(e.detail))
}

// Zoom (Go: Ctrl/⌘ + - 0); the settings file clamps the size.
export async function zoom(delta) {
  const px = delta === 0 ? cat.default_font_size_px : cur.px + delta
  const cfg = await invoke("config_set", { patch: { font_size_px: px } })
  announce(cfg)
}
