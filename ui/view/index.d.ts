// suzuri-view: hand-written types for index.js. Keep in step with it.

export { FORMAT_VERSION, DEFAULT_STYLE, Screen, decodeHeader, measureCell } from "./frame.js"
export type { ScreenStyle, CellMetrics, FrameHeader } from "./frame.js"
export { encodeKey } from "./keys.js"

/** The bundled Gohu (fonts/ in this package), for setStyle({ font: GOHU_FONT }). */
export const GOHU_FONT: Readonly<TermFont & { url: string; name: string; bundled: true }>

/** A mouse report for an app that asked for the mouse (rsz-term's MouseEvent). */
export interface MouseEv {
  kind: "press" | "release" | "move" | "wheelup" | "wheeldown"
  button: number
  /** For "move": the button held, or null. */
  held: number | null
  col: number
  row: number
  shift: boolean
  alt: boolean
  ctrl: boolean
}

/** A link at a visible cell (OSC 8 or a URL in the text). */
export interface Link {
  url: string
  row: number
  x0: number
  x1: number
}

/** The app's side: each app wires these to its own IPC. */
export interface TermHost {
  /** Bytes for the PTY (keys, IME text, focus reports). */
  write(data: Uint8Array | string): void
  /** A paste: the text, or null for "read the clipboard yourself". */
  paste(text: string | null): void
  resize(cols: number, rows: number): void
  /** The cell's size in pixels (images are sized in cells). */
  cellPx(w: number, h: number): void
  /** History lines; negative is up. */
  scroll(delta: number): void
  /** Frames up to `seq` are painted; the next may come. */
  ack(seq: number): void
  /** Send a full frame (after a style change). */
  redraw(): void
  focus(on: boolean): void
  /** op: "cell" | "word" | "line" | "extend". right: the cell's right half. */
  select(op: string, col: number, row: number, right: boolean): void
  /** Copy the selection; resolves whether there was one. */
  copy(): Promise<boolean>
  mouse(ev: MouseEv): void
  link(col: number, row: number): Promise<Link | null>
  openUrl(url: string): void
  /** The app's shortcuts go first: return true when the app took the key. */
  keyFilter?(e: KeyboardEvent): boolean
  /** Every message passed to TermView.message, after the view's own handling. */
  onMessage?(msg: TermMsg): void
}

/** An image placement: [image, row, col, cols, rows, z, inline, crop x, y, w, h]. */
export type Placement = [number, number, number, number, number, number, number, number, number, number, number]

/** Messages beside the frames (suzuri's Msg; Irori adds held and offline). */
export type TermMsg =
  | { t: "title"; title: string }
  | { t: "bell" }
  | { t: "clip"; text: string }
  | { t: "exit"; code: number | null }
  | { t: "done"; exit: number | null; command: string }
  | { t: "progress"; state: number; percent: number }
  | { t: "shell"; integrated: boolean; cwd: string; program: boolean; prompts: number }
  | { t: "img"; id: number; name: string; path: string | null; format: number; w: number; h: number; z: boolean; data: string }
  | { t: "imgdrop"; ids: number[] }
  | { t: "imgpos"; list: Placement[] }
  | { t: "held"; by: string | null }
  | { t: "offline" }

export interface TermFont {
  /** A CSS font-family list, e.g. `"Gohu", ui-monospace, monospace`. */
  family: string
  px: number
  /** A font file to load as `name` (default: the first family in `family`). */
  url?: string
  name?: string
  /** The bundled Gohu: ASCII runs drawn whole, bold as a double strike. Default: !!url. */
  bundled?: boolean
  /** Go suzuri's face name, for its probed cell metrics. */
  face?: string
}

export interface TermStyle {
  /** The default background. */
  bg?: string
  /** The selection wash, e.g. rgba(184, 160, 200, 0.38). */
  selection?: string
  /** Sets --rsz-cursor on the view (view.css colours the cursor with it). */
  cursor?: string
  font?: TermFont
}

export interface TermView {
  /** A frame: paints on the next animation frame, then calls host.ack(seq). A frame in
   *  another format version is refused (shown in the view, not acked). */
  feed(frame: ArrayBuffer | ArrayBufferView | number[], offset?: number): void
  /** title, exit, img, imgdrop, imgpos and progress act on the view; all go to host.onMessage. */
  message(msg: TermMsg | string): void
  setStyle(s: TermStyle): void
  /** No write, paste, mouse, scroll, select or copy reach the host. (After an exit
   *  message only write and paste stop: the history can still be scrolled and copied.) */
  setReadOnly(on: boolean): void
  focus(): void
  /** Removes the DOM and every listener, animation frame and observer the view added. */
  unmount(): void
  /** The last title message's title. */
  readonly title: string
}

export interface MountOptions {
  readOnly?: boolean
  /** The canvas's [left, top] offset in the view; default [6, 4]. */
  padding?: [number, number]
  /** macOS key handling (Cmd chords); default from navigator.platform. */
  isMac?: boolean
}

export function mount(el: HTMLElement, host: TermHost, opts?: MountOptions): TermView
