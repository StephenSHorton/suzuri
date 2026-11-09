// App shortcuts: a keydown to a command id (see commands in app.js), or
// null when the key belongs to the terminal. The same chords as Go suzuri
// (internal/ui/window_darwin.go, window_windows.go, chrome/commands.go):
// on macOS "mod" is Cmd (Ctrl also works for the Ctrl+Shift chords), on
// Windows it is Ctrl.
//
// Returns { id, arg } or null.

const ARROWS = { arrowleft: "left", arrowright: "right", arrowup: "up", arrowdown: "down" }

export function shortcut(e, isMac) {
  const k = (e.key || "").toLowerCase()
  const code = e.code || ""
  const meta = !!e.metaKey, ctrl = !!e.ctrlKey, alt = !!e.altKey, shift = !!e.shiftKey
  const mod = isMac ? meta : ctrl // the host modifier
  const modOrCtrl = meta || ctrl // Go's "ctrl" on macOS: Cmd or Ctrl
  const arrow = ARROWS[k]
  const digit = /^Digit[1-9]$/.test(code) ? Number(code.slice(5)) : /^[1-9]$/.test(k) ? Number(k) : 0
  const r = (id, arg) => ({ id, arg })

  // Tabs: Ctrl+Tab / Ctrl+Shift+Tab on both (Cmd+Tab is the OS app switcher).
  if (ctrl && !alt && k === "tab") return r(shift ? "prev_tab" : "next_tab")

  // Move the focused pane to the previous/next tab: ⌘⌥⇧←/→, Ctrl+Alt+Shift+←/→.
  if (mod && alt && shift && (arrow === "left" || arrow === "right")) return r(arrow === "left" ? "move_pane_prev_tab" : "move_pane_next_tab")

  // Move the focused pane (swap with its neighbour): ⌘⌃+arrows, Ctrl+Alt+arrows.
  if (isMac && meta && ctrl && !alt && !shift && arrow) return r("move_pane_" + arrow)
  if (!isMac && ctrl && alt && !shift && !meta && arrow) return r("move_pane_" + arrow)

  // Pane focus: ⌘⌥+arrows on macOS, Alt+arrows on Windows.
  if (isMac && meta && alt && !shift && arrow) return r("focus_" + arrow)
  if (!isMac && alt && !ctrl && !shift && arrow) return r("focus_" + arrow)

  // Split: Alt+Shift+= / Alt+Shift+- on Windows too (Windows Terminal's).
  if (!isMac && alt && shift && !ctrl) {
    if (code === "Equal" || k === "+" || k === "=") return r("split_right")
    if (code === "Minus" || k === "_" || k === "-") return r("split_down")
  }

  // Font zoom (Go: Ctrl/⌘ + - 0); "+" may come shifted.
  if (mod && !alt) {
    if (code === "Equal" || k === "=" || k === "+") return r("zoom_in")
    if (!shift && (code === "Minus" || k === "-")) return r("zoom_out")
    if (!shift && (code === "Digit0" || k === "0")) return r("zoom_reset")
  }

  const modShift = isMac ? modOrCtrl && shift : ctrl && shift
  if (modShift && !alt) {
    if (k === "t") return r("new_tab")
    if (k === "n") return r("new_window")
    if (k === "d") return r("split_right")
    if (k === "e") return r("split_down")
    if (k === "w") return r(isMac ? "close_tab" : "close_pane")
    if (k === "p") return r("palette")
    if (k === "h") return r("hud")
    if (k === "enter") return r("zoom_pane")
    if (k === "[" || k === "{" || code === "BracketLeft" || arrow === "left") return r("prev_tab")
    if (k === "]" || k === "}" || code === "BracketRight" || arrow === "right") return r("next_tab")
  }

  if (mod && !shift && !alt) {
    if (isMac && k === "d") return r("split_right") // the spike's ⌘D, kept
    if (isMac && k === "t") return r("new_tab")
    if (k === "w") return r("close_pane")
    if (k === "k" || k === "p") return r("palette")
    if (k === "," || code === "Comma") return r("settings")
    if (k === "/" || code === "Slash") return r("help")
    if (digit) return r("select_tab", digit - 1)
  }

  if (!meta && !ctrl && !alt && !shift && k === "f2") return r("rename_pane")
  return null
}

// The chord shown next to a command in the palette, per platform.
export function chordLabel(id, isMac) {
  return chordLabels(isMac)[id] || ""
}

// Every command's chord label by id (the shortcuts sheet, help.js, lists
// them all; its test checks none is left out).
export function chordLabels(isMac) {
  const m = isMac ? "⌘" : "Ctrl+"
  const ms = isMac ? "⌘⇧" : "Ctrl+Shift+"
  const mas = isMac ? "⌘⌥⇧" : "Ctrl+Alt+Shift+"
  return {
    new_tab: isMac ? "⌘T" : ms + "T",
    new_window: ms + "N",
    close_tab: isMac ? "⌘⇧W" : "",
    next_tab: ms + "] · Ctrl+Tab",
    prev_tab: ms + "[ · Ctrl+Shift+Tab",
    split_right: isMac ? "⌘D · ⌘⇧D" : ms + "D · Alt+Shift+=",
    split_down: isMac ? "⌘⇧E" : ms + "E · Alt+Shift+-",
    close_pane: m + "W",
    zoom_pane: ms + "Enter",
    focus_left: isMac ? "⌘⌥←" : "Alt+←",
    focus_right: isMac ? "⌘⌥→" : "Alt+→",
    focus_up: isMac ? "⌘⌥↑" : "Alt+↑",
    focus_down: isMac ? "⌘⌥↓" : "Alt+↓",
    move_pane_left: isMac ? "⌘⌃←" : "Ctrl+Alt+←",
    move_pane_right: isMac ? "⌘⌃→" : "Ctrl+Alt+→",
    move_pane_up: isMac ? "⌘⌃↑" : "Ctrl+Alt+↑",
    move_pane_down: isMac ? "⌘⌃↓" : "Ctrl+Alt+↓",
    move_pane_prev_tab: mas + "←",
    move_pane_next_tab: mas + "→",
    rename_pane: "F2",
    palette: m + "K · " + m + "P",
    settings: m + ",",
    help: m + "/",
    select_tab: m + "1–9",
    hud: ms + "H",
    zoom_in: m + "+",
    zoom_out: m + "-",
    zoom_reset: m + "0",
  }
}
