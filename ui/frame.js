// Decodes rsz-term frames (crates/term/src/frame.rs) and paints them onto a
// pane's canvas. Only each row's dirty column range is repainted. It runs in
// the paint worker (paint-worker.js) on an OffscreenCanvas, or on the main
// thread when the webview has no OffscreenCanvas, so it touches no DOM.

import { drawCellGlyph } from "./boxdraw.js"
export const DEFAULT_BG = 0x01000000
const A_BOLD = 1, A_ITALIC = 2, A_UNDERLINE = 4, A_STRIKE = 8, A_DIM = 16, A_HIDDEN = 32, A_SELECTED = 64
export const M_APP_CURSOR = 1, M_BRACKETED_PASTE = 2, M_ALT_SCREEN = 4, M_FOCUS = 8,
  M_MOUSE_CLICK = 16, M_MOUSE_DRAG = 32, M_MOUSE_MOTION = 64, M_SGR_MOUSE = 128,
  M_ALT_SCROLL = 256, M_APP_KEYPAD = 512, M_KITTY_SHIFT = 10

// The theme's void (default background) and the font come from settings
// (appearance.js); these are the values before they load.
let VOID = "#0c0c10"
// The selection: a wash of the theme accent over the cell background.
let SELECTION = "rgba(184, 160, 200, 0.38)"
export function setSelection(css) { SELECTION = css; styled() }
let FONT_PX = 14
let FAMILY = `"Gohu", ui-monospace, Menlo, Consolas, monospace`
export function setVoid(css) { VOID = css; styled() }
// Go suzuri draws bold with a bold face on Windows (GDI) and with the same
// face on macOS (brighter ink only, from Rust).
const IS_MAC = (globalThis.navigator?.platform || "").startsWith("Mac")
const BOLD_FACE = !IS_MAC
let BUNDLED = true
export function setFont(family, px, bundled = false, face = "") {
  FAMILY = family
  BUNDLED = bundled
  EM = IS_MAC ? GO_EM[bundled ? "gohu" : face.toLowerCase()] || null : null
  FONT_PX = px
  FACE = face
  metrics = null
  styled()
}
let FACE = ""
// The paint worker keeps its own copy of these settings: the UI thread
// listens (onStyle) and posts styleState() over; the worker applyStyle()s it.
let styleListener = null
export function onStyle(fn) { styleListener = fn }
function styled() { styleListener?.(styleState()) }
export function styleState() {
  return { void: VOID, selection: SELECTION, font: [FAMILY, FONT_PX, BUNDLED, FACE] }
}
export function applyStyle(st) {
  VOID = st.void
  SELECTION = st.selection
  setFont(...st.font)
}
export function fontSpec() { return `${FONT_PX}px ${FAMILY}` }

let metrics = null
// Go suzuri on macOS sizes cells from the face's own ascent and descent
// (x/image: em ratios below, probed from Go's faceForName; a face Go can't
// load falls back to Gohu, and so does the canvas). WebKit's canvas reports
// different line metrics, so the known faces use Go's ratios.
const GO_EM = {
  gohu: [0.9448, 0.2109], menlo: [0.9282, 0.2358], "sf mono": [0.9668, 0.2109], monaco: [1.0, 0.25],
  "jetbrains mono": [1.02, 0.3], "fira code": [0.9231, 0.3077], "courier new": [0.8325, 0.3003],
}
let EM = null
// Go suzuri's cell metrics (internal/ui/paint_software.go): width is the
// rounded advance of "M"; height fits ascent + descent + 1 and is never
// under the size. Faces other than the bundled Gohu are drawn one glyph per
// cell, as Go does, so a fractional advance can't drift and nothing ligates.
export function cellMetrics() {
  if (metrics) return metrics
  const c = (typeof OffscreenCanvas !== "undefined" ? new OffscreenCanvas(1, 1) : document.createElement("canvas")).getContext("2d")
  c.font = `${FONT_PX}px ${FAMILY}`
  const m = c.measureText("M")
  const adv = c.measureText("MMMMMMMMMM").width / 10
  const cw = Math.max(6, Math.round(adv) || Math.floor(FONT_PX * 3 / 5) + 1)
  let asc = Math.round(EM ? EM[0] * FONT_PX : m.fontBoundingBoxAscent || FONT_PX * 0.8)
  const desc = Math.round(EM ? EM[1] * FONT_PX : Math.abs(m.fontBoundingBoxDescent || FONT_PX * 0.2))
  let ch = Math.max(FONT_PX, asc + desc + 1)
  if (asc + desc > ch) asc = ch - desc
  const runs = BUNDLED && Math.abs(adv - cw) < 0.01
  metrics = { cw, ch, base: asc, runs }
  return metrics
}
export function resetMetrics() { metrics = null }

const hex = (v) => "#" + (v & 0xffffff).toString(16).padStart(6, "0")

export function decodeHeader(dv) {
  return {
    full: (dv.getUint8(1) & 1) !== 0,
    cols: dv.getUint16(2, true),
    rows: dv.getUint16(4, true),
    cx: dv.getUint16(6, true),
    cy: dv.getUint16(8, true),
    shape: dv.getUint8(10),
    modes: dv.getUint32(12, true),
    offset: dv.getUint32(16, true),
    history: dv.getUint32(20, true),
    seq: dv.getUint32(24, true),
    nrows: dv.getUint16(28, true),
  }
}

export class Screen {
  // canvas: an HTMLCanvasElement or an OffscreenCanvas. dpr: the device
  // pixel ratio (a worker has no window to ask).
  constructor(canvas, dpr = 1) {
    this.canvas = canvas
    // Opaque on macOS only: on Windows an opaque canvas gets ClearType
    // colour fringes, and Go suzuri draws greyscale text there.
    this.ctx = canvas.getContext("2d", { alpha: !IS_MAC })
    this.cols = 0
    this.rows = 0
    this.dpr = 0
    this.metrics = null
    this.wantDpr = dpr
  }

  // The canvas's backing store follows cols, rows and dpr; its CSS size is
  // the main thread's (cssSize), since an OffscreenCanvas has no style.
  setSize(cols, rows) {
    const { cw, ch } = cellMetrics()
    const dpr = this.wantDpr
    const m = cellMetrics()
    if (cols === this.cols && rows === this.rows && dpr === this.dpr && m === this.metrics) return false
    this.cols = cols; this.rows = rows; this.dpr = dpr; this.metrics = m
    this.canvas.width = Math.ceil(cols * cw * dpr)
    this.canvas.height = Math.ceil(rows * ch * dpr)
    if (this.canvas.style) cssSize(this.canvas, cols, rows)
    const ctx = this.ctx
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.textBaseline = "alphabetic"
    ctx.fillStyle = VOID
    ctx.fillRect(0, 0, cols * cw, rows * ch)
    return true
  }

  // Paint one frame (an ArrayBuffer, from byte `off`). Returns its header.
  apply(buf, off = 0) {
    const dv = new DataView(buf, off)
    const h = decodeHeader(dv)
    this.setSize(h.cols, h.rows)
    const { cw, ch, base, runs } = cellMetrics()
    const ctx = this.ctx
    let p = 32
    for (let r = 0; r < h.nrows; r++) {
      const y = dv.getUint16(p, true)
      const nruns = dv.getUint16(p + 2, true)
      const x0 = dv.getUint16(p + 4, true)
      const x1 = dv.getUint16(p + 6, true)
      p += 8
      const top = y * ch
      ctx.fillStyle = VOID
      ctx.fillRect(x0 * cw, top, (x1 - x0) * cw, ch)
      for (let k = 0; k < nruns; k++) {
        const col = dv.getUint16(p, true)
        const n = dv.getUint16(p + 2, true)
        const fg = dv.getUint32(p + 4, true)
        const bg = dv.getUint32(p + 8, true)
        const attrs = dv.getUint16(p + 12, true)
        p += 16
        const rx = col * cw
        if (bg !== DEFAULT_BG) {
          ctx.fillStyle = hex(bg)
          ctx.fillRect(rx, top, n * cw, ch)
        }
        if (attrs & A_SELECTED) {
          ctx.fillStyle = SELECTION
          ctx.fillRect(x0, top, n * cw, ch)
        }
        if (!(attrs & A_HIDDEN)) {
          ctx.font = `${attrs & A_ITALIC ? "italic " : ""}${attrs & A_BOLD && BOLD_FACE ? "bold " : ""}${FONT_PX}px ${FAMILY}`
          ctx.fillStyle = hex(fg)
          // ASCII stretches go out as one string; anything else per cell so
          // fallback glyphs can't push the grid out of line.
          let s = "", sx = 0
          for (let i = 0; i < n; i++) {
            const cp = dv.getUint32(p + i * 4, true)
            if (runs && cp >= 0x20 && cp < 0x7f) {
              if (!s) sx = col + i
              s += String.fromCharCode(cp)
              continue
            }
            if (s) { ctx.fillText(s, sx * cw, top + base); s = "" }
            if (cp === 0 || cp === 0x20) continue
            // Box drawing, blocks and wedges: drawn into the cell, as Go does.
            if (cp >= 0x2500 && drawCellGlyph(ctx, cp, (col + i) * cw, top, cw, ch)) continue
            ctx.fillText(String.fromCodePoint(cp), (col + i) * cw, top + base)
          }
          if (s && s.trim()) ctx.fillText(s, sx * cw, top + base)
          if (attrs & (A_UNDERLINE | A_STRIKE)) {
            const ly = attrs & A_UNDERLINE ? top + ch - 2 : top + ch / 2
            ctx.fillRect(rx, ly, n * cw, 1)
          }
        }
        p += n * 4
      }
    }
    return h
  }
}

// The canvas element's CSS size for a cols x rows screen.
export function cssSize(el, cols, rows) {
  const { cw, ch } = cellMetrics()
  el.style.width = `${cols * cw}px`
  el.style.height = `${rows * ch}px`
}
