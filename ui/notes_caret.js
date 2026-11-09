// The notes caret in the terminal's cursor shape (#294), as Go painted it.
// WebKit has no caret-shape, so block and underline are a box drawn over a
// transparent native caret; bar is the text box's own caret. The box sits
// where a hidden copy of the field (a mirror) puts the caret.

const COPY = [
  "boxSizing", "fontFamily", "fontSize", "fontWeight", "fontStyle", "fontVariant", "fontFeatureSettings",
  "letterSpacing", "wordSpacing", "lineHeight", "textTransform", "textIndent", "tabSize",
  "paddingTop", "paddingRight", "paddingBottom", "paddingLeft",
]

// The mirror's text split around the caret: before, the mark's character,
// after. The rest of the value stays in so a word wraps in the mirror as it
// does in the field. At the end or a newline the mark is a space.
export function mirrorParts(v, pos) {
  const ch = v.charAt(pos)
  if (!ch) return [v.slice(0, pos), " ", ""]
  if (ch === "\n") return [v.slice(0, pos), " ", v.slice(pos)]
  return [v.slice(0, pos), ch, v.slice(pos + 1)]
}

// Where the caret at pos sits in field, from the field's padding edge and
// before scrolling: {x, y, w, h}. w is the width of the character under it.
export function caretAt(field, pos) {
  const cs = getComputedStyle(field)
  const area = field.tagName === "TEXTAREA"
  const m = document.createElement("div")
  for (const k of COPY) m.style[k] = cs[k]
  const padX = parseFloat(cs.paddingLeft) + parseFloat(cs.paddingRight)
  Object.assign(m.style, {
    position: "absolute", visibility: "hidden", top: "0", left: "-10000px", border: "0", overflow: "hidden",
    boxSizing: "content-box", whiteSpace: area ? "pre-wrap" : "pre", overflowWrap: area ? "break-word" : "normal",
    width: area ? `${field.clientWidth - padX}px` : "auto",
  })
  const [before, at, after] = mirrorParts(field.value, pos)
  const mark = document.createElement("span")
  mark.textContent = at
  m.append(before, mark, after)
  document.body.append(m)
  const r = { x: mark.offsetLeft, y: mark.offsetTop, w: mark.offsetWidth, h: mark.offsetHeight }
  m.remove()
  return r
}

// True when the textarea's caret is on its first wrapped row. A word that
// wraps puts its start on row 2, which the mirror sees as it keeps the rest.
export function onFirstRow(field) {
  return caretAt(field, field.selectionStart).y === caretAt(field, 0).y
}

// Draw field's caret as shape ("block", "underline" or "bar"). The box lives
// in the field's parent, so rebuilding the panel body removes it.
export function caretStyle(field, shape) {
  if (shape === "bar") return
  if (globalThis.CSS?.supports?.("caret-shape", shape)) {
    field.style.setProperty("caret-shape", shape)
    return
  }
  const host = field.parentElement
  const box = document.createElement("div")
  box.className = "notes-caret"
  box.hidden = true
  host.append(box)
  field.style.caretColor = "transparent"
  let frame = 0
  const draw = () => {
    frame = 0
    if (!field.isConnected) return document.removeEventListener("selectionchange", soon)
    if (document.activeElement !== field || field.selectionStart !== field.selectionEnd) return (box.hidden = true)
    let c
    try {
      c = caretAt(field, field.selectionStart)
    } catch {
      // Can't measure: give the native caret back.
      box.hidden = true
      field.style.caretColor = ""
      return
    }
    const fr = field.getBoundingClientRect(), hr = host.getBoundingClientRect()
    const x = c.x - field.scrollLeft, y = c.y - field.scrollTop
    // Hidden when scrolled out of the field's box.
    if (y < 0 || y + c.h > field.clientHeight || x < 0 || x > field.clientWidth) return (box.hidden = true)
    const under = shape === "underline"
    Object.assign(box.style, {
      left: `${fr.left - hr.left - host.clientLeft + field.clientLeft + x}px`,
      top: `${fr.top - hr.top - host.clientTop + field.clientTop + y + (under ? c.h - 2 : 0)}px`,
      width: `${c.w}px`,
      height: `${under ? 2 : c.h}px`,
    })
    box.hidden = false
  }
  const soon = () => { if (!frame) frame = requestAnimationFrame(draw) }
  for (const ev of ["input", "keydown", "keyup", "mouseup", "scroll", "focus", "blur", "select"]) field.addEventListener(ev, soon)
  document.addEventListener("selectionchange", soon)
  soon()
}
