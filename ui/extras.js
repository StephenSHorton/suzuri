// Interim shortcuts for #286's panels, until the command palette (#283)
// gives them entries. macOS: ⌘⇧S send, ⌘⇧R receive, ⌘⇧L workspace.
// Windows: Ctrl+Shift+S / R / L.

import { promptSend, promptReceive } from "./transfer.js"
import { openWorkspace } from "./workspace.js"

export function extraShortcut(e, isMac) {
  if (!(isMac ? e.metaKey && e.shiftKey : e.ctrlKey && e.shiftKey) || e.altKey) return false
  const k = e.key.toLowerCase()
  if (k === "s") return promptSend(), true
  if (k === "r") return promptReceive(), true
  if (k === "l") return openWorkspace(), true
  return false
}
