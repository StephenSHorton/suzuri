// Box drawing (U+2500-257F), block elements (U+2580-259F) and the powerline
// wedges, drawn edge to edge in the cell the way Go suzuri does
// (internal/ui/boxdraw_software.go and boxdraw_windows.go, the same rules):
// font ink has padding, so stacked │ and joined ─ would show gaps, and some
// faces draw ▓▒░ taller than the cell. Integer maths as in Go.
// drawCellGlyph(ctx, cp, x, y, cw, ch) uses ctx.fillStyle as the ink and
// returns true when it drew the cell.

const N = 1, S = 2, E = 4, W = 8
const div = (a, b) => Math.trunc(a / b)

function lineThick(cw, ch, heavy) {
  let t = cw < ch ? div(cw, 10) : div(ch, 10)
  if (heavy) t = cw < ch ? div(cw, 6) : div(ch, 6)
  if (t < 1) t = 1
  if (heavy && t < 2) t = 2
  return t
}

function hStroke(ctx, x0, x1, y, t) {
  if (t < 1) t = 1
  if (x1 < x0) [x0, x1] = [x1, x0]
  const top = y - div(t, 2)
  if (x1 > x0) ctx.fillRect(x0, top, x1 - x0, t)
}

function vStroke(ctx, x, y0, y1, t) {
  if (t < 1) t = 1
  if (y1 < y0) [y0, y1] = [y1, y0]
  const left = x - div(t, 2)
  if (y1 > y0) ctx.fillRect(left, y0, t, y1 - y0)
}

function arms(ctx, c, a, heavy) {
  const t = lineThick(c.w, c.h, heavy)
  if (a & N) vStroke(ctx, c.cx, c.y, c.cy + div(t, 2), t)
  if (a & S) vStroke(ctx, c.cx, c.cy - div(t, 2), c.y + c.h, t)
  if (a & E) hStroke(ctx, c.cx - div(t, 2), c.x + c.w, c.cy, t)
  if (a & W) hStroke(ctx, c.x, c.cx + div(t, 2), c.cy, t)
}

// Light and heavy junctions: rune -> [arms, heavy]. Double corners are heavy
// single arms in Go ("approximate double corners with heavy single arms").
const JOINS = {
  "┌": [S | E, 0], "┐": [S | W, 0], "└": [N | E, 0], "┘": [N | W, 0],
  "├": [N | S | E, 0], "┤": [N | S | W, 0], "┬": [S | E | W, 0], "┴": [N | E | W, 0], "┼": [N | S | E | W, 0],
  "┏": [S | E, 1], "┓": [S | W, 1], "┗": [N | E, 1], "┛": [N | W, 1],
  "┣": [N | S | E, 1], "┫": [N | S | W, 1], "┳": [S | E | W, 1], "┻": [N | E | W, 1], "╋": [N | S | E | W, 1],
  "╔": [S | E, 1], "╗": [S | W, 1], "╚": [N | E, 1], "╝": [N | W, 1],
}

function ellipseCW(ctx, cx, cy, rx, ry, t, start, end) {
  if (rx < 1 || ry < 1) return
  if (t < 1) t = 1
  const arc = Math.abs(end - start) * ((rx + ry) / 2)
  const steps = Math.min(96, Math.max(16, Math.trunc(arc + 0.5)))
  const half = div(t, 2)
  for (let i = 0; i <= steps; i++) {
    const th = start + ((end - start) * i) / steps
    const x = cx + Math.round(rx * Math.cos(th))
    const y = cy + Math.round(ry * Math.sin(th))
    ctx.fillRect(x - half, y - half, t, t)
  }
}

function roundCorner(ctx, c, which) {
  const t = lineThick(c.w, c.h, false)
  let r = Math.min(c.w, c.h)
  r = div(r, 2) - t
  if (r < 2) r = 2
  let m = div(c.w, 2)
  if (r > m - 1 && m > 2) r = m - 1
  m = div(c.h, 2)
  if (r > m - 1 && m > 2) r = m - 1
  const { cx, cy } = c
  const P = Math.PI
  if (which === "╭") {
    ellipseCW(ctx, cx + r, cy + r, r, r, t, P, (3 * P) / 2)
    hStroke(ctx, cx + r, c.x + c.w, cy, t); vStroke(ctx, cx, cy + r, c.y + c.h, t)
  } else if (which === "╮") {
    ellipseCW(ctx, cx - r, cy + r, r, r, t, (3 * P) / 2, 2 * P)
    hStroke(ctx, c.x, cx - r, cy, t); vStroke(ctx, cx, cy + r, c.y + c.h, t)
  } else if (which === "╯") {
    ellipseCW(ctx, cx - r, cy - r, r, r, t, 0, P / 2)
    hStroke(ctx, c.x, cx - r, cy, t); vStroke(ctx, cx, c.y, cy - r, t)
  } else {
    ellipseCW(ctx, cx + r, cy - r, r, r, t, P / 2, P)
    hStroke(ctx, cx + r, c.x + c.w, cy, t); vStroke(ctx, cx, c.y, cy - r, t)
  }
}

function boxDrawing(ctx, ch, c) {
  const light = lineThick(c.w, c.h, false)
  switch (ch) {
    case "─": hStroke(ctx, c.x, c.x + c.w, c.cy, light); return true
    case "│": vStroke(ctx, c.cx, c.y, c.y + c.h, light); return true
    case "━": hStroke(ctx, c.x, c.x + c.w, c.cy, lineThick(c.w, c.h, true)); return true
    case "┃": vStroke(ctx, c.cx, c.y, c.y + c.h, lineThick(c.w, c.h, true)); return true
    case "═": hStroke(ctx, c.x, c.x + c.w, c.cy - light - 1, light); hStroke(ctx, c.x, c.x + c.w, c.cy + light + 1, light); return true
    case "║": vStroke(ctx, c.cx - light - 1, c.y, c.y + c.h, light); vStroke(ctx, c.cx + light + 1, c.y, c.y + c.h, light); return true
    case "╭": case "╮": case "╯": case "╰": roundCorner(ctx, c, ch); return true
  }
  const j = JOINS[ch]
  if (!j) return false
  arms(ctx, c, j[0], !!j[1])
  return true
}

function shade(ctx, c, level) {
  const step = Math.max(1, 4 - level)
  for (let y = c.y; y < c.y + c.h; y += step) {
    for (let x = c.x; x < c.x + c.w; x += step) {
      if ((div(x - c.x, step) + div(y - c.y, step)) % 2 !== 0) continue
      ctx.fillRect(x, y, Math.min(x + step, c.x + c.w) - x, Math.min(y + step, c.y + c.h) - y)
    }
  }
}

function block(ctx, ch, c) {
  const { x, y, w, h } = c
  switch (ch) {
    case "█": ctx.fillRect(x, y, w, h); return true
    case "▀": ctx.fillRect(x, y, w, div(h, 2)); return true
    case "▄": ctx.fillRect(x, y + div(h, 2), w, h - div(h, 2)); return true
    case "▌": ctx.fillRect(x, y, div(w, 2), h); return true
    case "▐": ctx.fillRect(x + div(w, 2), y, w - div(w, 2), h); return true
    case "▔": ctx.fillRect(x, y, w, Math.max(1, div(h, 8))); return true
    case "▁": { const hh = Math.max(1, div(h, 8)); ctx.fillRect(x, y + h - hh, w, hh); return true }
    case "▏": ctx.fillRect(x, y, Math.max(1, div(w, 8)), h); return true
    case "▎": ctx.fillRect(x, y, Math.max(1, div(w, 4)), h); return true
    case "▍": ctx.fillRect(x, y, Math.max(1, div(w * 3, 8)), h); return true
    case "░": shade(ctx, c, 1); return true
    case "▒": shade(ctx, c, 2); return true
    case "▓": shade(ctx, c, 3); return true
  }
  ctx.fillRect(x, y, w, h) // the rest of U+2580-259F: Go fills the cell
  return true
}

function triangle(ctx, c, right) {
  const { x, y, w, h } = c
  if (w < 1 || h < 1) return
  const half = Math.max(1, div(h, 2))
  for (let row = 0; row < h; row++) {
    const span = Math.max(1, row <= half ? div(w * row, half) : div(w * (h - 1 - row), half))
    ctx.fillRect(right ? x : x + w - span, y + row, span, 1)
  }
}

export function drawCellGlyph(ctx, cp, x, y, cw, ch) {
  const inBlock = cp >= 0x2580 && cp <= 0x259f
  const inBox = cp >= 0x2500 && cp <= 0x257f
  const r = String.fromCodePoint(cp)
  const c = { x, y, w: cw, h: ch, cx: x + div(cw, 2), cy: y + div(ch, 2) }
  if (inBlock) return block(ctx, r, c)
  if (inBox) return boxDrawing(ctx, r, c)
  switch (cp) {
    case 0xe0b0: case 0x25b6: case 0x25b8: case 0x25ba: triangle(ctx, c, true); return true
    case 0xe0b2: case 0x25c0: case 0x25c2: case 0x25c4: triangle(ctx, c, false); return true
  }
  return false
}
