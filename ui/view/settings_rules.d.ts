// suzuri-view/settings_rules.js: hand-written types. Keep in step with settings_rules.js.
// The Settings window's rules: its parts and pages, Cancel's restore, a
// change made elsewhere, a new profile's name. No DOM, no React.

/** One part of the Settings window. */
export type SettingsPart =
  | "theme" | "mode" | "font" | "size" | "text_size" | "cursor"
  | "new_tab_profile" | "notice" | "animate_unfocused" | "restore_sessions" | "profiles"

/** The Look page's parts, in order. */
export const LOOK_PARTS: SettingsPart[]
/** The Session page's parts, in order. */
export const SESSION_PARTS: SettingsPart[]
/** Every part (the default). */
export const ALL_PARTS: SettingsPart[]
/** The terminal's own parts: font, size, cursor, the profile new tabs use, the profiles. */
export const TERMINAL_PARTS: SettingsPart[]
/** The settings key each part reads and saves. */
export const PART_KEYS: Record<SettingsPart, string>

export interface SettingsPage {
  id: "look" | "session"
  label: string
  parts: SettingsPart[]
}
/** The pages that have at least one of these parts, each with its parts. */
export function pagesFor(parts?: SettingsPart[]): SettingsPage[]

/** A change made elsewhere while the window is open, folded into Cancel's snapshot. */
/** asked: the last value this window saved per key; an outside value equal to it is that save coming back. */
export function foldOutside<T extends Record<string, unknown>>(snap: T, cfg: T, outside: T, asked?: Record<string, unknown>): T
/** What Cancel saves: the keys that differ from the snapshot, never `window`; null when none do. */
export function restorePatch(snap: Record<string, unknown>, cfg: Record<string, unknown>): Record<string, unknown> | null
/** Whether two settings differ, leaving out `window`. */
export function changed(a: Record<string, unknown> | null | undefined, b: Record<string, unknown> | null | undefined): boolean
/** "Profile N": one past the count and past any name already taken. */
export function newProfileName(profiles: { name: string }[]): string
/** The settings path with the home folder as ~. */
export function shortPath(p: string | null | undefined): string
