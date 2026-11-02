// Decodes rsz-term frames (crates/term/src/frame.rs) and paints them onto a
// pane's canvas. Only the rows in the frame are repainted.

export const DEFAULT_BG = 0x01000000
const A_BOLD = 1, A_ITALIC = 2, A_UNDERLINE = 4, A_STRIKE = 8, A_DIM = 16, A_HIDDEN = 32
export const M_APP_CURSOR = 1, M_BRACKETED_PASTE = 2, M_ALT_SCREEN = 4, M_FOCUS = 8,
  M_MOUSE_CLICK = 16, M_MOUSE_DRAG = 32, M_MOUSE_MOTION = 64, M_SGR_MOUSE = 128,
  M_ALT_SCROLL = 256, M_APP_KEYPAD = 512

const VOID = "#0c0c10"
const FONT_PX = 14
const FAMILY = `"Gohu", ui-monospace, Menlo, Consolas, monospace`

let metrics = null
export function cellMetrics() {
  if (metrics) return metrics
  const c = document.createElement("canvas").getContext("2d")
  c.font = `${FONT_PX}px ${FAMILY}`
  const w = c.measureText("MMMMMMMMMM").width / 10
  metrics = { cw: w, ch: Math.ceil(FONT_PX * 1.15), base: Math.round(FONT_PX * 0.9) }
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
  constructor(canvas) {
    this.canvas = canvas
    this.ctx = canvas.getContext("2d", { alpha: false })
    this.cols = 0
    this.rows = 0
    this.dpr = 0
  }

  setSize(cols, rows) {
    const { cw, ch } = cellMetrics()
    const dpr = window.devicePixelRatio || 1
    if (cols === this.cols && rows === this.rows && dpr === this.dpr) return false
    this.cols = cols; this.rows = rows; this.dpr = dpr
    this.canvas.width = Math.ceil(cols * cw * dpr)
    this.canvas.height = Math.ceil(rows * ch * dpr)
    this.canvas.style.width = `${cols * cw}px`
    this.canvas.style.height = `${rows * ch}px`
    const ctx = this.ctx
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.textBaseline = "alphabetic"
    ctx.fillStyle = VOID
    ctx.fillRect(0, 0, cols * cw, rows * ch)
    return true
  }

  // Paint one frame (an ArrayBuffer). Returns its header.
  apply(buf) {
    const dv = new DataView(buf)
    const h = decodeHeader(dv)
    this.setSize(h.cols, h.rows)
    const { cw, ch, base } = cellMetrics()
    const ctx = this.ctx
    let p = 32
    for (let r = 0; r < h.nrows; r++) {
      const y = dv.getUint16(p, true)
      const nruns = dv.getUint16(p + 2, true)
      p += 4
      const top = y * ch
      ctx.fillStyle = VOID
      ctx.fillRect(0, top, this.cols * cw, ch)
      for (let k = 0; k < nruns; k++) {
        const col = dv.getUint16(p, true)
        const n = dv.getUint16(p + 2, true)
        const fg = dv.getUint32(p + 4, true)
        const bg = dv.getUint32(p + 8, true)
        const attrs = dv.getUint16(p + 12, true)
        p += 16
        const x0 = col * cw
        if (bg !== DEFAULT_BG) {
          ctx.fillStyle = hex(bg)
          ctx.fillRect(x0, top, n * cw, ch)
        }
        if (!(attrs & A_HIDDEN)) {
          ctx.font = `${attrs & A_ITALIC ? "italic " : ""}${attrs & A_BOLD ? "bold " : ""}${FONT_PX}px ${FAMILY}`
          ctx.fillStyle = hex(fg)
          ctx.globalAlpha = attrs & A_DIM ? 0.6 : 1
          // ASCII stretches go out as one string; anything else per cell so
          // fallback glyphs can't push the grid out of line.
          let s = "", sx = 0
          for (let i = 0; i < n; i++) {
            const cp = dv.getUint32(p + i * 4, true)
            if (cp >= 0x20 && cp < 0x7f) {
              if (!s) sx = col + i
              s += String.fromCharCode(cp)
              continue
            }
            if (s) { ctx.fillText(s, sx * cw, top + base); s = "" }
            if (cp === 0 || cp === 0x20) continue
            ctx.fillText(String.fromCodePoint(cp), (col + i) * cw, top + base)
          }
          if (s && s.trim()) ctx.fillText(s, sx * cw, top + base)
          if (attrs & (A_UNDERLINE | A_STRIKE)) {
            const ly = attrs & A_UNDERLINE ? top + ch - 2 : top + ch / 2
            ctx.fillRect(x0, ly, n * cw, 1)
          }
          ctx.globalAlpha = 1
        }
        p += n * 4
      }
    }
    return h
  }
}
