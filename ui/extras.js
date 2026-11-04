// Interim shortcuts for #286's panels, until the command palette (#283)
// gives them entries. macOS: ⌘⇧S send, ⌘⇧R receive.
// Windows: Ctrl+Shift+S / R.

import { promptSend, promptReceive } from "./transfer.js"

export function extraShortcut(e, isMac) {
  if (!(isMac ? e.metaKey && e.shiftKey : e.ctrlKey && e.shiftKey) || e.altKey) return false
  const k = e.key.toLowerCase()
  if (k === "s") return promptSend(), true
  if (k === "r") return promptReceive(), true
  return false
}

// Palette entries (#283's palette). app.js defines window.rsz after its
// imports (this file) have run, so register on the next tick.
setTimeout(() => window.rsz?.addCommands([
  { id: "transfer_send", title: "Send file (ticket)…", category: "Transfer", hint: "⌘⇧S / Ctrl+Shift+S", run: () => promptSend() },
  { id: "transfer_receive", title: "Receive ticket…", category: "Transfer", hint: "⌘⇧R / Ctrl+Shift+R", run: () => promptReceive() },
]))
