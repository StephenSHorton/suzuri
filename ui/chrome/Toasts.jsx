// In-window toasts as Astryx Toast: app.js's short status ("split right",
// "renamed") and the update offer (update.js). One ToastViewport, mounted
// through the overlay host so it sits inside the app's <Theme>; a child
// calls useToast() and hands its function to plain modules through
// showToast(). (A useToast() outside a viewport would start Astryx's
// fallback root outside the Theme.) Host messages that want a sound and a
// history entry still go to the notice panel (ui/host_toast.js).
import { useEffect } from "react"
import { ToastViewport, useToast } from "@astryxdesign/core/Toast"
import { mountOverlay } from "./overlays.js"

let show = null
// Toasts asked for before the viewport's child mounted.
const waiting = []

function Bridge() {
  const toast = useToast()
  useEffect(() => {
    show = toast
    for (const w of waiting.splice(0)) if (!w.dropped) w.dismiss = toast(w.options)
    return () => {
      if (show === toast) show = null
    }
  }, [toast])
  return null
}

let mounted = false
function mount() {
  if (mounted) return
  mounted = true
  // Top right, under the title bar (34px) and clear of the HUD at the
  // bottom right and the input bar along the bottom.
  mountOverlay("toasts", (
    <ToastViewport position="topEnd" inset={{ top: 44 }}>
      <Bridge />
    </ToastViewport>
  ))
}

// F6 belongs to the terminal (programs bind it), but while a toast shows,
// ToastViewport listens on document for F6 to pull focus into the toast.
// Not Astryx: a document listener of our own that ends F6 there. It is added
// after app.js's (the pane's keys and pageKeys, which hands a key typed on
// the page to the pane, still see F6) and before the viewport's, which
// adds its listener each time a toast appears, so that one never fires.
setTimeout(() =>
  document.addEventListener("keydown", (e) => {
    if (e.key === "F6" && !e.target.closest?.("[data-toast-id]")) e.stopImmediatePropagation()
  }),
)

// Show an Astryx toast (useToast's options: body, type, isAutoHide,
// autoHideDuration, endContent, uniqueID, collisionBehavior, onHide).
// Returns a function that dismisses it.
export function showToast(options) {
  mount()
  if (show) return show(options)
  const w = { options, dismiss: null, dropped: false }
  waiting.push(w)
  return () => {
    w.dropped = true
    w.dismiss?.()
  }
}
