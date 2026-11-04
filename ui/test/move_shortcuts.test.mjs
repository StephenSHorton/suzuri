import { test } from "node:test"
import assert from "node:assert/strict"
import { shortcut } from "../shortcuts.js"

const k = (o) => ({ key: "", code: "", metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...o })

test("move pane chords (Go's move_pane_*)", () => {
  assert.deepEqual(shortcut(k({ key: "ArrowLeft", metaKey: true, ctrlKey: true }), true), { id: "move_pane_left", arg: undefined })
  assert.deepEqual(shortcut(k({ key: "ArrowDown", ctrlKey: true, altKey: true }), false), { id: "move_pane_down", arg: undefined })
  // ⌘⌥ stays focus, and Ctrl+Alt+Shift stays move-to-tab.
  assert.equal(shortcut(k({ key: "ArrowLeft", metaKey: true, altKey: true }), true).id, "focus_left")
  assert.equal(shortcut(k({ key: "ArrowLeft", ctrlKey: true, altKey: true, shiftKey: true }), false).id, "move_pane_prev_tab")
})
