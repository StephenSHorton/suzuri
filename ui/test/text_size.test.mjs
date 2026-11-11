import { test } from "node:test"
import assert from "node:assert/strict"
import { TEXT_SIZES, textSizeScales, applyTextSize } from "../text_size.js"

test("standard is Astryx's default: every scale 1", () => {
  for (const v of Object.values(textSizeScales("l"))) assert.equal(v, "1")
})

test("large grows body more than titles, like Dynamic Type", () => {
  const s = textSizeScales("xl")
  assert.equal(s["--_dynamic-type-body"], String(Number((19 / 17).toFixed(4))))
  assert.equal(s["--_dynamic-type-footnote"], String(Number((15 / 13).toFixed(4))))
  assert.ok(Number(s["--_dynamic-type-body"]) > Number(s["--_dynamic-type-large-title"]))
})

test("covers every style Astryx scales, with its property names", () => {
  assert.deepEqual(Object.keys(textSizeScales("s")).sort(), [
    "--_dynamic-type-body", "--_dynamic-type-caption1", "--_dynamic-type-caption2", "--_dynamic-type-footnote",
    "--_dynamic-type-large-title", "--_dynamic-type-title1", "--_dynamic-type-title2", "--_dynamic-type-title3",
  ])
})

test("an unknown category falls back to standard", () => {
  assert.deepEqual(textSizeScales("huge"), textSizeScales("l"))
  assert.equal(TEXT_SIZES.length, 7)
})

test("applies inline on the root", () => {
  const set = {}
  applyTextSize("xxl", { style: { setProperty: (k, v) => { set[k] = v } } })
  assert.equal(set["--_dynamic-type-body"], String(Number((21 / 17).toFixed(4))))
})
