// Files dropped on the window paste their paths into the focused pane, as
// in any terminal (quoted, ui/drop_paths.js). suzuri no longer sends files.
import { dropText } from "./drop_paths.js"

const { listen } = window.__TAURI__.event
const windows = navigator.platform.startsWith("Win")

listen("tauri://drag-drop", ({ payload }) => {
  const text = dropText(payload?.paths, windows)
  if (text) window.rsz?.pasteFocused?.(text)
})
