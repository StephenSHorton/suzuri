// node --test ui/view/test
// Go suzuri's pointer and scrolling behaviours the view brought back: Ctrl+click
// opens links on macOS, a double or triple click selects on an alt screen
// that reports the mouse, the scrollback thumb, and eased scrolling.
import { test } from "node:test"
import assert from "node:assert/strict"
import { page, recordingHost, load, viewBody } from "./stub.mjs"

const { mount } = await import("../index.js")
const { M_ALT_SCREEN, M_MOUSE_CLICK } = await import("../frame.js")
const { easeLines, SmoothScroll, AT_ONCE } = await import("../smooth.js")

// full.bin with other modes, scroll offset and history (header bytes 12..23).
function frame({ modes = 0, offset = 0, history = 0 } = {}) {
  const buf = load("full.bin")
  const dv = new DataView(buf)
  dv.setUint32(12, modes, true)
  dv.setUint32(16, offset, true)
  dv.setUint32(20, history, true)
  return buf
}
const rowsOf = (buf) => new DataView(buf).getUint16(4, true)
const settle = () => new Promise((r) => setImmediate(r))

test("macOS: Ctrl+click opens a link as ⌘+click does; a plain click does not", async () => {
  const p = page()
  const url = "file:///tmp/notes.txt"
  const host = recordingHost({ link: async () => ({ url, row: 0, x0: 0, x1: 10 }) })
  const v = mount(p.el, host, { isMac: true })
  const body = viewBody(p.el)
  p.win.dispatch("mousemove", { target: body, clientX: 20, clientY: 10 })
  await settle()
  body.dispatch("mousedown", { button: 0, detail: 1, clientX: 20, clientY: 10 })
  assert.deepEqual(host.of("openUrl"), [])
  body.dispatch("mousedown", { button: 0, detail: 1, ctrlKey: true, clientX: 20, clientY: 10 })
  body.dispatch("mousedown", { button: 0, detail: 1, metaKey: true, clientX: 20, clientY: 10 })
  assert.deepEqual(host.of("openUrl"), [[url], [url]])
  v.unmount()
})

test("an alt screen that reports the mouse: single clicks go to the app, double and triple clicks select", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host, { isMac: true })
  v.feed(frame({ modes: M_ALT_SCREEN | M_MOUSE_CLICK }))
  p.tick()
  const body = viewBody(p.el)
  const at = { clientX: 20, clientY: 10 }
  body.dispatch("mousedown", { button: 0, detail: 1, ...at })
  p.win.dispatch("mouseup", { button: 0, ...at })
  assert.deepEqual(host.of("mouse").map((m) => m[0].kind), ["press", "release"])
  body.dispatch("mousedown", { button: 0, detail: 2, ...at })
  p.win.dispatch("mouseup", { button: 0, ...at })
  body.dispatch("mousedown", { button: 0, detail: 3, ...at })
  p.win.dispatch("mouseup", { button: 0, ...at })
  assert.deepEqual(host.of("select"), [["word", 2, 0, true], ["line", 2, 0, true]])
  assert.equal(host.of("mouse").length, 2, "the app hears no press for them")
  assert.equal(v.hasSelection, true)
  // The next single click is the app's again and drops the selection.
  body.dispatch("mousedown", { button: 0, detail: 1, ...at })
  assert.deepEqual(host.of("select").at(-1)[0], "clear")
  assert.equal(v.hasSelection, false)
  assert.equal(host.of("mouse").length, 3)
  v.unmount()
})

test("on the main screen a mouse-reporting app still gets double clicks", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  v.feed(frame({ modes: M_MOUSE_CLICK }))
  p.tick()
  viewBody(p.el).dispatch("mousedown", { button: 0, detail: 2, clientX: 20, clientY: 10 })
  assert.deepEqual(host.of("select"), [])
  assert.equal(host.of("mouse").length, 1)
  v.unmount()
})

test("the scrollback thumb shows while scrolled back, sized and placed by offset and history", () => {
  const p = page()
  const host = recordingHost()
  const v = mount(p.el, host)
  const track = viewBody(p.el).children.find((c) => c.className.split(" ").includes("rsz-scrollbar"))
  const thumb = track.children[0]
  v.feed(frame())
  p.tick()
  assert.equal(track.classList.contains("on"), false)
  const buf = frame({ offset: 100, history: 300 })
  const rows = rowsOf(buf)
  v.feed(buf)
  p.tick()
  assert.equal(track.classList.contains("on"), true)
  const height = rows * 15
  const size = Math.max(18, (height * rows) / (300 + rows))
  assert.equal(thumb.style.height, `${Math.round(size)}px`)
  assert.equal(thumb.style.top, `${Math.round(((height - size) * 200) / 300)}px`)
  // At the top of the history the thumb is at the top.
  v.feed(frame({ offset: 300, history: 300 }))
  p.tick()
  assert.equal(thumb.style.top, "0px")
  v.feed(frame({ offset: 0, history: 300 }))
  p.tick()
  assert.equal(track.classList.contains("on"), false)
  v.unmount()
})

test("a long wheel scroll eases over animation frames and adds up to the lines asked", () => {
  const p = page()
  let t = 0
  p.win.performance.now = () => t
  const host = recordingHost()
  const v = mount(p.el, host)
  viewBody(p.el).dispatch("wheel", { deltaY: -15 * 20 }) // 20 lines up
  assert.deepEqual(host.of("scroll"), [], "nothing before the next frame")
  const steps = []
  for (let i = 0; i < 100 && p.frames.size; i++) {
    t += 16
    p.tick()
    steps.push(host.of("scroll").length)
  }
  const sent = host.of("scroll").map((s) => s[0])
  assert.equal(sent.reduce((a, b) => a + b, 0), 20)
  assert.ok(sent.length > 2, `eased over frames: ${sent}`)
  assert.ok(sent[0] >= sent.at(-1), `fast first, slow last: ${sent}`)
  v.unmount()
})

test("typing stops an eased scroll; scrollBy eases too; smoothScroll false scrolls at once", () => {
  const p = page()
  let t = 0
  p.win.performance.now = () => t
  const host = recordingHost()
  const v = mount(p.el, host)
  v.scrollBy(30)
  t += 16
  p.tick()
  const first = host.of("scroll").length
  assert.equal(first, 1)
  v.key({ key: "a", code: "KeyA", shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, preventDefault() {} })
  assert.equal(p.frames.size, 0, "no more steps after a key")
  v.unmount()

  const q = page()
  const h2 = recordingHost()
  const w = mount(q.el, h2, { smoothScroll: false })
  w.scrollBy(-12)
  assert.deepEqual(h2.of("scroll"), [[-12]])
  w.unmount()
})

test("easeLines: at least a line, never past what is left, the ease's share otherwise", () => {
  assert.equal(easeLines(0, 0.016), 0)
  assert.equal(easeLines(1, 0.016), 1)
  assert.equal(easeLines(-1, 0.016), -1)
  assert.equal(easeLines(100, 0.016), Math.trunc(100 * (1 - Math.exp(-16 * 0.016))))
  assert.equal(easeLines(-100, 0.016), -easeLines(100, 0.016))
  // A long gap is capped at 50 ms.
  assert.equal(easeLines(100, 5), easeLines(100, 0.05))
  for (const left of [2, 7, 40, 500]) assert.ok(easeLines(left, 0.05) <= left)
})

test("SmoothScroll: small steps go at once, a turn drops the rest, the history's end stops it", () => {
  const frames = []
  const applied = []
  let t = 0
  const s = new SmoothScroll({
    raf: (fn) => frames.push(fn),
    cancel: () => { frames.length = 0 },
    now: () => t,
    apply: (n) => applied.push(n),
  })
  s.by(AT_ONCE)
  assert.deepEqual(applied, [AT_ONCE])
  assert.equal(frames.length, 0)
  s.by(50)
  assert.equal(frames.length, 1)
  s.by(-2) // a turn: the 50 up are dropped, and 2 down go at once
  assert.equal(s.left, 0)
  assert.deepEqual(applied.at(-1), -2)
  s.by(50)
  t += 16
  frames.shift()()
  assert.ok(s.left > 0 && s.left < 50)
  s.clamp(400, 400) // at the top: nothing more to scroll up
  assert.equal(s.left, 0)
  assert.equal(frames.length, 0)
})
