// Debug builds only (RSZ_UI_TEST=<scenario>): drives the pointer, paste and
// IME paths with DOM events, as the webview would deliver them, and logs
// what the core then holds. Posted OS mouse events don't reach a WKWebView
// in the background, and this needs nobody's real pointer.
import { cellMetrics } from "./frame.js"

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

export async function run(name, { invoke, log, first }) {
  const pane = first
  const say = (...a) => log("selftest", ...a)
  const state = async () => (await invoke("debug_pane", { id: pane.id })) || ["", null]
  const sel = async () => (await state())[1]
  const screen = async () => (await state())[0]

  // The client point at the middle of a cell (or its left/right edge).
  function at(col, row, edge = 0.5) {
    const { cw, ch } = cellMetrics()
    const r = pane.canvas.getBoundingClientRect()
    return { clientX: r.left + (col + edge) * cw, clientY: r.top + (row + 0.5) * ch }
  }
  const fire = (target, type, init) =>
    target.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, view: window, ...init }))
  async function click(col, row, opts = {}) {
    const p = at(col, row, opts.edge)
    for (let n = 1; n <= (opts.count || 1); n++) {
      fire(pane.body, "mousedown", { ...p, detail: n, button: opts.button || 0, buttons: 1, ...opts.mods })
      fire(window, "mouseup", { ...p, detail: n, button: opts.button || 0, ...opts.mods })
      await sleep(30)
    }
    await sleep(150)
  }
  async function drag(c0, r0, c1, r1, mods = {}) {
    const a = at(c0, r0, 0.1)
    fire(pane.body, "mousedown", { ...a, detail: 1, button: 0, buttons: 1, ...mods })
    for (let i = 1; i <= 8; i++) {
      const b = at(c0 + ((c1 - c0) * i) / 8, r0 + ((r1 - r0) * i) / 8, 0.9)
      fire(window, "mousemove", { ...b, buttons: 1, ...mods })
      await sleep(20)
    }
    fire(window, "mouseup", { ...at(c1, r1, 0.9), button: 0, ...mods })
    await sleep(150)
  }
  const type = async (s) => { await invoke("pane_write", { id: pane.id, data: s }); await sleep(600) }
  const lines = async () => (await screen()).split("\n")
  const findRow = async (needle) => (await lines()).findIndex((l) => l.includes(needle))

  await sleep(1500)
  say("start", name)
  if (name === "pointer") {
    await type("clear; printf 'see https://example.com/docs and www.rust-lang.org ok\\n\\e]8;;https://github.com/StephenSHorton\\e\\\\osc8 link\\e]8;;\\e\\\\ after\\nls /usr/local/bin --all\\n'\r")
    const r = await findRow("see https://")
    const col = (await lines())[r].indexOf("example")
    await click(col + 2, r, { count: 2 })
    say("double-click word:", JSON.stringify(await sel()))
    await click(1, r, { count: 3 })
    say("triple-click line:", JSON.stringify(await sel()))
    const r2 = await findRow("ls /usr/local")
    await click(5, r2, { count: 2 })
    say("double-click path:", JSON.stringify(await sel()))
    await drag(4, r, 7, r + 1)
    say("drag two rows:", JSON.stringify(await sel()))
    await click(9, r + 1, { mods: { shiftKey: true } })
    say("shift+click extends:", JSON.stringify(await sel()))
    say("screenshot: selection")
    await sleep(2500)
    await click(0, r + 3)
    say("single click clears:", JSON.stringify(await sel()))

    // Links: hover shows the underline, Cmd/Ctrl+click opens (dry here).
    const opened = []
    window.rszDryOpen = (u) => opened.push(u)
    const lc = (await lines())[r].indexOf("https://") + 3
    fire(pane.body, "mousemove", { ...at(lc, r), buttons: 0 })
    await sleep(300)
    const hov = pane.body.querySelector(".link-hover")
    say("hover:", !hov.hidden, hov.title.split("\n")[0], pane.body.classList.contains("on-link"))
    say("screenshot: hover")
    await sleep(2500)
    await click(lc, r, { mods: { metaKey: true, ctrlKey: false } })
    await click(lc, r, { mods: { ctrlKey: true, metaKey: false } })
    const wc = (await lines())[r].indexOf("www.") + 2
    fire(pane.body, "mousemove", { ...at(wc, r), buttons: 0 })
    await sleep(300)
    await click(wc, r, { mods: { metaKey: true, ctrlKey: true } })
    fire(pane.body, "mousemove", { ...at(2, r + 1), buttons: 0 })
    await sleep(300)
    await click(2, r + 1, { mods: { metaKey: true, ctrlKey: true } })
    say("opened:", JSON.stringify(opened))
    fire(pane.body, "mousemove", { ...at(30, r + 3), buttons: 0 })
    await sleep(300)
    say("hover off:", hov.hidden)
    // A plain click on a link doesn't open it.
    const before = opened.length
    await click(lc, r)
    say("plain click opens:", opened.length !== before)
  } else if (name === "mouse") {
    // An app that turns on 1002 + SGR, then shows what it receives.
    await type("clear; printf '\\e[?1002h\\e[?1006h'; cat -v\r")
    say("modes:", pane.modes, "took:", await invoke("pane_mouse", { id: pane.id, ev: { kind: "press", button: 0, held: null, col: 0, row: 0, shift: false, alt: false, ctrl: false } }))
    await click(4, 5)
    await drag(2, 3, 6, 3)
    const p = at(3, 2)
    pane.body.dispatchEvent(new WheelEvent("wheel", { ...p, deltaY: -cellMetrics().ch * 2, bubbles: true }))
    pane.body.dispatchEvent(new WheelEvent("wheel", { ...p, deltaY: cellMetrics().ch, bubbles: true }))
    await sleep(200)
    await click(4, 5, { button: 2 })
    // Shift bypasses the app: a selection instead of a report.
    await drag(0, 0, 3, 0, { shiftKey: true })
    say("shift-drag selection:", JSON.stringify(await sel()))
    await type("\r")
    say("screen:", JSON.stringify((await screen()).trim().split("\n").slice(-4)))
    say("screenshot: mouse")
    await sleep(2500)
    await type("\x03")
    await type("printf '\\e[?1002l\\e[?1006l\\e[?1000h'; cat -v\r")
    await click(4, 5)
    await type("\r")
    say("x10 screen:", JSON.stringify((await screen()).trim().split("\n").slice(-2)))
    await type("\x03printf '\\e[?1000l'\r")
  } else if (name === "paste") {
    await type("clear; printf '\\e[?2004h'; cat -v\r")
    await invoke("pane_paste", { id: pane.id, text: "one\ntwo \x1b[201~three" })
    await type("\r")
    say("bracketed:", JSON.stringify((await screen()).trim().split("\n").slice(-2)))
    await type("\x03printf '\\e[?2004l'; cat -v\r")
    await invoke("pane_paste", { id: pane.id, text: "plain\nline" })
    await type("\x04")
    say("plain:", JSON.stringify((await screen()).trim().split("\n").slice(-3)))
    // Right-click pastes the system clipboard (set by the runner).
    await type("cat -v\r")
    await click(3, 3, { button: 2 })
    await type("\r\x04")
    say("right-click:", JSON.stringify((await screen()).trim().split("\n").slice(-3)))
  } else if (name === "copykey") {
    // The runner presses ⌘C (keypost) after "ready".
    await type("clear; echo copy this word\r")
    const r = await findRow("copy this")
    await click(6, r, { count: 2 })
    say("selected:", JSON.stringify(await sel()))
    say("ready")
    await sleep(3000)
  } else if (name === "pastekey") {
    // The runner presses ⌘V after "ready".
    await type("clear; cat -v\r")
    say("ready")
    await sleep(2500)
    await type("\r\x04")
    say("pasted:", JSON.stringify((await screen()).trim().split("\n").slice(-3)))
  } else if (name === "barpaste") {
    // At the prompt ⌘V (runner) goes into the input bar, not the shell.
    await sleep(1500)
    say("bar active:", pane.barActive())
    say("ready")
    await sleep(2500)
    say("bar text:", JSON.stringify(pane.barEl.querySelector(".lines").textContent))
    const caret = pane.barEl.querySelector(".caret").getBoundingClientRect()
    const i = pane.input.getBoundingClientRect()
    say("textarea at bar caret:", Math.round(i.left - caret.left), Math.round(i.top - caret.top))
  } else if (name === "imereal") {
    // The runner switches to a real Japanese IME and types "nihon", Space,
    // Return through the OS (keypost) after "ready".
    await sleep(1500)
    say("ready")
    await sleep(2500)
    say("screenshot: composing")
    await sleep(3500)
    say("bar text:", JSON.stringify(pane.barEl.querySelector(".lines").textContent))
  } else if (name === "blocks") {
    // #296: bar lines become blocks, the shell's echo is hidden, a line
    // sent while the shell is busy waits in the queue.
    await sleep(1500)
    say("bar active:", pane.barActive())
    await pane.submit("echo one")
    await sleep(1200)
    await pane.submit("sleep 1; echo two")
    await sleep(100)
    await pane.submit("echo three")
    say("queued:", pane.queued)
    await sleep(4000)
    await pane.submit("cd")
    await sleep(1200)
    say("screen:", JSON.stringify((await screen()).split("\n").filter((l) => l.trim())))
    say("screenshot: blocks")
    await sleep(2500)
  } else if (name === "ime") {
    // The textarea follows the cursor so the candidate window opens there.
    await type("clear; echo ime test\r")
    const input = pane.input
    // At the prompt the caret is the input bar's.
    const c = (pane.barActive() ? pane.barEl.querySelector(".caret") : pane.cursor).getBoundingClientRect()
    const i = input.getBoundingClientRect()
    say("textarea at cursor:", Math.round(i.left - c.left), Math.round(i.top - c.top))
    input.focus()
    input.dispatchEvent(new CompositionEvent("compositionstart", { data: "" }))
    input.value = "にほ"
    input.dispatchEvent(new CompositionEvent("compositionupdate", { data: "にほ" }))
    const pre = document.querySelector(".preedit")
    say("preedit:", pre && !pre.hidden, pre?.textContent)
    say("screenshot: preedit")
    await sleep(2500)
    input.value = "日本"
    input.dispatchEvent(new CompositionEvent("compositionend", { data: "日本" }))
    await sleep(400)
    say("preedit after commit:", pre.hidden)
    say("committed to:", pane.barActive() ? "bar " + JSON.stringify(pane.barEl.querySelector(".lines").textContent) : JSON.stringify((await lines()).find((l) => l.includes("日本"))))
  } else if (name === "panedrag") {
    // #293: drag pane 1's title onto pane 2's bottom half (pane_drag.js).
    const shape = (n) => (n.pane ? String(n.pane.id) : `${n.dir}(${shape(n.a)},${shape(n.b)})`)
    const second = await window.rszHost.split("h", pane)
    await sleep(500)
    say("before:", shape(window.rszHost.tree()))
    const h = pane.head.getBoundingClientRect(), r = second.el.getBoundingClientRect()
    const from = { clientX: h.left + 40, clientY: h.top + h.height / 2 }
    const to = { clientX: r.left + r.width / 2, clientY: r.top + r.height * 0.9 }
    fire(pane.head, "mousedown", { ...from, button: 0, buttons: 1 })
    for (let i = 1; i <= 10; i++) {
      fire(window, "mousemove", { clientX: from.clientX + (to.clientX - from.clientX) * i / 10, clientY: from.clientY + (to.clientY - from.clientY) * i / 10, buttons: 1 })
      await sleep(20)
    }
    say("screenshot: drop hint")
    await sleep(1500)
    fire(window, "mouseup", { ...to, button: 0 })
    await sleep(400)
    say("after:", shape(window.rszHost.tree()), "focused:", window.rszHost.focused().id)
    // A drop that changes nothing (back onto pane 2's top, where pane 2 already is above it) is ignored.
    const h2 = second.head.getBoundingClientRect(), r1 = pane.el.getBoundingClientRect()
    fire(second.head, "mousedown", { clientX: h2.left + 40, clientY: h2.top + 5, button: 0, buttons: 1 })
    fire(window, "mousemove", { clientX: r1.left + r1.width / 2, clientY: r1.top + 8, buttons: 1 })
    fire(window, "mouseup", { clientX: r1.left + r1.width / 2, clientY: r1.top + 8, button: 0 })
    await sleep(300)
    say("no-op drop:", shape(window.rszHost.tree()))
  }
  say("done", name)
}
