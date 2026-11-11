// node --test ui/test
// theme_palette.js: the terminal and card colours derived from an Astryx
// theme's tokens, checked against every shipped theme in both modes (their
// theme.css, resolved as the page would) and against invented token sets.
import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { join, dirname } from "node:path"
import { fileURLToPath } from "node:url"
import { THEME_IDS, themeTokens } from "./theme_css.mjs"
import {
  BLACK_CONTRAST, MIN_CONTRAST, MIN_CONTRAST_DIM, RADIUS_TOKEN, TOKENS,
  contrast, deriveLook, lengthPx, parseColor,
} from "../theme_palette.js"

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..")
const HEX = /^#[0-9a-f]{6}$/

test("parses the colour forms getComputedStyle and theme files write", () => {
  assert.deepEqual(parseColor("#ff705d"), { r: 255, g: 112, b: 93, a: 1 })
  assert.deepEqual(parseColor("#FFF"), { r: 255, g: 255, b: 255, a: 1 })
  assert.equal(parseColor("#ffffff1A").a, 0x1a / 255)
  assert.deepEqual(parseColor("rgb(27, 27, 27)"), { r: 27, g: 27, b: 27, a: 1 })
  assert.deepEqual(parseColor("rgba(255, 255, 255, 0.1)"), { r: 255, g: 255, b: 255, a: 0.1 })
  assert.deepEqual(parseColor("rgb(1 2 3 / 50%)"), { r: 1, g: 2, b: 3, a: 0.5 })
  assert.deepEqual(parseColor("color(srgb 1 0 0.5)"), { r: 255, g: 0, b: 127.5, a: 1 })
  for (const bad of ["", "red", "light-dark(#000, #fff)", "#12345", undefined]) assert.equal(parseColor(bad), null, String(bad))
  assert.equal(lengthPx("0.75rem"), 12)
  assert.equal(lengthPx("12px"), 12)
  assert.equal(lengthPx("auto"), null)
})

for (const id of THEME_IDS) {
  for (const mode of ["dark", "light"]) {
    test(`${id} ${mode}: every colour is set and readable`, () => {
      const tokens = themeTokens(id, mode)
      for (const name of ["--color-background-body", "--color-text-primary", "--color-accent"]) assert.ok(name in tokens, name)
      const look = deriveLook({ ...tokens })
      assert.ok(look)
      const t = look.terminal
      // Gothic is dark in both modes; the rest follow the mode.
      assert.equal(look.dark, id === "gothic" || mode === "dark")
      for (const k of ["bg", "fg", "cursor", "panel", "accent"]) assert.match(t[k], HEX, k)
      assert.equal(t.bg, tokens["--color-background-body"].toLowerCase().slice(0, 7))
      assert.match(t.selection, /^rgba\(\d+, \d+, \d+, 0\.38\)$/)
      assert.equal(t.ansi.length, 16)
      t.ansi.forEach((c, i) => {
        assert.match(c, HEX, `ansi ${i}`)
        const cr = contrast(c, t.bg)
        if (i === 0) {
          assert.ok(cr >= BLACK_CONTRAST[0] - 0.01 && cr <= BLACK_CONTRAST[1], `black ${c} at ${cr.toFixed(2)}:1`)
        } else {
          const min = i === 8 ? MIN_CONTRAST_DIM : MIN_CONTRAST
          assert.ok(cr >= min, `ansi ${i} ${c} reads at only ${cr.toFixed(2)}:1 on ${t.bg}`)
        }
      })
      for (let i = 1; i <= 6; i++) {
        assert.notEqual(t.ansi[i], t.ansi[i + 8], `bright ${i} differs`)
        assert.ok(contrast(t.ansi[i + 8], t.bg) >= contrast(t.ansi[i], t.bg), `bright ${i} reads at least as well`)
      }
      assert.equal(t.ansi[15], t.fg)
      // The block cursor, blended (difference) over the background, shows.
      const [f, b] = [parseColor(t.cursorFill), parseColor(t.bg)]
      const shown = { r: Math.abs(f.r - b.r), g: Math.abs(f.g - b.g), b: Math.abs(f.b - b.b) }
      assert.ok(contrast(shown, b) >= MIN_CONTRAST_DIM, `cursor shows as ${JSON.stringify(shown)}`)
      const a = parseColor(t.cursor)
      if (a.r <= b.r && a.g <= b.g && a.b <= b.b) assert.deepEqual(shown, { r: a.r, g: a.g, b: a.b }, "an accent darker than the background shows as itself")
      const c = look.card
      for (const k of ["surface", "text", "muted", "border", "accent", "on_accent", "success", "warning", "error"]) assert.match(c[k], HEX, `card ${k}`)
      assert.ok(contrast(c.text, c.surface) >= MIN_CONTRAST, "card text")
      assert.ok(c.radius >= 0 && c.radius <= 32)
    })
  }
}

test("an invented theme: unreadable family colours are lifted, missing ones fall back", () => {
  const tokens = {
    "--color-background-body": "rgb(16, 16, 16)",
    "--color-background-surface": "rgb(32, 32, 32)",
    "--color-text-primary": "rgb(240, 240, 240)",
    "--color-text-secondary": "rgb(60, 60, 60)", // too dark: lifted
    "--color-accent": "rgb(255, 0, 128)",
    "--color-border": "rgba(255, 255, 255, 0.1)",
    // Every red variant is near the background.
    "--color-text-red": "rgb(60, 0, 0)",
    "--color-icon-red": "rgb(50, 10, 10)",
    "--color-text-green": "color(srgb 0 0.8 0.2)",
    [RADIUS_TOKEN]: "4px",
  }
  const look = deriveLook(tokens)
  const t = look.terminal
  assert.equal(look.dark, true)
  assert.equal(t.bg, "#101010")
  assert.equal(t.cursor, "#ff0080")
  assert.equal(t.selection, "rgba(255, 0, 128, 0.38)")
  t.ansi.slice(1).forEach((c, i) => assert.ok(contrast(c, t.bg) >= (i === 7 ? MIN_CONTRAST_DIM : MIN_CONTRAST), `ansi ${i + 1} ${c}`))
  // The red keeps its hue: red stays the strongest channel.
  const red = parseColor(t.ansi[1])
  assert.ok(red.r > red.g && red.r > red.b, t.ansi[1])
  // The border's 10% white is composited over the card surface.
  assert.equal(look.card.border, "#363636")
  assert.equal(look.card.radius, 4)
})

test("a light theme gets light-mode paint", () => {
  const look = deriveLook({ "--color-background-body": "#fafafa", "--color-text-primary": "#111111" })
  assert.equal(look.dark, false)
  assert.equal(look.terminal.fg, "#111111")
  assert.equal(look.card.radius, 12)
})

test("no background or text yet: nothing to derive", () => {
  assert.equal(deriveLook({}), null)
  assert.equal(deriveLook({ "--color-background-body": "#000000" }), null)
  assert.equal(deriveLook({ "--color-background-body": "light-dark(#fff, #000)", "--color-text-primary": "#fff" }), null)
})

test("every token deriveLook reads is listed for the page's probe", () => {
  const src = readFileSync(join(root, "ui/theme_palette.js"), "utf8")
  const fixed = [...src.matchAll(/"(--color-[a-z-]+)"/g)].map((m) => m[1])
  for (const name of fixed) assert.ok(TOKENS.includes(name), name)
})

test("Rust's startup palette (crates/term palette::NEUTRAL_DARK) is Neutral dark's", () => {
  const src = readFileSync(join(root, "crates/term/src/palette.rs"), "utf8")
  const block = src.slice(src.indexOf("pub const NEUTRAL_DARK"), src.indexOf("};", src.indexOf("pub const NEUTRAL_DARK")))
  const field = (k) => `#${new RegExp(`${k}: hex\\(0x([0-9a-f]{6})\\)`).exec(block)[1]}`
  const ansi = [...block.slice(block.indexOf("ansi16")).matchAll(/hex\(0x([0-9a-f]{6})\)/g)].map((m) => `#${m[1]}`)
  const t = deriveLook(themeTokens("neutral", "dark")).terminal
  assert.deepEqual({ bg: field("bg"), fg: field("fg"), panel: field("panel"), accent: field("accent"), ansi },
    { bg: t.bg, fg: t.fg, panel: t.panel, accent: t.accent, ansi: t.ansi })
  assert.match(block, /dark: true/)
})

test("the cards' startup look (app/src/toast_look.rs) is Neutral dark's", () => {
  const src = readFileSync(join(root, "app/src/toast_look.rs"), "utf8")
  const c = deriveLook(themeTokens("neutral", "dark")).card
  for (const v of [c.surface, c.text, c.muted, c.accent, c.on_accent, c.success, c.warning, c.error]) {
    assert.ok(src.includes(`hex("${v}")`), `${v} in toast_look.rs`)
  }
  assert.ok(src.includes(`"${c.border}"`), "the composited border")
})

test("Settings' theme swatches are the themes' own colours", () => {
  const src = readFileSync(join(root, "ui/chrome/themes.js"), "utf8")
  const block = src.slice(src.indexOf("export const SWATCHES"))
  for (const id of THEME_IDS) {
    const m = new RegExp(`\\n  ${id}: \\[([^\\]]*)\\]`).exec(block)
    assert.ok(m, id)
    const got = [...m[1].matchAll(/"(#[0-9a-f]{6})"/g)].map((x) => x[1])
    const [l, d] = [themeTokens(id, "light"), themeTokens(id, "dark")]
    const want = [l["--color-background-body"], d["--color-background-body"], l["--color-accent"], d["--color-accent"]].map((s) => s.toLowerCase())
    assert.deepEqual(got, want, id)
  }
})

test("the themes Settings offers (config.rs) are the ones the UI can load", () => {
  const rs = readFileSync(join(root, "app/src/config.rs"), "utf8")
  const ids = [.../UI_THEMES: &\[&str\] = &\[([^\]]*)\]/.exec(rs)[1].matchAll(/"([a-z0-9]+)"/g)].map((m) => m[1])
  assert.deepEqual(ids, THEME_IDS)
  const js = readFileSync(join(root, "ui/chrome/themes.js"), "utf8")
  for (const id of ids) assert.match(js, new RegExp(`\\n  ${id}: async`), id)
  // Each theme package is pinned to core's exact version.
  const pkg = JSON.parse(readFileSync(join(root, "ui/package.json"), "utf8")).dependencies
  for (const id of ids) assert.equal(pkg[`@astryxdesign/theme-${id}`], pkg["@astryxdesign/core"], id)
})
