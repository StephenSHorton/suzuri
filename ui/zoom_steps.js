// Zoom steps for the bundled Gohu. Gohu is a 14px pixel font traced into
// outlines, so it is only crisp where each of its pixels covers a whole
// number of device pixels: 14, 21, 28, 35 on a 2x screen; 14, 28 on 1x.
// In between, its pixels turn into uneven fractions and blur. So zoom with
// Gohu steps between those sizes instead of by 1px. Other faces scale fine
// and keep 1px steps.

export const GOHU_DESIGN_PX = 14

// The crisp sizes within [min, max] at device pixel ratio dpr. 14 and 28
// (whole multiples of the design size) are always in, so a fractional dpr
// such as 1.5 still has somewhere to go.
export function gohuSizes(dpr, min, max) {
  const d = dpr > 0 ? dpr : 1
  const out = []
  for (let px = min; px <= max; px++) {
    const k = (px * d) / GOHU_DESIGN_PX
    if (Math.abs(k - Math.round(k)) < 1e-6 || px % GOHU_DESIGN_PX === 0) out.push(px)
  }
  return out
}

// The next size from px in the direction of delta (+1 or -1): the nearest
// crisp size strictly beyond px, or px itself at the end of the range. A
// size picked in Settings between crisp ones moves to its neighbour.
export function gohuStep(px, delta, dpr, min, max) {
  const sizes = gohuSizes(dpr, min, max)
  if (delta > 0) return sizes.find((s) => s > px) ?? px
  if (delta < 0) return [...sizes].reverse().find((s) => s < px) ?? px
  return px
}
