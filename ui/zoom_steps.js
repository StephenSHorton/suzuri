// Zoom steps for the bundled Gohu. Gohu is a 14px pixel font traced into
// outlines, so it is only crisp where each of its pixels covers a whole
// number of device pixels: 14, 21, 28, 35 on a 2x screen; 14, 28 on 1x.
// In between, its pixels turn into uneven fractions and blur. So zoom with
// Gohu steps between those sizes instead of by 1px. Other faces scale fine
// and keep 1px steps.

export const GOHU_DESIGN_PX = 14

// The crisp sizes within [min, max] at device pixel ratio dpr, or null when
// the ratio is fractional (Windows at 125% or 150%): there Gohu is soft at
// every size, even 14, so there is nothing to step between.
export function gohuSizes(dpr, min, max) {
  const d = dpr > 0 ? dpr : 1
  if (Math.abs(d - Math.round(d)) > 1e-6) return null
  const out = []
  for (let px = min; px <= max; px++) {
    if (((px * d) % GOHU_DESIGN_PX) === 0) out.push(px)
  }
  return out
}

// The next size from px in the direction of delta (+1 or -1): the nearest
// crisp size strictly beyond px, or px itself at the end of the range. A
// size picked in Settings between crisp ones moves to its neighbour. With
// a fractional ratio it is the plain 1px step.
export function gohuStep(px, delta, dpr, min, max) {
  const sizes = gohuSizes(dpr, min, max)
  if (!sizes) return px + delta
  if (delta > 0) return sizes.find((s) => s > px) ?? px
  if (delta < 0) return [...sizes].reverse().find((s) => s < px) ?? px
  return px
}
