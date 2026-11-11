// node --test 'ui/*.test.mjs'
import { test } from "node:test"
import assert from "node:assert/strict"

import { gohuSizes, gohuStep } from "./zoom_steps.js"

test("Gohu's crisp sizes follow the screen's pixel ratio", () => {
  assert.deepEqual(gohuSizes(2, 10, 36), [14, 21, 28, 35])
  assert.deepEqual(gohuSizes(1, 10, 36), [14, 28])
  assert.deepEqual(gohuSizes(1.5, 10, 36), [14, 28])
  assert.deepEqual(gohuSizes(3, 10, 36), [14, 28])
  assert.deepEqual(gohuSizes(0, 10, 36), [14, 28])
})

test("Gohu zoom steps between crisp sizes", () => {
  assert.equal(gohuStep(14, 1, 2, 10, 36), 21)
  assert.equal(gohuStep(21, 1, 2, 10, 36), 28)
  assert.equal(gohuStep(28, -1, 2, 10, 36), 21)
  assert.equal(gohuStep(14, 1, 1, 10, 36), 28)
})

test("Gohu zoom stops at the ends", () => {
  assert.equal(gohuStep(14, -1, 2, 10, 36), 14)
  assert.equal(gohuStep(35, 1, 2, 10, 36), 35)
  assert.equal(gohuStep(28, 1, 1, 10, 36), 28)
})

test("a size between crisp ones moves to its neighbour", () => {
  assert.equal(gohuStep(16, 1, 2, 10, 36), 21)
  assert.equal(gohuStep(16, -1, 2, 10, 36), 14)
  assert.equal(gohuStep(12, -1, 2, 10, 36), 12)
  assert.equal(gohuStep(36, -1, 2, 10, 36), 35)
})
