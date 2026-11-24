// suzuri-view/palette: hand-written types. Keep in step with react/Palette.jsx
// (built to dist/palette.js). Needs react, react-dom and @astryxdesign/core,
// and an Astryx <Theme> around it.
import type { ReactElement } from "react"
import type { PaletteCommand } from "./palette_rules"

export * from "./palette_rules"

export interface PaletteProps<C extends PaletteCommand = PaletteCommand> {
  /** The commands, in order, grouped by category. */
  commands: C[]
  /** The chosen command, called after the palette has closed and handed focus back. */
  onRun: (command: C) => void
  /** The palette is done (a command chosen, Esc, or the open chord again): unmount it. */
  onClose: () => void
  /** A keydown that closes it again; default Cmd/Ctrl + K or P. */
  isToggleKey?: (e: KeyboardEvent | { key: string; ctrlKey: boolean; metaKey: boolean }) => boolean
  /** The query field's hint; default "Type a command…". */
  placeholder?: string
}

/** suzuri's command palette: an Astryx CommandPalette, open while mounted. */
export function Palette<C extends PaletteCommand>(props: PaletteProps<C>): ReactElement

export interface PaletteViewProps<C extends PaletteCommand = PaletteCommand> {
  items: C[]
  /** It closed: the command to run, or null for a dismissal. Runs nothing itself. */
  onDone: (command: C | null) => void
  /** The open chord came again. */
  onToggle: () => void
  isToggleKey?: PaletteProps["isToggleKey"]
  placeholder?: string
}

/** The bare view under Palette, for a host that runs commands its own way. */
export function PaletteView<C extends PaletteCommand>(props: PaletteViewProps<C>): ReactElement
