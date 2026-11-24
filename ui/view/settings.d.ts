// suzuri-view/settings: hand-written types. Keep in step with react/Settings.jsx
// (built to dist/settings.js). Needs react, react-dom, @astryxdesign/core and
// lucide-react, and an Astryx <Theme> around it.
import type { ReactElement, ReactNode } from "react"
import type { SettingsPart } from "./settings_rules"

export * from "./settings_rules"

/** The settings, as rsz-settings saves them. A host that shows only some parts may pass only their keys. */
export interface Settings {
  font_face?: string
  font_size_px?: number
  cursor?: string
  profiles?: { name: string; shell?: string; cwd?: string }[]
  active_profile?: string
  ui_theme?: string
  ui_mode?: string
  ui_text_size?: string
  notice_position?: string
  animate_unfocused?: boolean
  restore_sessions?: boolean
  [key: string]: unknown
}

/** The choices and limits: rsz-settings' meta(). Only what the shown parts use is read. */
export interface SettingsMeta {
  /** The settings file, shown in the footer as "Saved to …". */
  path?: string
  fonts?: string[]
  font_min?: number
  font_max?: number
  font_default?: number
  cursors?: string[]
  ui_themes?: string[]
  ui_theme_labels?: Record<string, string>
  ui_modes?: string[]
  ui_mode_labels?: Record<string, string>
  text_sizes?: string[]
  text_size_labels?: Record<string, string>
  notice_positions?: string[]
  notice_labels?: Record<string, string>
  [key: string]: unknown
}

export interface SettingsWindowProps {
  /** The settings now. A new object while open is a change made elsewhere: shown, and kept by Cancel. */
  settings: Settings
  meta: SettingsMeta
  /** Saves a patch. Resolve to the saved settings, or to nothing (the window merges the patch). A rejection shows in the footer. */
  save: (patch: Partial<Settings>) => Promise<Settings | void>
  /**
   * The window is done (Done, Enter, Ctrl/Cmd+, or Cancel, Esc): unmount it.
   * `finished` resolves once a save in flight, and Cancel's restore, has landed.
   */
  onClose: (kept: boolean, finished: Promise<{ changed: boolean; settings: Settings }>) => void
  /** The parts to show; default every part. Hearth's terminal: TERMINAL_PARTS. */
  parts?: SettingsPart[]
  /** The dialog's width in px; default 600. */
  width?: number
  /** An icon beside each theme's name in the Theme list. */
  themeIcon?: (id: string) => ReactNode
  /** The page it opens on; default the first with parts. */
  initialPage?: "look" | "session"
  onPageChange?: (page: "look" | "session") => void
}

/** suzuri's Settings window: an Astryx Dialog, always open while mounted. */
export function SettingsWindow(props: SettingsWindowProps): ReactElement

/** The cursor names and each theme's one-line note. */
export const LABELS: {
  cursors: Record<string, string>
  theme_notes: Record<string, string>
}
