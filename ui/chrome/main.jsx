// The React chrome's entry: Astryx's stylesheets in cascade order, the
// Neutral theme in dark mode, and the title bar mounted where index.html
// leaves room for it. app.js and the other plain modules talk to it through
// chrome/store.js.
import "@astryxdesign/core/reset.css"
import "@astryxdesign/core/astryx.css"
import "@astryxdesign/theme-neutral/theme.css"
import "./chrome.css"

import { createRoot } from "react-dom/client"
import { flushSync } from "react-dom"
import { Theme } from "@astryxdesign/core/theme"
import { neutralTheme } from "@astryxdesign/theme-neutral/built"
import { chrome } from "./store.js"
import { TitleBar } from "./TitleBar.jsx"
import { watchWindow } from "./Caption.jsx"

const host = document.getElementById("chrome-bar")
const root = createRoot(host)
// Render synchronously so #bar exists before the modules that follow
// (bell.js anchors its popover on the bell button).
flushSync(() =>
  root.render(
    <Theme theme={neutralTheme} mode="dark">
      <TitleBar />
    </Theme>,
  ),
)
watchWindow(chrome)
