// node --test 'ui/*.test.mjs'
import { test } from "node:test"
import assert from "node:assert/strict"

import {
  newActivity, step, activity, rollUp, cleanTitle, titleGlyph,
  OUT_QUIET_MS, STALE_MS, LONG_CMD_MS,
} from "./activity_state.js"

// A pane with suzuri's shell hooks, at its prompt.
const atPrompt = () => {
  const s = newActivity()
  step(s, { t: "shell", integrated: true, program: false, prompts: 1 }, 0)
  return s
}
const run = (s, now, ctx) => step(s, { t: "shell", integrated: true, program: true, prompts: s.shell.prompts }, now, ctx)
const prompt = (s, now) => step(s, { t: "shell", integrated: true, program: false, prompts: s.shell.prompts + 1 }, now)
// Output every 250 ms from `from` to `to` (the poll's view of the PTY).
const out = (s, from, to) => { for (let t = from; t <= to; t += 250) step(s, { t: "output", at: t }, t) }

test("titles lose their status glyphs, not their words", () => {
  assert.equal(cleanTitle("⠋ Grok"), "Grok")
  assert.equal(cleanTitle("✳ Claude Code"), "Claude Code")
  assert.equal(cleanTitle("⠐ ⠂ fix the tests"), "fix the tests")
  assert.equal(cleanTitle("* Grok"), "Grok")
  assert.equal(cleanTitle("xargs"), "xargs")
  assert.equal(cleanTitle("~/projects/suzuri"), "~/projects/suzuri")
  assert.equal(titleGlyph("⠙ Grok"), "⠙")
  assert.equal(titleGlyph("vim"), "")
})

test("the prompt is idle and a running command is busy", () => {
  const s = atPrompt()
  assert.equal(activity(s, 10), "idle")
  run(s, 100)
  assert.equal(activity(s, 200), "busy")
  // A quiet build is still busy: no output guess while the shell knows.
  assert.equal(activity(s, 60000), "busy")
  prompt(s, 61000)
  assert.equal(activity(s, 61001), "idle")
})

test("typing at the prompt never spins (Go spun on every echo)", () => {
  const s = atPrompt()
  for (let t = 0; t < 5000; t += 150) {
    step(s, { t: "input" }, t)
    step(s, { t: "output", at: t + 20 }, t + 20)
  }
  assert.equal(activity(s, 5000), "idle")
})

test("an interactive program falls back to the output guess", () => {
  const s = atPrompt()
  run(s, 0)
  step(s, { t: "input" }, 500) // ssh, a REPL: it took the keys
  assert.equal(activity(s, 600), "idle")
  // A burst shorter than a second (a redraw, one reply) stays idle.
  out(s, 1000, 1500)
  assert.equal(activity(s, 1600), "idle")
  // Steady output spins, and short pauses don't flicker it off.
  out(s, 5000, 7000)
  assert.equal(activity(s, 7100), "busy")
  out(s, 8800, 9000)
  assert.equal(activity(s, 9100), "busy")
  assert.equal(activity(s, 9000 + OUT_QUIET_MS + 1), "idle")
})

test("a full-screen app is interactive", () => {
  const s = atPrompt()
  run(s, 0)
  assert.equal(activity(s, 100, { alt: true }), "idle")
  out(s, 200, 1600)
  assert.equal(activity(s, 1700, { alt: true }), "busy")
})

test("a title spinner is busy until it stops, and never outlives its program", () => {
  const s = atPrompt()
  run(s, 0)
  step(s, { t: "input" }, 100) // claude takes the keys
  step(s, { t: "title", title: "✳ Claude Code" }, 200)
  assert.equal(activity(s, 300), "idle")
  step(s, { t: "title", title: "⠂ Fix the tests" }, 1000)
  step(s, { t: "title", title: "⠐ Fix the tests" }, 1500)
  assert.equal(activity(s, 1600), "busy")
  step(s, { t: "title", title: "✳ Fix the tests" }, 2000)
  assert.equal(activity(s, 2001), "idle")
  // Go's stale spinner: a glyph left in the title after the program exits.
  step(s, { t: "title", title: "⠂ Fix the tests" }, 5000)
  prompt(s, 6000)
  assert.equal(activity(s, 6001), "idle")
})

test("a frozen braille title goes stale", () => {
  const s = newActivity() // no shell hooks (fish, a custom command)
  step(s, { t: "title", title: "⠿ tool" }, 0)
  assert.equal(activity(s, 1000), "busy")
  assert.equal(activity(s, STALE_MS + 1), "idle")
})

test("a geometric glyph only counts while it animates", () => {
  const s = newActivity()
  step(s, { t: "title", title: "● build" }, 0)
  assert.equal(activity(s, 10), "idle")
  step(s, { t: "title", title: "◐ build" }, 100)
  step(s, { t: "title", title: "◓ build" }, 200)
  assert.equal(activity(s, 300), "busy")
  assert.equal(activity(s, 3000), "idle")
})

test("OSC 9;4 progress is busy while set or indeterminate", () => {
  const s = newActivity()
  step(s, { t: "progress", state: 3 }, 0)
  assert.equal(activity(s, 1), "busy")
  step(s, { t: "progress", state: 2 }, 2)
  assert.equal(activity(s, 3), "idle")
  step(s, { t: "progress", state: 1 }, 4)
  step(s, { t: "progress", state: 0 }, 5)
  assert.equal(activity(s, 6), "idle")
})

test("the agent's report wins and clears at the next prompt", () => {
  const s = atPrompt()
  run(s, 0)
  step(s, { t: "agent", state: "working" }, 10)
  step(s, { t: "title", title: "✳ idle-looking" }, 20)
  assert.equal(activity(s, 30), "busy")
  step(s, { t: "agent", state: "blocked" }, 40)
  assert.equal(activity(s, 50), "blocked")
  step(s, { t: "agent", state: "done" }, 60)
  assert.equal(activity(s, 70), "idle")
  step(s, { t: "agent", state: "working" }, 80)
  prompt(s, 90) // the agent exited without saying so
  assert.equal(activity(s, 100), "idle")
  step(s, { t: "agent", state: "bogus" }, 110)
  assert.equal(s.agent, "")
})

test("a bell you didn't see is blocked until you look", () => {
  const s = atPrompt()
  run(s, 0)
  step(s, { t: "bell" }, 10, { looking: true })
  assert.equal(activity(s, 20), "busy")
  step(s, { t: "notice" }, 30, { looking: false })
  assert.equal(activity(s, 40), "blocked")
  step(s, { t: "seen" }, 50)
  assert.equal(activity(s, 60), "busy")
  step(s, { t: "bell" }, 70)
  step(s, { t: "agent", state: "working" }, 80)
  assert.equal(activity(s, 90), "busy")
})

test("command finished: long commands in a pane you're not looking at", () => {
  const s = atPrompt()
  run(s, 0)
  const short = step(s, { t: "done", exit: 0, command: "ls" }, LONG_CMD_MS - 1)
  assert.equal(short, null)
  prompt(s, LONG_CMD_MS)
  run(s, 20000)
  assert.equal(step(s, { t: "done", exit: 0, command: "cargo build" }, 40000, { looking: true }), null)
  prompt(s, 40000)
  run(s, 50000)
  assert.deepEqual(step(s, { t: "done", exit: 101, command: "cargo test" }, 70000), {
    title: "cargo test", body: "failed (exit 101)", failed: true,
  })
  prompt(s, 70000)
  run(s, 80000)
  assert.deepEqual(step(s, { t: "done", exit: 0, command: "" }, 95000), { title: "Command", body: "succeeded", failed: false })
  prompt(s, 95000)
  run(s, 100000)
  assert.equal(step(s, { t: "done", exit: 0, command: "_suzuri_cd x" }, 200000), null)
  // A done with no command running (no start seen) never notifies.
  prompt(s, 200000)
  assert.equal(step(s, { t: "done", exit: 0, command: "x" }, 300000), null)
})

test("exit is dead, and tabs roll up blocked over busy", () => {
  const s = atPrompt()
  step(s, { t: "exit" }, 0)
  assert.equal(activity(s, 1), "dead")
  assert.equal(rollUp(["idle", "busy", "blocked"]), "blocked")
  assert.equal(rollUp(["idle", "busy", "dead"]), "busy")
  assert.equal(rollUp(["dead", "dead"]), "dead")
  assert.equal(rollUp(["dead", "idle"]), "idle")
})
