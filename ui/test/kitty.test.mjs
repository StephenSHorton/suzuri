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
