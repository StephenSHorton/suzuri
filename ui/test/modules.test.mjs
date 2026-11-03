// node --test ui/test
// A top-level name declared twice in one module makes the webview reject the
// whole module (app.js then never runs and the window stays empty), and
// node --check doesn't catch it. Two merges each adding a `relayout` did that.
import { test } from "node:test"
import assert from "node:assert/strict"
import { readdirSync, readFileSync } from "node:fs"
import { join, dirname } from "node:path"
import { fileURLToPath } from "node:url"

const ui = join(dirname(fileURLToPath(import.meta.url)), "..")
const decl = /^(?:export\s+)?(?:async\s+)?(?:function\*?|class|const|let|var)\s+([A-Za-z_$][\w$]*)/

test("no top-level name is declared twice in a ui module", () => {
  const dups = []
  for (const f of readdirSync(ui).filter((f) => f.endsWith(".js"))) {
    const seen = new Map()
    readFileSync(join(ui, f), "utf8").split("\n").forEach((line, i) => {
      const m = decl.exec(line)
      if (!m) return
      if (seen.has(m[1])) dups.push(`${f}:${i + 1} ${m[1]} (first at ${seen.get(m[1])})`)
      else seen.set(m[1], i + 1)
    })
  }
  assert.deepEqual(dups, [])
})
