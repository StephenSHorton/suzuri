// The Warp-style input bar (#285): the command line under each pane. A port
// of Go suzuri's internal/ui/input_bar.go and its bar-mode keys
// (window_darwin.go / window_windows.go). Pure state, no DOM, so
// `node --test ui/` covers it.
//
// The buffer is an array of code points; `cursor` is an index into it
// (0..length). Explicit "\n" is a line break (Shift+Enter); long lines
// soft-wrap at the bar's width.

export const PROMPT = "❯ "
export const MAX_HISTORY = 200
export const MAX_VISUAL_ROWS = 8

const isSpace = (c) => c === " " || c === "\t" || c === "\n"

export class InputBar {
  constructor() {
    this.runes = []
    this.cursor = 0
    this.history = []
    this.histIdx = -1 // -1: editing the live line
    this.draft = ""
  }

  text() {
    return this.runes.join("")
  }

  clear() {
    this.runes = []
    this.cursor = 0
    this.histIdx = -1
    this.draft = ""
  }

  leaveHistory() {
    if (this.histIdx >= 0) {
      this.histIdx = -1
      this.draft = ""
    }
  }

  clamp() {
    this.cursor = Math.max(0, Math.min(this.cursor, this.runes.length))
  }

  // Typed or pasted text. CR is dropped, tabs become two spaces (Tab is
  // completion, #295), other control characters are dropped, "\n" is kept.
  insert(s) {
    const clean = []
    for (const c of s) {
      if (c === "\r") continue
      if (c === "\n") clean.push("\n")
      else if (c === "\t") clean.push(" ", " ")
      else if (c.codePointAt(0) >= 32 && c.codePointAt(0) !== 0x7f) clean.push(c)
    }
    if (!clean.length) return
    this.leaveHistory()
    this.clamp()
    this.runes.splice(this.cursor, 0, ...clean)
    this.cursor += clean.length
  }

  insertNewline() {
    this.insert("\n")
  }

  backspace() {
    this.leaveHistory()
    if (this.cursor <= 0) return
    this.runes.splice(this.cursor - 1, 1)
    this.cursor--
  }

  deleteForward() {
    this.leaveHistory()
    if (this.cursor >= this.runes.length) return
    this.runes.splice(this.cursor, 1)
  }

  moveLeft() {
    if (this.cursor > 0) this.cursor--
  }

  moveRight() {
    if (this.cursor < this.runes.length) this.cursor++
  }

  wordBoundary(i, dir) {
    const r = this.runes
    i = Math.max(0, Math.min(i, r.length))
    if (dir < 0) {
      while (i > 0 && isSpace(r[i - 1])) i--
      while (i > 0 && !isSpace(r[i - 1])) i--
      return i
    }
    while (i < r.length && !isSpace(r[i])) i++
    while (i < r.length && isSpace(r[i])) i++
    return i
  }

  moveWordLeft() {
    this.cursor = this.wordBoundary(this.cursor, -1)
  }

  moveWordRight() {
    this.cursor = this.wordBoundary(this.cursor, 1)
  }

  deleteWordLeft() {
    this.leaveHistory()
    if (this.cursor <= 0) return
    const start = this.wordBoundary(this.cursor, -1)
    this.runes.splice(start, this.cursor - start)
    this.cursor = start
  }

  deleteWordRight() {
    this.leaveHistory()
    if (this.cursor >= this.runes.length) return
    const end = this.wordBoundary(this.cursor, 1)
    this.runes.splice(this.cursor, end - this.cursor)
  }

  // Home/End: the current logical line (between "\n"s), not the buffer.
  moveHome() {
    let i = this.cursor
    while (i > 0 && this.runes[i - 1] !== "\n") i--
    this.cursor = i
  }

  moveEnd() {
    let i = this.cursor
    while (i < this.runes.length && this.runes[i] !== "\n") i++
    this.cursor = i
  }

  moveDocHome() {
    this.cursor = 0
  }

  moveDocEnd() {
    this.cursor = this.runes.length
  }

  historyUp() {
    if (!this.history.length) return
    if (this.histIdx < 0) {
      this.draft = this.text()
      this.histIdx = this.history.length - 1
    } else if (this.histIdx > 0) {
      this.histIdx--
    }
    this.applyHistory()
  }

  historyDown() {
    if (this.histIdx < 0) return
    if (this.histIdx + 1 >= this.history.length) {
      this.histIdx = -1
      this.runes = [...this.draft]
      this.cursor = this.runes.length
      this.draft = ""
      return
    }
    this.histIdx++
    this.applyHistory()
  }

  applyHistory() {
    this.runes = [...this.history[this.histIdx]]
    this.cursor = this.runes.length
  }

  // The line to send (without the CR); clears the bar. Non-blank lines go
  // into history, without repeating the last one.
  submit() {
    const line = this.text()
    if (line.trim() !== "" && this.history[this.history.length - 1] !== line) {
      this.history.push(line)
      if (this.history.length > MAX_HISTORY) this.history.splice(0, this.history.length - MAX_HISTORY)
    }
    this.clear()
    return line
  }

  // Display rows of at most `width` columns, and the caret's row and column.
  layout(width) {
    width = Math.max(1, width)
    const lines = []
    const at = new Array(this.runes.length + 1)
    let row = 0
    let col = 0
    let cur = []
    const flush = () => {
      lines.push(cur.join(""))
      cur = []
      row++
      col = 0
    }
    at[0] = [0, 0]
    this.runes.forEach((c, i) => {
      if (c === "\n") {
        flush()
        at[i + 1] = [row, col]
        return
      }
      if (col >= width) flush()
      cur.push(c)
      col++
      at[i + 1] = [row, col]
    })
    if (cur.length || !lines.length || this.runes[this.runes.length - 1] === "\n") lines.push(cur.join(""))
    this.clamp()
    let [cr, cc] = at[this.cursor]
    if (cr >= lines.length) {
      cr = lines.length - 1
      cc = [...lines[cr]].length
    }
    return { lines, row: cr, col: cc }
  }

  visualRows(width) {
    return Math.max(1, Math.min(MAX_VISUAL_ROWS, this.layout(width).lines.length))
  }

  // Up/Down inside a multi-row buffer; false on the first/last row (the
  // caller then walks history).
  moveVisualUp(width) {
    const { row, col } = this.layout(width)
    if (row <= 0) return false
    this.cursor = this.indexAt(width, row - 1, col)
    return true
  }

  moveVisualDown(width) {
    const { lines, row, col } = this.layout(width)
    if (row >= lines.length - 1) return false
    this.cursor = this.indexAt(width, row + 1, col)
    return true
  }

  indexAt(width, wantRow, wantCol) {
    width = Math.max(1, width)
    let row = 0
    let col = 0
    for (let i = 0; i < this.runes.length; i++) {
      const c = this.runes[i]
      if (row === wantRow && col >= wantCol) return i
      if (c === "\n") {
        if (row === wantRow) return i
        row++
        col = 0
        continue
      }
      if (col >= width) {
        row++
        col = 0
        if (row === wantRow && col >= wantCol) return i
      }
      col++
      if (row === wantRow && col > wantCol) return i + 1
    }
    return this.runes.length
  }

  // At most `maxRows` display rows, scrolled to keep the caret in view.
  visible(width, maxRows = MAX_VISUAL_ROWS) {
    const { lines, row, col } = this.layout(width)
    if (lines.length <= maxRows) return { lines, row, col }
    let start = Math.max(0, row - maxRows + 1)
    const end = Math.min(lines.length, start + maxRows)
    start = Math.max(0, end - maxRows)
    return { lines: lines.slice(start, end), row: row - start, col }
  }
}

// A keydown while the bar has the keys. Returns what the app should do:
//   { kind: "edit" }              the bar changed (repaint, maybe relayout)
//   { kind: "submit", line }      Enter: send the line
//   { kind: "pty", data }         bytes for the PTY (Ctrl+C on an empty bar…)
//   { kind: "scroll", pages }     PageUp/PageDown: scroll the pane
//   { kind: "none" }              consumed, nothing to do
//   null                          not a bar key: the app handles it
// Text itself arrives through the input event (insert), not here.
export function barKey(bar, e, { isMac, cols }) {
  const meta = isMac && e.metaKey
  const ctrl = e.ctrlKey && !e.metaKey
  const alt = e.altKey
  const shift = e.shiftKey
  // Option/Ctrl: word moves and deletes.
  const wordMod = (alt && !meta && !ctrl) || (ctrl && !meta && !alt)
  switch (e.key) {
    case "Enter":
      if (shift) {
        bar.insertNewline()
        return { kind: "edit" }
      }
      return { kind: "submit", line: bar.submit() }
    case "Backspace":
      if (meta && !alt && !ctrl && !shift) bar.clear() // ⌘⌫ clears the whole bar
      else if (wordMod) bar.deleteWordLeft()
      else bar.backspace()
      return { kind: "edit" }
    case "Delete":
      if (meta && !alt && !ctrl && !shift) bar.clear()
      else if (wordMod) bar.deleteWordRight()
      else bar.deleteForward()
      return { kind: "edit" }
    case "ArrowUp":
      if (meta && !alt) bar.moveDocHome()
      else if (!bar.moveVisualUp(cols)) bar.historyUp()
      return { kind: "edit" }
    case "ArrowDown":
      if (meta && !alt) bar.moveDocEnd()
      else if (!bar.moveVisualDown(cols)) bar.historyDown()
      return { kind: "edit" }
    case "ArrowLeft":
      if (meta && !alt) bar.moveHome()
      else if (wordMod) bar.moveWordLeft()
      else bar.moveLeft()
      return { kind: "edit" }
    case "ArrowRight":
      if (meta && !alt) bar.moveEnd()
      else if (wordMod) bar.moveWordRight()
      else bar.moveRight()
      return { kind: "edit" }
    case "Home":
      if (meta || ctrl) bar.moveDocHome()
      else bar.moveHome()
      return { kind: "edit" }
    case "End":
      if (meta || ctrl) bar.moveDocEnd()
      else bar.moveEnd()
      return { kind: "edit" }
    case "PageUp":
      return { kind: "scroll", pages: 0.5 }
    case "PageDown":
      return { kind: "scroll", pages: -0.5 }
    case "Escape":
      bar.clear()
      return { kind: "edit" }
    case "Tab":
      return { kind: "none" } // completion is #295; never move focus
  }
  if (ctrl && !alt && !shift && e.key.length === 1) {
    const k = e.key.toLowerCase()
    // Ctrl+C: clear the bar, or interrupt when it is empty.
    if (k === "c") {
      if (bar.runes.length) {
        bar.clear()
        return { kind: "edit" }
      }
      return { kind: "pty", data: "\x03" }
    }
    // Ctrl+D on an empty bar: EOF to the shell (exit). Ctrl+L: clear screen.
    if (k === "d" && !bar.runes.length) return { kind: "pty", data: "\x04" }
    if (k === "l") return { kind: "pty", data: "\x0c" }
    // Emacs line keys, which shells users expect here too.
    if (k === "a") return bar.moveHome(), { kind: "edit" }
    if (k === "e") return bar.moveEnd(), { kind: "edit" }
    if (k === "u") {
      const end = bar.cursor
      bar.moveHome()
      bar.runes.splice(bar.cursor, end - bar.cursor)
      bar.leaveHistory()
      return { kind: "edit" }
    }
    if (k === "k") {
      const start = bar.cursor
      bar.moveEnd()
      bar.runes.splice(start, bar.cursor - start)
      bar.cursor = start
      bar.leaveHistory()
      return { kind: "edit" }
    }
    if (k === "w") return bar.deleteWordLeft(), { kind: "edit" }
    return { kind: "none" }
  }
  return null
}

// The bar's height in rows: the cwd line (when known) plus its content rows.
export function barRows(bar, cols, cwd) {
  return (cwd ? 1 : 0) + bar.visualRows(cols)
}

// The cwd as shown: home becomes ~.
export function displayPath(cwd, home) {
  if (!cwd) return ""
  if (home && (cwd === home || cwd.startsWith(home + "/") || cwd.startsWith(home + "\\"))) return "~" + cwd.slice(home.length)
  return cwd
}
