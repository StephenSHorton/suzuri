// What a file drop types into a pane, as other terminals do: each path
// quoted for the shell, separated by spaces, with a trailing space so the
// next word can follow. No Tauri here (tests import it).

// Characters a shell takes as part of a plain word.
const POSIX_SAFE = /^[\w@%+=:,./~-]+$/
const WIN_SAFE = /^[\w@%+=:,./\\~-]+$/

// One path for sh/zsh/bash: as is when safe, else in single quotes (a ' in
// the path closes, escapes and reopens them).
export function quotePosix(p) {
  return POSIX_SAFE.test(p) ? p : `'${p.replaceAll("'", `'\\''`)}'`
}

// One path for PowerShell or cmd: double quotes when it needs them, as
// Windows Terminal does. Windows paths can't hold a ".
export function quoteWindows(p) {
  return WIN_SAFE.test(p) ? p : `"${p}"`
}

// The text a drop of `paths` pastes; "" for none.
export function dropText(paths, windows = false) {
  const q = windows ? quoteWindows : quotePosix
  const ps = (paths || []).filter((p) => typeof p === "string" && p !== "")
  return ps.length ? ps.map(q).join(" ") + " " : ""
}
