// Decodes rsz-term frames (crates/term/src/frame.rs) and paints them onto a
// pane's canvas. Only each row's dirty column range is repainted. It runs in
// the paint worker (paint-worker.js) on an OffscreenCanvas, or on the main
// thread when the webview has no OffscreenCanvas, so it touches no DOM.

import { drawCellGlyph } from "./boxdraw.js"
import { GDI_CELLS, GDI_MIN_PX } from "./gdi_cells.js"
export const DEFAULT_BG = 0x01000000
// The wire format's version (header byte 11, FORMAT_VERSION in frame.rs).
export const FORMAT_VERSION = 1
const A_BOLD = 1, A_ITALIC = 2, A_UNDERLINE = 4, A_STRIKE = 8, A_DIM = 16, A_HIDDEN = 32, A_SELECTED = 64
export const M_APP_CURSOR = 1, M_BRACKETED_PASTE = 2, M_ALT_SCREEN = 4, M_FOCUS = 8,
  M_MOUSE_CLICK = 16, M_MOUSE_DRAG = 32, M_MOUSE_MOTION = 64, M_SGR_MOUSE = 128,
  M_ALT_SCROLL = 256, M_APP_KEYPAD = 512, M_KITTY_SHIFT = 10

const IS_MAC = (globalThis.navigator?.platform || "").startsWith("Mac")
// Go suzuri draws bold with a bold face on Windows (GDI) and with the same
// face on macOS (brighter ink only, from Rust).
const BOLD_FACE = !IS_MAC

// A screen's style: the theme's void (default background), the selection
// wash, and the font. Each Screen has its own, so two terminals on one page
// can differ; nothing here is module state.
//   void, selection: CSS colours
//   family, px: the CSS font family and size
//   bundled: the face is the bundled Gohu (ASCII runs drawn as one string,
//     bold as a 1px double strike, as GDI does)
//   face: Go's face name (picks Go's cell metrics on Windows and macOS)
//   em: Go's [ascent, descent] em ratios on macOS, or null (fontStyle sets it)
export const DEFAULT_STYLE = Object.freeze({
  void: "#0c0c10",
  selection: "rgba(184, 160, 200, 0.38)",
  family: `"Gohu", ui-monospace, Menlo, Consolas, monospace`,
  px: 14,
  bundled: true,
  face: "",
  em: null,
})

// The font fields of a style, as suzuri's setFont(family, px, bundled, face).
export function fontStyle(family, px, bundled = false, face = "") {
  return { family, px, bundled, face, em: IS_MAC ? GO_EM[bundled ? "gohu" : face.toLowerCase()] || null : null }
}

// Go suzuri on macOS sizes cells from the face's own ascent and descent
// (x/image: em ratios below, probed from Go's faceForName; a face Go can't
// load falls back to Gohu, and so does the canvas). WebKit's canvas reports
// different line metrics, so the known faces use Go's ratios.
const GO_EM = {
  gohu: [0.9448, 0.2109], menlo: [0.9282, 0.2358], "sf mono": [0.9668, 0.2109], monaco: [1.0, 0.25],
  "jetbrains mono": [1.02, 0.3], "fira code": [0.9231, 0.3077], "courier new": [0.8325, 0.3003],
}

// Go suzuri's cell metrics (internal/ui/paint_software.go): width is the
// rounded advance of "M"; height fits ascent + descent + 1 and is never
// under the size. Faces other than the bundled Gohu are drawn one glyph per
// cell, as Go does, so a fractional advance can't drift and nothing ligates.
// Measures every call: callers cache (Screen.cellMetrics). doc: the
// document to make a measuring canvas in when there is no OffscreenCanvas
// (the package reads no page globals; Screen passes its canvas's).
export function measureCell(style, doc) {
  const { family, px, bundled, face, em } = style
  const scratch = typeof OffscreenCanvas !== "undefined" ? new OffscreenCanvas(1, 1) : doc?.createElement("canvas")
  if (!scratch) throw new Error("measureCell: no OffscreenCanvas and no document to measure in")
  const c = scratch.getContext("2d")
  c.font = `${px}px ${family}`
  const m = c.measureText("M")
  const adv = c.measureText("MMMMMMMMMM").width / 10
  const cw = Math.max(6, Math.round(adv) || Math.floor(px * 3 / 5) + 1)
  let asc = Math.round(em ? em[0] * px : m.fontBoundingBoxAscent || px * 0.8)
  const desc = Math.round(em ? em[1] * px : Math.abs(m.fontBoundingBoxDescent || px * 0.2))
  let ch = Math.max(px, asc + desc + 1)
  if (asc + desc > ch) asc = ch - desc
  // Windows: Go sizes cells with GDI (cell from "M"'s extent and TEXTMETRIC
  // height, baseline at its ascent); use those numbers where we have them.
  const gdi = IS_MAC ? null : gdiCell(face, px)
  if (gdi) return { cw: gdi[0], ch: gdi[1], base: gdi[2], runs: bundled && Math.abs(adv - gdi[0]) < 0.01 }
  return { cw, ch, base: asc, runs: bundled && Math.abs(adv - cw) < 0.01 }
}

export function gdiCell(face, px) {
  const t = GDI_CELLS[(face || "").toLowerCase()]
  return t ? t[px - GDI_MIN_PX] || null : null
}
const hex = (v) => "#" + (v & 0xffffff).toString(16).padStart(6, "0")

export function decodeHeader(dv) {
  return {
    full: (dv.getUint8(1) & 1) !== 0,
    cols: dv.getUint16(2, true),
    rows: dv.getUint16(4, true),
    cx: dv.getUint16(6, true),
    cy: dv.getUint16(8, true),
    shape: dv.getUint8(10),
    version: dv.getUint8(11),
    modes: dv.getUint32(12, true),
    offset: dv.getUint32(16, true),
    history: dv.getUint32(20, true),
    seq: dv.getUint32(24, true),
    nrows: dv.getUint16(28, true),
    // Entries in the cluster table after the rows (0: none).
    nclusters: dv.getUint16(30, true),
  }
}

// The cluster table (frame.rs): y -> Map(col -> {s, w}), the cell's whole
// grapheme and its width in columns. null when the frame has none.
function readClusters(dv, h) {
  if (!h.nclusters) return null
  const end = dv.byteLength
  let p = 32
  for (let r = 0; r < h.nrows && p + 8 <= end; r++) {
    const nruns = dv.getUint16(p + 2, true)
    p += 8
    for (let k = 0; k < nruns && p + 16 <= end; k++) p += 16 + 4 * dv.getUint16(p + 2, true)
  }
  const map = new Map()
  for (let e = 0; e < h.nclusters && p + 6 <= end; e++) {
    const y = dv.getUint16(p, true), col = dv.getUint16(p + 2, true)
    const w = dv.getUint8(p + 4), n = dv.getUint8(p + 5)
    if (p + 6 + 4 * n > end) break
    const cps = []
    for (let i = 0; i < n; i++) cps.push(dv.getUint32(p + 6 + 4 * i, true))
    p += 6 + 4 * n
    let row = map.get(y)
    if (!row) map.set(y, row = new Map())
    row.set(col, { s: String.fromCodePoint(...cps), w })
  }
  return map
}

// Colour emoji faces, named first only for emoji: the text family keeps
// none, so © ™ ↔ stay text.
const EMOJI_FAMILY = `"Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol", "Noto Color Emoji"`
const PICTO = /\p{Extended_Pictographic}|\p{Regional_Indicator}/u
const EMOJI = /\p{Extended_Pictographic}|\p{Regional_Indicator}|\uFE0F|\u200D|\u20E3/u
// A two-column grapheme drawn as an emoji (not when VS15 asks for text).
const isEmoji = (s, w) => w === 2 && !s.includes("\uFE0E") && EMOJI.test(s)

export class Screen {
  // canvas: an HTMLCanvasElement or an OffscreenCanvas. dpr: the device
  // pixel ratio (a worker has no window to ask).
  // style: any DEFAULT_STYLE fields (copied over DEFAULT_STYLE).
  constructor(canvas, dpr = 1, style = DEFAULT_STYLE) {
    this.canvas = canvas
    this.style = { ...DEFAULT_STYLE, ...style }
    this.cell = null // cellMetrics(), cached until the style's font changes
    // Opaque: text gets ClearType on Windows, as Go's GDI text does (box,
    // block and shade glyphs are filled rects, crisp in both).
    this.ctx = canvas.getContext("2d", { alpha: false })
    this.cols = 0
    this.rows = 0
    this.dpr = 0
    this.metrics = null
    this.wantDpr = dpr
    // Emoji fit per `${px}|${w}|${s}`: {width, sc}, measured once.
    this.fit = new Map()
  }

  // Change the style (any DEFAULT_STYLE fields). A font change re-measures
  // the cells; the canvas resizes at its next setSize/apply, and the caller
  // asks for a full frame, since nothing redraws what is already painted.
  setStyle(st) {
    const font = ["family", "px", "bundled", "face", "em"].some((k) => k in st && st[k] !== this.style[k])
    this.style = { ...this.style, ...st }
    if (font) this.cell = null
    this.fit.clear()
  }

  // This screen's cell size: {cw, ch, base, runs}. The same object until the
  // font changes, so setSize can tell when it has to resize.
  cellMetrics() {
    return this.cell || (this.cell = measureCell(this.style, this.canvas.ownerDocument))
  }

  // Measure again (a font that was loading has loaded).
  resetMetrics() { this.cell = null; this.fit.clear() }

  fontSpec() { return `${this.style.px}px ${this.style.family}` }

  // The canvas's backing store follows cols, rows and dpr; its CSS size is
  // the main thread's (cssSize), since an OffscreenCanvas has no style.
  setSize(cols, rows) {
    const m = this.cellMetrics()
    const { cw, ch } = m
    const dpr = this.wantDpr
    if (cols === this.cols && rows === this.rows && dpr === this.dpr && m === this.metrics) return false
    this.cols = cols; this.rows = rows; this.dpr = dpr; this.metrics = m
    this.canvas.width = Math.ceil(cols * cw * dpr)
    this.canvas.height = Math.ceil(rows * ch * dpr)
    if (this.canvas.style) cssSize(this.canvas, cols, rows, m)
    const ctx = this.ctx
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.textBaseline = "alphabetic"
    ctx.fillStyle = this.style.void
    ctx.fillRect(0, 0, cols * cw, rows * ch)
    return true
  }

  // Draw one grapheme s over w cells at x. Emoji take the emoji faces,
  // centred, and shrink (clipped to their cells) only when they overflow;
  // no bold double strike. Other clusters (e + U+0301) draw in the run's
  // font with text(). The box is always w*cw by ch, never measured.
  drawCluster(ctx, s, w, x, top, cw, ch, base, runFont, text) {
    if (!isEmoji(s, w)) { text(s, x, top + base); return }
    const { px, family } = this.style
    ctx.font = `${px}px ${EMOJI_FAMILY}, ${family}`
    const key = `${px}|${w}|${s}`
    let f = this.fit.get(key)
    if (!f) {
      const m = ctx.measureText(s)
      const width = m.width || px
      // A worker canvas can report no bounding box: guess from px.
      const asc = m.actualBoundingBoxAscent || px * 0.8
      const desc = m.actualBoundingBoxDescent || px * 0.2
      const tall = asc + desc > 0 ? asc + desc : px
      f = { width, sc: Math.min(1, (w * cw) / width, ch / tall) }
      this.fit.set(key, f)
    }
    const bw = w * cw
    if (f.sc >= 0.999) {
      ctx.fillText(s, x + (bw - f.width) / 2, top + base)
    } else {
      // Clip in whole device pixels, so a fractional dpr leaves no seam.
      const d = this.dpr || 1
      const px0 = (v) => Math.round(v * d) / d
      ctx.save()
      ctx.beginPath()
      ctx.rect(px0(x), px0(top), px0(x + bw) - px0(x), px0(top + ch) - px0(top))
      ctx.clip()
      ctx.translate(x + bw / 2, top + base)
      ctx.scale(f.sc, f.sc)
      ctx.fillText(s, -f.width / 2, 0)
      ctx.restore()
    }
    ctx.font = runFont
  }

  // Paint one frame (an ArrayBuffer, from byte `off`). Returns its header.
  apply(buf, off = 0) {
    const dv = new DataView(buf, off)
    const h = decodeHeader(dv)
    if (h.version !== FORMAT_VERSION) throw new Error(`terminal format v${h.version}, this app knows v${FORMAT_VERSION}`)
    this.setSize(h.cols, h.rows)
    const { cw, ch, base, runs } = this.cellMetrics()
    const { void: VOID, selection: SELECTION, px: FONT_PX, family: FAMILY, bundled: BUNDLED } = this.style
    const ctx = this.ctx
    const clusters = readClusters(dv, h)
    let p = 32
    for (let r = 0; r < h.nrows; r++) {
      const y = dv.getUint16(p, true)
      const rc = clusters && clusters.get(y)
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
          ctx.fillRect(rx, top, n * cw, ch)
        }
        if (!(attrs & A_HIDDEN)) {
          // Bold on Windows: a bold face, as GDI picks; the bundled Gohu has
          // none, and GDI emboldens it with a crisp 1px double strike, so do
          // the same instead of the canvas's smeared synthetic bold.
          const wantBold = attrs & A_BOLD && BOLD_FACE
          const strike = wantBold && BUNDLED
          const runFont = `${attrs & A_ITALIC ? "italic " : ""}${wantBold && !strike ? "bold " : ""}${FONT_PX}px ${FAMILY}`
          ctx.font = runFont
          const text = strike ? (t, x, y) => { ctx.fillText(t, x, y); ctx.fillText(t, x + 1, y) } : (t, x, y) => ctx.fillText(t, x, y)
          ctx.fillStyle = hex(fg)
          // ASCII stretches go out as one string; anything else per cell so
          // fallback glyphs can't push the grid out of line.
          let s = "", sx = 0
          for (let i = 0; i < n; i++) {
            const cp = dv.getUint32(p + i * 4, true)
            // A multi-codepoint grapheme (its base may be ASCII or a space).
            const c = rc && rc.get(col + i)
            if (c) {
              if (s) { text(s, sx * cw, top + base); s = "" }
              this.drawCluster(ctx, c.s, c.w, (col + i) * cw, top, cw, ch, base, runFont, text)
              continue
            }
            if (runs && cp >= 0x20 && cp < 0x7f) {
              if (!s) sx = col + i
              s += String.fromCharCode(cp)
              continue
            }
            if (s) { text(s, sx * cw, top + base); s = "" }
            if (cp === 0 || cp === 0x20) continue
            // Box drawing, blocks and wedges: drawn into the cell, as Go does.
            if (cp >= 0x2500 && drawCellGlyph(ctx, cp, (col + i) * cw, top, cw, ch)) continue
            // A one-codepoint wide pictograph (😀): the emoji path too.
            if (cp > 0x7f && i + 1 < n && dv.getUint32(p + (i + 1) * 4, true) === 0 && PICTO.test(String.fromCodePoint(cp))) {
              this.drawCluster(ctx, String.fromCodePoint(cp), 2, (col + i) * cw, top, cw, ch, base, runFont, text)
              continue
            }
            text(String.fromCodePoint(cp), (col + i) * cw, top + base)
          }
          if (s && s.trim()) text(s, sx * cw, top + base)
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

// The canvas element's CSS size for a cols x rows screen of cells m.
export function cssSize(el, cols, rows, m) {
  const { cw, ch } = m
  el.style.width = `${cols * cw}px`
  el.style.height = `${rows * ch}px`
}
