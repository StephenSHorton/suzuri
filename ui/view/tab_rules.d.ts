// suzuri-view/tab_rules.js: hand-written types. Keep in step with tab_rules.js.
// Tab and pane rules: the strip label, the window title, the limits and the
// zoom cycle. No DOM.

/** The most tabs, and the most panes over all tabs. */
export const MAX_TABS: number
export const MAX_PANES: number

export interface TabTitleInput {
  /** The tab's own name (Rename tab). */
  name?: string
  /** The name it froze at on its first split (stickyAt). */
  sticky?: string
  /** Every pane's label, "" when a pane has no title yet. */
  panes: string[]
  /** The focused pane's label. */
  focused?: string
  /** The focused pane is on the alt screen ("app N" instead of "shell N"). */
  alt?: boolean
  /** The tab's index, from 0. */
  index: number
}
/** The strip label: name, sticky (split tabs only), focused title, any title, then "shell N"/"app N"; a leading "Administrator: " goes. */
export function tabTitle(t: TabTitleInput): string
/** The name a tab freezes at when it is split; a renamed or already frozen tab keeps what it has. */
export function stickyAt(name: string | undefined, sticky: string | undefined, focused: string | undefined): string
/** The OS window title: "suzuri — label", or "suzuri". */
export function windowTitle(label: string): string
/** While a pane is maximized: the pane the focus arrows zoom to (tree order, left/up back, right/down on), or null with under two panes. */
export function zoomCycle<P>(panes: P[], current: P, dx: number, dy: number): P | null
