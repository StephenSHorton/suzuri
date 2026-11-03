// Kitty keyboard protocol pieces of the key encoder, after Go suzuri's
// internal/ui/kitty_keyboard.go. The engine keeps the progressive-enhancement
// flag stacks (one per screen) and answers CSI ? u; the frame's mode word
// carries the live flags (M_KITTY_SHIFT). Like Go suzuri, only Enter and a few
// chords legacy xterm can't express use CSI u: arrows and the rest stay legacy.
import { M_KITTY_SHIFT } from "./frame.js"

export const KITTY_DISAMBIGUATE = 1
export const KITTY_ALL_KEYS = 8

// The app's live Kitty flags (CSI > flags u) from a frame's mode word.
export function kittyFlags(modes) {
  return (modes >>> M_KITTY_SHIFT) & 31
}

// True when the app asked for disambiguation or every key as an escape.
export function kittyActive(modes) {
  return (kittyFlags(modes) & (KITTY_DISAMBIGUATE | KITTY_ALL_KEYS)) !== 0
}

// 1 + the shift/alt/ctrl/super bitfield, as the protocol counts modifiers.
export function kittyMods(shift, alt, ctrl, sup) {
  return 1 + (shift ? 1 : 0) + (alt ? 2 : 0) + (ctrl ? 4 : 0) + (sup ? 8 : 0)
}

export function csiU(code, mods) {
  return mods <= 1 ? `\x1b[${code}u` : `\x1b[${code};${mods}u`
}

// Enter: plain CR unmodified. With modifiers it is CSI 13 ; mods u, which is
// how Claude Code and Grok tell Shift+Enter (newline) from Enter, except a
// bare Alt+Enter outside the protocol, which stays ESC CR.
export function encodeEnter(modes, shift, alt, ctrl, sup) {
  if (!shift && !alt && !ctrl && !sup) return "\r"
  const mods = kittyMods(shift, alt, ctrl, sup)
  if (!kittyActive(modes) && alt && !shift && !ctrl && !sup) return "\x1b\r"
  return csiU(13, mods)
}

// Chords for full-screen apps (alt screen) that have no legacy encoding,
// from Go suzuri's alt-screen key path:
//   Cmd+Z → SUB (undo, as Ctrl+Z); Cmd+Shift+Z → CSI 122;10u (redo);
//   Ctrl+; ' . , / → CSI u with Ctrl; Ctrl+Shift+letter → CSI u.
// Returns the bytes, or null when the chord isn't one of these.
export function encodeAltScreenChord(e, isMac) {
  const ctrl = e.ctrlKey, shift = e.shiftKey, alt = e.altKey, sup = isMac && e.metaKey
  const letter = e.code && e.code.startsWith("Key") ? e.code.slice(3).toLowerCase() : null
  if (sup && !ctrl && !alt && letter === "z") return shift ? csiU(122, kittyMods(true, false, false, true)) : "\x1a"
  if (ctrl && !sup && !alt && !shift) {
    const punct = { Semicolon: ";", Quote: "'", Period: ".", Comma: ",", Slash: "/" }[e.code]
    if (punct) return csiU(punct.codePointAt(0), kittyMods(false, false, true, false))
  }
  if (ctrl && shift && !sup && !alt && letter) return csiU(letter.codePointAt(0), kittyMods(true, false, true, false))
  return null
}
