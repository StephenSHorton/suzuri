// Astryx's seven themes (Settings › Look › Theme, config ui_theme) and the
// theme the chrome shows now. Neutral, the default, is bundled with the
// page (chrome/main.jsx imports its CSS); every other theme's built module,
// CSS and fonts load the first time it is picked (a dynamic import, so the
// page doesn't carry all seven). Fonts are local @fontsource copies of what
// each theme's fonts.css asks Google for (the CSP allows no outside
// sources; that fonts.css is never imported), Latin subset only.
import { neutralTheme } from "@astryxdesign/theme-neutral/built"

// id → its built theme, once its CSS and fonts are in the page.
const LOADERS = {
  neutral: async () => neutralTheme,
  stone: async () => {
    const [m] = await Promise.all([
      import("@astryxdesign/theme-stone/built"),
      import("@astryxdesign/theme-stone/theme.css"),
      // Figtree (body) ships with the page (chrome.css); Montserrat
      // headings, JetBrains Mono code.
      import("@fontsource/montserrat/latin-400.css"),
      import("@fontsource/montserrat/latin-500.css"),
      import("@fontsource/montserrat/latin-600.css"),
      import("@fontsource/montserrat/latin-700.css"),
      ...jetbrainsMono(),
    ])
    return m.stoneTheme
  },
  matcha: async () => {
    const [m] = await Promise.all([
      import("@astryxdesign/theme-matcha/built"),
      import("@astryxdesign/theme-matcha/theme.css"),
      import("@fontsource/dm-sans/latin-400.css"),
      import("@fontsource/dm-sans/latin-500.css"),
      import("@fontsource/dm-sans/latin-600.css"),
      import("@fontsource/dm-sans/latin-700.css"),
      // Headings: one weight, as Google serves it.
      import("@fontsource/playwrite-us-trad/latin-400.css"),
      ...jetbrainsMono(),
    ])
    return m.matchaTheme
  },
  chocolate: async () => {
    const [m] = await Promise.all([
      import("@astryxdesign/theme-chocolate/built"),
      import("@astryxdesign/theme-chocolate/theme.css"),
      import("@fontsource/albert-sans/latin-400.css"),
      import("@fontsource/albert-sans/latin-500.css"),
      import("@fontsource/albert-sans/latin-600.css"),
      import("@fontsource/albert-sans/latin-700.css"),
      import("@fontsource/fraunces/latin-400.css"),
      import("@fontsource/fraunces/latin-500.css"),
      import("@fontsource/fraunces/latin-600.css"),
      import("@fontsource/fraunces/latin-700.css"),
      ...jetbrainsMono(),
    ])
    return m.chocolateTheme
  },
  butter: async () => {
    const [m] = await Promise.all([
      import("@astryxdesign/theme-butter/built"),
      import("@astryxdesign/theme-butter/theme.css"),
      import("@fontsource/outfit/latin-400.css"),
      import("@fontsource/outfit/latin-500.css"),
      import("@fontsource/outfit/latin-600.css"),
      import("@fontsource/outfit/latin-700.css"),
      // Display text.
      import("@fontsource/sarina/latin-400.css"),
      ...jetbrainsMono(),
    ])
    return m.butterTheme
  },
  gothic: async () => {
    const [m] = await Promise.all([
      import("@astryxdesign/theme-gothic/built"),
      import("@astryxdesign/theme-gothic/theme.css"),
      import("@fontsource/fustat/latin-400.css"),
      import("@fontsource/fustat/latin-500.css"),
      import("@fontsource/fustat/latin-600.css"),
      import("@fontsource/fustat/latin-700.css"),
      // Display text, with its blackletter fallback.
      import("@fontsource/manufacturing-consent/latin-400.css"),
      import("@fontsource/unifrakturmaguntia/latin-400.css"),
      ...jetbrainsMono(),
    ])
    return m.gothicTheme
  },
  y2k: async () => {
    const [m] = await Promise.all([
      import("@astryxdesign/theme-y2k/built"),
      import("@astryxdesign/theme-y2k/theme.css"),
      import("@fontsource/poppins/latin-400.css"),
      import("@fontsource/poppins/latin-500.css"),
      import("@fontsource/poppins/latin-600.css"),
      import("@fontsource/poppins/latin-700.css"),
      // Display text.
      import("@fontsource/crimson-text/latin-400.css"),
      import("@fontsource/crimson-text/latin-600.css"),
      import("@fontsource/crimson-text/latin-700.css"),
      ...jetbrainsMono(),
    ])
    return m.y2kTheme
  },
}

// Every theme but Neutral sets its code face to JetBrains Mono.
function jetbrainsMono() {
  return [
    import("@fontsource/jetbrains-mono/latin-400.css"),
    import("@fontsource/jetbrains-mono/latin-500.css"),
    import("@fontsource/jetbrains-mono/latin-600.css"),
    import("@fontsource/jetbrains-mono/latin-700.css"),
  ]
}

export const THEME_IDS = Object.keys(LOADERS)
export const DEFAULT_THEME = "neutral"
export const MODES = ["dark", "light", "system"]
export const DEFAULT_MODE = "dark"

// The swatch Settings shows beside each theme: its body colour in light
// and dark mode and its accent in each (the tokens' values in theme.css;
// test/theme_palette.test.mjs keeps them in step).
export const SWATCHES = {
  neutral: ["#f1f1f1", "#1b1b1b", "#1b1b1b", "#f1f1f1"],
  stone: ["#f0f0f7", "#101018", "#25252a", "#f0f0f9"],
  matcha: ["#f0f0e0", "#12140e", "#3e481d", "#c0cba9"],
  chocolate: ["#fffcf7", "#141010", "#8c5927", "#d4a06a"],
  butter: ["#fdfbe4", "#261a13", "#225bff", "#fdee8c"],
  gothic: ["#101314", "#101314", "#e8f1f6", "#e8f1f6"],
  y2k: ["#cccffa", "#0e0f1a", "#2d241b", "#edeffc"],
}

const loaded = new Map() // id → Promise of the built theme

/** Theme `id`'s built object, its CSS and fonts loaded; Neutral for an unknown id. */
export function loadTheme(id) {
  const key = id in LOADERS ? id : DEFAULT_THEME
  if (!loaded.has(key)) {
    const p = LOADERS[key]()
    // A failed load (a missing chunk) can be tried again next time.
    p.catch(() => loaded.delete(key))
    loaded.set(key, p)
  }
  return loaded.get(key)
}

// The chrome's theme now: {id, theme, mode}. chrome/main.jsx's root Theme
// and the title bar's nested one read it; appearance.js sets it.
let state = { id: DEFAULT_THEME, theme: neutralTheme, mode: DEFAULT_MODE }
const subs = new Set()

export const chromeTheme = {
  get: () => state,
  subscribe(fn) {
    subs.add(fn)
    return () => subs.delete(fn)
  },
  set(next) {
    state = { ...state, ...next }
    for (const fn of subs) fn()
  },
}
