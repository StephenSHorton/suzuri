import { test } from "node:test"
import assert from "node:assert/strict"
import { preamble, resumeCommand, savedPlan } from "../restore_session.js"

const SESSION = "093417ed-34d5-48b1-8e1a-f7698077b897"

test("the saved tabs and splits come back with each pane's folder, text and focus", () => {
  const s = {
    v: 1, active: 1, saved_at: 1795325315860,
    tabs: [
      { name: "", sticky: "", focused: 1, zoomed: 0, tree: { id: 1, title: "", cwd: "/a", text: "one" } },
      {
        name: "work", sticky: "zsh", focused: 3, zoomed: 3,
        tree: { dir: "v", ratio: 0.4, a: { id: 2, title: "logs", cwd: "/b" }, b: { id: 3, title: "", cwd: "/c", claude: { session: SESSION, cwd: "/c" } } },
      },
    ],
  }
  const p = savedPlan(s)
  assert.equal(p.active, 1)
  assert.equal(p.savedAt, 1795325315860)
  assert.equal(p.tabs.length, 2)
  assert.deepEqual(p.tabs[0].tree, { id: 1, title: "", cwd: "/a", text: "one", claude: null })
  const t = p.tabs[1]
  assert.equal(t.name, "work")
  assert.equal(t.sticky, "zsh")
  assert.equal(t.focused, 3)
  assert.equal(t.zoomed, 3)
  assert.equal(t.tree.dir, "v")
  assert.equal(t.tree.ratio, 0.4)
  assert.equal(t.tree.a.title, "logs")
  assert.equal(t.tree.b.claude.session, SESSION)
  assert.equal(resumeCommand(t.tree.b), `claude --resume ${SESSION}`)
  assert.equal(resumeCommand(t.tree.a), "")
})

test("broken or empty parts of a session are left out", () => {
  const p = savedPlan({ active: 5, tabs: [{ tree: null }, { tree: { dir: "h", a: { id: 4 }, b: "junk" }, focused: 99, zoomed: 4 }] })
  assert.equal(p.tabs.length, 1)
  assert.deepEqual(p.tabs[0].tree, { id: 4, title: "", cwd: "", text: "", claude: null })
  assert.equal(p.tabs[0].focused, 4, "an unknown focus falls to the first pane")
  assert.equal(p.tabs[0].zoomed, 0, "a lone pane is not zoomed")
  assert.equal(p.active, 0)
  assert.deepEqual(savedPlan(null).tabs, [])
})

test("a conversation id that is not one is never offered", () => {
  const p = savedPlan({ tabs: [{ tree: { id: 1, claude: { session: "x; rm -rf ~" } } }] })
  assert.equal(p.tabs[0].tree.claude, null)
  assert.equal(resumeCommand(p.tabs[0].tree), "")
})

test("the preamble shows the old text, then a restored mark", () => {
  const out = preamble({ text: "$ ls\r\nfoo\n\x1b[31mred\x07" }, 1, () => "21 Nov 2026, 14:02")
  const lines = out.split("\r\n")
  assert.deepEqual(lines.slice(0, 3), ["$ ls", "foo", "[31mred"], "control characters taken out")
  assert.ok(!out.slice(0, out.indexOf("───")).includes("\x1b[31m"), "the old text sets no colours")
  assert.match(lines[3], /─── restored · saved 21 Nov 2026, 14:02 ───/)
  assert.ok(out.endsWith("\r\n"))
})

test("a Claude Code pane's preamble offers the resume and runs nothing", () => {
  const out = preamble({ text: "", claude: { session: SESSION } }, 0)
  assert.match(out, /─── restored ───/)
  assert.match(out, /Claude Code was running here\. Press Enter to resume the conversation\./)
  assert.ok(!out.includes("claude --resume"), "the command is in the input bar, not typed into the shell")
})

test("a pane keeps the shell it was started with; an older save without one gets the active profile's", () => {
  const p = savedPlan({
    tabs: [
      { tree: { dir: "h", a: { id: 1, command: ["cmd.exe"] }, b: { id: 2 } } },
      { tree: { dir: "h", a: { id: 3, command: [] }, b: { id: 4, command: ["", 5] } } },
    ],
  })
  assert.deepEqual(p.tabs[0].tree.a.command, ["cmd.exe"])
  assert.equal("command" in p.tabs[0].tree.b, false, "no command saved: the active profile's shell")
  assert.equal("command" in p.tabs[1].tree.a, false, "an empty command is none")
  assert.equal("command" in p.tabs[1].tree.b, false, "a broken command is none")
})
