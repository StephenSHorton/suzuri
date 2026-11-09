// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { checkMessage } from "../update_check.js"

test("check for updates: what it says", () => {
  assert.deepEqual(checkMessage({ found: null, current: "0.10.0" }), { text: "Up to date (0.10.0).", offer: false, ms: 4000 })
  assert.equal(checkMessage({ found: { version: "0.11.0" }, current: "0.10.0" }).offer, true)
  assert.match(checkMessage({ error: "network down" }).text, /^Update check failed: network down/)
  assert.equal(checkMessage({ error: "dev build: updates are off" }).text, "Dev build: updates are off.")
})
