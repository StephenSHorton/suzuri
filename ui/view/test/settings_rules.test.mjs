// node --test ui/view/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { foldOutside, restorePatch, pagesFor, newProfileName, changed, TERMINAL_PARTS, ALL_PARTS, LOOK_PARTS, SESSION_PARTS, PART_KEYS } from "../settings_rules.js"

test("Esc after an outside change keeps it and undoes only this screen's", () => {
  const snap = { font_size_px: 14, ui_theme: "neutral", cursor: "block" }
  const cfg = { font_size_px: 18, ui_theme: "neutral", cursor: "block" } // 18 set on this screen
  const outside = { font_size_px: 18, ui_theme: "matcha", cursor: "block" } // Hearth set matcha
  const next = foldOutside(snap, cfg, outside)
  assert.deepEqual(next, { font_size_px: 14, ui_theme: "matcha", cursor: "block" })
  assert.equal(snap.ui_theme, "neutral", "the old snapshot is not changed")
})

test("nothing changed elsewhere: the snapshot stays", () => {
  const snap = { font_size_px: 14 }
  assert.deepEqual(foldOutside(snap, { font_size_px: 16 }, { font_size_px: 16 }), snap)
})

test("Cancel puts back only the keys that differ, never the window frame", () => {
  const snap = { font_size_px: 14, cursor: "block", window: { w: 800 }, profiles: [{ name: "a" }] }
  const cfg = { font_size_px: 18, cursor: "block", window: { w: 900 }, profiles: [{ name: "a" }, { name: "b" }] }
  assert.deepEqual(restorePatch(snap, cfg), { font_size_px: 14, profiles: [{ name: "a" }] })
  assert.equal(restorePatch(snap, { ...snap, window: { w: 1 } }), null)
})

test("changed leaves out the window frame", () => {
  assert.equal(changed({ a: 1, window: 1 }, { a: 1, window: 2 }), false)
  assert.equal(changed({ a: 1 }, { a: 2 }), true)
})

test("parts: every part on one page, each with a settings key", () => {
  assert.deepEqual([...LOOK_PARTS, ...SESSION_PARTS], ALL_PARTS)
  for (const p of ALL_PARTS) assert.ok(PART_KEYS[p], p)
  assert.deepEqual(pagesFor().map((p) => p.id), ["look", "session"])
})

test("terminal parts: font, size, cursor on Look; new-tab profile and profiles on Session", () => {
  assert.deepEqual(pagesFor(TERMINAL_PARTS), [
    { id: "look", label: "Look", parts: ["font", "size", "cursor"] },
    { id: "session", label: "Session", parts: ["new_tab_profile", "profiles"] },
  ])
  assert.deepEqual(pagesFor(["profiles"]).map((p) => p.id), ["session"])
})

test("a new profile's name skips names already taken", () => {
  assert.equal(newProfileName([{ name: "default" }]), "Profile 2")
  assert.equal(newProfileName([{ name: "x" }, { name: "profile 3" }]), "Profile 4")
})

test("a host passing this screen's own save back: Cancel still undoes it", () => {
  const snap = { cursor: "block", font_size_px: 14 }
  const cfg = { cursor: "block", font_size_px: 14 } // the save is still in flight
  const echo = { cursor: "bar", font_size_px: 14 } // the host's settings event, with this screen's Bar
  assert.deepEqual(foldOutside(snap, cfg, echo, { cursor: "bar" }), snap)
  // Something else changed at the same time: that one is kept.
  assert.deepEqual(foldOutside(snap, cfg, { cursor: "bar", font_size_px: 20 }, { cursor: "bar" }), { cursor: "block", font_size_px: 20 })
})
