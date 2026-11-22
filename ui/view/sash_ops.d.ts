// suzuri-view/sash_ops.js: hand-written types. Keep in step with sash_ops.js.
// The geometry behind dragging a split's divider (the sash); no DOM.
import type { Rect, PaneNode } from "./layout_ops.js"

/** A divider: its split's direction and the gap strip between the sides. */
export interface Sash { dir: "h" | "v"; rect: Rect }

/** Hit pad in px past each side of the gap. */
export const SASH_PAD: number
/** The smallest a side may get by dragging, in px. */
export const MIN_W: number
export const MIN_H: number

/** The divider strip of a split between its two sides' rects. */
export function sashRect(dir: "h" | "v", ra: Rect, rb: Rect): Rect
/** The first sash under (px, py), padded across its thin axis only, or null. */
export function hitSash<S extends Sash>(sashes: S[], px: number, py: number, pad?: number): S | null
/** a's share when the divider's centre sits at `at`, clamped to RATIO_MIN..RATIO_MAX and `min` px a side; null when there's no room. */
export function ratioAt(start: number, len: number, gap: number, at: number, min?: number): number | null
/** Drops the ratios of node and the splits it runs into in the same direction, so the run's panes come out equal. Whether any changed. */
export function equalizeRun(node: PaneNode | null): boolean

/** Holds PTY resizes back while a divider is dragged. */
export interface ResizeGate {
  readonly held: boolean
  hold(): void
  /** Stops holding and sends what is pending. */
  release(): void
  /** Sends now when nothing is held, else keeps the newest send per key and lets one batch through every `ms`. */
  run(key: unknown, send: () => void): void
}
export function resizeGate(ms: number, now?: () => number, later?: (fn: () => void, ms: number) => unknown): ResizeGate
