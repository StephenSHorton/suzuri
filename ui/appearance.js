// Appearance: Go suzuri's 16 themes, ANSI map and font settings. The
// catalogue lives in Rust (palette.rs) and the choice in the settings file
// (config.rs); Rust recolours the panes' cells itself. The chrome around the
// panes is Astryx (theme Neutral, dark) and never follows the terminal
// theme: this sets only what the terminal grid takes from the theme (its
// background, foreground, cursor and selection) and the terminal font,
// whenever the settings change (the `rsz:config` event from settings.js),
// and runs font zoom.
import { setFont, setSelection, setVoid } from "./frame.js"
import { announce } from "./settings.js"
import { fontToast, hostToast } from "./host_toast.js"
import { applyTextSize } from "./text_size.js"

const { invoke } = window.__TAURI__.core

let cat = null // { themes, default_font_face, default_font_size_px }
let cur = { theme: "", face: "", px: 0, text: "" }
let onFont = () => {}

// Go's face names → CSS families. The default face is the bundled Gohu.
function family(face) {
  const q = (s) => `"${s.replace(/"/g, "")}"`
  const head = face === cat.default_font_face ? q("Gohu")
    : face === "SF Mono" ? `${q("SF Mono")}, ui-monospace`
    : q(face)
  return `${head}, "Gohu", ui-monospace, Menlo, Consolas, monospace`
}

const rgba = (h, a) =>
  `rgba(${parseInt(h.slice(1, 3), 16)}, ${parseInt(h.slice(3, 5), 16)}, ${parseInt(h.slice(5, 7), 16)}, ${a})`

// The terminal's own colours, which style.css reads on the panes:
// --term-bg (the pane under the canvas), --term-fg (text drawn over the
// grid: the IME preedit, the link underline), --term-cursor (the cursor, in
// the theme's border role as Go draws it), and the selection the canvas
// paints. Nothing else on :root follows the terminal theme.
function paintTerminal(id) {
  const t = cat.themes.find((x) => x.id === id) || cat.themes[0]
  const c = t.chrome
  const root = document.documentElement.style
  root.setProperty("--term-bg", c["term-bg"])
  root.setProperty("--term-fg", c.text)
  root.setProperty("--term-cursor", c.border)
  setVoid(c["term-bg"])
  setSelection(rgba(c.primary, 0.38))
}

async function apply(cfg) {
  if (!cat || !cfg) return
  // The chrome's text size (text_size.js); the terminal's is font_size_px.
  if (cfg.ui_text_size !== cur.text) {
    cur.text = cfg.ui_text_size
    applyTextSize(cur.text)
  }
  if (cfg.theme !== cur.theme) {
    cur.theme = cfg.theme
    paintTerminal(cfg.theme)
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
  const before = cur.px
  const px = delta === 0 ? cat.default_font_size_px : cur.px + delta
  const cfg = await invoke("config_set", { patch: { font_size_px: px } })
  hostToast(fontToast(delta, before, cfg.font_size_px, cat.default_font_size_px))
  announce(cfg)
}
