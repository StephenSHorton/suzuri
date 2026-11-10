import { test } from "node:test"
import assert from "node:assert/strict"
import { hintChords } from "../palette.js"
import { chordLabels } from "../shortcuts.js"

test("palette hints: chord labels become Kbd keys", () => {
  assert.deepEqual(hintChords("Ctrl+Shift+T"), ["ctrl+shift+T"])
  assert.deepEqual(hintChords("Ctrl++"), ["ctrl+plus"])
  assert.deepEqual(hintChords("Ctrl+Shift+] · Ctrl+Tab"), ["ctrl+shift+]", "ctrl+tab"])
  assert.deepEqual(hintChords("Alt+←"), ["alt+left"])
  assert.deepEqual(hintChords("F2"), ["F2"])
  assert.deepEqual(hintChords("⌘⇧W"), ["meta+shift+W"])
  assert.deepEqual(hintChords("⌘+"), ["meta+plus"])
  assert.deepEqual(hintChords("⌘⇧M / ⌃⇧M"), ["meta+shift+M", "ctrl+shift+M"])
  assert.deepEqual(hintChords("⌘1–9"), ["meta+1–9"])
})

test("palette hints: anything else stays text", () => {
  assert.equal(hintChords(""), null)
  assert.equal(hintChords(undefined), null)
  assert.equal(hintChords("pwsh"), null)
  assert.equal(hintChords("C:\\Program Files\\PowerShell\\7\\pwsh.exe"), null)
  assert.equal(hintChords("Stay awake 15m"), null)
  assert.equal(hintChords("Session history · 🔔"), null)
  assert.equal(hintChords("☕ prevent sleep"), null)
})

test("palette hints: every shortcut label is a chord", () => {
  for (const mac of [true, false]) {
    for (const [id, label] of Object.entries(chordLabels(mac))) {
      if (!label) continue
      assert.ok(hintChords(label), `${mac ? "mac" : "win"} ${id}: ${label}`)
    }
  }
})
