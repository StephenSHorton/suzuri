// suzuri-view/palette_rules.js: hand-written types. Keep in step with palette_rules.js.
// The command palette's filter and chord labels. No DOM, no React.

/** A palette command. Anything else on it is the host's (ids, args) and comes back to onRun. */
export interface PaletteCommand {
  title: string
  /** The group it shows under. */
  category?: string
  /** A chord label ("Ctrl+Shift+T", "⌘⇧W", "F2"; alternatives joined by " · " or " / ") shows as keys; anything else as text. */
  hint?: string
  [key: string]: unknown
}

/** The commands with every word of the query in their title, hint or category. */
export function filterCommands<C extends PaletteCommand>(items: C[], query: string): C[]
/** A hint as Kbd key strings, one per alternative; null when it is not all chords. */
export function hintChords(hint: string | null | undefined): string[] | null
