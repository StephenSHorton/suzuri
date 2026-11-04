// The DOM, canvas and host stubs the view's tests mount into (no
// dependencies): elements, a document, a window, a 2D context that records,
// and a host that records every call. Shared by ui/view/test and ui/test.
import { readFileSync } from "node:fs"

// ---- the stub: elements, a document, a window, a 2D context that records.
// Text is round(px * 4/7) wide per char, ascent round(px * .8), descent
// round(px * .2): 14px makes 8x15 cells, as in ui/test/frame.test.mjs.
const glyphW = (font) => Math.round(Number(/(\d+)px/.exec(font)?.[1] || 14) * 4 / 7)
export function recorder() {
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
export class El extends Target {
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

export function page() {
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

export function recordingHost(extra = {}) {
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

export const load = (name) => {
  const b = readFileSync(new URL(`../../../testdata/frames/${name}`, import.meta.url))
  return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength)
}
export const key = (o) => ({ key: "", code: "", shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, isComposing: false, ...o })

// Listeners added and not removed, over every stub target.
export const liveListeners = () => live
export const viewBody = (el) => el.children[0]
export const textarea = (el) => viewBody(el).children.find((c) => c.tagName === "TEXTAREA")
export const canvasOf = (el) => viewBody(el).children.find((c) => c.tagName === "CANVAS")
