// suzuri's side of the frame decoder (ui/view/frame.js, the view package):
// one style for the whole window, as appearance.js sets it. In the package
// each Screen has its own style and metrics; here every Screen suzuri makes
// follows the window's (Screen below), and cellMetrics() is the window's
// cell size for layout.
import { Screen as ViewScreen, DEFAULT_STYLE, fontStyle, measureCell } from "./view/frame.js"
export {
  DEFAULT_BG, FORMAT_VERSION, DEFAULT_STYLE, decodeHeader, gdiCell, fontStyle, measureCell,
  M_APP_CURSOR, M_BRACKETED_PASTE, M_ALT_SCREEN, M_FOCUS, M_MOUSE_CLICK, M_MOUSE_DRAG, M_MOUSE_MOTION,
  M_SGR_MOUSE, M_ALT_SCROLL, M_APP_KEYPAD, M_KITTY_SHIFT,
} from "./view/frame.js"
import { cssSize as viewCssSize } from "./view/frame.js"

// The window's style; replaced (never mutated) on each change, with `rev`
// counting changes so Screens know when to pick it up.
let style = { ...DEFAULT_STYLE }
let rev = 0
let metrics = null

function changed(st) {
  style = { ...style, ...st }
  rev++
  if (["family", "px", "bundled", "face", "em"].some((k) => k in st)) metrics = null
  styleListener?.(styleState())
}
export function setVoid(css) { changed({ void: css }) }
export function setSelection(css) { changed({ selection: css }) }
export function setFont(family, px, bundled = false, face = "") {
  changed(fontStyle(family, px, bundled, face))
}

// The paint worker keeps its own copy of these settings: the UI thread
// listens (onStyle) and posts styleState() over; the worker applies it to
// its Screens (paint-worker.js).
let styleListener = null
export function onStyle(fn) { styleListener = fn }
export function styleState() {
  return { void: style.void, selection: style.selection, font: [style.family, style.px, style.bundled, style.face] }
}
// A styleState() as Screen.setStyle fields.
export function styleFields(st) {
  return { void: st.void, selection: st.selection, ...fontStyle(...st.font) }
}
export function applyStyle(st) { changed(styleFields(st)) }
export function fontSpec() { return `${style.px}px ${style.family}` }

// The window's cell size, as every suzuri Screen measures it.
export function cellMetrics() {
  return metrics || (metrics = measureCell(style, globalThis.document))
}
export function resetMetrics() { metrics = null; rev++ }

export function cssSize(el, cols, rows) { viewCssSize(el, cols, rows, cellMetrics()) }

// A Screen that follows the window's style: it takes the latest before each
// resize or paint. Its metrics object is the window's, so it stays the same
// object until the font changes (Screen.setSize compares it).
export class Screen extends ViewScreen {
  constructor(canvas, dpr = 1) {
    super(canvas, dpr, style)
    this.rev = rev
    this.cell = cellMetrics()
  }
  sync() {
    if (this.rev === rev) return
    this.rev = rev
    this.style = { ...style }
    this.cell = cellMetrics()
  }
  cellMetrics() { this.sync(); return this.cell || (this.cell = cellMetrics()) }
  setSize(cols, rows) { this.sync(); return super.setSize(cols, rows) }
  apply(buf, off = 0) { this.sync(); return super.apply(buf, off) }
}
