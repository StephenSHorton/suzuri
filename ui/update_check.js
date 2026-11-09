// What the palette's "Check for updates" says (Go's runUpdateCheck toasts).
// No Tauri here, so tests can import it.
//   found: update_check's result (null when up to date), current: the
//   running version, error: update_check's error, if it failed.
// Returns { text, offer, ms }: offer the update (the Install banner), or
// show text (update.js puts it on a notice card; ms is how long it would
// stay, 0 for until closed).
export function checkMessage({ found, current, error }) {
  if (error != null) {
    const e = String(error)
    if (e.startsWith("dev build")) return { text: "Dev build: updates are off.", offer: false, ms: 5000 }
    return { text: `Update check failed: ${e}`, offer: false, ms: 0 }
  }
  if (found) return { text: "", offer: true, ms: 0 }
  return { text: current ? `Up to date (${current}).` : "Up to date.", offer: false, ms: 4000 }
}
