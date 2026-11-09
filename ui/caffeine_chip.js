// What the ☕ button in the bar shows for a caffeine Status (Go's
// renderCaffeineChip): always there, dim when off, bright with the time left
// ("∞", "14m", "40s") when on. A click toggles.
export function chipText(s) {
  return s?.active && s.label ? `☕ ${s.label}` : "☕"
}

export function chipTitle(s) {
  if (!s?.active) return "Caffeine off: the computer may sleep (click to keep it awake)"
  const left = s.label === "∞" ? "" : ` for ${s.label}`
  return `Caffeine on${left}: the computer stays awake (click to turn off)`
}
