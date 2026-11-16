// node --test ui/test/
import { test } from "node:test"
import assert from "node:assert/strict"
import { kittyFlags, kittyActive, encodeEnter, encodeAltScreenChord } from "../kitty.js"
import { encodeKey } from "../keys.js"

const ALT = 4, KITTY1 = 1 << 10
const key = (o) => ({ key: "", code: "", shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, ...o })

test("flags come from the mode word", () => {
  assert.equal(kittyFlags(31 << 10 | 512), 31)
  assert.equal(kittyActive(0), false)
  assert.equal(kittyActive(KITTY1), true)
  assert.equal(kittyActive(2 << 10), false) // event types alone
})

test("Enter matches Go suzuri's encodeEnter", () => {
  assert.equal(encodeEnter(0, false, false, false, false), "\r")
  assert.equal(encodeEnter(0, true, false, false, false), "\x1b[13;2u")
  assert.equal(encodeEnter(0, false, true, false, false), "\x1b\r")
  assert.equal(encodeEnter(KITTY1, false, true, false, false), "\x1b[13;3u")
  assert.equal(encodeEnter(0, false, false, true, false), "\x1b[13;5u")
  assert.equal(encodeKey(key({ key: "Enter", shiftKey: true }), 0, true), "\x1b[13;2u")
})

test("alt-screen chords", () => {
  const mac = true
  assert.equal(encodeAltScreenChord(key({ key: "z", code: "KeyZ", metaKey: true }), mac), "\x1a")
  assert.equal(encodeAltScreenChord(key({ key: "Z", code: "KeyZ", metaKey: true, shiftKey: true }), mac), "\x1b[122;10u")
  assert.equal(encodeAltScreenChord(key({ key: ";", code: "Semicolon", ctrlKey: true }), mac), "\x1b[59;5u")
  assert.equal(encodeAltScreenChord(key({ key: "/", code: "Slash", ctrlKey: true }), mac), "\x1b[47;5u")
  assert.equal(encodeAltScreenChord(key({ key: "I", code: "KeyI", ctrlKey: true, shiftKey: true }), mac), "\x1b[105;6u")
  assert.equal(encodeAltScreenChord(key({ key: "a", code: "KeyA", ctrlKey: true }), mac), null)
  // Only on the alt screen; Cmd stays the app's on the main screen.
  assert.equal(encodeKey(key({ key: "z", code: "KeyZ", metaKey: true }), 0, mac), null)
  assert.equal(encodeKey(key({ key: "z", code: "KeyZ", metaKey: true }), ALT, mac), "\x1a")
  assert.equal(encodeKey(key({ key: "a", code: "KeyA", ctrlKey: true }), ALT, mac), "\x01")
})

test("macOS Cmd+Left / Cmd+Right go to the line ends as Ctrl+A / Ctrl+E; Option and Windows keys unchanged", () => {
  const mac = true
  for (const modes of [0, ALT]) {
    assert.equal(encodeKey(key({ key: "ArrowLeft", code: "ArrowLeft", metaKey: true }), modes, mac), "\x01")
    assert.equal(encodeKey(key({ key: "ArrowRight", code: "ArrowRight", metaKey: true }), modes, mac), "\x05")
  }
  // Other Cmd arrows and Cmd+Shift/Ctrl/Option chords stay the app's.
  assert.equal(encodeKey(key({ key: "ArrowUp", metaKey: true }), 0, mac), null)
  assert.equal(encodeKey(key({ key: "ArrowLeft", metaKey: true, shiftKey: true }), 0, mac), null)
  assert.equal(encodeKey(key({ key: "ArrowLeft", metaKey: true, altKey: true }), 0, mac), null)
  assert.equal(encodeKey(key({ key: "ArrowLeft", metaKey: true, ctrlKey: true }), 0, mac), null)
  // Option+Arrow (word jumps) is the xterm modified-cursor sequence, as before.
  assert.equal(encodeKey(key({ key: "ArrowLeft", altKey: true }), 0, mac), "\x1b[1;3D")
  assert.equal(encodeKey(key({ key: "ArrowRight", altKey: true }), 0, mac), "\x1b[1;3C")
  // Plain arrows, and non-Mac (Windows/Linux) keys, are unchanged.
  assert.equal(encodeKey(key({ key: "ArrowLeft" }), 0, mac), "\x1b[D")
  assert.equal(encodeKey(key({ key: "ArrowLeft", ctrlKey: true }), 0, false), "\x1b[1;5D")
  assert.equal(encodeKey(key({ key: "ArrowRight", ctrlKey: true }), 0, false), "\x1b[1;5C")
  assert.equal(encodeKey(key({ key: "ArrowLeft", metaKey: true }), 0, false), "\x1b[1;9D")
  assert.equal(encodeKey(key({ key: "Home" }), 0, false), "\x1b[H")
})
