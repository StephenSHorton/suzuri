// node --test ui/view/test
// The view package's TermView, mounted into a small hand-written DOM stub:
// frames from rsz-term's encoder (testdata/frames) paint on the next
// animation frame and are acked; keys reach host.write; unmount leaves no
// listener, frame or observer behind; two views keep their own metrics.
import { test } from "node:test"
import assert from "node:assert/strict"
import { page, recordingHost, load, key, liveListeners, viewBody, textarea, canvasOf } from "./stub.mjs"

const { mount, FORMAT_VERSION, clipShortcut } = await import("../index.js")
const { M_ALT_SCREEN, M_ALT_SCROLL, M_APP_CURSOR, M_FOCUS } = await import("../frame.js")

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
  const before = liveListeners()
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  assert.ok(liveListeners() > before, "the view listens")
  assert.ok(p.win.ls.length > 0, "the pointer listens on the window")
  v.feed(load("full.bin"))
  assert.equal(p.frames.size, 1)
  v.unmount()
  assert.equal(liveListeners(), before, "every listener removed")
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

// ---- the per-pane controller moved out of suzuri's app.js (Stage B of #375)

// full.bin with other modes (header bytes 12..15).
function withModes(modes) {
  const buf = load("full.bin")
  new DataView(buf).setUint32(12, modes, true)
  return buf
}

test("keys become the bytes the shell expects, on host.write", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host, { isMac: false })
  const ta = textarea(p.el)
  const cases = [
    [{ key: "a", code: "KeyA" }, "a"],
    [{ key: "Enter", code: "Enter" }, "\r"],
    [{ key: "Backspace", code: "Backspace" }, "\x7f"],
    [{ key: "Tab", code: "Tab" }, "\t"],
    [{ key: "Escape", code: "Escape" }, "\x1b"],
    [{ key: "ArrowUp", code: "ArrowUp" }, "\x1b[A"],
    [{ key: "ArrowLeft", code: "ArrowLeft", ctrlKey: true }, "\x1b[1;5D"],
    [{ key: "Delete", code: "Delete" }, "\x1b[3~"],
    [{ key: "F1", code: "F1" }, "\x1bOP"],
    [{ key: "b", code: "KeyB", altKey: true }, "\x1bb"],
    [{ key: "c", code: "KeyC", ctrlKey: true }, "\x03"], // no selection: ^C
  ]
  for (const [k, bytes] of cases) {
    const e = ta.dispatch("keydown", key(k))
    assert.deepEqual(host.of("write").at(-1), [bytes], k.key)
    assert.equal(e.defaultPrevented, true, k.key)
  }
  // A lone modifier is no key; a composing key is the IME's.
  const n = host.of("write").length
  ta.dispatch("keydown", key({ key: "Shift", code: "ShiftLeft", shiftKey: true }))
  ta.dispatch("keydown", key({ key: "a", code: "KeyA", isComposing: true }))
  assert.equal(host.of("write").length, n)
  // The app's cursor-key mode follows the last frame.
  v.feed(withModes(M_APP_CURSOR))
  p.tick()
  assert.equal(v.modes, M_APP_CURSOR)
  ta.dispatch("keydown", key({ key: "ArrowUp", code: "ArrowUp" }))
  assert.deepEqual(host.of("write").at(-1), ["\x1bOA"])
  v.unmount()
})

test("IME: the preedit shows while composing, and the committed text is written once", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const ta = textarea(p.el)
  const pre = viewBody(p.el).children.find((c) => c.classList.contains("preedit"))
  ta.dispatch("compositionstart", { data: "" })
  assert.equal(pre.hidden, false)
  ta.dispatch("compositionupdate", { data: "にほ" })
  assert.equal(pre.textContent, "にほ")
  // Input while composing waits for the commit.
  ta.value = "にほ"
  ta.dispatch("input", { isComposing: true })
  assert.deepEqual(host.of("write"), [])
  ta.value = "日本"
  ta.dispatch("compositionend", { data: "日本" })
  assert.equal(pre.hidden, true)
  assert.equal(pre.textContent, "")
  assert.deepEqual(host.of("write"), [["日本"]])
  assert.equal(ta.value, "")
  v.unmount()
})

test("textFilter takes committed text before host.write", () => {
  const p = page()
  const took = []
  const host = recordingHost({ textFilter: (t) => (took.push(t), t !== "pass") })
  const v = mount(p.el, host)
  const ta = textarea(p.el)
  ta.value = "日本"
  ta.dispatch("compositionend", {})
  ta.value = "pass"
  ta.dispatch("input", {})
  assert.deepEqual(took, ["日本", "pass"])
  assert.deepEqual(host.of("write"), [["pass"]])
  v.unmount()
})

test("paste: chords ask the host to read the clipboard (debounced); paste events hand over their text", () => {
  const p = page()
  let t = 1000
  p.win.performance.now = () => t
  const host = recordingHost()
  const v = mount(p.el, host, { isMac: false })
  const ta = textarea(p.el)
  for (const k of [{ key: "v", code: "KeyV", ctrlKey: true }, { key: "V", code: "KeyV", ctrlKey: true, shiftKey: true }, { key: "Insert", code: "Insert", shiftKey: true }]) {
    t += 1000
    const e = ta.dispatch("keydown", key(k))
    assert.equal(e.defaultPrevented, true)
  }
  assert.deepEqual(host.of("paste"), [[null], [null], [null]])
  // A second press inside 280 ms (a keydown and a menu action) is one paste.
  t += 100
  ta.dispatch("keydown", key({ key: "v", code: "KeyV", ctrlKey: true }))
  assert.equal(host.of("paste").length, 3)
  ta.dispatch("paste", { clipboardData: { getData: (type) => (type === "text/plain" ? "hi\nthere" : "") } })
  assert.deepEqual(host.of("paste").at(-1), ["hi\nthere"])
  assert.deepEqual(host.of("write"), [], "nothing typed")
  // macOS: ⌘V; Ctrl+V is ^V there.
  const q = page()
  q.win.performance.now = () => 5000
  const mac = recordingHost()
  const w = mount(q.el, mac, { isMac: true })
  textarea(q.el).dispatch("keydown", key({ key: "v", code: "KeyV", metaKey: true }))
  textarea(q.el).dispatch("keydown", key({ key: "v", code: "KeyV", ctrlKey: true }))
  assert.deepEqual(mac.of("paste"), [[null]])
  assert.deepEqual(mac.of("write"), [["\x16"]])
  v.unmount()
  w.unmount()
})

test("pasteFilter sees the clipboard first; true keeps its text from host.paste", () => {
  const p = page()
  const seen = []
  const host = recordingHost({ pasteFilter: (cd) => (seen.push(cd.getData("text/plain")), cd.image === true) })
  const v = mount(p.el, host)
  const ta = textarea(p.el)
  ta.dispatch("paste", { clipboardData: { image: true, getData: () => "" } })
  ta.dispatch("paste", { clipboardData: { getData: () => "text" } })
  assert.deepEqual(seen, ["", "text"])
  assert.deepEqual(host.of("paste"), [["text"]])
  v.unmount()
})

test("copy: Ctrl+Shift+C copies; Ctrl+C copies only with a selection, and is ^C when nothing was copied", async () => {
  const p = page()
  let had = false
  const host = recordingHost({ copy: async () => { host.calls.push(["copy"]); return had } })
  const v = mount(p.el, host, { isMac: false })
  const ta = textarea(p.el)
  ta.dispatch("keydown", key({ key: "C", code: "KeyC", ctrlKey: true, shiftKey: true }))
  ta.dispatch("keydown", key({ key: "Insert", code: "Insert", ctrlKey: true }))
  await new Promise((r) => setImmediate(r))
  assert.equal(host.of("copy").length, 2)
  assert.deepEqual(host.of("write"), [])
  // Select a word with the pointer, then Ctrl+C: a copy; nothing copied -> ^C.
  viewBody(p.el).dispatch("mousedown", { button: 0, detail: 2, clientX: 20, clientY: 10 })
  assert.deepEqual(host.of("select"), [["word", 2, 0, true]])
  assert.equal(v.hasSelection, true)
  ta.dispatch("keydown", key({ key: "c", code: "KeyC", ctrlKey: true }))
  await new Promise((r) => setImmediate(r))
  assert.equal(host.of("copy").length, 3)
  assert.deepEqual(host.of("write"), [["\x03"]])
  had = true
  ta.dispatch("keydown", key({ key: "c", code: "KeyC", ctrlKey: true }))
  await new Promise((r) => setImmediate(r))
  assert.equal(host.of("copy").length, 4)
  assert.deepEqual(host.of("write"), [["\x03"]])
  v.unmount()
})

test("ready false: the pointer takes no press until the host has a terminal", () => {
  const p = page()
  const host = recordingHost()
  let ready = false
  const v = mount(p.el, host, { isMac: false, ready: () => ready })
  const body = viewBody(p.el)
  const e = body.dispatch("mousedown", { button: 0, detail: 2, clientX: 20, clientY: 10 })
  assert.equal(e.defaultPrevented, false)
  assert.deepEqual(host.of("select"), [])
  assert.equal(v.hasSelection, false)
  assert.notEqual(p.el.ownerDocument.activeElement, textarea(p.el))
  ready = true
  body.dispatch("mousedown", { button: 0, detail: 2, clientX: 20, clientY: 10 })
  assert.deepEqual(host.of("select"), [["word", 2, 0, true]])
  assert.equal(v.hasSelection, true)
  v.unmount()
})

test("clipShortcut classifies the clipboard chords per platform", () => {
  assert.equal(clipShortcut(key({ key: "c", ctrlKey: true, shiftKey: true }), false, false), "copy")
  assert.equal(clipShortcut(key({ key: "v", ctrlKey: true }), false, false), "paste")
  assert.equal(clipShortcut(key({ key: "c", ctrlKey: true }), false, false), null)
  assert.equal(clipShortcut(key({ key: "c", ctrlKey: true }), false, true), "copy-or-interrupt")
  assert.equal(clipShortcut(key({ key: "c", ctrlKey: true, altKey: true }), false, true), null)
  assert.equal(clipShortcut(key({ key: "c", metaKey: true }), true, false), "copy")
  assert.equal(clipShortcut(key({ key: "v", metaKey: true, shiftKey: true }), true, false), null)
  assert.equal(clipShortcut(key({ key: "v", ctrlKey: true }), true, false), null)
})

test("keyFilter goes before the clipboard chords and the encoder; true leaves the event alone", () => {
  const p = page()
  p.win.performance.now = () => 10_000
  const host = recordingHost({ keyFilter: (e) => e.ctrlKey && e.shiftKey })
  const v = mount(p.el, host, { isMac: false })
  const ta = textarea(p.el)
  const e = ta.dispatch("keydown", key({ key: "V", code: "KeyV", ctrlKey: true, shiftKey: true }))
  assert.equal(e.defaultPrevented, false)
  assert.deepEqual(host.of("paste"), [])
  ta.dispatch("keydown", key({ key: "v", code: "KeyV", ctrlKey: true }))
  assert.deepEqual(host.of("paste"), [[null]])
  ta.dispatch("keydown", key({ key: "x", code: "KeyX" }))
  assert.deepEqual(host.of("write"), [["x"]])
  v.unmount()
})

test("the wheel: history lines to host.scroll, arrow keys on an alt screen that asked for them", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const body = viewBody(p.el)
  body.dispatch("wheel", { deltaY: -10 }) // under a 15px line: kept
  assert.deepEqual(host.of("scroll"), [])
  body.dispatch("wheel", { deltaY: -20 }) // 30px now: two lines up
  body.dispatch("wheel", { deltaY: 45 })
  assert.deepEqual(host.of("scroll"), [[2], [-3]])
  v.feed(withModes(M_ALT_SCREEN))
  p.tick()
  body.dispatch("wheel", { deltaY: -30 })
  assert.deepEqual(host.of("write"), [], "an alt screen that didn't ask gets nothing")
  v.feed(withModes(M_ALT_SCREEN | M_ALT_SCROLL | M_APP_CURSOR))
  p.tick()
  body.dispatch("wheel", { deltaY: -30 })
  body.dispatch("wheel", { deltaY: 15 })
  assert.deepEqual(host.of("write"), [["\x1bOA\x1bOA"], ["\x1bOB"]])
  assert.equal(host.of("scroll").length, 2)
  v.unmount()
})

test("wheelKeys takes the wheel's arrow keys when the host has it, after an exit too", () => {
  const p = page()
  const keys = []
  const host = recordingHost({ wheelKeys: (d) => keys.push(d) })
  const v = mount(p.el, host)
  v.feed(withModes(M_ALT_SCREEN | M_ALT_SCROLL))
  p.tick()
  viewBody(p.el).dispatch("wheel", { deltaY: -30 })
  v.message({ t: "exit", code: 0 })
  viewBody(p.el).dispatch("wheel", { deltaY: 15 })
  assert.deepEqual(keys, ["\x1b[A\x1b[A", "\x1b[B"])
  assert.deepEqual(host.of("write"), [])
  v.setReadOnly(true)
  viewBody(p.el).dispatch("wheel", { deltaY: 15 })
  assert.equal(keys.length, 2, "read-only sends nothing")
  v.unmount()
})

test("an exited terminal still keeps its encodable keys from the browser", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  v.message({ t: "exit", code: 0 })
  const tab = textarea(p.el).dispatch("keydown", key({ key: "Tab", code: "Tab" }))
  assert.equal(tab.defaultPrevented, true, "Tab does not move the focus")
  const shift = textarea(p.el).dispatch("keydown", key({ key: "Shift", code: "ShiftLeft", shiftKey: true }))
  assert.equal(shift.defaultPrevented, false)
  assert.deepEqual(host.of("write"), [])
  v.unmount()
})

test("onPaint follows each painted frame with its header and stats, after the ack", () => {
  const p = page()
  const order = []
  const host = recordingHost({
    onPaint: (h, stats) => order.push(["paint", h.seq, stats]),
    ack: (seq) => order.push(["ack", seq]),
  })
  const v = mount(p.el, host)
  const full = load("full.bin"), part = load("partial.bin")
  v.feed(full)
  v.feed(part)
  p.tick()
  assert.equal(order.length, 2)
  assert.deepEqual(order[0], ["ack", 8])
  assert.deepEqual(order[1].slice(0, 2), ["paint", 8])
  assert.equal(order[1][2].frames, 2)
  assert.equal(order[1][2].bytes, full.byteLength + part.byteLength)
  v.unmount()
})

test("reportFocus false: focus and blur reach neither host.focus nor the PTY", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host, { reportFocus: false })
  v.feed(withModes(M_FOCUS))
  p.tick()
  v.focus()
  textarea(p.el).dispatch("blur")
  v.unmount()
  assert.deepEqual(host.of("focus"), [])
  assert.deepEqual(host.of("write"), [])
  // The default reports both.
  const q = page()
  const h2 = recordingHost()
  const w = mount(q.el, h2)
  w.feed(withModes(M_FOCUS))
  q.tick()
  w.focus()
  textarea(q.el).dispatch("blur")
  assert.deepEqual(h2.of("focus"), [[true], [false]])
  assert.deepEqual(h2.of("write"), [["\x1b[I"], ["\x1b[O"]])
  w.unmount()
})

test("caret: the textarea and preedit sit on the host's element, in viewport coordinates, while focused", () => {
  const p = page()
  const at = p.doc.createElement("div")
  at.getBoundingClientRect = () => ({ left: 120, top: 300, width: 8, height: 15 })
  const v = mount(p.el, recordingHost(), { caret: () => at })
  const ta = textarea(p.el)
  assert.ok(!viewBody(p.el).children.some((c) => c.classList.contains("preedit")), "the preedit waits for a composition")
  assert.equal(ta.style.position, "fixed")
  assert.equal(ta.style.left, "0", "not placed while unfocused")
  v.focus()
  assert.deepEqual([ta.style.left, ta.style.top, ta.style.height], ["120px", "300px", "15px"])
  at.getBoundingClientRect = () => ({ left: 128, top: 300, width: 8, height: 15 })
  v.placeInput()
  assert.equal(ta.style.left, "128px")
  ta.dispatch("compositionstart", {})
  // On the document's body, above every view, as suzuri's one preedit was.
  const pre = p.doc.body.children.find((c) => c.classList.contains("preedit"))
  assert.ok(pre)
  assert.deepEqual([pre.style.position, pre.style.left, pre.style.top], ["fixed", "128px", "300px"])
  ta.dispatch("compositionend", {})
  assert.equal(pre.parent, null, "the commit takes it off the page")
  ta.dispatch("compositionstart", {})
  assert.equal(pre.parent, p.doc.body)
  v.unmount()
  assert.equal(pre.parent, null, "so does unmount")
})

test("paint external: no canvas context, feed refused, painted() follows the header and acks", () => {
  const p = page()
  const orig = p.doc.createElement
  let contexts = 0
  p.doc.createElement = (tag) => {
    const el = orig(tag)
    if (tag === "canvas") el.getContext = () => { contexts++; return el.ctx }
    return el
  }
  const host = recordingHost({ onPaint: (h, stats) => host.calls.push(["onPaint", h.seq, stats]) })
  const v = mount(p.el, host, { paint: "external" })
  assert.equal(contexts, 0, "the canvas can still be transferred")
  assert.equal(v.canvas, canvasOf(p.el))
  assert.throws(() => v.feed(load("full.bin")))
  const h = { seq: 5, cols: 10, rows: 3, cx: 2, cy: 1, shape: 1, modes: M_ALT_SCREEN, offset: 4, history: 90 }
  v.painted(h)
  assert.deepEqual(host.of("ack"), [[5]])
  assert.deepEqual(host.of("onPaint"), [[5, undefined]])
  assert.equal(v.modes, M_ALT_SCREEN)
  assert.equal(v.canvas.style.width, `${10 * 8}px`)
  assert.equal(v.canvas.style.height, `${3 * 15}px`)
  const body = viewBody(p.el)
  const cursor = body.children.find((c) => c.className.startsWith("cursor"))
  assert.equal(cursor.style.left, `${6 + 2 * 8}px`)
  const scrolled = body.children.find((c) => c.className === "scrolled")
  assert.equal(scrolled.hidden, false)
  assert.equal(scrolled.textContent, "↑ 4 / 90")
  v.unmount()
})

test("screen: the host's Screen measures and paints the view", () => {
  const p = page()
  let made = null
  const host = recordingHost()
  const v = mount(p.el, host, {
    screen: (canvas) => (made = {
      canvas,
      cellMetrics: () => ({ cw: 10, ch: 20, base: 16, runs: false }),
      setStyle() {}, resetMetrics() {}, fontSpec: () => "20px x",
      apply: () => ({ seq: 9, cols: 4, rows: 2, cx: 1, cy: 1, shape: 1, modes: 0, offset: 0, history: 0 }),
    }),
  })
  assert.equal(made.canvas, canvasOf(p.el))
  assert.deepEqual(host.of("cellPx"), [[10, 20]])
  v.feed(load("full.bin"))
  p.tick()
  assert.deepEqual(host.of("ack"), [[9]])
  v.unmount()
})

// ---- behaviour that must stay as suzuri's app.js had it (Stage B review)

test("paint time is the frame applies alone, not the DOM work after them", async () => {
  const { Screen } = await import("../frame.js")
  const p = page()
  let clock = 0
  p.win.performance.now = () => clock
  const stats = []
  const host = recordingHost({ onPaint: (h, s) => stats.push(s) })
  const caretEl = p.doc.createElement("div")
  const v = mount(p.el, host, {
    // Each apply takes 5 ms; placing the textarea on the caret (afterPaint) 50.
    screen: (c) => {
      const s = new Screen(c, 1)
      const apply = s.apply.bind(s)
      s.apply = (...a) => { clock += 5; return apply(...a) }
      return s
    },
    caret: () => { clock += 50; return caretEl },
  })
  v.focus()
  v.feed(load("full.bin"))
  v.feed(load("partial.bin"))
  p.tick()
  assert.equal(stats.length, 1)
  assert.equal(stats[0].ms, 10, "two applies, no afterPaint time")
  v.unmount()
})

test("a frame shorter than its header is refused and shown, not acked", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  v.feed(new Uint8Array(10))
  p.tick()
  assert.deepEqual(host.of("ack"), [])
  assert.equal(p.frames.size, 0, "nothing queued")
  const err = viewBody(p.el).children.find((c) => c.className === "rsz-error")
  assert.equal(err.hidden, false)
  assert.match(err.textContent, /10 bytes/)
  // The next good frame paints, acks and clears it.
  v.feed(load("full.bin"))
  p.tick()
  assert.deepEqual(host.of("ack"), [[7]])
  assert.equal(err.hidden, true)
  v.unmount()
})

test("debouncePaste false: every paste chord reaches the host, which debounces it itself", () => {
  const p = page()
  p.win.performance.now = () => 1000
  const host = recordingHost()
  const v = mount(p.el, host, { isMac: false, debouncePaste: false })
  const ta = textarea(p.el)
  ta.dispatch("keydown", key({ key: "v", code: "KeyV", ctrlKey: true }))
  ta.dispatch("keydown", key({ key: "v", code: "KeyV", ctrlKey: true }))
  assert.deepEqual(host.of("paste"), [[null], [null]], "same instant, both handed over")
  v.unmount()
})

test("framesPending: an imgpos waits for the next painted frame while the host holds frames", async () => {
  const p = page()
  const saved = globalThis.createImageBitmap
  globalThis.createImageBitmap = async () => ({ width: 4, height: 4, close() {} })
  try {
    let held = true
    const host = recordingHost()
    const v = mount(p.el, host, { paint: "external", framesPending: () => held })
    const layer = viewBody(p.el).children.find((c) => c.className === "imgs")
    v.message({ t: "img", id: 1, format: 100, data: btoa("png") })
    await new Promise((r) => setTimeout(r, 0))
    v.message({ t: "imgpos", list: [[1, 0, 0, 1, 1, 0, 1, 0, 0, 0, 0]] })
    assert.equal(layer.children.length, 0, "not before its frame")
    held = false
    v.painted({ seq: 1, cols: 10, rows: 3, cx: 0, cy: 0, shape: 1, modes: 0, offset: 0, history: 0 })
    assert.equal(layer.children.length, 1, "placed with the frame")
    // Nothing held: placed at once.
    v.message({ t: "imgpos", list: [] })
    assert.equal(layer.children.length, 0)
    v.unmount()
  } finally {
    globalThis.createImageBitmap = saved
  }
})

test("view.key handles a keydown that landed elsewhere as its textarea's", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const e = { target: p.doc.body, defaultPrevented: false, preventDefault() { this.defaultPrevented = true }, ...key({ key: "a", code: "KeyA" }) }
  v.key(e)
  assert.equal(e.defaultPrevented, true)
  assert.deepEqual(host.of("write"), [["a"]])
  v.unmount()
  v.key({ ...e })
  assert.deepEqual(host.of("write"), [["a"]], "nothing after unmount")
})
