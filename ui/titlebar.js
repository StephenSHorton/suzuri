// The title bar. On Windows the window has no native caption (main.rs turns
// decorations off), so the tab strip is the title bar, as in Windows
// Terminal: this draws minimize / maximize / close at its right edge, in
// Windows' own Segoe Fluent glyphs, and the strip drags and double-click
// maximizes through data-tauri-drag-region. On the Mac the traffic lights
// already sit in the strip (titleBarStyle Overlay), so only the + icon here.
import { icon } from "./icons.js"

document.getElementById("new-tab").innerHTML = icon("plus")

const isMac = navigator.platform.startsWith("Mac")
if (!isMac) {
  const win = window.__TAURI__.window.getCurrentWindow()
  const bar = document.getElementById("bar")
  const caption = document.createElement("div")
  caption.id = "caption"
  // Segoe Fluent Icons / MDL2 code points: ChromeMinimize, ChromeMaximize,
  // ChromeRestore, ChromeClose.
  caption.innerHTML =
    '<button class="min" title="Minimize" tabindex="-1"></button>' +
    '<button class="max" title="Maximize" tabindex="-1"></button>' +
    '<button class="close" title="Close" tabindex="-1"></button>'
  bar.appendChild(caption)
  const max = caption.querySelector(".max")
  // Keep the pane's keyboard: a click here must not take focus.
  caption.addEventListener("mousedown", (e) => e.preventDefault())
  caption.querySelector(".min").onclick = () => win.minimize()
  max.onclick = () => win.toggleMaximize()
  caption.querySelector(".close").onclick = () => win.close()

  const sync = async () => {
    const zoomed = await win.isMaximized().catch(() => false)
    max.textContent = zoomed ? "" : ""
    max.title = zoomed ? "Restore Down" : "Maximize"
    document.body.classList.toggle("maximized", zoomed)
  }
  sync()
  win.onResized(sync)
  // Windows greys its caption in a window that isn't active.
  win.onFocusChanged(({ payload }) => document.body.classList.toggle("inactive", !payload))
}
