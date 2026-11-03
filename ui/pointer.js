// suzuri's pointer: the view package's (ui/view/pointer.js) wired to the
// pane_* commands, with the window's cell size (frame.js cellMetrics).
import { installPointer as install } from "./view/pointer.js"
import { cellMetrics } from "./frame.js"

export function installPointer(pane, { invoke, isMac, focus, paste }) {
  return install(pane, {
    isMac, focus, paste,
    metrics: cellMetrics,
    live: () => !!pane.id,
    select: (op, c) => invoke("pane_select", { id: pane.id, op, ...c }),
    mouse: (ev) => invoke("pane_mouse", { id: pane.id, ev }),
    scroll: (delta) => invoke("pane_scroll", { id: pane.id, delta }),
    link: (col, row) => invoke("pane_link", { id: pane.id, col, row }),
    // rszDryOpen: the selftest's hook (selftest.js), so a test click opens nothing.
    openUrl: (url) => {
      if (window.rszDryOpen) window.rszDryOpen(url)
      else invoke("open_url", { url }).catch((err) => console.warn(err))
    },
  })
}
