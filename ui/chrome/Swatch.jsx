// A theme's swatch beside its name in Settings' Theme list (suzuri-view's
// Settings window takes it as themeIcon): its body colour in light and dark
// mode, then its accent in each (chrome/themes.js SWATCHES). Not Astryx: it
// has no colour-swatch component; the dots are spans sized and rounded with
// its tokens (chrome.css .rsz-swatch).
import { SWATCHES } from "./themes.js"

export function Swatch({ id }) {
  return (
    <span className="rsz-swatch" aria-hidden="true">
      {(SWATCHES[id] || []).map((c, i) => <span key={i} style={{ background: c }} />)}
    </span>
  )
}
