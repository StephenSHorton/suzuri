// node --test ui/view/test
// The view package's TermView, mounted into a small hand-written DOM stub:
// frames from rsz-term's encoder (testdata/frames) paint on the next
// animation frame and are acked; keys reach host.write; unmount leaves no
// listener, frame or observer behind; two views keep their own metrics.
import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

// ---- the stub: elements, a document, a window, a 2D context that records.
// Text is round(px * 4/7) wide per char, ascent round(px * .8), descent
// round(px * .2): 14px makes 8x15 cells, as in ui/test/frame.test.mjs.
const glyphW = (font) => Math.round(Number(/(\d+)px/.exec(font)?.[1] || 14) * 4 / 7)
function recorder() {
  const ops = []
  const ctx = {
    ops, fillStyle: "#000", font: "14px x", textBaseline: "",
    setTransform() {},
    fillRect(x, y, w, h) { ops.push({ op: "rect", style: ctx.fillStyle, r: [x, y, w, h] }) },
    fillText(t, x, y) { ops.push({ op: "text", t, at: [x, y] }) },
    drawImage() {},
    measureText(s) {
      const px = Number(/(\d+)px/.exec(ctx.font)?.[1] || 14)
      return { width: s.length * glyphW(ctx.font), fontBoundingBoxAscent: Math.round(px * 0.8), fontBoundingBoxDescent: Math.round(px * 0.2) }
    },
  }
  return ctx
}
globalThis.OffscreenCanvas = class {
  constructor(w, h) { this.width = w; this.height = h; this.ctx = recorder() }
  getContext() { return this.ctx }
}

let live = 0 // listeners added and not removed, over every stub target
class Target {
  constructor() { this.ls = [] }
  addEventListener(type, fn, o) { this.ls.push({ type, fn, o }); live++ }
  removeEventListener(type, fn) {
    const i = this.ls.findIndex((l) => l.type === type && l.fn === fn)
    if (i >= 0) { this.ls.splice(i, 1); live-- }
  }
  dispatch(type, ev = {}) {
    const e = { type, target: this, defaultPrevented: false, preventDefault() { this.defaultPrevented = true }, ...ev }
    for (const l of [...this.ls]) if (l.type === type) l.fn(e)
    return e
  }
}
class Style {
  setProperty(k, v) { this[k] = v }
}
class El extends Target {
  constructor(doc, tag) {
    super()
    this.ownerDocument = doc
    this.tagName = tag.toUpperCase()
    this.nodeType = 1
    this.style = new Style()
    this.children = []
    this.parent = null
    this.dataset = {}
    this.hidden = false
    this.className = ""
    this.textContent = ""
    this.value = ""
    this.attrs = {}
    // classList over className, as the DOM keeps them.
    const get = () => new Set(this.className.split(" ").filter(Boolean))
    const set = (cls) => { this.className = [...cls].join(" ") }
    this.classList = {
      add: (c) => set(get().add(c)), contains: (c) => get().has(c),
      remove: (c) => { const s = get(); s.delete(c); set(s) },
      toggle: (c, on) => ((on ?? !get().has(c)) ? this.classList.add(c) : this.classList.remove(c)),
    }
    this.clientWidth = 800
    this.clientHeight = 400
    if (tag === "canvas") { this.width = 0; this.height = 0; this.ctx = recorder() }
  }
  getContext() { return this.ctx }
  setAttribute(k, v) { this.attrs[k] = v }
  append(...cs) { for (const c of cs) { c.remove(); c.parent = this; this.children.push(c) } }
  remove() {
    if (!this.parent) return
    this.parent.children.splice(this.parent.children.indexOf(this), 1)
    this.parent = null
  }
  get isConnected() { let n = this; while (n.parent) n = n.parent; return n === this.ownerDocument.body }
  contains(n) { for (; n; n = n.parent) if (n === this) return true; return false }
  focus() { this.ownerDocument.activeElement = this; this.dispatch("focus") }
  getBoundingClientRect() { return { left: 0, top: 0, width: this.clientWidth, height: this.clientHeight } }
  querySelector() { return null }
}

function page() {
  const win = new Target()
  const doc = new Target()
  doc.defaultView = win
  doc.createElement = (tag) => new El(doc, tag)
  doc.body = new El(doc, "body")
  doc.querySelector = () => null
  const frames = new Map()
  let nextFrame = 1
  const observers = []
  Object.assign(win, {
    devicePixelRatio: 1,
    navigator: { platform: "Win32" },
    performance: { now: () => 0 },
    requestAnimationFrame(fn) { const id = nextFrame++; frames.set(id, fn); return id },
    cancelAnimationFrame(id) { frames.delete(id) },
    ResizeObserver: class {
      constructor(fn) { this.fn = fn; this.els = new Set(); observers.push(this) }
      observe(el) { this.els.add(el) }
      disconnect() { this.els.clear() }
    },
  })
  // Run the pending animation frames.
  const tick = () => { const fs = [...frames.values()]; frames.clear(); for (const f of fs) f(0) }
  const el = doc.createElement("div")
  doc.body.append(el)
  return { win, doc, el, frames, observers, tick }
}

function recordingHost(extra = {}) {
  const calls = []
  const rec = (name) => (...a) => { calls.push([name, ...a]) }
  return {
    calls,
    of: (name) => calls.filter((c) => c[0] === name).map((c) => c.slice(1)),
    write: rec("write"), paste: rec("paste"), resize: rec("resize"), cellPx: rec("cellPx"),
    scroll: rec("scroll"), ack: rec("ack"), redraw: rec("redraw"), focus: rec("focus"),
    select: rec("select"), mouse: rec("mouse"), openUrl: rec("openUrl"),
    copy: async () => { calls.push(["copy"]); return false },
    link: async () => null,
    ...extra,
  }
}

const load = (name) => {
  const b = readFileSync(new URL(`../../../testdata/frames/${name}`, import.meta.url))
  return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength)
}
const key = (o) => ({ key: "", code: "", shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, isComposing: false, ...o })

const { mount, FORMAT_VERSION } = await import("../index.js")
const viewBody = (el) => el.children[0]
const textarea = (el) => viewBody(el).children.find((c) => c.tagName === "TEXTAREA")
const canvasOf = (el) => viewBody(el).children.find((c) => c.tagName === "CANVAS")

test("mount builds the pane and sizes the terminal to it", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const body = viewBody(p.el)
  assert.ok(body.classList.contains("rsz-view"))
  for (const cls of ["cursor", "scrolled", "link-hover", "imgs", "preedit"]) {
    assert.ok(body.children.some((c) => c.className.split(" ").includes(cls)), cls)
  }
  assert.ok(textarea(p.el) && canvasOf(p.el))
  // 800x400 less 6/4 padding on each side, in 8x15 cells.
  assert.deepEqual(host.of("cellPx"), [[8, 15]])
  assert.deepEqual(host.of("resize"), [[Math.floor(788 / 8), Math.floor(392 / 15)]])
  assert.equal(p.observers[0].els.has(body), true)
  v.unmount()
})

test("a fed frame paints on the next animation frame, then acks its seq", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  v.feed(load("full.bin"))
  assert.deepEqual(host.of("ack"), [], "not before the animation frame")
  assert.equal(p.frames.size, 1)
  p.tick()
  assert.deepEqual(host.of("ack"), [[7]])
  const ops = canvasOf(p.el).ctx.ops
  assert.ok(ops.some((o) => o.op === "text" && o.t === "ab"))
  // Two frames before one animation frame: one paint, the last seq acked.
  v.feed(load("partial.bin"))
  v.feed(new Uint8Array(load("partial.bin")))
  assert.equal(p.frames.size, 1)
  p.tick()
  assert.deepEqual(host.of("ack"), [[7], [8]])
  // The cursor follows the frame: col 2, row 1 of 8x15 cells, past the padding.
  const cursor = viewBody(p.el).children.find((c) => c.className.startsWith("cursor"))
  assert.equal(cursor.style.left, `${6 + 2 * 8}px`)
  assert.equal(cursor.style.top, `${4 + 1 * 15}px`)
  v.unmount()
})

test("a frame in another format version is refused and shown, not acked", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const buf = load("full.bin")
  new Uint8Array(buf)[11] = FORMAT_VERSION + 1
  v.feed(buf)
  p.tick()
  assert.deepEqual(host.of("ack"), [])
  const err = viewBody(p.el).children.find((c) => c.className === "rsz-error")
  assert.equal(err.hidden, false)
  assert.equal(err.textContent, `terminal format v${FORMAT_VERSION + 1}, this app knows v${FORMAT_VERSION}`)
  v.unmount()
})

test("a keydown on the view's textarea reaches host.write", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const ta = textarea(p.el)
  const e = ta.dispatch("keydown", key({ key: "a", code: "KeyA" }))
  assert.deepEqual(host.of("write"), [["a"]])
  assert.equal(e.defaultPrevented, true)
  ta.dispatch("keydown", key({ key: "Enter", code: "Enter" }))
  assert.deepEqual(host.of("write").at(-1), ["\r"])
  // IME text arrives as input.
  ta.value = "漢"
  ta.dispatch("input", {})
  assert.deepEqual(host.of("write").at(-1), ["漢"])
  // A paste event hands the host its text.
  ta.dispatch("paste", { clipboardData: { getData: () => "pasted" } })
  assert.deepEqual(host.of("paste"), [["pasted"]])
  v.unmount()
})

test("the app's keyFilter goes first, and read-only sends nothing", () => {
  const p = page()
  const host = recordingHost({ keyFilter: (e) => e.key === "t" && e.ctrlKey })
  const v = mount(p.el, host)
  const ta = textarea(p.el)
  ta.dispatch("keydown", key({ key: "t", code: "KeyT", ctrlKey: true }))
  assert.deepEqual(host.of("write"), [])
  v.setReadOnly(true)
  ta.dispatch("keydown", key({ key: "a", code: "KeyA" }))
  ta.dispatch("paste", { clipboardData: { getData: () => "x" } })
  viewBody(p.el).dispatch("wheel", { deltaY: 45 })
  assert.deepEqual(host.of("write"), [])
  assert.deepEqual(host.of("paste"), [])
  assert.deepEqual(host.of("scroll"), [])
  v.setReadOnly(false)
  ta.dispatch("keydown", key({ key: "a", code: "KeyA" }))
  assert.deepEqual(host.of("write"), [["a"]])
  v.unmount()
})

test("the wheel scrolls the history in whole lines", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  viewBody(p.el).dispatch("wheel", { deltaY: -30 }) // two 15px lines up
  assert.deepEqual(host.of("scroll"), [[2]])
  v.unmount()
})

test("unmount leaves no listener, animation frame or observer behind", () => {
  const before = live
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  assert.ok(live > before, "the view listens")
  assert.ok(p.win.ls.length > 0, "the pointer listens on the window")
  v.feed(load("full.bin"))
  assert.equal(p.frames.size, 1)
  v.unmount()
  assert.equal(live, before, "every listener removed")
  assert.equal(p.win.ls.length, 0)
  assert.equal(p.doc.ls.length, 0)
  assert.equal(p.frames.size, 0, "the pending animation frame cancelled")
  assert.equal(p.observers[0].els.size, 0, "the resize observer disconnected")
  assert.equal(p.el.children.length, 0, "the DOM removed")
  // Nothing reaches the host after unmount.
  v.feed(load("full.bin"))
  v.message({ t: "title", title: "x" })
  p.tick()
  assert.deepEqual(host.of("ack"), [])
  v.unmount() // twice is fine
})

test("two views with different styles keep their own metrics", () => {
  const p = page()
  const a = recordingHost(), b = recordingHost()
  const va = mount(p.el, a)
  const vb = mount(p.el, b)
  vb.setStyle({ bg: "#101010", font: { family: "Mono", px: 21 } })
  // b re-measured (12x22 cells: 21px * 4/7 wide, ascent 17 + descent 4 + 1 high),
  // refitted and asked for a full frame; a did none of that.
  assert.deepEqual(b.of("cellPx").at(-1), [12, 22])
  assert.deepEqual(b.of("resize").at(-1), [Math.floor(788 / 12), Math.floor(392 / 22)])
  assert.equal(b.of("redraw").length, 1)
  assert.deepEqual(a.of("cellPx"), [[8, 15]])
  assert.equal(a.of("redraw").length, 0)
  // Each paints with its own cells and background.
  va.feed(load("full.bin"))
  vb.feed(load("full.bin"))
  p.tick()
  const [ca, cb] = p.el.children.map((c) => c.children.find((x) => x.tagName === "CANVAS"))
  assert.deepEqual([ca.width, ca.height], [10 * 8, 3 * 15])
  assert.deepEqual([cb.width, cb.height], [10 * 12, 3 * 22])
  assert.equal(cb.ctx.ops[0].style, "#101010")
  assert.notEqual(ca.ctx.ops[0].style, "#101010")
  va.unmount()
  vb.unmount()
})

test("messages: title, progress and exit act on the view and reach onMessage", () => {
  const p = page()
  const seen = []
  const host = recordingHost({ onMessage: (m) => seen.push(m.t) })
  const v = mount(p.el, host)
  v.message({ t: "title", title: "vim" })
  v.message(JSON.stringify({ t: "progress", state: 1, percent: 40 }))
  assert.equal(v.title, "vim")
  assert.equal(viewBody(p.el).dataset.progress, "1")
  v.message({ t: "exit", code: 0 })
  textarea(p.el).dispatch("keydown", key({ key: "a", code: "KeyA" }))
  assert.deepEqual(host.of("write"), [], "an exited terminal takes no keys")
  assert.deepEqual(seen, ["title", "progress", "exit"])
  v.unmount()
})

test("after exit the history still scrolls and copies, as in suzuri; keys and paste stop", async () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const ta = textarea(p.el)
  v.message({ t: "exit", code: 1 })
  viewBody(p.el).dispatch("wheel", { deltaY: -30 })
  assert.deepEqual(host.of("scroll"), [[2]])
  ta.dispatch("keydown", key({ key: "C", code: "KeyC", ctrlKey: true, shiftKey: true }))
  await new Promise((r) => setImmediate(r))
  assert.equal(host.of("copy").length, 1)
  ta.dispatch("keydown", key({ key: "a", code: "KeyA" }))
  ta.dispatch("keydown", key({ key: "V", code: "KeyV", ctrlKey: true, shiftKey: true }))
  ta.dispatch("paste", { clipboardData: { getData: () => "x" } })
  assert.deepEqual(host.of("write"), [])
  assert.deepEqual(host.of("paste"), [])
  // Read-only still stops scroll and copy.
  v.setReadOnly(true)
  viewBody(p.el).dispatch("wheel", { deltaY: -30 })
  ta.dispatch("keydown", key({ key: "C", code: "KeyC", ctrlKey: true, shiftKey: true }))
  await new Promise((r) => setImmediate(r))
  assert.deepEqual(host.of("scroll"), [[2]])
  assert.equal(host.of("copy").length, 1)
  v.unmount()
})

test("an error clears when a frame paints or the style changes", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const err = viewBody(p.el).children.find((c) => c.className === "rsz-error")
  const bad = load("full.bin")
  new Uint8Array(bad)[11] = FORMAT_VERSION + 1
  v.feed(bad)
  assert.equal(err.hidden, false)
  v.feed(load("full.bin"))
  p.tick()
  assert.equal(err.hidden, true)
  assert.equal(host.of("ack").length, 1)
  v.feed(bad)
  assert.equal(err.hidden, false)
  v.setStyle({ bg: "#000000" })
  assert.equal(err.hidden, true)
  v.unmount()
})

test("unmount with focus tells the host the view lost it", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  v.focus()
  assert.deepEqual(host.of("focus"), [[true]])
  v.unmount()
  assert.deepEqual(host.of("focus"), [[true], [false]])
  // Unfocused: nothing to tell.
  const host2 = recordingHost()
  const w = mount(p.el, host2)
  w.unmount()
  assert.deepEqual(host2.of("focus"), [])
})

test("setStyle with a font url adds one face per document and unmount takes it off", async () => {
  const p = page()
  const added = new Set()
  p.doc.fonts = {
    add(f) { added.add(f) }, delete(f) { return added.delete(f) },
    load: async () => [],
  }
  p.win.FontFace = class { constructor(name, src) { this.family = name; this.src = src } load() { return Promise.resolve(this) } }
  const font = { family: '"Gohu", monospace', px: 14, url: "file:///gohu.ttf" }
  const a = mount(p.el, recordingHost())
  const b = mount(p.el, recordingHost())
  a.setStyle({ font })
  a.setStyle({ font })
  b.setStyle({ font })
  assert.equal(added.size, 1, "repeats and a second view share one face")
  assert.equal([...added][0].family, "Gohu")
  a.unmount()
  assert.equal(added.size, 1, "b still holds it")
  b.setStyle({ font: { ...font, url: "file:///other.ttf" } })
  assert.equal(added.size, 1, "a new font lets go of the old one")
  assert.equal([...added][0].src, "url(file:///other.ttf)")
  b.unmount()
  assert.equal(added.size, 0)
  await new Promise((r) => setTimeout(r, 0))
})

test("an image decode still running at unmount closes its bitmap", async () => {
  const { PaneImages } = await import("../images.js")
  const p = page()
  let finish
  const closed = []
  const saved = globalThis.createImageBitmap
  globalThis.createImageBitmap = () => new Promise((r) => { finish = r })
  try {
    const imgs = new PaneImages(p.el)
    imgs.add({ id: 1, format: 100, data: btoa("png") })
    await new Promise((r) => setTimeout(r, 0))
    imgs.destroy()
    finish({ width: 1, height: 1, close() { closed.push(1) } })
    await new Promise((r) => setTimeout(r, 0))
    assert.deepEqual(closed, [1])
    assert.equal(imgs.images.size, 0)
    assert.equal(imgs.waiting.size, 0)
  } finally {
    globalThis.createImageBitmap = saved
  }
})
