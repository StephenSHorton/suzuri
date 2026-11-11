// The title bar's right-hand buttons (bell, cup) follow the platform. On
// Windows they match the caption buttons beside them: square, 46 px wide,
// the bar's full height, a flat fill on hover and while pressed. On the Mac
// they stay Astryx's rounded ghost buttons, like a Mac toolbar's. Done as an
// Astryx theme override (defineTheme components), scoped by a nested Theme
// around those buttons only, so the rest of the app keeps the stock theme.
// It extends whichever theme is active (chrome/themes.js), one override per
// theme: Astryx keeps a theme's injected CSS by name, so each needs its own.
import { defineTheme } from "@astryxdesign/core/theme"

const slot = { borderRadius: "0", width: "46px", minWidth: "46px", height: "34px", minHeight: "34px" }

const made = new Map() // base theme → its bar theme

export function windowsBarTheme(base) {
  if (!made.has(base)) {
    made.set(base, defineTheme({
      name: `suzuri-windows-bar-${base.name}`,
      extends: base,
      components: {
        button: { base: slot },
        "toggle-button": { base: slot },
      },
    }))
  }
  return made.get(base)
}

export const isMac = navigator.platform.startsWith("Mac")
