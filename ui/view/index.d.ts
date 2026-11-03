// suzuri-view: hand-written types for index.js. Keep in step with it.

export { FORMAT_VERSION, DEFAULT_STYLE, Screen, decodeHeader, measureCell } from "./frame.js"
export type { ScreenStyle, CellMetrics, FrameHeader } from "./frame.js"
export { encodeKey } from "./keys.js"
import type { FrameHeader, CellMetrics } from "./frame.js"

/** The copy/paste chords the view runs itself (Go suzuri's): Cmd+C/V on macOS;
 *  elsewhere Ctrl+Shift+C/V, Ctrl+Insert, Shift+Insert, Ctrl+V, and Ctrl+C when
 *  there is a selection ("copy-or-interrupt": ^C goes out when nothing was copied).
 *  A keyFilter can call it to leave those keys to the view. */
export function clipShortcut(
  e: Pick<KeyboardEvent, "key" | "altKey" | "ctrlKey" | "metaKey" | "shiftKey">,
  isMac: boolean,
  hasSelection: boolean,
): "copy" | "paste" | "copy-or-interrupt" | null

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
  /** The app's shortcuts go first: return true when the app took the key (the
   *  view then does nothing with it, not even preventDefault). */
  keyFilter?(e: KeyboardEvent): boolean
  /** Committed text (IME, dead keys) before it is written: return true when the
   *  app took it (suzuri's input bar). */
  textFilter?(text: string): boolean
  /** A paste event's clipboard before the view sends its text to paste():
   *  return true when the app took it (suzuri: an image on the alt screen). */
  pasteFilter?(clipboard: DataTransfer): boolean
  /** After a frame paints, the view has followed it (cursor, badge) and acked
   *  it. stats: what this paint applied, when the view painted it. */
  onPaint?(header: FrameHeader, stats?: PaintStats): void
  /** The arrow keys the wheel sends on an alt screen that asked for them
   *  (DEC 1007). Default: write(). suzuri sends them straight to the PTY, not
   *  timed as typed input. Called after an exit too (not in read-only mode). */
  wheelKeys?(data: string): void
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

export interface PaintStats {
  /** Frames applied in this animation frame. */
  frames: number
  bytes: number
  /** Time spent applying them, in ms. */
  ms: number
}

/** What a view paints and measures with (frame.js's Screen is one). */
export interface ScreenLike {
  cellMetrics(): CellMetrics
  setStyle(st: object): void
  resetMetrics(): void
  fontSpec(): string
  /** Paints a frame; not called when the host paints (paint: "external"). */
  apply?(buf: ArrayBuffer, off?: number): FrameHeader
}

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
  /** Removes the DOM and every listener, animation frame and observer the view
   *  added. A large image the view opened stays up until it is clicked. */
  unmount(): void
  /** The last title message's title. */
  readonly title: string
  /** The last painted frame's modes (frame.js M_* bits). */
  readonly modes: number
  /** A selection was made with the pointer (Ctrl+C then copies). */
  readonly hasSelection: boolean
  /** The canvas the frames paint on (for paint: "external", the one to transfer). */
  readonly canvas: HTMLCanvasElement
  /** Measure and fit again now (after a font change the host made outside
   *  setStyle; size changes refit on their own). */
  fit(): void
  /** Put the IME textarea back on the caret (after the host moved opts.caret's element). */
  placeInput(): void
  /** Handle a keydown as if it reached this view's textarea (the host's filters,
   *  the clipboard chords, the key encoder), for a key that landed elsewhere on
   *  the page while this view is the host's focused one. */
  key(e: KeyboardEvent): void
  /** paint: "external" only: the host painted a frame with this header; the view
   *  follows it (cursor, badge, canvas CSS size), acks it, then calls onPaint. */
  painted(header: FrameHeader): void
}

export interface MountOptions {
  readOnly?: boolean
  /** The canvas's [left, top] offset in the view; default [6, 4]. */
  padding?: [number, number]
  /** macOS key handling (Cmd chords); default from navigator.platform. */
  isMac?: boolean
  /** Default true: the textarea's focus and blur call host.focus and send focus
   *  reports. false: the host reports focus itself (suzuri's focused pane). */
  reportFocus?: boolean
  /** Default always true: whether the host has a terminal behind the view yet.
   *  While false the pointer ignores presses, selection, mouse reports and link
   *  hover (suzuri: until pane_open has answered). */
  ready?: () => boolean
  /** The element the IME textarea and preedit sit on (default: the terminal's
   *  cursor). They are then placed in viewport coordinates, so it may lie outside
   *  the view; null puts them on the cursor. */
  caret?: () => Element | null
  /** The Screen to paint and measure with (default: a Screen of its own, with
   *  DEFAULT_STYLE). suzuri passes Screens that follow its one window style. */
  screen?: (canvas: HTMLCanvasElement) => ScreenLike
  /** "external": the host paints (a worker with the canvas transferred): the view
   *  makes no canvas context, feed() throws, and the host calls painted(header). */
  paint?: "view" | "external"
  /** Default always false: whether the host holds frames for this view that it has
   *  not fed or painted yet. While true an imgpos waits for the next painted frame
   *  (suzuri: a worker pane's frames held until pane_open answers). */
  framesPending?: () => boolean
  /** Default true: paste chords within 280 ms of the last are one paste. false: every
   *  chord calls host.paste(null), for a host that debounces its own clipboard paste. */
  debouncePaste?: boolean
}

export function mount(el: HTMLElement, host: TermHost, opts?: MountOptions): TermView
