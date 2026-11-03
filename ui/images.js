// suzuri's side of the inline images: each pane's view (ui/view/images.js)
// has its own large-image modal, one on the page at a time. Escape closes
// it whichever pane opened it (pane_input.js keyFilter), as when suzuri had
// one textarea for every pane.
export function closeLarge() {
  const m = document.querySelector(".img-modal")
  if (!m) return false
  m.remove()
  return true
}
