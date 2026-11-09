// Eased scrollback scrolling, as Go suzuri's tickSmooth eased it (an
// exponential ease, k = 16): the wheel and PageUp/PageDown add lines to a
// target, and each animation frame sends the whole lines the ease has
// covered since the last one. The core scrolls its history by lines and
// paints whole rows, so the ease is in lines, not pixels. A step of a few
// lines with nothing under way goes at once (a trackpad's own deltas are
// already smooth); a longer one glides in over ~0.3 s.

export const EASE_K = 16
export const AT_ONCE = 3 // lines

// Lines to send now out of `left` still to go, `dt` seconds after the last
// step: the ease's share, rounded toward zero, and at least one line.
export function easeLines(left, dt, k = EASE_K) {
  if (!left) return 0
  const t = Math.min(0.05, Math.max(1 / 240, dt || 0))
  const n = Math.trunc(left * (1 - Math.exp(-k * t)))
  return n || Math.sign(left)
}

// env: {raf(fn) -> id, cancel(id), now() -> ms, apply(lines)}; lines > 0
// go up into the history, as host.scroll's delta.
export class SmoothScroll {
  constructor(env) {
    this.env = env
    this.left = 0
    this.raf = 0
    this.last = 0
  }

  by(lines) {
    if (!lines) return
    // A turn of direction drops what was left the other way.
    if (Math.sign(lines) !== Math.sign(this.left)) this.stop()
    if (!this.left && Math.abs(lines) <= AT_ONCE) return this.env.apply(lines)
    this.left += lines
    if (this.raf) return
    this.last = this.env.now() - 1000 / 60
    this.raf = this.env.raf(() => this.step())
  }

  step() {
    this.raf = 0
    if (!this.left) return
    const t = this.env.now()
    const n = easeLines(this.left, (t - this.last) / 1000)
    this.last = t
    this.left -= n
    this.env.apply(n)
    if (this.left) this.raf = this.env.raf(() => this.step())
  }

  // The history ran out that way (offset 0 going down, the top going up):
  // what is left would scroll nothing.
  clamp(offset, history) {
    if ((this.left < 0 && offset <= 0) || (this.left > 0 && offset >= history)) this.stop()
  }

  stop() {
    this.left = 0
    if (this.raf) this.env.cancel(this.raf)
    this.raf = 0
  }
}
