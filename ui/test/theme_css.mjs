// Test helper (not a test): an Astryx theme's resolved colour tokens, read
// straight from its shipped theme.css, as the webview would resolve them on
// <html> in one mode. Only the theme's first `:scope {}` token block counts:
// later blocks restyle particular elements ([data-astryx-media], status
// tints) and re-declare tokens such as --color-text-primary for those.
import { readFileSync } from "node:fs"
import { join, dirname } from "node:path"
import { fileURLToPath } from "node:url"

const ui = join(dirname(fileURLToPath(import.meta.url)), "..")

export const THEME_IDS = ["neutral", "stone", "matcha", "chocolate", "butter", "gothic", "y2k"]

export function themeCss(id) {
  return readFileSync(join(ui, `node_modules/@astryxdesign/theme-${id}/dist/theme.css`), "utf8")
}

// The `--name: value;` declarations of the first :scope block that holds
// the colour tokens.
function tokenBlock(css) {
  let at = 0
  for (;;) {
    const start = css.indexOf(":scope {", at)
    if (start < 0) throw new Error("no :scope token block")
    const end = css.indexOf("\n  }", start)
    const block = css.slice(start, end)
    if (block.includes("--color-background-body:")) return block
    at = end
  }
}

// The text inside the parentheses that open at `open` (index of "(").
function group(s, open) {
  let depth = 0
  for (let i = open; i < s.length; i++) {
    if (s[i] === "(") depth++
    else if (s[i] === ")" && --depth === 0) return [s.slice(open + 1, i), i]
  }
  throw new Error(`unbalanced: ${s}`)
}

function splitTop(s) {
  const out = []
  let depth = 0, from = 0
  for (let i = 0; i < s.length; i++) {
    if (s[i] === "(") depth++
    else if (s[i] === ")") depth--
    else if (s[i] === "," && depth === 0) { out.push(s.slice(from, i).trim()); from = i + 1 }
  }
  out.push(s.slice(from).trim())
  return out
}

function resolve(value, raw, dark, seen = new Set()) {
  let v = value
  for (let i; (i = v.indexOf("var(")) >= 0;) {
    const [inner, end] = group(v, i + 3)
    const [name, fallback] = splitTop(inner)
    if (seen.has(name)) throw new Error(`cycle at ${name}`)
    const sub = name in raw ? resolve(raw[name], raw, dark, new Set([...seen, name])) : fallback ?? ""
    v = v.slice(0, i) + sub + v.slice(end + 1)
  }
  for (let i; (i = v.indexOf("light-dark(")) >= 0;) {
    const [inner, end] = group(v, i + 10)
    const [light, darkV] = splitTop(inner)
    v = v.slice(0, i) + (dark ? darkV : light) + v.slice(end + 1)
  }
  return v.trim()
}

/** Every token of theme `id` in `mode` ("light" | "dark"), resolved. */
export function themeTokens(id, mode) {
  const raw = {}
  for (const m of tokenBlock(themeCss(id)).matchAll(/^\s*(--[\w-]+):\s*(.+);\s*$/gm)) raw[m[1]] = m[2]
  const out = {}
  for (const k of Object.keys(raw)) out[k] = resolve(raw[k], raw, mode === "dark")
  return out
}
