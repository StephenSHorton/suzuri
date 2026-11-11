// The title bar's right-hand buttons (bell, cup) follow the platform. On
// Windows they match the caption buttons beside them: square, 46 px wide,
// the bar's full height, a flat fill on hover and while pressed. On the Mac
// they stay Astryx's rounded ghost buttons, like a Mac toolbar's. Done as an
// Astryx theme override (defineTheme components), scoped by a nested Theme
// around those buttons only, so the rest of the app keeps stock Neutral.
import { defineTheme } from "@astryxdesign/core/theme"
import { neutralTheme } from "@astryxdesign/theme-neutral/built"

const slot = { borderRadius: "0", width: "46px", minWidth: "46px", height: "34px", minHeight: "34px" }

export const windowsBarTheme = defineTheme({
  name: "suzuri-windows-bar",
  extends: neutralTheme,
  components: {
    button: { base: slot },
    "toggle-button": { base: slot },
  },
})

export const isMac = navigator.platform.startsWith("Mac")
