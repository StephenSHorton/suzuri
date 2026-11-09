// node --test ui/test
import { test } from "node:test"
import assert from "node:assert/strict"
import { dropText, quotePosix, quoteWindows } from "../drop_paths.js"

test("plain paths go in as they are", () => {
  assert.equal(quotePosix("/Users/me/notes.txt"), "/Users/me/notes.txt")
  assert.equal(quoteWindows("C:\\Users\\me\\notes.txt"), "C:\\Users\\me\\notes.txt")
})

test("paths with spaces or shell characters are quoted", () => {
  assert.equal(quotePosix("/Users/me/My File.png"), "'/Users/me/My File.png'")
  assert.equal(quotePosix("/tmp/a$b&c;(d)"), "'/tmp/a$b&c;(d)'")
  assert.equal(quotePosix("/tmp/it's"), "'/tmp/it'\\''s'")
  assert.equal(quoteWindows("C:\\Program Files\\x.exe"), "\"C:\\Program Files\\x.exe\"")
  assert.equal(quoteWindows("C:\\a&b"), "\"C:\\a&b\"")
})

test("every dropped path, space separated, with a trailing space", () => {
  assert.equal(dropText(["/a", "/b c"]), "/a '/b c' ")
  assert.equal(dropText(["C:\\a", "C:\\b c"], true), "C:\\a \"C:\\b c\" ")
})

test("nothing dropped pastes nothing", () => {
  assert.equal(dropText([]), "")
  assert.equal(dropText(undefined), "")
  assert.equal(dropText(["", null]), "")
})
