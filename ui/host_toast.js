// Host feedback as notice cards (Go postHostToast): caffeine, font size,
// copied, settings saved, update checks. app/src/notices.rs picks the sound
// and the kind, so a repeat of one kind replaces its card. No Tauri at
// import, so tests can import the message helpers.
export function hostToast(text) {
  if (!text) return
  globalThis.__TAURI__?.core.invoke("notice_toast", { text: String(text) }).catch(() => {})
}

// Go zoomFont / zoomFontReset. delta 0 is a reset; before and after are the
// sizes around the change, def the default size.
export function fontToast(delta, before, after, def) {
  if (delta === 0) return before === def ? `font ${def}px (default)` : `font ${after}px (reset)`
  return after === before ? `font ${before}px (limit)` : `font ${after}px`
}
