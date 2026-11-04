// suzuri-view/keys.js: hand-written types. Keep in step with keys.js.

/** Turns a keydown into the bytes an xterm-256color app expects, or null. */
export function encodeKey(e: KeyboardEvent, modes: number, isMac: boolean): string | null
