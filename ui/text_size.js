// The chrome's text size (Settings › Look › Text size, config ui_text_size).
//
// Astryx sizes text the way iOS Dynamic Type does: each font-size step
// follows one UIKit text style, and every style grows on its own curve as
// the reader's text size category changes (body more than titles). Astryx
// reads the category from the system only on iOS and Android, so on the
// Mac and Windows it stays at Large. This picks the category ourselves.
//
// Not public Astryx API: the scales are the --_dynamic-type-* properties
// Astryx's root Theme writes (core/src/theme/dynamicType.ts), and the curves
// are its UIKit table (core/src/theme/uikitTypography.ts, the seven standard
// sizes). Set inline on <html>, they win over the rule Astryx adopts. After
// an Astryx upgrade, check both files still match.

export const TEXT_SIZES = ["xs", "s", "m", "l", "xl", "xxl", "xxxl"]
const LARGE = TEXT_SIZES.indexOf("l")

// Point sizes per UIKit text style, xs … xxxl (uikitTypography.ts).
const UIKIT = {
  largeTitle: [31, 32, 33, 34, 36, 38, 40],
  title1: [25, 26, 27, 28, 30, 32, 34],
  title2: [19, 20, 21, 22, 24, 26, 28],
  title3: [17, 18, 19, 20, 22, 24, 26],
  body: [14, 15, 16, 17, 19, 21, 23],
  footnote: [12, 12, 12, 13, 15, 17, 19],
  caption1: [11, 11, 11, 12, 14, 16, 18],
  caption2: [11, 11, 11, 11, 13, 15, 17],
}

const prop = (style) => `--_dynamic-type-${style.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`)}`

/** Each scale property and its value for a category (all 1 at "l"). */
export function textSizeScales(category) {
  const col = TEXT_SIZES.indexOf(category)
  const at = col < 0 ? LARGE : col
  return Object.fromEntries(
    Object.entries(UIKIT).map(([style, row]) => [prop(style), String(Number((row[at] / row[LARGE]).toFixed(4)))]),
  )
}

/** Sets the chrome's text size on the document root. */
export function applyTextSize(category, root = document.documentElement) {
  for (const [name, value] of Object.entries(textSizeScales(category))) root.style.setProperty(name, value)
}
