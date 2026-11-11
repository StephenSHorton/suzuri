// The terminal's colours and the notice cards' colours, derived from the
// active Astryx theme. A pure function: resolved token values in (CSS colour
// strings, as appearance.js reads them off <html> once the theme applies),
// palettes out. Rust paints the terminal cells and the cards with them
// (appearance_set in app/src/main.rs; its built-in default, Neutral dark,
// is this function's output for Neutral's dark tokens, which
// test/theme_palette.test.mjs checks).
//
// The mapping (the terminal background is the token the panes paint,
// --color-background-body):
//   background   --color-background-body
//   foreground   --color-text-primary
//   cursor       --color-accent; the selection is the accent at 38%
//   panel        --color-background-surface (reverse video's default)
//   0 black      the background end of the gray ramp: the first of
//                --color-{background,border,icon,text}-gray and
//                --color-text-disabled within 1.1-3:1 of the background
//                (some themes keep their light grays in dark mode), else
//                the background 15% toward the foreground. A band colour,
//                not ink, so it may be hard to read as text
//   8 br. black  --color-text-disabled, lifted until it reads at 3:1
//   1-6          red, green, yellow, blue, magenta (purple; pink if a theme
//                has no purple), cyan (cyan; else teal): the family's
//                border / icon /
//                text / background token, mixed toward the foreground
//                until it reads at 4.5:1 on the background (most already
//                do; the hue stays); of those, the most colourful
//   9-14         of the same, the most legible one that reads better than
//                the normal one, else the normal one mixed 30% toward the
//                foreground
//   7 white      --color-text-secondary (at least 4.5:1)
//   15 br. white --color-text-primary
// Light and dark come from the resolved background, not the Mode setting
// (Gothic has one look in both modes; System is the OS's call).

/** Every token deriveLook reads. */
export const FAMILIES = ["red", "green", "yellow", "blue", "purple", "pink", "cyan", "teal", "gray"]
const VARIANTS = ["border", "icon", "text", "background"]
export const TOKENS = [
  "--color-background-body", "--color-background-surface",
  "--color-text-primary", "--color-text-secondary", "--color-text-disabled",
  "--color-accent", "--color-on-accent", "--color-border",
  "--color-success", "--color-warning", "--color-error",
  ...FAMILIES.flatMap((f) => VARIANTS.map((v) => `--color-${v}-${f}`)),
]
/** Non-colour tokens: the cards' corner radius. */
export const RADIUS_TOKEN = "--radius-container"

export const MIN_CONTRAST = 4.5
export const MIN_CONTRAST_DIM = 3
export const SELECTION_ALPHA = 0.38
/** ANSI black is a band near the background: this far from it, no more. */
export const BLACK_CONTRAST = [1.1, 3]

// ---- colours: {r, g, b, a} with channels 0..255 and alpha 0..1.

/** A CSS colour as getComputedStyle or a theme file writes it, or null. */
export function parseColor(s) {
  if (typeof s !== "string") return null
  const v = s.trim().toLowerCase()
  let m = /^#([0-9a-f]{3,8})$/.exec(v)
  if (m) {
    let h = m[1]
    if (h.length === 3 || h.length === 4) h = [...h].map((c) => c + c).join("")
    if (h.length !== 6 && h.length !== 8) return null
    const n = (i) => parseInt(h.slice(i, i + 2), 16)
    return { r: n(0), g: n(2), b: n(4), a: h.length === 8 ? n(6) / 255 : 1 }
  }
  const alpha = (x) => (x == null ? 1 : x.endsWith("%") ? parseFloat(x) / 100 : parseFloat(x))
  m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)\s*(?:[,/]\s*([\d.]+%?)\s*)?\)$/.exec(v)
  if (m) return { r: +m[1], g: +m[2], b: +m[3], a: alpha(m[4]) }
  m = /^color\(srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*(?:\/\s*([\d.]+%?)\s*)?\)$/.exec(v)
  if (m) return { r: m[1] * 255, g: m[2] * 255, b: m[3] * 255, a: alpha(m[4]) }
  return null
}

const clamp = (x) => Math.max(0, Math.min(255, Math.round(x)))

/** c drawn over an opaque base. */
export function over(c, base) {
  const a = c.a ?? 1
  return { r: clamp(c.r * a + base.r * (1 - a)), g: clamp(c.g * a + base.g * (1 - a)), b: clamp(c.b * a + base.b * (1 - a)), a: 1 }
}

/** A fraction t of the way from a toward b. */
export function mix(a, b, t) {
  return { r: clamp(a.r + (b.r - a.r) * t), g: clamp(a.g + (b.g - a.g) * t), b: clamp(a.b + (b.b - a.b) * t), a: 1 }
}

export function hex(c) {
  return "#" + [c.r, c.g, c.b].map((x) => clamp(x).toString(16).padStart(2, "0")).join("")
}

function luminance(c) {
  const lin = (v) => {
    const s = v / 255
    return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b)
}

/** WCAG contrast ratio (1..21). Takes colours or CSS strings. */
export function contrast(a, b) {
  const [x, y] = [a, b].map((c) => luminance(typeof c === "string" ? parseColor(c) : c))
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
}

// How coloured c is: the spread of its channels (0 for grays).
const chroma = (c) => Math.max(c.r, c.g, c.b) - Math.min(c.r, c.g, c.b)

const WHITE = { r: 255, g: 255, b: 255, a: 1 }
const BLACK = { r: 0, g: 0, b: 0, a: 1 }

// c, mixed toward `toward` in 5% steps until it reads at `min` on bg.
function legible(c, bg, toward, min) {
  for (let t = 0; t <= 1.0001; t += 0.05) {
    const m = mix(c, toward, t)
    if (contrast(m, bg) >= min) return m
  }
  return toward
}

/** A CSS length (px or rem at a 16 px root) in px, or null. */
export function lengthPx(s) {
  const m = /^\s*([\d.]+)(px|rem)\s*$/.exec(String(s ?? ""))
  return m ? (m[2] === "rem" ? +m[1] * 16 : +m[1]) : null
}

/**
 * The terminal and card palettes for a resolved token set
 * ({"--color-…": "css colour", "--radius-container": "12px"}), or null
 * when the background or text token is missing (the theme's CSS has not
 * applied yet).
 */
export function deriveLook(tokens) {
  const raw = (name) => parseColor(tokens[name])
  const body = raw("--color-background-body")
  const text = raw("--color-text-primary")
  if (!body || !text) return null
  const bg = over(body, BLACK)
  const dark = contrast(bg, WHITE) > contrast(bg, BLACK)
  const on = (name, base = bg) => {
    const c = raw(name)
    return c ? over(c, base) : null
  }
  const fg = over(text, bg)

  // Every variant of the first family the theme has, on the background,
  // each made legible.
  const candidates = (families) =>
    (families.map((f) => VARIANTS.map((v) => on(`--color-${v}-${f}`)).filter(Boolean)).find((cs) => cs.length) ?? [])
      .map((c) => legible(c, bg, fg, MIN_CONTRAST))
  const best = (cs, score) => cs.reduce((a, c) => (score(c) > score(a) ? c : a))
  const pair = (families) => {
    const cs = candidates(families)
    if (!cs.length) return [fg, fg]
    const normal = best(cs, chroma)
    const others = cs.filter((c) => hex(c) !== hex(normal) && contrast(c, bg) > contrast(normal, bg))
    const bright = others.length ? best(others, (c) => contrast(c, bg)) : mix(normal, fg, 0.3)
    return [normal, bright]
  }
  const [red, brRed] = pair(["red"])
  const [green, brGreen] = pair(["green"])
  const [yellow, brYellow] = pair(["yellow"])
  const [blue, brBlue] = pair(["blue"])
  const [magenta, brMagenta] = pair(["purple", "pink"])
  const [cyan, brCyan] = pair(["cyan", "teal"])
  const black = ["background", "border", "icon", "text"].map((v) => on(`--color-${v}-gray`))
    .concat(on("--color-text-disabled"))
    .find((c) => c && contrast(c, bg) >= BLACK_CONTRAST[0] && contrast(c, bg) <= BLACK_CONTRAST[1])
    ?? mix(bg, fg, 0.15)
  const brBlack = legible(on("--color-text-disabled") ?? mix(bg, fg, 0.4), bg, fg, MIN_CONTRAST_DIM)
  const white = legible(on("--color-text-secondary") ?? mix(fg, bg, 0.2), bg, fg, MIN_CONTRAST)
  const ansi = [black, red, green, yellow, blue, magenta, cyan, white, brBlack, brRed, brGreen, brYellow, brBlue, brMagenta, brCyan, fg]

  const accent = on("--color-accent") ?? fg
  const surface = on("--color-background-surface") ?? bg
  const onSurface = (name, fallback) => {
    const c = raw(name)
    return c ? over(c, surface) : fallback
  }
  const sel = raw("--color-accent") ?? fg
  return {
    dark,
    terminal: {
      bg: hex(bg),
      fg: hex(fg),
      cursor: hex(accent),
      selection: `rgba(${sel.r}, ${sel.g}, ${sel.b}, ${SELECTION_ALPHA})`,
      panel: hex(surface),
      accent: hex(accent),
      ansi: ansi.map(hex),
    },
    card: {
      surface: hex(surface),
      text: hex(onSurface("--color-text-primary", fg)),
      muted: hex(onSurface("--color-text-secondary", mix(fg, surface, 0.35))),
      border: hex(onSurface("--color-border", mix(surface, fg, 0.1))),
      accent: hex(onSurface("--color-accent", accent)),
      on_accent: hex(onSurface("--color-on-accent", surface)),
      success: hex(onSurface("--color-success", green)),
      warning: hex(onSurface("--color-warning", yellow)),
      error: hex(onSurface("--color-error", red)),
      radius: lengthPx(tokens[RADIUS_TOKEN]) ?? 12,
    },
  }
}
