// The React chrome's entry: Astryx's stylesheets in cascade order, the
// active theme (chrome/themes.js: Neutral in dark mode until the settings
// say otherwise; Neutral's CSS is bundled, the others load when picked),
// and the title bar mounted where index.html leaves room for it, plus every
// overlay the plain modules mount (chrome/overlays.js). app.js and the other
// plain modules talk to it through chrome/store.js; appearance.js switches
// the theme.
import "@astryxdesign/core/reset.css"
import "@astryxdesign/core/astryx.css"
import "@astryxdesign/theme-neutral/theme.css"
import "./chrome.css"

import { createRoot } from "react-dom/client"
import { Fragment, useSyncExternalStore } from "react"
import { flushSync } from "react-dom"
import { Theme } from "@astryxdesign/core/theme"
import { chrome } from "./store.js"
import { chromeTheme } from "./themes.js"
import { TitleBar } from "./TitleBar.jsx"
import { watchWindow } from "./Caption.jsx"
import { overlays } from "./overlays.js"

// Every overlay a module mounted (chrome/overlays.js), in mount order.
function OverlayHost() {
  const items = useSyncExternalStore(overlays.subscribe, overlays.get)
  return [...items].map(([key, el]) => <Fragment key={key}>{el}</Fragment>)
}

// The root Theme: Astryx puts its name and mode on <html>, so the panes
// (plain DOM outside React) take the same tokens.
function Root() {
  const { theme, mode } = useSyncExternalStore(chromeTheme.subscribe, chromeTheme.get)
  return (
    <Theme theme={theme} mode={mode}>
      <TitleBar />
      <OverlayHost />
    </Theme>
  )
}

const host = document.getElementById("chrome-bar")
const root = createRoot(host)
// Render synchronously so #bar exists before the modules that follow
// (bell.js anchors its popover on the bell button).
flushSync(() => root.render(<Root />))
watchWindow(chrome)
