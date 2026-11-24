// suzuri-view/palette's maxHeight: the built palette passes it to Astryx's
// CommandPalette dialog, default min(480px, 85dvh).
// Builds dist/ first (view/build.mjs, what npm pack runs), so it tests the
// JS a host gets.
import { test, before } from "node:test"
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { fileURLToPath } from "node:url"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"

let PaletteView, PALETTE_MAX_HEIGHT
before(async () => {
  execFileSync(process.execPath, [fileURLToPath(new URL("../view/build.mjs", import.meta.url))], { stdio: "ignore" })
  ;({ PaletteView, PALETTE_MAX_HEIGHT } = await import("../view/dist/palette.js"))
})

const items = [{ title: "New tab", category: "Tabs", hint: "Ctrl+Shift+T" }]
const render = (props) =>
  renderToStaticMarkup(h(PaletteView, { items, onDone() {}, onToggle() {}, ...props }))

test("the default caps the palette at min(480px, 85dvh)", () => {
  assert.equal(PALETTE_MAX_HEIGHT, "min(480px, 85dvh)")
  assert.match(render({}), /max-height:\s*min\(480px,\s*85dvh\)/)
})

test("a host's maxHeight reaches the dialog", () => {
  assert.match(render({ maxHeight: 200 }), /max-height:\s*200px/)
  assert.match(render({ maxHeight: "70dvh" }), /max-height:\s*70dvh/)
})
