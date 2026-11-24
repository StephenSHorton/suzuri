// The Settings window's rules, with no React and no Tauri: which parts it
// can show, what Cancel puts back, how a change made elsewhere folds in, and
// a new profile's name. The window itself is suzuri-view/settings
// (react/Settings.jsx); node's tests import this file.

// Every part of the window, page by page, in the order they show.
export const LOOK_PARTS = ["theme", "mode", "font", "size", "text_size", "cursor"]
export const SESSION_PARTS = ["new_tab_profile", "notice", "animate_unfocused", "restore_sessions", "profiles"]
export const ALL_PARTS = [...LOOK_PARTS, ...SESSION_PARTS]

// The terminal's own parts: what an app that hosts suzuri's terminal but not
// its window (Hearth) shows. Font, size, cursor, the profiles and the
// profile new tabs use.
export const TERMINAL_PARTS = ["font", "size", "cursor", "new_tab_profile", "profiles"]

// The settings key each part reads and saves.
export const PART_KEYS = {
  theme: "ui_theme",
  mode: "ui_mode",
  font: "font_face",
  size: "font_size_px",
  text_size: "ui_text_size",
  cursor: "cursor",
  new_tab_profile: "active_profile",
  notice: "notice_position",
  animate_unfocused: "animate_unfocused",
  restore_sessions: "restore_sessions",
  profiles: "profiles",
}

// The pages that have at least one of these parts, each with its parts.
export function pagesFor(parts = ALL_PARTS) {
  const keep = new Set(parts)
  return [
    { id: "look", label: "Look", parts: LOOK_PARTS.filter((p) => keep.has(p)) },
    { id: "session", label: "Session", parts: SESSION_PARTS.filter((p) => keep.has(p)) },
  ].filter((p) => p.parts.length > 0)
}

// An open Settings screen and a change made elsewhere (another window,
// Hearth, a hand edit): Esc puts back what was there when the screen opened,
// so each key the outside change set becomes part of that, and Esc undoes
// only what was changed on this screen. Returns the new snapshot.
//
// asked holds the last value this screen saved for each key: a host that
// passes its own saves back (Hearth follows its settings event) brings
// those values in again, and they are this screen's, not someone else's.
export function foldOutside(snap, cfg, outside, asked = {}) {
  const next = { ...snap }
  for (const k of Object.keys(outside)) {
    const v = JSON.stringify(outside[k])
    if (v === JSON.stringify(cfg[k])) continue
    if (k in asked && v === JSON.stringify(asked[k])) continue
    next[k] = structuredClone(outside[k])
  }
  return next
}

// What Cancel saves to put back the settings from when the screen opened:
// only the keys that differ now, and never the window frame (it saves
// itself meanwhile). null when nothing differs.
export function restorePatch(snap, cfg) {
  const patch = {}
  for (const k of Object.keys(snap || {})) {
    if (k === "window") continue
    if (JSON.stringify(snap[k]) !== JSON.stringify(cfg?.[k])) patch[k] = structuredClone(snap[k])
  }
  return Object.keys(patch).length ? patch : null
}

// Whether two settings differ, leaving out the window frame.
export function changed(a, b) {
  const { window: _a, ...x } = a || {}
  const { window: _b, ...y } = b || {}
  return JSON.stringify(x) !== JSON.stringify(y)
}

// A new profile's name: "Profile N", N one past the count and past any name
// already taken (case aside).
export function newProfileName(profiles) {
  let n = profiles.length + 1
  while (profiles.some((p) => String(p.name).toLowerCase() === `profile ${n}`)) n++
  return `Profile ${n}`
}

// The settings path with the home folder as ~, for the footer.
export function shortPath(p) {
  return String(p ?? "").replace(/^\/Users\/[^/]+|^\/home\/[^/]+|^[A-Za-z]:\\Users\\[^\\]+/, "~")
}
