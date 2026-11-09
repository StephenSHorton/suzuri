// node --test ui/test
// DECSET 1004 focus reports (ui/focus_report.js): pane, tab and window focus,
// each change once, only to panes whose program turned the mode on.
import { test } from "node:test"
import assert from "node:assert/strict"
import { FocusReports, FOCUS_IN, FOCUS_OUT } from "../focus_report.js"
import { M_FOCUS } from "../view/frame.js"

function setup() {
  const a = { id: 1, modes: M_FOCUS, exited: false }
  const b = { id: 2, modes: M_FOCUS, exited: false }
  const c = { id: 3, modes: 0, exited: false } // never asked
  const env = { focused: a, sent: [] }
  const r = new FocusReports({
    panes: () => [a, b, c],
    focused: () => env.focused,
    write: (p, data) => env.sent.push([p.id, data]),
    windowFocused: true,
  })
  return { a, b, c, env, r }
}

test("pane focus: the pane left hears ESC[O, the pane entered ESC[I, once each", () => {
  const { b, env, r } = setup()
  r.sync()
  assert.deepEqual(env.sent, [[1, FOCUS_IN], [2, FOCUS_OUT]])
  env.sent.length = 0
  r.sync()
  assert.deepEqual(env.sent, [], "nothing changed, nothing sent")
  env.focused = b
  r.sync()
  assert.deepEqual(env.sent, [[1, FOCUS_OUT], [2, FOCUS_IN]])
})

test("window focus: the focused pane hears ESC[O on blur and ESC[I on return", () => {
  const { env, r } = setup()
  r.sync()
  env.sent.length = 0
  r.setWindow(false)
  assert.deepEqual(env.sent, [[1, FOCUS_OUT]])
  r.setWindow(false)
  assert.equal(env.sent.length, 1)
  r.setWindow(true)
  assert.deepEqual(env.sent, [[1, FOCUS_OUT], [1, FOCUS_IN]])
})

test("a program that turns the mode on hears where it stands; off and on again, it hears again", () => {
  const { c, env, r } = setup()
  r.sync()
  env.sent.length = 0
  c.modes = M_FOCUS
  r.check(c)
  assert.deepEqual(env.sent, [[3, FOCUS_OUT]])
  c.modes = 0
  r.check(c)
  c.modes = M_FOCUS
  env.focused = c
  r.check(c)
  assert.deepEqual(env.sent, [[3, FOCUS_OUT], [3, FOCUS_IN]])
})

test("panes without an id yet, or exited, hear nothing", () => {
  const { a, env, r } = setup()
  a.id = 0
  r.check(a)
  a.id = 1
  a.exited = true
  r.check(a)
  assert.deepEqual(env.sent, [])
})

test("listen: the window's focus and blur events", () => {
  const { env, r } = setup()
  const ls = {}
  r.listen({ addEventListener: (type, fn) => { ls[type] = fn } })
  r.sync()
  env.sent.length = 0
  ls.blur()
  ls.focus()
  assert.deepEqual(env.sent, [[1, FOCUS_OUT], [1, FOCUS_IN]])
})
