// suzuri-view: one terminal view. mount(el, host) builds the pane's DOM in
// `el` and returns a TermView; the frames it is fed are painted on the next
// animation frame and acked to the host, and keys, IME, paste, the mouse
// and the wheel come back out as host calls. No Tauri, no page globals:
// every listener, timer and observer it adds is its own and unmount()
// removes them. Types: index.d.ts.
import { Screen, DEFAULT_STYLE, FORMAT_VERSION, fontStyle, measureCell, cssSize, M_ALT_SCREEN, M_ALT_SCROLL, M_APP_CURSOR, M_FOCUS } from "./frame.js"
import { encodeKey } from "./keys.js"
import { installPointer } from "./pointer.js"
import { PaneImages } from "./images.js"

export { FORMAT_VERSION, DEFAULT_STYLE }
export { Screen, decodeHeader, measureCell } from "./frame.js"
export { encodeKey } from "./keys.js"

// The bundled Gohu, ready for setStyle({ font: GOHU_FONT }): its file ships
// in fonts/, and the url resolves next to this module (a bundler that
// follows new URL(..., import.meta.url) copies it).
export const GOHU_FONT = Object.freeze({
  family: DEFAULT_STYLE.family,
  px: DEFAULT_STYLE.px,
  name: "Gohu",
  url: new URL("./fonts/GohuFontuni14NerdFontMono-Regular.ttf", import.meta.url).href,
  bundled: true,
})

// Url'd font faces each document holds for its views: one FontFace per
// name and url, counted, and taken off doc.fonts when its last view lets go
// (a new font or unmount), so setStyle repeats and unmounts leave no faces.
const faces = new WeakMap() // document -> Map(key -> {face, refs})

function holdFace(doc, win, name, url) {
  let byKey = faces.get(doc)
  if (!byKey) faces.set(doc, (byKey = new Map()))
  const key = `${name}
${url}`
  let e = byKey.get(key)
  if (!e) {
    e = { face: new win.FontFace(name, `url(${url})`), refs: 0 }
    doc.fonts.add(e.face)
    byKey.set(key, e)
  }
  e.refs++
  return key
}

function releaseFace(doc, key) {
  const byKey = faces.get(doc)
  const e = byKey?.get(key)
  if (!e || --e.refs > 0) return
  byKey.delete(key)
  doc.fonts.delete?.(e.face)
}

const PASTE_DEBOUNCE_MS = 280 // one key press can arrive as a keydown and a menu action

// Copy and paste chords (Go suzuri's): ⌘C/⌘V on macOS; elsewhere
// Ctrl+Shift+C/V, Ctrl+Insert, Shift+Insert, Ctrl+V, and Ctrl+C when there
// is a selection (otherwise it stays ^C). "copy-or-interrupt" is that
// Ctrl+C: it copies, and sends ^C when there turned out to be nothing to copy.
export function clipShortcut(e, isMac, hasSelection) {
  const k = (e.key || "").toLowerCase()
  if (e.altKey) return null
  if (isMac) {
    if (!e.metaKey || e.ctrlKey || e.shiftKey) return null
    if (k === "c") return "copy"
    if (k === "v") return "paste"
    return null
  }
  if (e.metaKey) return null
  if (e.ctrlKey && e.shiftKey && k === "c") return "copy"
  if (e.ctrlKey && e.shiftKey && k === "v") return "paste"
  if (e.ctrlKey && !e.shiftKey && e.key === "Insert") return "copy"
  if (e.shiftKey && !e.ctrlKey && e.key === "Insert") return "paste"
  if (e.ctrlKey && !e.shiftKey && k === "v") return "paste"
  if (e.ctrlKey && !e.shiftKey && k === "c" && hasSelection) return "copy-or-interrupt"
  return null
}

// What a view measures with when its host paints (opts.paint "external"):
// a Screen's style and cell metrics with no canvas context, so the host can
// still transfer the canvas to a worker.
class Measure {
  constructor(doc, style) {
    this.doc = doc
    this.style = { ...DEFAULT_STYLE, ...style }
    this.cell = null
  }
  setStyle(st) { Screen.prototype.setStyle.call(this, st) }
  cellMetrics() { return this.cell || (this.cell = measureCell(this.style, this.doc)) }
  resetMetrics() { this.cell = null }
  fontSpec() { return `${this.style.px}px ${this.style.family}` }
}

export function mount(el, host, opts = {}) {
  const doc = el.ownerDocument
  const win = doc.defaultView || globalThis
  const isMac = opts.isMac ?? (win.navigator?.platform || "").startsWith("Mac")
  const [padX, padY] = opts.padding || [6, 4]
  const external = opts.paint === "external"
  const reportFocus = opts.reportFocus ?? true
  const caret = opts.caret || null
  // Whether the host has a terminal behind the view yet (opts.ready); until
  // then the pointer takes no press, selection or link hover.
  const hostReady = opts.ready || (() => true)
  // Frames the host holds and has not handed over yet (opts.framesPending):
  // an imgpos waits for the next paint while there are any.
  const framesPending = opts.framesPending || (() => false)
  // Paste chords are debounced here unless the host debounces its own
  // clipboard paste (opts.debouncePaste false).
  const debouncePaste = opts.debouncePaste ?? true
  let readOnly = !!opts.readOnly
  let alive = true

  // ---- DOM: body > canvas, cursor, scrolled badge, error, textarea, preedit
  // (the pointer adds .link-hover, the images their .imgs layer). The body's
  // size and clipping are the stylesheet's (view.css), so an app's own
  // layout (suzuri's .pane .body) decides them.
  const make = (tag, cls) => { const d = doc.createElement(tag); if (cls) d.className = cls; return d }
  const body = make("div", "rsz-view body")
  body.style.position = "relative"
  body.style.setProperty("--rsz-pad-x", `${padX}px`)
  body.style.setProperty("--rsz-pad-y", `${padY}px`)
  const canvas = make("canvas")
  Object.assign(canvas.style, { position: "absolute", left: `${padX}px`, top: `${padY}px` })
  const cursor = make("div", "cursor")
  Object.assign(cursor.style, { position: "absolute", pointerEvents: "none" })
  const scrolled = make("div", "scrolled")
  scrolled.hidden = true
  const error = make("div", "rsz-error")
  error.hidden = true
  // One textarea per view holds this view's keyboard focus, so paste and IME
  // events land here. It sits on the cursor so IME candidate windows open
  // there; transparent text and caret keep it invisible.
  const input = make("textarea")
  input.setAttribute("autocapitalize", "off")
  input.setAttribute("autocomplete", "off")
  input.spellcheck = false
  Object.assign(input.style, {
    position: "absolute", left: "0", top: "0", width: "1px", height: "16px", padding: "0", border: "0",
    opacity: "0", resize: "none", overflow: "hidden", zIndex: "-1", pointerEvents: "none",
  })
  const preedit = make("div", "preedit rsz-preedit")
  preedit.hidden = true
  Object.assign(preedit.style, { position: "absolute", pointerEvents: "none", whiteSpace: "pre" })
  body.append(canvas, cursor, scrolled, error, input)
  el.append(body)

  // With a caret element (opts.caret) the textarea and preedit sit on its
  // client rect, in viewport coordinates, so it may lie outside the view;
  // the preedit then goes on the document's body when a composition starts,
  // above every view (as suzuri's one preedit was).
  if (caret) input.style.position = preedit.style.position = "fixed"
  const preeditHome = caret ? doc.body : body
  if (!caret) body.append(preedit)

  const screen = opts.screen
    ? opts.screen(canvas)
    : external ? new Measure(doc, DEFAULT_STYLE) : new Screen(canvas, win.devicePixelRatio || 1, DEFAULT_STYLE)
  const metrics = () => screen.cellMetrics()
  const images = new PaneImages(body, { padding: [padX, padY] })

  // The state the pointer reads (pointer.js); `modes` is the last frame's.
  const pane = { body, canvas, size: [0, 0], modes: 0, exited: false, hasSelection: false, wheelToApp: null, onFrame: null }
  // What may reach the host: nothing after unmount, and in read-only mode no
  // write, paste, mouse, scroll, select or copy. After the terminal exits it
  // takes no more bytes (write, paste), but its history can still be
  // scrolled, selected and copied, as in suzuri (the pointer refuses a new
  // press on an exited pane itself).
  const can = () => alive && !readOnly
  const canSend = () => can() && !pane.exited
  const write = (data) => { if (canSend()) host.write(data) }

  const listeners = []
  const on = (t, type, fn, o) => { t.addEventListener(type, fn, o); listeners.push([t, type, fn, o]) }

  const disposePointer = installPointer(pane, {
    isMac,
    metrics,
    live: () => alive && hostReady(),
    focus: () => view.focus(),
    paste: () => paste(),
    select: (op, c) => { if (can()) host.select(op, c.col, c.row, c.right) },
    mouse: (ev) => { if (can()) host.mouse(ev) },
    scroll: (delta) => { if (can()) host.scroll(delta) },
    link: (col, row) => (alive ? host.link(col, row) : Promise.resolve(null)),
    openUrl: (url) => { if (alive) host.openUrl(url) },
  })

  // ---- frames: everything queued is painted in one animation frame, then
  // the last frame's seq is acked (one frame in flight on the host's side).
  const queue = []
  let raf = 0
  let pendingPlace = null
  function schedule() {
    if (raf || !alive) return
    raf = win.requestAnimationFrame(paintQueued)
  }
  function paintQueued() {
    raf = 0
    if (!alive || !queue.length) return
    const t = now()
    const frames = queue.length
    let h
    let bytes = 0
    try {
      for (const [buf, off] of queue) {
        h = screen.apply(buf, off)
        bytes += buf.byteLength - off
      }
    } catch (err) {
      queue.length = 0
      return showError(String(err.message || err))
    }
    // Paint time is the applies alone, not the DOM work that follows them.
    const stats = { frames, bytes, ms: now() - t }
    queue.length = 0
    hideError()
    afterPaint(h)
    host.ack(h.seq >>> 0)
    host.onPaint?.(h, stats)
  }
  const now = () => win.performance?.now?.() ?? Date.now()

  function showError(text) {
    error.textContent = text
    error.hidden = false
  }
  // A frame that paints, or a new style, clears the last error.
  function hideError() {
    if (error.hidden) return
    error.hidden = true
    error.textContent = ""
  }

  // DOM that follows a painted frame: modes, cursor, the scrolled badge.
  // The caller then acks the frame and calls the host's onPaint.
  function afterPaint(h) {
    pane.modes = h.modes
    if (pendingPlace) {
      images.place(pendingPlace)
      pendingPlace = null
    }
    const { cw, ch } = metrics()
    const cs = cursor.style
    cursor.hidden = h.shape === 0
    cursor.className = "cursor " + (["", "", "underline", "beam", "hollow"][h.shape] || "")
    cs.left = `${padX + h.cx * cw}px`
    cs.top = `${padY + h.cy * ch}px`
    cs.width = `${cw}px`
    cs.height = `${ch}px`
    cs.setProperty("--ch", `${ch}px`)
    scrolled.hidden = h.offset === 0
    if (h.offset) scrolled.textContent = `↑ ${h.offset} / ${h.history}`
    pane.onFrame?.()
    placeInput()
  }

  // The textarea and the IME preedit sit on the terminal's cursor, or on the
  // host's caret element. That one is read from layout, so only while the
  // textarea has focus or a composition shows.
  function placeInput() {
    if (!alive) return
    if (caret) {
      if (doc.activeElement !== input && preedit.hidden) return
      const c = (caret() || cursor).getBoundingClientRect()
      input.style.left = `${c.left}px`
      input.style.top = `${c.top}px`
      input.style.height = `${c.height || metrics().ch}px`
      if (!preedit.hidden) {
        preedit.style.left = input.style.left
        preedit.style.top = input.style.top
      }
      return
    }
    const { ch } = metrics()
    input.style.left = cursor.style.left || `${padX}px`
    input.style.top = cursor.style.top || `${padY}px`
    input.style.height = `${ch}px`
    preedit.style.left = input.style.left
    preedit.style.top = input.style.top
    preedit.style.lineHeight = `${ch}px`
  }

  // ---- size: cols x rows that fit the body, and the cell's pixels.
  let cell = null
  function sendCell() {
    const { cw, ch } = metrics()
    if (cell && cw === cell[0] && ch === cell[1]) return
    cell = [cw, ch]
    images.setCell(cw, ch)
    host.cellPx(Math.round(cw), Math.round(ch))
  }
  function fit() {
    if (!alive) return
    sendCell()
    const { cw, ch } = metrics()
    const w = body.clientWidth - 2 * padX
    const hgt = body.clientHeight - 2 * padY
    if (!(w > 0) || !(hgt > 0)) return
    const cols = Math.max(2, Math.floor(w / cw))
    const rows = Math.max(1, Math.floor(hgt / ch))
    if (cols === pane.size[0] && rows === pane.size[1]) return
    pane.size = [cols, rows]
    host.resize(cols, rows)
  }
  const ro = win.ResizeObserver ? new win.ResizeObserver(() => fit()) : null
  ro?.observe(body)

  // ---- keys, IME, paste, copy
  async function copy() {
    if (!can()) return false
    return host.copy()
  }
  // Committed text (IME, dead keys): the host's textFilter may take it.
  function text(v) {
    if (!canSend()) return
    if (host.textFilter?.(v)) return
    host.write(v)
  }
  let lastPaste = 0
  function paste() {
    if (!canSend()) return
    const now = win.performance?.now?.() ?? Date.now()
    if (debouncePaste) {
      if (now - lastPaste < PASTE_DEBOUNCE_MS) return
      lastPaste = now
    }
    host.paste(null)
  }
  // A key for this view: its textarea's keydown, or one the host hands over
  // (view.key) while the keyboard sits elsewhere.
  function keydown(e) {
    if (!alive || e.isComposing) return
    if (host.keyFilter?.(e)) return
    const clip = clipShortcut(e, isMac, pane.hasSelection)
    if (clip) {
      e.preventDefault()
      if (clip === "paste") paste()
      else if (clip === "copy") copy()
      else copy().then((had) => { if (!had) write("\x03") })
      return
    }
    if (e.key === "Escape" && images.closeLarge()) { e.preventDefault(); return }
    // An encodable key is the terminal's even after the exit, when nothing
    // is sent: no browser default, such as Tab moving focus. (A read-only
    // view leaves keys to the page.)
    if (!can()) return
    const data = encodeKey(e, pane.modes, isMac)
    if (data == null) return
    e.preventDefault()
    write(data)
  }
  on(input, "keydown", keydown)
  // Text that arrives without a usable keydown (IME commit, dead keys).
  on(input, "input", (e) => {
    const v = input.value
    input.value = ""
    if (v && !e.isComposing) text(v)
  })
  on(input, "compositionstart", () => {
    if (!preeditHome.contains(preedit)) preeditHome.append(preedit)
    preedit.hidden = false
    placeInput()
  })
  on(input, "compositionupdate", (e) => {
    preedit.textContent = e.data
    placeInput()
  })
  on(input, "compositionend", () => {
    preedit.hidden = true
    preedit.textContent = ""
    if (caret) preedit.remove() // one on the page at a time
    const v = input.value
    input.value = ""
    if (v) text(v)
  })
  on(input, "paste", (e) => {
    e.preventDefault()
    if (!canSend()) return
    const cd = e.clipboardData
    if (cd && host.pasteFilter?.(cd)) return
    const t = cd?.getData("text/plain")
    if (t) host.paste(t)
  })
  // Focus reports follow the textarea's focus, unless the host reports
  // focus itself (opts.reportFocus false, as suzuri does for its panes).
  if (reportFocus) {
    on(input, "focus", () => {
      if (!alive) return
      host.focus(true)
      if (pane.modes & M_FOCUS) write("\x1b[I")
    })
    on(input, "blur", () => {
      if (!alive) return
      host.focus(false)
      if (pane.modes & M_FOCUS) write("\x1b[O")
    })
  }

  // ---- the wheel: to the app when it reports the mouse, arrow keys on the
  // alt screen when it asked for them, else the history.
  let wheel = 0
  on(body, "wheel", (e) => {
    const { ch } = metrics()
    wheel += e.deltaY / ch
    const lines = Math.trunc(wheel)
    if (!lines) return
    wheel -= lines
    if (!can()) return
    if (pane.wheelToApp?.(e, lines)) return
    if (pane.modes & M_ALT_SCREEN) {
      if (pane.modes & M_ALT_SCROLL) {
        const seq = ((pane.modes & M_APP_CURSOR ? "\x1bO" : "\x1b[") + (lines < 0 ? "A" : "B")).repeat(Math.abs(lines))
        // host.wheelKeys, when the host has it, takes them as they are
        // (suzuri: straight to the PTY, not timed as typed input).
        if (host.wheelKeys) host.wheelKeys(seq)
        else write(seq)
      }
      return
    }
    host.scroll(-lines)
  }, { passive: true })

  // ---- style: a font change re-measures, refits and asks for a full frame;
  // colours alone need only the full frame.
  let fontGen = 0
  let faceKey = null // this view's hold in `faces`, if its font has a url
  function restyled(font) {
    if (!alive) return
    if (font) {
      preedit.style.font = screen.fontSpec()
      fit()
    }
    host.redraw()
  }

  // A frame the host painted (opts.paint "external"): the canvas's CSS size
  // follows its grid and cells here, since the host's canvas has no style.
  let css = null
  function paintedOutside(h) {
    const m = metrics()
    if (!css || css[0] !== h.cols || css[1] !== h.rows || css[2] !== m) {
      css = [h.cols, h.rows, m]
      cssSize(canvas, h.cols, h.rows, m)
    }
    hideError()
    afterPaint(h)
    host.ack(h.seq >>> 0)
    host.onPaint?.(h)
  }

  const view = {
    feed(frame, off = 0) {
      if (!alive) return
      if (external) throw new Error(`feed: this view's host paints (paint: "external"); call painted(header)`)
      const buf = toBuffer(frame)
      if (buf.byteLength - off < 32) {
        // Refused, not acked, as a frame in another version is.
        queue.length = 0
        return showError(`terminal frame of ${Math.max(0, buf.byteLength - off)} bytes, shorter than its 32-byte header`)
      }
      const version = new DataView(buf, off).getUint8(11)
      if (version !== FORMAT_VERSION) {
        // Refused, not acked: the host sends nothing more until a view that
        // knows the format takes over.
        queue.length = 0
        return showError(`terminal format v${version}, this app knows v${FORMAT_VERSION}`)
      }
      queue.push([buf, off])
      schedule()
    },

    message(m) {
      if (!alive) return
      const msg = typeof m === "string" ? JSON.parse(m) : m
      switch (msg.t) {
        case "title": view.title = msg.title || ""; break
        case "exit":
          pane.exited = true
          body.classList.add("exited")
          break
        case "img": images.add(msg); break
        case "imgdrop": images.drop(msg.ids); break
        case "imgpos":
          // With the frame it goes with, if that frame is still queued.
          // (Queued here, or still held by the host.)
          if (queue.length || framesPending()) pendingPlace = msg.list
          else images.place(msg.list)
          break
        case "progress":
          body.dataset.progress = msg.state ? String(msg.state) : ""
          body.style.setProperty("--progress", `${msg.percent}%`)
          break
      }
      host.onMessage?.(msg)
    },

    setStyle(s) {
      if (!alive) return
      hideError()
      const st = {}
      if (s.bg) { st.void = s.bg; body.style.background = s.bg }
      if (s.selection) st.selection = s.selection
      if (s.cursor) body.style.setProperty("--rsz-cursor", s.cursor)
      const f = s.font
      if (f) Object.assign(st, fontStyle(f.family, f.px, f.bundled ?? !!f.url, f.face || ""))
      screen.setStyle(st)
      restyled(!!f)
      if (!f) return
      // Measure again once the face has loaded (until then the canvas
      // measures a fallback).
      const gen = ++fontGen
      const fonts = doc.fonts
      if (!fonts?.load) return
      let ready = Promise.resolve()
      const prev = faceKey
      faceKey = null
      if (f.url && win.FontFace) {
        faceKey = holdFace(doc, win, f.name || firstFamily(f.family), f.url)
        ready = faces.get(doc).get(faceKey).face.load()
      }
      if (prev) releaseFace(doc, prev)
      ready.then(() => fonts.load(screen.fontSpec())).then(() => {
        if (!alive || gen !== fontGen) return
        screen.resetMetrics()
        restyled(true)
      }, () => {})
    },

    setReadOnly(on) {
      readOnly = !!on
      body.classList.toggle("read-only", readOnly)
    },

    focus() {
      if (!alive) return
      input.focus({ preventScroll: true })
      placeInput()
    },

    fit() { fit() },

    key(e) { keydown(e) },

    placeInput() { placeInput() },

    painted(h) {
      if (!alive) return
      if (!external) throw new Error(`painted: only for a view whose host paints (paint: "external")`)
      paintedOutside(h)
    },

    unmount() {
      if (!alive) return
      // Removing a focused textarea need not fire blur: tell the host here.
      const hadFocus = doc.activeElement === input
      alive = false
      if (raf) win.cancelAnimationFrame(raf)
      raf = 0
      queue.length = 0
      ro?.disconnect()
      disposePointer()
      for (const [t, type, fn, o] of listeners) t.removeEventListener(type, fn, o)
      listeners.length = 0
      // A large image this view opened stays up, as suzuri's did when its
      // pane closed: a click on it (or the app) closes it.
      images.destroy()
      if (faceKey) releaseFace(doc, faceKey)
      faceKey = null
      body.remove()
      preedit.remove()
      if (hadFocus && reportFocus) host.focus(false)
    },

    title: "",
    get modes() { return pane.modes },
    get hasSelection() { return pane.hasSelection },
    canvas,
  }

  placeInput()
  fit()
  return view
}

function toBuffer(f) {
  if (f instanceof ArrayBuffer) return f
  if (ArrayBuffer.isView(f)) return f.buffer.slice(f.byteOffset, f.byteOffset + f.byteLength)
  if (Array.isArray(f)) return new Uint8Array(f).buffer
  throw new TypeError("feed: a frame is an ArrayBuffer, a typed array or a byte array")
}

// `"Gohu", monospace` -> Gohu: the FontFace name a url'd font registers as.
function firstFamily(family) {
  return family.split(",")[0].trim().replace(/^["']|["']$/g, "")
}
