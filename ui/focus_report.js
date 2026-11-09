// Focus reports (DECSET 1004), as Go suzuri's syncTermFocus sent them: a
// pane whose program turned the mode on hears ESC[I while it is the
// focused pane of a focused window, and ESC[O otherwise. So Claude Code or
// vim learn when you switch panes, tabs or apps. Each pane hears a change
// once, and a program that has just turned the mode on hears where it
// stands (Go's armFocus reply). No Tauri here (tests import it).
import { M_FOCUS } from "./view/frame.js"

export const FOCUS_IN = "\x1b[I"
export const FOCUS_OUT = "\x1b[O"

// env: {panes() -> every pane, focused() -> pane | null, write(pane, data),
//   windowFocused?: bool}. A pane: {id, exited, modes}.
export class FocusReports {
  constructor(env) {
    this.env = env
    this.window = env.windowFocused ?? true
    this.sent = new WeakMap() // pane -> the last state it heard
  }

  // The OS window gained or lost focus.
  setWindow(on) {
    this.window = !!on
    this.sync()
  }

  // After the focused pane changed: every pane hears what changed for it.
  sync() {
    for (const p of this.env.panes()) this.check(p)
  }

  // One pane, e.g. after its frame painted (its modes may have changed).
  check(p) {
    if (!p.id || p.exited || !(p.modes & M_FOCUS)) {
      // Off: the next time it is turned on, the program hears the state.
      this.sent.delete(p)
      return
    }
    const on = this.window && p === this.env.focused()
    if (this.sent.get(p) === on) return
    this.sent.set(p, on)
    this.env.write(p, on ? FOCUS_IN : FOCUS_OUT)
  }

  // The window's focus and blur drive setWindow.
  listen(win) {
    win.addEventListener("focus", () => this.setWindow(true))
    win.addEventListener("blur", () => this.setWindow(false))
  }
}
