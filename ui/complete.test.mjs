// node --test 'ui/*.test.mjs'
import { test } from "node:test"
import assert from "node:assert/strict"

import { InputBar, barKey } from "./bar.js"
import {
  tokenAtCursor,
  historyCompletions,
  pathCompletions,
  splitPathToken,
  complete,
  ghostSuffix,
  acceptGhost,
  DirCache,
  PENDING,
} from "./complete.js"

// A fake file system: dir spec -> entries; a missing key reads as unreadable.
const fakeFs = (tree, sep = "/") => ({
  sep,
  seen: [],
  list(dir) {
    this.seen.push(dir)
    return dir in tree ? tree[dir] : null
  },
})
const d = (name) => ({ name, dir: true })
const f = (name) => ({ name, dir: false })
const bar = (text, history = []) => {
  const b = new InputBar()
  b.history = history
  b.insert(text)
  return b
}

test("tokenAtCursor: the token, its prefix and the first word", () => {
  const rs = [..."cd Doc"]
  assert.deepEqual(tokenAtCursor(rs, rs.length), { start: 3, end: 6, prefix: "Doc", firstWord: false })
  assert.deepEqual(tokenAtCursor([..."echo"], 4), { start: 0, end: 4, prefix: "echo", firstWord: true })
  assert.equal(tokenAtCursor([..."ls|gr"], 5).start, 3)
})

test("historyCompletions: newest first, no repeats", () => {
  const got = historyCompletions(["echo one", "echo two", "ls", "echo two"], "echo")
  assert.deepEqual(got, ["echo two", "echo one"])
  assert.deepEqual(historyCompletions(["Echo X"], "ech"), ["Echo X"])
  assert.deepEqual(historyCompletions(["ls"], "  "), [])
})

test("pathCompletions: dirs get the separator, hidden only on a dot, quoted with spaces, sorted", () => {
  const fs = fakeFs({ "": [d("Documents"), f("readme.txt"), f(".env"), d("My Stuff"), f("apple")] })
  assert.deepEqual(pathCompletions(fs, "Doc"), ["Documents/"])
  assert.deepEqual(pathCompletions(fs, "read"), ["readme.txt"])
  assert.deepEqual(pathCompletions(fs, "."), [".env"])
  assert.deepEqual(pathCompletions(fs, "My"), ['"My Stuff/"'])
  // As Go suzuri: sorted as written, so a quoted name comes first.
  assert.deepEqual(pathCompletions(fs, ""), ['"My Stuff/"', "apple", "Documents/", "readme.txt"])
  assert.deepEqual(pathCompletions(fakeFs({}), "x"), [], "unreadable directory: no matches")
  assert.equal(pathCompletions({ sep: "/", list: () => undefined }, "x"), null, "still fetching")
})

test("splitPathToken: relative, home, absolute and Windows forms", () => {
  assert.deepEqual(splitPathToken("src/ma", "/"), { dirPart: "src/", base: "ma", dir: "src/", sep: "/" })
  assert.deepEqual(splitPathToken("src/", "/"), { dirPart: "src/", base: "", dir: "src/", sep: "/" })
  assert.deepEqual(splitPathToken("~", "/"), { dirPart: "~/", base: "", dir: "~", sep: "/" })
  assert.deepEqual(splitPathToken("~/Doc", "/"), { dirPart: "~/", base: "Doc", dir: "~", sep: "/" })
  assert.deepEqual(splitPathToken("~/a/b/c", "/"), { dirPart: "~/a/b/", base: "c", dir: "~/a/b", sep: "/" })
  assert.deepEqual(splitPathToken("/us", "/"), { dirPart: "/", base: "us", dir: "/", sep: "/" })
  assert.deepEqual(splitPathToken("/usr/lo", "/"), { dirPart: "/usr/", base: "lo", dir: "/usr/", sep: "/" })
  assert.deepEqual(splitPathToken("C:\\Us", "\\"), { dirPart: "C:\\", base: "Us", dir: "C:\\", sep: "\\" })
  assert.deepEqual(splitPathToken("~\\Doc", "\\"), { dirPart: "~\\", base: "Doc", dir: "~", sep: "\\" })
  assert.deepEqual(splitPathToken("Doc", "\\"), { dirPart: "", base: "Doc", dir: "", sep: "\\" })
  assert.deepEqual(splitPathToken('"My St', "/"), { dirPart: "", base: "My St", dir: "", sep: "/" })
})

test("Tab completes a path and cycles; Shift+Tab goes back", () => {
  const fs = fakeFs({ "": [d("alpha"), d("alpine"), f("beta")] })
  const b = bar("cd al")
  assert.equal(complete(b, fs), true)
  assert.equal(b.text(), "cd alpha/")
  assert.equal(complete(b, fs), true)
  assert.equal(b.text(), "cd alpine/")
  assert.equal(complete(b, fs), true)
  assert.equal(b.text(), "cd alpha/")
  assert.equal(complete(b, fs, true), true)
  assert.equal(b.text(), "cd alpine/")
  // An edit ends the cycle.
  b.insert("x")
  assert.equal(b.comp, null)
})

test("one match: the next Tab looks again, into the directory", () => {
  const fs = fakeFs({ "": [d("src")], "src/": [f("main.rs"), f("lib.rs")] })
  const b = bar("vim sr")
  complete(b, fs)
  assert.equal(b.text(), "vim src/")
  assert.equal(b.comp, null)
  complete(b, fs)
  assert.equal(b.text(), "vim src/lib.rs")
})

test("Tab completes the whole line from history", () => {
  const b = bar("Get-", ["Get-ChildItem -Recurse", "Get-Process"])
  assert.equal(complete(b, fakeFs({})), true)
  assert.equal(b.text(), "Get-Process")
  complete(b, fakeFs({}))
  assert.equal(b.text(), "Get-ChildItem -Recurse")
  // A multi-word line: the line is replaced, not just the token.
  const g = bar("git sta", ["git status -sb"])
  complete(g, fakeFs({}))
  assert.equal(g.text(), "git status -sb")
})

test("a path-like token prefers paths over history", () => {
  const b = bar("./bu", ["./build.sh --all"])
  complete(b, fakeFs({ "./": [f("build.sh"), f("bump.sh")] }))
  assert.equal(b.text(), "./build.sh")
})

test("nothing to complete: false and no change", () => {
  const b = bar("zz")
  assert.equal(complete(b, fakeFs({ "": [f("a")] })), false)
  assert.equal(b.text(), "zz")
})

test("PENDING while the directory is fetched, then completes", async () => {
  let resolve
  const cache = new DirCache({ sep: "/", fetch: () => new Promise((r) => (resolve = r)) })
  cache.cwd = "/home/u"
  const b = bar("cd Do")
  assert.equal(complete(b, cache), PENDING)
  assert.equal(b.text(), "cd Do")
  await new Promise((r) => setTimeout(r))
  resolve([d("Documents"), d("Downloads")])
  await cache.load("")
  assert.equal(complete(b, cache), true)
  assert.equal(b.text(), "cd Documents/")
})

test("ghost text: history first, then a path, none on an empty bar", () => {
  const fs = fakeFs({ "": [d("Documents")] })
  assert.equal(ghostSuffix(bar("cd Doc"), fs), "uments/")
  assert.equal(ghostSuffix(bar(""), fs), "")
  assert.equal(ghostSuffix(bar("Get-P", ["Get-Process -Id 1"]), fs), "rocess -Id 1")
  assert.equal(ghostSuffix(bar("git sta", ["git status -sb", "git commit -m 'x'", "ls"]), fs), "tus -sb")
  // Not at the end of the line, or browsing history: none.
  const mid = bar("Get-P", ["Get-Process"])
  mid.cursor = 2
  assert.equal(ghostSuffix(mid, fs), "")
  const h = bar("", ["Get-Process"])
  h.historyUp()
  assert.equal(ghostSuffix(h, fs), "")
  // An empty first word gets no path ghost.
  assert.equal(ghostSuffix(bar(" "), fs), "")
})

test("ghost text previews the next match while cycling", () => {
  const fs = fakeFs({ "": [d("alpha"), d("alpine")] })
  const b = bar("cd al")
  complete(b, fs)
  assert.equal(ghostSuffix(b, fs), "")
  assert.equal(b.text(), "cd alpha/")
  const c = bar("cd alp")
  complete(c, fs)
  assert.equal(c.text(), "cd alpha/")
  assert.equal(ghostSuffix(c, fs), "", "alpine/ doesn't extend alpha/")
})

test("acceptGhost takes the suggestion once", () => {
  const b = bar("echo he", ["echo hello world"])
  assert.equal(acceptGhost(b, fakeFs({})), true)
  assert.equal(b.text(), "echo hello world")
  assert.equal(b.cursor, b.runes.length)
  assert.equal(ghostSuffix(b, fakeFs({})), "")
  assert.equal(acceptGhost(b, fakeFs({})), false)
})

test("keys: Tab and Shift+Tab ask to complete, Right at the end accepts the ghost", () => {
  const k = (b, key, mods = {}, fs) =>
    barKey(b, { key, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...mods }, { isMac: true, cols: 40, fs })
  const b = bar("echo he", ["echo hello"])
  assert.deepEqual(k(b, "Tab"), { kind: "complete", reverse: false })
  assert.deepEqual(k(b, "Tab", { shiftKey: true }), { kind: "complete", reverse: true })
  assert.deepEqual(k(b, "Tab", { metaKey: true }), { kind: "none" })
  k(b, "ArrowRight", {}, fakeFs({}))
  assert.equal(b.text(), "echo hello")
  // Not at the end: Right just moves.
  const m = bar("echo he", ["echo hello"])
  m.cursor = 2
  k(m, "ArrowRight", {}, fakeFs({}))
  assert.equal(m.cursor, 3)
  assert.equal(m.text(), "echo he")
})

test("DirCache: per cwd, fetched once, refreshed after ttl, stale served meanwhile", async () => {
  let t = 0
  const calls = []
  const cache = new DirCache({ sep: "/", ttl: 100, now: () => t, fetch: (dir, cwd) => (calls.push([dir, cwd]), [f(cwd + ":" + dir)]) })
  cache.cwd = "/a"
  assert.equal(cache.list(""), undefined)
  await cache.load("")
  assert.deepEqual(cache.list(""), [f("/a:")])
  assert.equal(calls.length, 1)
  cache.cwd = "/b"
  assert.equal(cache.list(""), undefined)
  await cache.load("")
  assert.deepEqual(cache.list(""), [f("/b:")])
  cache.cwd = "/a"
  t = 200
  assert.deepEqual(cache.list(""), [f("/a:")], "stale entry served while it refreshes")
  await cache.load("")
  assert.equal(calls.length, 3)
})
