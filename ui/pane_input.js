// suzuri's side of a pane's keys, typed text and pastes. Each pane is a
// TermView (ui/view) that encodes keys and runs the clipboard chords itself;
// these are the TermHost filters app.js gives it, which take what is
// suzuri's first, in the order the one shared textarea used to:
//   the view's copy/paste chords, Escape on a large image, app shortcuts,
//   then the input bar at a prompt;
//   anything left is the view's, which encodes it and calls host.write.
// No Tauri here (tests import it): app.js passes what it needs in `env`.
//
// pane: app.js's PaneView: {view, bar, barActive(), barKeydown(e),
//   renderBar(), clearQueue(), keyTime}
import { clipShortcut, encodeKey } from "./view/index.js"
import { M_ALT_SCREEN } from "./view/frame.js"

// env: {isMac, shortcut(e, isMac) -> {id, arg} | null, run(id, arg),
//   closeLarge() -> bool,
//   now() -> ms (performance.now), copyOrInterrupt?()}
export function keyFilter(pane, env) {
  return (e) => {
    // The clipboard chords came first and stay the view's, except Ctrl+C
    // with a selection: suzuri copies, and when there was nothing to copy
    // sends ^C to the pane focused by then (env.copyOrInterrupt).
    const clip = clipShortcut(e, env.isMac, pane.view.hasSelection)
    if (clip === "copy-or-interrupt" && env.copyOrInterrupt) {
      e.preventDefault()
      env.copyOrInterrupt()
      return true
    }
    if (clip) return false
    // Escape closes a large image, whichever pane opened it.
    if (e.key === "Escape" && env.closeLarge()) { e.preventDefault(); return true }
    const sc = env.shortcut(e, env.isMac)
    if (sc) { e.preventDefault(); env.run(sc.id, sc.arg); return true }
    // At the prompt the bar has every other key, taken or not.
    if (pane.barActive()) {
      if (pane.barKeydown(e)) e.preventDefault()
      return true
    }
    // ^C from a key drops the queued lines, on an exited pane too (whose
    // view then writes nothing).
    if (encodeKey(e, pane.view.modes, env.isMac) === "\x03") pane.clearQueue()
    // The view encodes it and writes it now (host.write): latency is timed
    // from the key event. Only this keydown's write.
    pane.keyTime = e.timeStamp || env.now()
    queueMicrotask(() => { pane.keyTime = 0 })
    return false
  }
}

// Keys while no pane's textarea has the keyboard (the page itself focused,
// e.g. after the focused pane's textarea was moved or removed). With a
// focused pane (env.pane()) the key is that pane's, as when suzuri's one
// textarea had it: its view takes it (keyFilter, then the encoder) and its
// textarea gets the keyboard back. With none (before the first pane) the
// clipboard chords, Escape on a large image and the app shortcuts still run.
// Keys in a pane, a panel, the palette or a rename box (any other target)
// are theirs, and while an overlay or a panel is open (env.blocked) the
// page's are nobody's, as before.
// env: {isMac, page: [targets that count as no focus], blocked(), pane() ->
//   focused PaneView | null, hasSelection(), clip(kind), closeLarge(),
//   shortcut(e, isMac), run(id, arg)}
export function pageKeys(env) {
  return (e) => {
    if (e.isComposing || !env.page.includes(e.target) || env.blocked()) return
    const pane = env.pane?.()
    if (pane) {
      pane.view.focus()
      pane.view.key(e)
      return
    }
    const clip = clipShortcut(e, env.isMac, env.hasSelection())
    if (clip) { e.preventDefault(); env.clip(clip); return }
    if (e.key === "Escape" && env.closeLarge()) { e.preventDefault(); return }
    const sc = env.shortcut(e, env.isMac)
    if (sc) { e.preventDefault(); env.run(sc.id, sc.arg) }
  }
}

// What host.write does with the view's bytes: `send(data, t0)` with the key
// event's time when the bytes are that key's (keyFilter has already dropped
// the queue for a key's ^C; a ^C after an empty copy is not a key's).
export function writeFrom(pane) {
  return (data) => {
    const t0 = pane.keyTime
    pane.keyTime = 0
    pane.send(data, t0 || undefined)
  }
}

// IME commits and dead keys: into the bar at a prompt.
export function textFilter(pane) {
  return (text) => {
    if (!pane.barActive()) return false
    pane.bar.insert(text)
    pane.renderBar()
    return true
  }
}

// Pasted text: into the bar at a prompt (newlines kept); otherwise the core
// frames it (bracketed paste when the app asked).
export function pasteText(pane, text, invoke) {
  if (pane.barActive()) return pane.bar.insert(text.replace(/\r\n?/g, "\n")), pane.renderBar()
  invoke("pane_paste", { id: pane.id, text })
}

// A paste event: a full-screen app (Claude Code, Grok) gets a pasted image
// as a file path it can attach, ahead of any text (paste_image.js).
// env: {invoke, log, clipboardImage(data), saveClipboardImage(blob) -> Promise<path>}
export function pasteFilter(pane, env) {
  return (data) => {
    const text = data.getData("text/plain")
    const image = pane.view.modes & M_ALT_SCREEN ? env.clipboardImage(data) : null
    if (image) {
      env.saveClipboardImage(image).then(
        (path) => pasteText(pane, path, env.invoke),
        (err) => { env.log("image paste failed", String(err)); if (text) pasteText(pane, text, env.invoke) },
      )
      return true
    }
    if (text) pasteText(pane, text, env.invoke)
    return true
  }
}
