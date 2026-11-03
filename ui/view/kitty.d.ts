// suzuri-view/kitty.js: hand-written types. Keep in step with kitty.js.

export const KITTY_DISAMBIGUATE: number
export const KITTY_ALL_KEYS: number
/** The app's live Kitty flags from a frame's mode word. */
export function kittyFlags(modes: number): number
/** True when the app asked for disambiguation or every key as an escape. */
export function kittyActive(modes: number): boolean
/** 1 + the shift/alt/ctrl/super bitfield. */
export function kittyMods(shift: boolean, alt: boolean, ctrl: boolean, sup: boolean): number
export function csiU(code: number, mods: number): string
export function encodeEnter(modes: number, shift: boolean, alt: boolean, ctrl: boolean, sup: boolean): string
/** Alt-screen chords with no legacy encoding, or null. */
export function encodeAltScreenChord(e: KeyboardEvent, isMac: boolean): string | null
