// The bridge between the plain-JS app (app.js, activity.js, bell.js,
// caffeine.js) and the React chrome (chrome/*.jsx). The app writes what the
// chrome shows with `chrome.set`; the chrome calls back through
// `chrome.actions`, which the app fills in. Nothing on the keystroke-to-paint
// path goes through here: panes, splits and keys stay in app.js.
//
// State:
//   tabs: [{ id, label, title, index, panes, active, activity, renaming }]
//   unread: notification count (bell.js)
//   caffeine: caffeine Status from Rust, or null (caffeine.js)
//   maximized, inactive: the window (Windows caption buttons)

let state = { tabs: [], unread: 0, caffeine: null, maximized: false, inactive: false, bellOpen: false }
const subs = new Set()

export const chrome = {
  get: () => state,
  set(patch) {
    const p = typeof patch === "function" ? patch(state) : patch
    if (!p || Object.keys(p).length === 0) return
    state = { ...state, ...p }
    for (const f of subs) f()
  },
  subscribe(f) {
    subs.add(f)
    return () => subs.delete(f)
  },
  // Filled by the modules that own each action.
  actions: {
    showTab: () => {},
    closeTab: () => {},
    newTab: () => {},
    startRename: () => {},
    finishRename: () => {},
    toggleBell: () => {},
    toggleCaffeine: () => {},
  },
}

window.rszChrome = chrome
