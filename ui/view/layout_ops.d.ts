// suzuri-view/layout_ops.js: hand-written types. Keep in step with layout_ops.js.
// The pane tree suzuri splits, closes, swaps and moves panes in. Every
// operation changes the tree in place; none touch the DOM.

/** A pane: a leaf. P is whatever the app keeps per pane (suzuri: its Pane object). */
export interface Leaf<P = unknown> { pane: P }
/** A split: "h" puts a left of b, "v" puts a above b. ratio is a's share (0.12..0.88); without one the sides are weighed by how many panes they hold. */
export interface Split<P = unknown> { dir: "h" | "v"; a: PaneNode<P>; b: PaneNode<P>; ratio?: number }
export type PaneNode<P = unknown> = Leaf<P> | Split<P>
/** A move or drop edge, as edgeOf names it. */
export type Edge = "left" | "right" | "top" | "bottom"
/** A rect in screen px. */
export interface Rect { x: number; y: number; w: number; h: number }
/** A pane's rect on screen, for neighbor. */
export interface PaneRect<P = unknown> extends Rect { pane: P }

/** The smallest and largest share a split's side may have. */
export const RATIO_MIN: number
export const RATIO_MAX: number

/** pane's leaf in tree, or null. */
export function leafOf<P>(tree: PaneNode<P> | null, pane: P): Leaf<P> | null
/** The split directly holding pane's leaf, or null for a lone pane. */
export function parentOf<P>(tree: PaneNode<P> | null, pane: P, node?: PaneNode<P> | null): Split<P> | null
/** a's current share of a split: its ratio, or the pane-count weight. */
export function shareOf(node: Split): number
/** Trades two panes' places. False when either is missing or they are the same. */
export function swapPanes<P>(tree: PaneNode<P> | null, a: P, b: P): boolean
/** Takes pane's leaf out, folding its sibling into the parent. Returns the new root: null when pane was the only one. */
export function removePane<P>(tree: PaneNode<P> | null, pane: P): PaneNode<P> | null
/** A move edge's canonical name (also takes "up" and "down", any case), or "" for a bad one. */
export function edgeOf(edge: string): Edge | ""
/** Moves pane next to target on one edge. Returns the new root, or null when the move isn't possible. */
export function dockBeside<P>(tree: PaneNode<P> | null, pane: P, target: P, edge: string): PaneNode<P> | null
/** Grows pane's side of its split by delta (shrinks with a negative one). */
export function adjustRatio<P>(tree: PaneNode<P> | null, pane: P, delta: number): boolean
/** Sets pane's split to half and half. */
export function equalize<P>(tree: PaneNode<P> | null, pane: P): boolean
/** Turns pane's split from side by side to stacked, or back. */
export function rotate<P>(tree: PaneNode<P> | null, pane: P): boolean
/** Swaps the two sides of pane's split. */
export function swapSides<P>(tree: PaneNode<P> | null, pane: P): boolean
/** The pane next to `from` in direction (dx, dy), each -1, 0 or 1; null at the edge. */
export function neighbor<P>(rects: PaneRect<P>[], from: P, dx: number, dy: number): P | null
/** The edge of rect nearest the pointer, for a pane dropped there; "" for an empty rect. */
export function dropEdge(rect: Rect, px: number, py: number): Edge | ""
/** A deep copy of the tree's nodes (the panes themselves are shared). */
export function cloneTree<P, T extends PaneNode<P> | null | undefined>(n: T): T
/** Whether two trees have the same splits and the same panes in the same places (ratios aside). */
export function sameShape<P>(a: PaneNode<P> | null, b: PaneNode<P> | null): boolean
/** Whether dropping pane on target's edge would change the layout. */
export function dropChanges<P>(tree: PaneNode<P> | null, pane: P, target: P, edge: string): boolean
