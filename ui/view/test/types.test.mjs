// node --test ui/view/test
// The hand-written .d.ts files declare every runtime export of the module
// next to them (no TypeScript here, so the names are matched by hand), and
// package.json gives each exported .js subpath its types.
import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

globalThis.OffscreenCanvas ??= class { getContext() { return null } }
const here = (f) => new URL(`../${f}`, import.meta.url)

// Value names a .d.ts exports: export const/function/class NAME, and
// export { A, B } [from "..."] (not `export type { ... }`).
function declared(file) {
  const src = readFileSync(here(file), "utf8")
  const names = new Set()
  for (const m of src.matchAll(/^export\s+(?:declare\s+)?(?:const|function|class)\s+([A-Za-z_$][\w$]*)/gm)) names.add(m[1])
  for (const m of src.matchAll(/^export\s+\{([^}]*)\}/gm)) {
    for (const n of m[1].split(",")) if (n.trim()) names.add(n.trim().split(/\s+as\s+/).pop())
  }
  return [...names].sort()
}

for (const [js, dts] of [["index.js", "index.d.ts"], ["frame.js", "frame.d.ts"], ["keys.js", "keys.d.ts"], ["kitty.js", "kitty.d.ts"]]) {
  test(`${dts} declares exactly ${js}'s exports`, async () => {
    const mod = await import(here(js))
    assert.deepEqual(declared(dts), Object.keys(mod).sort())
  })
}

test("package.json: every exported .js has types, and the files ship", () => {
  const pkg = JSON.parse(readFileSync(here("package.json"), "utf8"))
  for (const [path, target] of Object.entries(pkg.exports)) {
    if (path !== "." && !path.endsWith(".js")) continue
    assert.equal(typeof target, "object", path)
    assert.deepEqual(Object.keys(target)[0], "types", `${path}: types first`)
    assert.ok(readFileSync(here(target.types.slice(2)), "utf8").length > 0)
  }
  assert.ok(pkg.files.includes("*.d.ts"))
  assert.ok(pkg.files.includes("fonts"))
})

test("the bundled Gohu ships with the package and view.css declares it", async () => {
  const { GOHU_FONT } = await import(here("index.js"))
  assert.ok(GOHU_FONT.url.endsWith("/fonts/GohuFontuni14NerdFontMono-Regular.ttf"))
  assert.ok(readFileSync(new URL(GOHU_FONT.url)).length > 100000)
  assert.ok(readFileSync(here("fonts/COPYING-LICENSE"), "utf8").length > 0)
  const css = readFileSync(here("view.css"), "utf8")
  assert.match(css, /@font-face\s*\{[^}]*font-family:\s*"Gohu";[^}]*url\("fonts\/GohuFontuni14NerdFontMono-Regular\.ttf"\)/)
})
