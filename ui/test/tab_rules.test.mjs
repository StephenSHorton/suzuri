// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { tabTitle, stickyAt, windowTitle, zoomCycle, MAX_TABS, MAX_PANES } from "../tab_rules.js"

const tab = (o) => ({ name: "", sticky: "", panes: [""], focused: "", alt: false, index: 0, ...o })

test("an untitled tab is shell N, or app N on the alt screen", () => {
  assert.equal(tabTitle(tab({ index: 2 })), "shell 3")
  assert.equal(tabTitle(tab({ index: 0, alt: true })), "app 1")
})

test("the focused pane's title, then any pane's", () => {
  assert.equal(tabTitle(tab({ panes: ["vim"], focused: "vim" })), "vim")
  assert.equal(tabTitle(tab({ panes: ["", "htop"], focused: "" })), "htop")
})

test("Rename tab wins over everything", () => {
  assert.equal(tabTitle(tab({ name: "work", sticky: "zsh", panes: ["a", "b"], focused: "a" })), "work")
})

test("a split tab keeps its sticky name; a solo tab follows its pane", () => {
  assert.equal(tabTitle(tab({ sticky: "zsh", panes: ["Claude", "zsh"], focused: "Claude" })), "zsh")
  assert.equal(tabTitle(tab({ sticky: "zsh", panes: ["Claude"], focused: "Claude" })), "Claude")
})

test("Windows' Administrator prefix goes", () => {
  assert.equal(tabTitle(tab({ panes: ["Administrator: pwsh"], focused: "Administrator: pwsh" })), "pwsh")
})

test("sticky freezes once, never over a rename", () => {
  assert.equal(stickyAt("", "", "Grok"), "Grok")
  assert.equal(stickyAt("", "", "shell"), "shell")
  assert.equal(stickyAt("", "zsh", "Grok"), "zsh")
  assert.equal(stickyAt("mine", "", "Grok"), "")
  assert.equal(stickyAt("", "", ""), "")
})

test("window title", () => {
  assert.equal(windowTitle("zsh"), "suzuri — zsh")
  assert.equal(windowTitle(""), "suzuri")
})

test("zoom cycles in tree order and wraps", () => {
  const ps = ["a", "b", "c"]
  assert.equal(zoomCycle(ps, "a", 1, 0), "b")
  assert.equal(zoomCycle(ps, "c", 0, 1), "a")
  assert.equal(zoomCycle(ps, "a", -1, 0), "c")
  assert.equal(zoomCycle(ps, "b", 0, -1), "a")
  assert.equal(zoomCycle(["a"], "a", 1, 0), null)
})

test("Go's limits", () => {
  assert.equal(MAX_TABS, 16)
  assert.equal(MAX_PANES, 24)
})
