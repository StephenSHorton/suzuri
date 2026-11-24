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

for (const [js, dts] of [["index.js", "index.d.ts"], ["frame.js", "frame.d.ts"], ["keys.js", "keys.d.ts"], ["kitty.js", "kitty.d.ts"], ["layout_ops.js", "layout_ops.d.ts"], ["sash_ops.js", "sash_ops.d.ts"], ["tab_rules.js", "tab_rules.d.ts"], ["settings_rules.js", "settings_rules.d.ts"], ["palette_rules.js", "palette_rules.d.ts"]]) {
  test(`${dts} declares exactly ${js}'s exports`, async () => {
    const mod = await import(here(js))
    assert.deepEqual(declared(dts), Object.keys(mod).sort())
  })
}

// The React parts (react/*.jsx, built to dist/ when packed) need Astryx and
// a DOM, so their exports are read from the source: each .d.ts declares its
// own names plus everything its rules file exports (export *).
for (const [jsx, dts, rules] of [["react/Settings.jsx", "settings.d.ts", "settings_rules.js"], ["react/Palette.jsx", "palette.d.ts", "palette_rules.js"]]) {
  test(`${dts} declares exactly ${jsx}'s exports`, async () => {
    assert.ok(readFileSync(here(dts), "utf8").includes(`export * from "./${rules.replace(".js", "")}"`))
    const types = new Set([...declared(dts), ...Object.keys(await import(here(rules)))])
    assert.deepEqual([...types].sort(), declared(jsx))
  })
}

test("package.json: every exported .js has types, and the files ship", () => {
  const pkg = JSON.parse(readFileSync(here("package.json"), "utf8"))
  for (const [path, target] of Object.entries(pkg.exports)) {
    if (path !== "." && !path.endsWith(".js") && !["./settings", "./palette"].includes(path)) continue
    assert.equal(typeof target, "object", path)
    assert.deepEqual(Object.keys(target)[0], "types", `${path}: types first`)
    assert.ok(readFileSync(here(target.types.slice(2)), "utf8").length > 0)
  }
  assert.ok(pkg.files.includes("*.d.ts"))
  assert.ok(pkg.files.includes("fonts"))
  // The React parts ship built (no JSX), with their peers optional so a
  // host that only mounts the terminal needs none of them.
  assert.ok(pkg.files.includes("dist"))
  assert.equal(pkg.exports["./settings"].default, "./dist/settings.js")
  assert.equal(pkg.exports["./palette"].default, "./dist/palette.js")
  assert.equal(pkg.scripts.prepack, "node build.mjs")
  for (const peer of ["react", "react-dom", "@astryxdesign/core", "lucide-react"]) {
    assert.ok(pkg.peerDependencies[peer], peer)
    assert.equal(pkg.peerDependenciesMeta[peer].optional, true, peer)
  }
})

test("the terminal's own modules import no React", () => {
  for (const f of ["index.js", "frame.js", "keys.js", "kitty.js", "layout_ops.js", "sash_ops.js", "tab_rules.js", "settings_rules.js", "palette_rules.js"]) {
    assert.doesNotMatch(readFileSync(here(f), "utf8"), /from\s+"(react|react-dom|@astryxdesign|lucide-react)/, f)
  }
})

test("the bundled Gohu ships with the package and view.css declares it", async () => {
  const { GOHU_FONT } = await import(here("index.js"))
  assert.ok(GOHU_FONT.url.endsWith("/fonts/GohuFontuni14NerdFontMono-Regular.ttf"))
  assert.ok(readFileSync(new URL(GOHU_FONT.url)).length > 100000)
  assert.ok(readFileSync(here("fonts/COPYING-LICENSE"), "utf8").length > 0)
  const css = readFileSync(here("view.css"), "utf8")
  assert.match(css, /@font-face\s*\{[^}]*font-family:\s*"Gohu";[^}]*url\("fonts\/GohuFontuni14NerdFontMono-Regular\.ttf"\)/)
})
