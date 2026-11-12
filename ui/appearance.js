// Appearance: the Astryx theme and mode (Settings › Look, config ui_theme
// and ui_mode), the terminal font settings, and font zoom, applied whenever
// the settings change (the `rsz:config` event from settings.js).
//
// A theme change restyles everything at once: the React chrome (the root
// Theme in chrome/main.jsx, which puts the theme on <html> so the panes
// take its tokens too), then, from the tokens as they resolve on the page,
// the terminal's own colours (theme_palette.js) and the notice cards: the
// grid's background and selection here (frame.js, which the paint worker
// follows), the cells' ink and the cards in Rust (appearance_set). In mode
// System the OS's light/dark switch does the same.
import { flushSync } from "react-dom"
import { setFont, setSelection, setVoid } from "./frame.js"
import { announce } from "./settings.js"
import { fontToast, hostToast } from "./host_toast.js"
import { applyTextSize } from "./text_size.js"
import { chromeTheme, loadTheme, DEFAULT_THEME, DEFAULT_MODE, MODES } from "./chrome/themes.js"
import { TOKENS, RADIUS_TOKEN, deriveLook } from "./theme_palette.js"

const { invoke } = window.__TAURI__.core
const log = (line) => invoke("ui_log", { line: `appearance: ${line}` }).catch(() => {})

let cat = null // { default_font_face, default_font_size_px }
let cur = { theme: "", mode: "", face: "", px: 0, text: "" }
let onFont = () => {}

// Go's face names → CSS families. The default face is the bundled Gohu.
function family(face) {
  const q = (s) => `"${s.replace(/"/g, "")}"`
  const head = face === cat.default_font_face ? q("Gohu")
    : face === "SF Mono" ? `${q("SF Mono")}, ui-monospace`
    : q(face)
  return `${head}, "Gohu", ui-monospace, Menlo, Consolas, monospace`
}

// ---- the theme

// Bumped per theme change, so a slow load that lost the race (Settings'
// Cancel right behind a preview) doesn't apply.
let seq = 0
let lastLook = ""

async function applyTheme(id, mode) {
  const my = ++seq
  let theme
  try {
    theme = await loadTheme(id)
  } catch (e) {
    log(`theme ${id} failed to load: ${e}`)
    id = DEFAULT_THEME
    theme = await loadTheme(id)
  }
  if (my !== seq) return
  // flushSync runs the root Theme's layout effect, which moves <html>'s
  // data-astryx-theme / data-theme, before the tokens are read below.
  flushSync(() => chromeTheme.set({ id, theme, mode }))
  await repaint(my)
}

// A short wait between tries (a timer, not a frame: frames don't run while
// the window is hidden, and startup waits on this).
const nextFrame = () => new Promise((r) => setTimeout(r, 16))

// The theme's stylesheet is in and applies to <html>.
function themeApplied() {
  const html = document.documentElement
  return html.getAttribute("data-astryx-theme") === chromeTheme.get().theme.name
    && getComputedStyle(html).getPropertyValue("--color-background-body").trim() !== ""
}

// Every token theme_palette.js reads, resolved to a colour the way the page
// paints it (light-dark() picked by the mode): through a hidden probe's
// computed colour, since a custom property alone reads back unresolved.
function readTokens() {
  const html = getComputedStyle(document.documentElement)
  const probe = document.createElement("div")
  probe.style.cssText = "position: absolute; width: 0; height: 0; overflow: hidden; visibility: hidden; pointer-events: none"
  document.body.appendChild(probe)
  const cs = getComputedStyle(probe)
  const out = {}
  for (const name of TOKENS) {
    if (!html.getPropertyValue(name).trim()) continue // not in this theme
    probe.style.color = `var(${name})`
    out[name] = cs.color
  }
  probe.style.borderTopLeftRadius = `var(${RADIUS_TOKEN})`
  out[RADIUS_TOKEN] = cs.borderTopLeftRadius
  probe.remove()
  return out
}

async function repaint(my = seq) {
  // A theme loaded just now may take a frame or two to apply.
  for (let i = 0; i < 60; i++) {
    if (my !== seq) return
    const look = themeApplied() && deriveLook(readTokens())
    if (look) return paint(look)
    await nextFrame()
  }
  log(`theme ${chromeTheme.get().id} never applied; terminal colours unchanged`)
}

function paint(look) {
  const key = JSON.stringify(look)
  if (key === lastLook) return
  lastLook = key
  const t = look.terminal
  // What style.css draws over the grid: the pane under the canvas, text
  // over the grid (IME preedit, link underline) and the cursor.
  const root = document.documentElement.style
  root.setProperty("--term-bg", t.bg)
  root.setProperty("--term-fg", t.fg)
  root.setProperty("--term-cursor", t.cursor)
  root.setProperty("--term-cursor-fill", t.cursorFill)
  setVoid(t.bg)
  setSelection(t.selection)
  invoke("appearance_set", { look }).catch((e) => log(`appearance_set: ${e}`))
}

// Mode System: the OS's light/dark switch re-resolves every light-dark()
// token, so the terminal and cards follow.
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
  if (cur.mode === "system") nextFrame().then(() => repaint())
})

// ---- settings

async function apply(cfg) {
  if (!cat || !cfg) return
  // The chrome's text size (text_size.js); the terminal's is font_size_px.
  if (cfg.ui_text_size !== cur.text) {
    cur.text = cfg.ui_text_size
    applyTextSize(cur.text)
  }
  let themed = null
  const mode = MODES.includes(cfg.ui_mode) ? cfg.ui_mode : DEFAULT_MODE
  if (cfg.ui_theme !== cur.theme || mode !== cur.mode) {
    cur.theme = cfg.ui_theme
    cur.mode = mode
    themed = applyTheme(cfg.ui_theme || DEFAULT_THEME, mode)
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
  await themed
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
