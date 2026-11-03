// node --test ui/
import { test } from "node:test"
import assert from "node:assert/strict"

import { InputBar, barKey, displayPath, MAX_VISUAL_ROWS } from "./bar.js"

const bar = (text, cursor) => {
  const b = new InputBar()
  b.insert(text)
  if (cursor !== undefined) b.cursor = cursor
  return b
}
const key = (b, k, mods = {}, opts = { isMac: true, cols: 20 }) =>
  barKey(b, { key: k, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...mods }, opts)

test("insert cleans control characters and keeps newlines", () => {
  const b = bar("a\tb\r\nc\x07d")
  assert.equal(b.text(), "a  b\ncd")
  assert.equal(b.cursor, 7)
  const e = bar("🙂x", 1)
  e.insert("é")
  assert.equal(e.text(), "🙂éx")
})

test("word moves and deletes stop at spaces and newlines", () => {
  const b = bar("git commit -m\nfix")
  b.moveWordLeft()
  assert.equal(b.cursor, 14)
  b.moveWordLeft()
  assert.equal(b.cursor, 11)
  b.deleteWordLeft()
  assert.equal(b.text(), "git -m\nfix")
  b.moveWordRight()
  assert.equal(b.cursor, 7)
  b.cursor = 0
  b.deleteWordRight()
  assert.equal(b.text(), "-m\nfix")
})

test("Home and End are the logical line; Cmd+Up/Down the buffer", () => {
  const b = bar("one\ntwo three", 6)
  b.moveHome()
  assert.equal(b.cursor, 4)
  b.moveEnd()
  assert.equal(b.cursor, 13)
  key(b, "ArrowUp", { metaKey: true })
  assert.equal(b.cursor, 0)
  key(b, "ArrowDown", { metaKey: true })
  assert.equal(b.cursor, 13)
})

test("history: up saves the draft, down restores it, no repeats", () => {
  const b = new InputBar()
  for (const l of ["ls", "ls", "pwd", "  "]) {
    b.insert(l)
    b.submit()
  }
  assert.deepEqual(b.history, ["ls", "pwd"])
  b.insert("dra")
  b.historyUp()
  assert.equal(b.text(), "pwd")
  b.historyUp()
  b.historyUp()
  assert.equal(b.text(), "ls")
  b.historyDown()
  assert.equal(b.text(), "pwd")
  b.historyDown()
  assert.equal(b.text(), "dra")
  // Editing a recalled line leaves history browsing.
  b.historyUp()
  b.insert("!")
  assert.equal(b.histIdx, -1)
})

test("Up and Down move inside a multi-row buffer before history", () => {
  const b = new InputBar()
  b.history = ["old"]
  b.insert("abc\ndefgh")
  key(b, "ArrowUp")
  assert.deepEqual([b.text(), b.cursor], ["abc\ndefgh", 3], "row 2 col 5 → end of row 1")
  key(b, "ArrowUp")
  assert.equal(b.text(), "old", "first row: history")
})

test("layout soft-wraps at the width and keeps the caret", () => {
  const b = bar("abcdefgh\nxy")
  assert.deepEqual(b.layout(3), { lines: ["abc", "def", "gh", "xy"], row: 3, col: 2 })
  b.cursor = 3
  assert.deepEqual(b.layout(3).row, 0)
  const t = bar("a\n")
  assert.deepEqual(t.layout(10), { lines: ["a", ""], row: 1, col: 0 })
  assert.deepEqual(new InputBar().layout(10), { lines: [""], row: 0, col: 0 })
})

test("visual rows cap at the maximum and the view follows the caret", () => {
  const b = bar("\n".repeat(20))
  assert.equal(b.visualRows(10), MAX_VISUAL_ROWS)
  const v = b.visible(10)
  assert.equal(v.lines.length, MAX_VISUAL_ROWS)
  assert.equal(v.row, MAX_VISUAL_ROWS - 1)
  b.cursor = 0
  assert.equal(b.visible(10).row, 0)
})

test("Enter submits, Shift+Enter adds a line", () => {
  const b = bar("echo hi")
  assert.deepEqual(key(b, "Enter", { shiftKey: true }), { kind: "edit" })
  assert.equal(b.text(), "echo hi\n")
  b.backspace()
  assert.deepEqual(key(b, "Enter"), { kind: "submit", line: "echo hi" })
  assert.equal(b.text(), "")
  assert.deepEqual(key(b, "Enter"), { kind: "submit", line: "" }, "an empty Enter still goes to the shell")
})

test("⌘⌫ clears the bar; Option/Ctrl+⌫ a word; on Windows Ctrl is the word key", () => {
  const b = bar("one two")
  key(b, "Backspace", { altKey: true })
  assert.equal(b.text(), "one ")
  key(b, "Backspace", { metaKey: true })
  assert.equal(b.text(), "")
  const w = bar("one two")
  key(w, "Backspace", { ctrlKey: true }, { isMac: false, cols: 20 })
  assert.equal(w.text(), "one ")
})

test("Ctrl+C clears the bar, or interrupts when it is empty", () => {
  const b = bar("half typed")
  assert.deepEqual(key(b, "c", { ctrlKey: true }), { kind: "edit" })
  assert.equal(b.text(), "")
  assert.deepEqual(key(b, "c", { ctrlKey: true }), { kind: "pty", data: "\x03" })
  assert.deepEqual(key(b, "d", { ctrlKey: true }), { kind: "pty", data: "\x04" })
  assert.deepEqual(key(bar("x"), "d", { ctrlKey: true }), { kind: "none" })
  assert.deepEqual(key(b, "l", { ctrlKey: true }), { kind: "pty", data: "\x0c" })
})

test("Ctrl+U/K/A/E/W edit the line", () => {
  const b = bar("aa bb\ncc dd", 9)
  key(b, "u", { ctrlKey: true })
  assert.deepEqual([b.text(), b.cursor], ["aa bb\ndd", 6])
  key(b, "k", { ctrlKey: true })
  assert.equal(b.text(), "aa bb\n")
  key(b, "a", { ctrlKey: true })
  b.cursor = 5
  key(b, "w", { ctrlKey: true })
  assert.equal(b.text(), "aa \n")
})

test("keys the bar does not own are left to the app", () => {
  assert.equal(key(bar(""), "d", { metaKey: true }), null)
  assert.equal(key(bar(""), "x"), null)
  assert.deepEqual(key(bar(""), "Tab"), { kind: "none" })
  assert.deepEqual(key(bar(""), "PageUp"), { kind: "scroll", pages: 0.5 })
})

test("displayPath shortens home", () => {
  assert.equal(displayPath("/Users/s/projects", "/Users/s"), "~/projects")
  assert.equal(displayPath("/Users/s", "/Users/s"), "~")
  assert.equal(displayPath("/Users/stephen", "/Users/s"), "/Users/stephen")
  assert.equal(displayPath("C:\\Users\\s\\x", "C:\\Users\\s"), "~\\x")
})
