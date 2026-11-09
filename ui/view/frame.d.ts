// suzuri-view/frame.js: hand-written types. Keep in step with frame.js.

/** The default-background colour value in a frame's runs. */
export const DEFAULT_BG: number
/** The frame wire format this package decodes (header byte 11). */
export const FORMAT_VERSION: number

/** Mode bits in a frame header's `modes` word. */
export const M_APP_CURSOR: number
export const M_BRACKETED_PASTE: number
export const M_ALT_SCREEN: number
export const M_FOCUS: number
export const M_MOUSE_CLICK: number
export const M_MOUSE_DRAG: number
export const M_MOUSE_MOTION: number
export const M_SGR_MOUSE: number
export const M_ALT_SCROLL: number
export const M_APP_KEYPAD: number
/** The shift of the Kitty keyboard flags inside `modes` (kitty.js kittyFlags). */
export const M_KITTY_SHIFT: number

/** A Screen's style. */
export interface ScreenStyle {
  /** The default background (CSS colour). */
  void: string
  /** The selection wash (CSS colour). */
  selection: string
  /** The CSS font-family list. */
  family: string
  px: number
  /** The bundled Gohu: ASCII runs drawn whole, bold as a 1px double strike. */
  bundled: boolean
  /** Go suzuri's face name (picks Go's cell metrics). */
  face: string
  /** Go's [ascent, descent] em ratios on macOS, or null. */
  em: [number, number] | null
}

/** A cell: width, height, baseline, and whether ASCII runs draw as one string. */
export interface CellMetrics {
  cw: number
  ch: number
  base: number
  runs: boolean
}

/** A decoded frame header (the first 32 bytes). */
export interface FrameHeader {
  full: boolean
  cols: number
  rows: number
  cx: number
  cy: number
  /** Cursor shape: 0 hidden, 1 block, 2 underline, 3 beam, 4 hollow. */
  shape: number
  version: number
  modes: number
  /** Lines scrolled back from the bottom. */
  offset: number
  history: number
  seq: number
  nrows: number
  /** Entries in the optional cluster table after the rows (0: none). */
  nclusters: number
}

export const DEFAULT_STYLE: Readonly<ScreenStyle>

/** The font fields of a ScreenStyle (em from Go's ratios on macOS). */
export function fontStyle(family: string, px: number, bundled?: boolean, face?: string): Pick<ScreenStyle, "family" | "px" | "bundled" | "face" | "em">

/** Measures a cell for `style`. `doc` makes the measuring canvas when there is no OffscreenCanvas. */
export function measureCell(style: ScreenStyle, doc?: Document | null): CellMetrics

/** Go's Windows (GDI) cell [width, height, baseline] for a face and size, or null. */
export function gdiCell(face: string, px: number): [number, number, number] | null

export function decodeHeader(dv: DataView): FrameHeader

/** Paints frames onto one canvas, with its own style and cell metrics. */
export class Screen {
  /** `style` fields left out come from DEFAULT_STYLE. */
  constructor(canvas: HTMLCanvasElement | OffscreenCanvas, dpr?: number, style?: Partial<ScreenStyle>)
  canvas: HTMLCanvasElement | OffscreenCanvas
  style: ScreenStyle
  cols: number
  rows: number
  /** Changes the style; a font change re-measures (the caller asks for a full frame). */
  setStyle(st: Partial<ScreenStyle>): void
  /** The same object until the font changes. */
  cellMetrics(): CellMetrics
  /** Measure again (a font that was loading has loaded). */
  resetMetrics(): void
  /** `${px}px ${family}`, for document.fonts.load. */
  fontSpec(): string
  /** Sizes the backing store; returns whether it changed. */
  setSize(cols: number, rows: number): boolean
  /** Paints one frame from byte `off`; throws on another format version. */
  apply(buf: ArrayBuffer, off?: number): FrameHeader
}

/** Sets a canvas element's CSS size for cols x rows cells. */
export function cssSize(el: HTMLElement, cols: number, rows: number, m: CellMetrics): void
