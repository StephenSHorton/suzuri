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
//   cursor       --color-accent; the selection is the accent at 38%; the
//                block cursor's blended fill, cursorFill below
//   panel        --color-background-surface (reverse video's default)
//   0 black      the background end of the gray ramp: the first of
//                --color-{background,border,icon,text}-gray and
//                --color-text-disabled within 1.1-3:1 of the background
//                (some themes keep their light grays in dark mode), else
//                the background 15% toward the foreground. A band colour,
//                not ink, so it may be hard to read as text
//   8 br. black  --color-text-disabled, lifted until it reads at 3:1
//   1-6          red, green, yellow, blue, magenta (purple; pink if a theme
//                has no purple), cyan (cyan; else teal): the HUE of the
//                family's most colourful token, at a set OKLCH lightness
//                (LIGHTNESS) and the theme's chroma held to CHROMA, then
//                made to read at 4.5:1 on the background
//   9-14         the same hue, lighter on dark themes and darker on light
//                ones, reading at least as well as the normal colour
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

// ---- OKLCH (Björn Ottosson's OKLab, polar): perceptual lightness L 0..1,
// chroma C, hue h in degrees. The ANSI colours keep the theme's hue and
// take a fixed lightness, so they look equally bright across hues.

/** ANSI normal and bright lightness on a dark and on a light background. */
export const LIGHTNESS = { dark: [0.72, 0.82], light: [0.5, 0.42] }
/** The chroma range the ANSI colours are held to. */
export const CHROMA = [0.09, 0.17]

const toLin = (v) => {
  const s = v / 255
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}
const fromLin = (v) => 255 * (v <= 0.0031308 ? 12.92 * v : 1.055 * v ** (1 / 2.4) - 0.055)

/** A colour as OKLCH {L, C, h}. */
export function toOklch(c) {
  const [r, g, b] = [c.r, c.g, c.b].map(toLin)
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  const L = 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s
  const A = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s
  const B = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s
  return { L, C: Math.hypot(A, B), h: ((Math.atan2(B, A) * 180) / Math.PI + 360) % 360 }
}

// OKLCH to linear sRGB (may be out of gamut).
function lchToLin(L, C, h) {
  const A = C * Math.cos((h * Math.PI) / 180)
  const B = C * Math.sin((h * Math.PI) / 180)
  const l = (L + 0.3963377774 * A + 0.2158037573 * B) ** 3
  const m = (L - 0.1055613458 * A - 0.0638541728 * B) ** 3
  const s = (L - 0.0894841775 * A - 1.291485548 * B) ** 3
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ]
}

/** An OKLCH colour as RGB, chroma reduced until it fits sRGB. */
export function fromOklch(L, C, h) {
  let c = C
  for (let i = 0; i < 40; i++) {
    const rgb = lchToLin(L, c, h)
    if (rgb.every((v) => v >= -0.0005 && v <= 1.0005)) {
      const [r, g, b] = rgb.map((v) => clamp(fromLin(Math.min(1, Math.max(0, v)))))
      return { r, g, b, a: 1 }
    }
    c *= 0.92
  }
  const [r, g, b] = lchToLin(L, 0, h).map((v) => clamp(fromLin(Math.min(1, Math.max(0, v)))))
  return { r, g, b, a: 1 }
}

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

/**
 * The block cursor's fill. style.css draws it with mix-blend-mode:
 * difference (the glyph under it inverts), which shows |fill - bg|. Per
 * channel: where the accent is darker than the background, the fill is
 * bg - accent, so the cursor shows as the accent itself (a dark cursor on a
 * light theme; the accent as the fill would come out nearly the
 * background); elsewhere the accent, as on dark themes before. (A channel
 * brighter than a light background can't be shown exactly: Butter light's
 * blue accent shows as a dark green, still well readable.)
 */
export function cursorFill(accent, bg) {
  const ch = (a, b) => (a <= b ? b - a : a)
  return { r: ch(accent.r, bg.r), g: ch(accent.g, bg.g), b: ch(accent.b, bg.b), a: 1 }
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

  // A family's colours: the HUE comes from the theme (its most colourful
  // variant), but the lightness and chroma are set here, so every theme
  // gets distinct, vivid terminal colours in its own tint. Taking the
  // theme's shades as they are left some themes with only pale or only
  // near-black choices (Stone dark: red, yellow, blue and magenta all
  // near-white; Neutral and Stone light: maroon-black reds).
  const pair = (families) => {
    const cs = families.map((f) => VARIANTS.map((v) => on(`--color-${v}-${f}`)).filter(Boolean)).find((x) => x.length) ?? []
    if (!cs.length) return [fg, fg]
    const lchs = cs.map(toOklch)
    const hue = lchs.reduce((a, c) => (c.C > a.C ? c : a)).h
    // Muted themes stay a little muted, but never so grey the hues blur.
    const C = Math.min(Math.max(Math.max(...lchs.map((c) => c.C)), CHROMA[0]), CHROMA[1])
    const [ln, lb] = dark ? LIGHTNESS.dark : LIGHTNESS.light
    const normal = legible(fromOklch(ln, C, hue), bg, fg, MIN_CONTRAST)
    let bright = legible(fromOklch(lb, C * 0.9, hue), bg, fg, MIN_CONTRAST)
    if (hex(bright) === hex(normal) || contrast(bright, bg) < contrast(normal, bg)) bright = legible(mix(normal, fg, 0.3), bg, fg, MIN_CONTRAST)
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
      cursorFill: hex(cursorFill(accent, bg)),
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
