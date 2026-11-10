// The text beside the cup icon in the bar for a caffeine Status (Go's
// renderCaffeineChip): the cup is always there, dim when off, bright with
// the time left ("∞", "14m", "40s") when on. A click toggles.
export function chipText(s) {
  return s?.active && s.label ? s.label : ""
}

export function chipTitle(s) {
  if (!s?.active) return "Caffeine off: the computer may sleep (click to keep it awake)"
  const left = s.label === "∞" ? "" : ` for ${s.label}`
  return `Caffeine on${left}: the computer stays awake (click to turn off)`
}

// The toast for a caffeine change (Go applyCaffeineAction, caffeineTick).
// action: "toggle", "on" (with minutes), "off", or null for a status the
// app sent by itself; prev and next: the Status before and after (prev may
// carry `ends`, the ms clock time a timed hold was due to end). "" for
// nothing to say (launch, the once-a-second label refresh).
export function caffeineToast(action, prev, next, minutes, now = Date.now()) {
  if (next?.error && (action || next.error !== prev?.error)) return `caffeine failed: ${next.error}`
  if (!next) return ""
  switch (action) {
    case "toggle":
      return next.active ? "caffeine on · sleep prevented" : "caffeine off"
    case "on":
      if (!minutes) return "caffeine on · until toggled off"
      return minutes >= 60 && minutes % 60 === 0 ? `caffeine on · ${minutes / 60}h` : `caffeine on · ${minutes}m`
    case "off":
      return prev?.active ? "caffeine off" : "caffeine already off"
  }
  // A timed hold running out: it went off within seconds of its end.
  const left = prev?.ends != null ? (prev.ends - now) / 1000 : prev?.remaining_s
  if (prev?.active && left != null && left <= 2 && !next.active) return "caffeine off · timer ended"
  return ""
}
