// Windows' minimize / maximize / close, drawn in the title bar because the
// window has no native caption (main.rs turns decorations off on Windows).
// Windows' own Segoe Fluent glyphs at Windows' size (46 px wide, full bar
// height), so they look like every other app's; close turns red on hover.
const win = () => window.__TAURI__.window.getCurrentWindow()
const keepFocus = (e) => e.preventDefault()

export function Caption({ maximized }) {
  return (
    <div className="rsz-caption" onMouseDown={keepFocus}>
      <button className="min" title="Minimize" tabIndex={-1} onClick={() => win().minimize()}>
        {""}
      </button>
      <button className="max" title={maximized ? "Restore Down" : "Maximize"} tabIndex={-1} onClick={() => win().toggleMaximize()}>
        {maximized ? "" : ""}
      </button>
      <button className="close" title="Close" tabIndex={-1} onClick={() => win().close()}>
        {""}
      </button>
    </div>
  )
}

// Keeps the store's window state current: the restore glyph while
// maximized, and Windows' greyed caption while another app is in front.
export function watchWindow(chrome) {
  if (navigator.platform.startsWith("Mac")) return
  const w = win()
  const sync = async () => chrome.set({ maximized: await w.isMaximized().catch(() => false) })
  sync()
  w.onResized(sync)
  w.onFocusChanged(({ payload }) => chrome.set({ inactive: !payload }))
}
