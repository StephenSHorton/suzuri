// Key events to the bytes an xterm-256color shell expects: legacy encoding,
// plus the Kitty keyboard pieces Go suzuri sends (ui/kitty.js).
import { M_APP_CURSOR, M_ALT_SCREEN } from "./frame.js"
import { encodeEnter, encodeAltScreenChord } from "./kitty.js"

const CSI = "\x1b["
const SS3 = "\x1bO"

function modParam(e) {
  const m = 1 + (e.shiftKey ? 1 : 0) + (e.altKey ? 2 : 0) + (e.ctrlKey ? 4 : 0) + (e.metaKey ? 8 : 0)
  return m > 1 ? m : 0
}

const CURSOR = { ArrowUp: "A", ArrowDown: "B", ArrowRight: "C", ArrowLeft: "D", Home: "H", End: "F" }
const TILDE = { Insert: 2, Delete: 3, PageUp: 5, PageDown: 6, F5: 15, F6: 17, F7: 18, F8: 19, F9: 20, F10: 21, F11: 23, F12: 24 }
const FKEY = { F1: "P", F2: "Q", F3: "R", F4: "S" }

// Returns the bytes for a keydown, or null to let the UI handle it.
export function encodeKey(e, modes, isMac) {
  const m = modParam(e)
  if (modes & M_ALT_SCREEN) {
    const chord = encodeAltScreenChord(e, isMac)
    if (chord !== null) return chord
  }
  // macOS: Cmd combos belong to the app; Option is Meta (ESC prefix).
  if (isMac && e.metaKey) return null
  const k = e.key
  if (k in CURSOR) {
    if (m) return `${CSI}1;${m}${CURSOR[k]}`
    return (modes & M_APP_CURSOR ? SS3 : CSI) + CURSOR[k]
  }
  if (k in TILDE) return m ? `${CSI}${TILDE[k]};${m}~` : `${CSI}${TILDE[k]}~`
  if (k in FKEY) return m ? `${CSI}1;${m}${FKEY[k]}` : SS3 + FKEY[k]
  switch (k) {
    case "Enter":
      return encodeEnter(modes, e.shiftKey, e.altKey, e.ctrlKey, false)
    case "Backspace":
      return (e.altKey ? "\x1b" : "") + (e.ctrlKey ? "\x08" : "\x7f")
    case "Tab":
      return e.shiftKey ? `${CSI}Z` : "\t"
    case "Escape":
      return "\x1b"
  }
  if (k.length === 0 || [...k].length !== 1) return null // Shift, Dead, Unidentified...
  if (e.ctrlKey && !e.altKey) {
    const c = k.toLowerCase().charCodeAt(0)
    if (c >= 97 && c <= 122) return String.fromCharCode(c - 96)
    if (k === " " || k === "@" || k === "2") return "\x00"
    if (k === "[" || k === "3") return "\x1b"
    if (k === "\\" || k === "4") return "\x1c"
    if (k === "]" || k === "5") return "\x1d"
    if (k === "^" || k === "6") return "\x1e"
    if (k === "_" || k === "-" || k === "7") return "\x1f"
    return null
  }
  if (e.altKey) {
    // Option on macOS produces composed characters in e.key (å for a);
    // use the physical key so Option works as Meta.
    const base = e.code.startsWith("Key") ? e.code.slice(3).toLowerCase() : k
    return "\x1b" + (e.shiftKey ? base.toUpperCase() : base)
  }
  return k
}
