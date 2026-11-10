// node --test ui/test
// suzuri's keys, text and pastes on a pane's TermView (ui/pane_input.js),
// mounted into the view package's DOM stub: what used to be app.js's one
// shared textarea. The order is the old handler's: the view's clipboard
// chords, Escape on a large image, app shortcuts, the panel shortcuts, the
// input bar at a prompt, then the view's key encoder.
import { test } from "node:test"
import assert from "node:assert/strict"
import { page, recordingHost, key, textarea, viewBody, load } from "../view/test/stub.mjs"

const { mount } = await import("../view/index.js")
const { keyFilter, pageKeys, textFilter, pasteFilter, pasteText, writeFrom } = await import("../pane_input.js")
const { shortcut } = await import("../shortcuts.js")
const { M_ALT_SCREEN } = await import("../view/frame.js")

// A PaneView's input side, recording what it was asked to do.
function setup({ isMac = false, bar = false, largeImage = false, copyOrInterrupt } = {}) {
  const p = page()
  p.win.performance.now = () => 100_000
  const did = []
  const pane = {
    id: 3, keyTime: 0, bar: { text: "", insert(t) { this.text += t } },
    barActive: () => bar,
    barKeydown: (e) => (did.push(["bar", e.key]), e.key.length === 1),
    renderBar: () => did.push(["renderBar"]),
    clearQueue: () => did.push(["clearQueue"]),
    send: (data, t0) => did.push(["send", data, t0]),
  }
  const invoked = []
  const invoke = (cmd, args) => (invoked.push([cmd, args]), Promise.resolve())
  const env = {
    isMac, shortcut,
    closeLarge: () => (largeImage ? (largeImage = false, did.push(["closeLarge"]), true) : false),
    run: (id, arg) => did.push(["run", id, arg]),
    now: () => 42,
    copyOrInterrupt,
  }
  const host = recordingHost({
    write: writeFrom(pane),
    paste: (text) => (text == null ? did.push(["clipboardPaste"]) : pasteText(pane, text, invoke)),
    keyFilter: keyFilter(pane, env),
    textFilter: textFilter(pane),
    pasteFilter: pasteFilter(pane, {
      invoke, log: (...a) => did.push(["log", ...a]),
      clipboardImage: (cd) => cd.image || null,
      saveClipboardImage: async (img) => `/tmp/${img}.png`,
    }),
  })
  pane.view = mount(p.el, host, { isMac, reportFocus: false })
  const ta = textarea(p.el)
  const press = (k) => ta.dispatch("keydown", key({ timeStamp: 7, ...k }))
  return { p, pane, did, invoked, host, ta, press, setBar: (on) => { bar = on } }
}

test("a terminal key goes out with its event time, through pane.send", () => {
  const { did, press, pane } = setup()
  const e = press({ key: "a", code: "KeyA" })
  assert.equal(e.defaultPrevented, true)
  assert.deepEqual(did, [["send", "a", 7]])
  press({ key: "ArrowUp", code: "ArrowUp", timeStamp: 0 })
  assert.deepEqual(did.at(-1), ["send", "\x1b[A", 42], "no timeStamp: now")
  pane.view.unmount()
})

test("^C from a key drops the queue; a ^C after an empty copy does not", async () => {
  const { did, press, pane, p } = setup()
  press({ key: "c", code: "KeyC", ctrlKey: true })
  assert.deepEqual(did, [["clearQueue"], ["send", "\x03", 7]])
  // With a selection Ctrl+C copies first; nothing copied sends ^C, no key time.
  viewBody(p.el).dispatch("mousedown", { button: 0, detail: 2, clientX: 4, clientY: 4 })
  did.length = 0
  press({ key: "c", code: "KeyC", ctrlKey: true })
  await new Promise((r) => setImmediate(r))
  assert.deepEqual(did, [["send", "\x03", undefined]])
  pane.view.unmount()
})

test("Ctrl+C with a selection is suzuri's when it has copyOrInterrupt (^C to the pane focused by then)", () => {
  const calls = []
  const { did, press, pane, p, host } = setup({ copyOrInterrupt: () => calls.push("copyOrInterrupt") })
  viewBody(p.el).dispatch("mousedown", { button: 0, detail: 2, clientX: 4, clientY: 4 })
  const e = press({ key: "c", code: "KeyC", ctrlKey: true })
  assert.equal(e.defaultPrevented, true)
  assert.deepEqual(calls, ["copyOrInterrupt"])
  assert.equal(host.of("copy").length, 0, "not the view's copy")
  assert.deepEqual(did, [])
  // Without a selection it stays the terminal's ^C.
  pane.view.unmount()
})

test("the key time lasts only for that keydown's write", async () => {
  const { pane, press } = setup()
  press({ key: "Shift", code: "ShiftLeft", shiftKey: true }) // no bytes
  assert.equal(pane.keyTime, 7)
  await Promise.resolve()
  assert.equal(pane.keyTime, 0)
  pane.view.unmount()
})

test("app shortcuts beat the terminal and are prevented", () => {
  const { did, press, host, pane } = setup()
  const e = press({ key: "T", code: "KeyT", ctrlKey: true, shiftKey: true })
  assert.equal(e.defaultPrevented, true)
  assert.deepEqual(did, [["run", "new_tab", undefined]])
  press({ key: "ArrowLeft", code: "ArrowLeft", altKey: true })
  assert.deepEqual(did.at(-1), ["run", "focus_left", undefined])
  assert.deepEqual(host.of("write"), [])
  pane.view.unmount()
})

test("the clipboard chords stay the view's, ahead of the bar and the shortcuts", () => {
  const { did, press, pane, host } = setup({ bar: true })
  press({ key: "v", code: "KeyV", ctrlKey: true })
  press({ key: "C", code: "KeyC", ctrlKey: true, shiftKey: true })
  assert.deepEqual(did, [["clipboardPaste"]])
  assert.equal(host.of("copy").length, 1)
  pane.view.unmount()
})

test("Escape closes a large image before the bar or the terminal sees it", () => {
  const { did, press, pane } = setup({ bar: true, largeImage: true })
  const e = press({ key: "Escape", code: "Escape" })
  assert.equal(e.defaultPrevented, true)
  assert.deepEqual(did, [["closeLarge"]])
  press({ key: "Escape", code: "Escape" })
  assert.deepEqual(did.at(-1), ["bar", "Escape"], "no image: the bar's")
  pane.view.unmount()
})

test("at a prompt the bar has every other key, taken or not", () => {
  const { did, press, pane, host } = setup({ bar: true })
  const a = press({ key: "a", code: "KeyA" })
  const f5 = press({ key: "F5", code: "F5" })
  assert.equal(a.defaultPrevented, true)
  assert.equal(f5.defaultPrevented, false, "the bar didn't take it")
  assert.deepEqual(did, [["bar", "a"], ["bar", "F5"]])
  assert.deepEqual(host.of("write"), [])
  pane.view.unmount()
})

test("IME commits go into the bar at a prompt, to the PTY otherwise", () => {
  const { did, ta, pane, setBar } = setup({ bar: true })
  ta.value = "日本"
  ta.dispatch("compositionend", {})
  assert.equal(pane.bar.text, "日本")
  assert.deepEqual(did, [["renderBar"]])
  setBar(false)
  ta.value = "語"
  ta.dispatch("compositionend", {})
  assert.deepEqual(did.at(-1), ["send", "語", undefined])
  pane.view.unmount()
})

test("paste events: text to the bar at a prompt, else pane_paste; images only count on the alt screen", () => {
  const { ta, pane, invoked, setBar } = setup({ bar: true })
  const cd = (text, image) => ({ image, getData: (t) => (t === "text/plain" ? text : "") })
  ta.dispatch("paste", { clipboardData: cd("one\r\ntwo") })
  assert.equal(pane.bar.text, "one\ntwo")
  setBar(false)
  ta.dispatch("paste", { clipboardData: cd("ls") })
  assert.deepEqual(invoked, [["pane_paste", { id: 3, text: "ls" }]])
  // On the main screen an image is ignored; its text goes.
  ta.dispatch("paste", { clipboardData: cd("caption", "shot") })
  assert.deepEqual(invoked.at(-1), ["pane_paste", { id: 3, text: "caption" }])
  pane.view.unmount()
})

test("an image paste on the alt screen pastes the saved file's path", async () => {
  const { ta, pane, invoked, p } = setup()
  const alt = load("full.bin")
  new DataView(alt).setUint32(12, M_ALT_SCREEN, true)
  pane.view.feed(alt)
  p.tick()
  assert.equal(pane.view.modes, M_ALT_SCREEN)
  ta.dispatch("paste", { clipboardData: { image: "shot", getData: () => "caption" } })
  await new Promise((r) => setImmediate(r))
  assert.deepEqual(invoked, [["pane_paste", { id: 3, text: "/tmp/shot.png" }]])
  pane.view.unmount()
})

test("the wheel scrolls the pane's history through host.scroll", () => {
  const { p, host, pane } = setup()
  viewBody(p.el).dispatch("wheel", { deltaY: -45 })
  assert.deepEqual(host.of("scroll"), [[3]])
  pane.view.unmount()
})

test("pageKeys: with no pane's textarea focused the app shortcuts and chords still run", () => {
  const p = page()
  const did = []
  let blocked = false
  let large = true
  const keys = pageKeys({
    isMac: false, page: [p.doc.body, null],
    blocked: () => blocked,
    hasSelection: () => false,
    clip: (kind) => did.push(["clip", kind]),
    closeLarge: () => (large ? (large = false, did.push(["closeLarge"]), true) : false),
    shortcut,
    run: (id, arg) => did.push(["run", id, arg]),
  })
  const ev = (k, target = p.doc.body) => {
    const e = { target, defaultPrevented: false, preventDefault() { this.defaultPrevented = true }, ...key(k) }
    keys(e)
    return e
  }
  assert.equal(ev({ key: "T", code: "KeyT", ctrlKey: true, shiftKey: true }).defaultPrevented, true)
  ev({ key: "v", code: "KeyV", ctrlKey: true })
  ev({ key: "Escape", code: "Escape" })
  assert.equal(ev({ key: "a", code: "KeyA" }).defaultPrevented, false, "typing goes nowhere, as before")
  assert.deepEqual(did, [["run", "new_tab", undefined], ["clip", "paste"], ["closeLarge"]])
  // A key in a pane, a panel or a box is theirs; nothing while an overlay is open.
  did.length = 0
  ev({ key: "T", code: "KeyT", ctrlKey: true, shiftKey: true }, p.doc.createElement("textarea"))
  blocked = true
  ev({ key: "T", code: "KeyT", ctrlKey: true, shiftKey: true })
  ev({ key: "T", code: "KeyT", ctrlKey: true, shiftKey: true, isComposing: true })
  assert.deepEqual(did, [])
})

test("Ctrl+C on an exited pane still drops the queue; nothing is sent", () => {
  const { did, press, pane } = setup()
  pane.view.message({ t: "exit", code: 0 })
  const e = press({ key: "c", code: "KeyC", ctrlKey: true })
  assert.equal(e.defaultPrevented, true)
  assert.deepEqual(did, [["clearQueue"]])
  pane.view.unmount()
})

test("pageKeys: with the page focused and a pane focused, typing reaches that pane and it takes the keyboard back", () => {
  const { did, pane, p, ta } = setup()
  p.doc.activeElement = p.doc.body
  const keys = pageKeys({
    isMac: false, page: [p.doc.body, null],
    blocked: () => false,
    pane: () => pane,
    hasSelection: () => false,
    clip: (kind) => did.push(["clip", kind]),
    closeLarge: () => false,
    shortcut,
    run: (id, arg) => did.push(["run", id, arg]),
  })
  const ev = (k) => {
    const e = { target: p.doc.body, defaultPrevented: false, preventDefault() { this.defaultPrevented = true }, ...key({ timeStamp: 7, ...k }) }
    keys(e)
    return e
  }
  assert.equal(ev({ key: "a", code: "KeyA" }).defaultPrevented, true)
  assert.deepEqual(did, [["send", "a", 7]])
  assert.equal(p.doc.activeElement, ta, "the pane's textarea has the keyboard again")
  // App shortcuts still run, through the pane's keyFilter.
  did.length = 0
  ev({ key: "T", code: "KeyT", ctrlKey: true, shiftKey: true })
  assert.deepEqual(did, [["run", "new_tab", undefined]])
  pane.view.unmount()
})

test("writes wait behind a clipboard paste in flight, in order, and stop waiting once it settles", async () => {
  const { writeAfterPaste, pasteInOrder } = await import("../pane_input.js")
  const pane = {}
  const sent = []
  writeAfterPaste(pane, () => sent.push("a"))
  assert.deepEqual(sent, ["a"], "no paste: written at once")
  let answer
  const pasted = pasteInOrder(pane, () => new Promise((ok) => (answer = ok)).then(() => sent.push("paste")))
  writeAfterPaste(pane, () => sent.push("\r"))
  writeAfterPaste(pane, () => sent.push("b"))
  await new Promise((r) => setTimeout(r, 5))
  assert.deepEqual(sent, ["a"], "keys wait for the paste")
  answer()
  await pasted
  await new Promise((r) => setTimeout(r, 5))
  assert.deepEqual(sent, ["a", "paste", "\r", "b"])
  assert.equal(pane.pasteGate, null)
  // A failed paste still lets the keys through.
  const failed = pasteInOrder(pane, () => Promise.reject(new Error("clipboard not answering")))
  writeAfterPaste(pane, () => sent.push("c"))
  await assert.rejects(failed)
  await new Promise((r) => setTimeout(r, 5))
  assert.deepEqual(sent.at(-1), "c")
  writeAfterPaste(pane, () => sent.push("d"))
  assert.deepEqual(sent.at(-1), "d", "settled: written at once again")
})
