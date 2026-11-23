import { test } from "node:test"
import assert from "node:assert/strict"
import { foldOutside } from "../settings_follow.js"

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
