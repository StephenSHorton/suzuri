// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { shortcut } from "../shortcuts.js"
import { filterCommands } from "../palette.js"

const key = (key, mods = {}, code = "") => ({ key, code, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...mods })
const id = (e, mac) => shortcut(e, mac)?.id ?? null

test("macOS: Go suzuri's Cmd chords", () => {
  const M = true
  assert.equal(id(key("t", { metaKey: true }), M), "new_tab")
  assert.equal(id(key("T", { metaKey: true, shiftKey: true }), M), "new_tab")
  assert.equal(id(key("T", { ctrlKey: true, shiftKey: true }), M), "new_tab")
  assert.equal(id(key("N", { metaKey: true, shiftKey: true }), M), "new_window")
  assert.equal(id(key("k", { metaKey: true }), M), "palette")
  assert.equal(id(key("p", { metaKey: true }), M), "palette")
  assert.equal(id(key(",", { metaKey: true }, "Comma"), M), "settings")
  assert.equal(id(key("d", { metaKey: true }), M), "split_right")
  assert.equal(id(key("D", { metaKey: true, shiftKey: true }), M), "split_right")
  assert.equal(id(key("E", { metaKey: true, shiftKey: true }), M), "split_down")
  assert.equal(id(key("w", { metaKey: true }), M), "close_pane")
  assert.equal(id(key("W", { metaKey: true, shiftKey: true }), M), "close_tab")
  assert.equal(id(key("Tab", { ctrlKey: true }), M), "next_tab")
  assert.equal(id(key("Tab", { ctrlKey: true, shiftKey: true }), M), "prev_tab")
  assert.equal(id(key("}", { metaKey: true, shiftKey: true }, "BracketRight"), M), "next_tab")
  assert.equal(id(key("ArrowLeft", { metaKey: true, shiftKey: true }), M), "prev_tab")
  assert.equal(id(key("ArrowLeft", { metaKey: true, altKey: true }), M), "focus_left")
  assert.equal(id(key("ArrowUp", { ctrlKey: true, altKey: true }), M), "focus_up")
  assert.equal(id(key("ArrowRight", { metaKey: true, altKey: true, shiftKey: true }), M), "move_pane_next_tab")
  assert.equal(id(key("Enter", { metaKey: true, shiftKey: true }), M), "zoom_pane")
  assert.deepEqual(shortcut(key("3", { metaKey: true }, "Digit3"), M), { id: "select_tab", arg: 2 })
  assert.equal(id(key("F2"), M), "rename_pane")
})

test("macOS: terminal keys stay with the terminal", () => {
  const M = true
  for (const e of [key("a"), key("k", { ctrlKey: true }), key("p", { ctrlKey: true }), key("w", { ctrlKey: true }),
    key("ArrowLeft", { altKey: true }), key("1", { ctrlKey: true }, "Digit1"), key("c", { metaKey: true }), key("v", { metaKey: true })])
    assert.equal(id(e, M), null, JSON.stringify(e))
})

test("Windows: Go suzuri's Ctrl chords", () => {
  const W = false
  assert.equal(id(key("T", { ctrlKey: true, shiftKey: true }), W), "new_tab")
  assert.equal(id(key("N", { ctrlKey: true, shiftKey: true }), W), "new_window")
  assert.equal(id(key("k", { ctrlKey: true }), W), "palette")
  assert.equal(id(key("P", { ctrlKey: true, shiftKey: true }), W), "palette")
  assert.equal(id(key(",", { ctrlKey: true }, "Comma"), W), "settings")
  assert.equal(id(key("D", { ctrlKey: true, shiftKey: true }), W), "split_right")
  assert.equal(id(key("E", { ctrlKey: true, shiftKey: true }), W), "split_down")
  assert.equal(id(key("+", { altKey: true, shiftKey: true }, "Equal"), W), "split_right")
  assert.equal(id(key("_", { altKey: true, shiftKey: true }, "Minus"), W), "split_down")
  assert.equal(id(key("w", { ctrlKey: true }), W), "close_pane")
  assert.equal(id(key("W", { ctrlKey: true, shiftKey: true }), W), "close_pane")
  assert.equal(id(key("Tab", { ctrlKey: true }), W), "next_tab")
  assert.equal(id(key("ArrowLeft", { altKey: true }), W), "focus_left")
  assert.equal(id(key("ArrowLeft", { ctrlKey: true, altKey: true, shiftKey: true }), W), "move_pane_prev_tab")
  assert.deepEqual(shortcut(key("1", { ctrlKey: true }, "Digit1"), W), { id: "select_tab", arg: 0 })
  assert.equal(id(key("a", { ctrlKey: true }), W), null)
  assert.equal(id(key("c", { ctrlKey: true }), W), null)
  assert.equal(id(key("d", { metaKey: true }), W), null)
})

test("palette filter: every word, any order, case-insensitive", () => {
  const items = [
    { title: "New tab", category: "Tabs", hint: "⌘T" },
    { title: "Move pane to tab: 2 build", category: "Panes" },
    { title: "Split right", category: "Panes", hint: "⌘D" },
  ]
  assert.equal(filterCommands(items, "").length, 3)
  assert.deepEqual(filterCommands(items, "TAB").map((i) => i.title), ["New tab", "Move pane to tab: 2 build"])
  assert.deepEqual(filterCommands(items, "build move").map((i) => i.title), ["Move pane to tab: 2 build"])
  assert.deepEqual(filterCommands(items, "panes split").map((i) => i.title), ["Split right"])
  assert.equal(filterCommands(items, "zzz").length, 0)
})

test("font zoom: Cmd/Ctrl + - 0, as Go suzuri (#284)", () => {
  for (const [mac, mod] of [[true, { metaKey: true }], [false, { ctrlKey: true }]]) {
    assert.equal(id(key("=", mod, "Equal"), mac), "zoom_in")
    assert.equal(id(key("+", { ...mod, shiftKey: true }, "Equal"), mac), "zoom_in")
    assert.equal(id(key("-", mod, "Minus"), mac), "zoom_out")
    assert.equal(id(key("0", mod, "Digit0"), mac), "zoom_reset")
  }
  // Windows Terminal's split chords stay splits.
  assert.equal(id(key("+", { altKey: true, shiftKey: true }, "Equal"), false), "split_right")
  assert.equal(id(key("_", { altKey: true, shiftKey: true }, "Minus"), false), "split_down")
})
