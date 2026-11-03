// suzuri's inline images: the view package's (ui/view/images.js). Each pane
// has its own large-image modal there; Escape closes whichever is open.
export { PaneImages } from "./view/images.js"

export function closeLarge() {
  const m = document.querySelector(".img-modal")
  if (!m) return false
  m.remove()
  return true
}
