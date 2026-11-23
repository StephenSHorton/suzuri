// An open Settings screen and a change made elsewhere (another window,
// Hearth, a hand edit): Esc puts back what was there when the screen opened,
// so each key the outside change set becomes part of that, and Esc undoes
// only what was changed on this screen. Returns the new snapshot.
export function foldOutside(snap, cfg, outside) {
  const next = { ...snap }
  for (const k of Object.keys(outside)) {
    if (JSON.stringify(outside[k]) !== JSON.stringify(cfg[k])) next[k] = structuredClone(outside[k])
  }
  return next
}
