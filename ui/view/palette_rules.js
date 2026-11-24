// The command palette's rules, with no React: which commands a query keeps,
// and a command's hint as keyboard chords. The palette itself is
// suzuri-view/palette (react/Palette.jsx); node's tests import this file.

// Go suzuri's filter (internal/chrome/palette.go): a plain substring match
// over title, hint and category, every word somewhere ("move 2" finds
// "Move pane to tab: 2 …").
export function filterCommands(items, query) {
  const q = query.trim().toLowerCase()
  if (!q) return items.slice()
  const words = q.split(/\s+/)
  return items.filter((it) => {
    const hay = `${it.title} ${it.hint || ""} ${it.category || ""}`.toLowerCase()
    return words.every((w) => hay.includes(w))
  })
}

const MAC_MODS = { "⌘": "meta", "⌥": "alt", "⇧": "shift", "⌃": "ctrl" }
const NAMED = { "←": "left", "→": "right", "↑": "up", "↓": "down", "+": "plus", enter: "enter", tab: "tab", esc: "escape", escape: "escape" }
const keyName = (k) => NAMED[k.toLowerCase()] || k

// Not Astryx: Kbd takes key strings but nothing parses suzuri's
// platform-specific chord labels (shortcuts.js chordLabels), so this does.
// One chord label ("Ctrl+Shift+T", "Ctrl++", "⌘⇧W", "F2") as Kbd keys
// ("ctrl+shift+T", "ctrl+plus", "meta+shift+W", "F2"); null when it is not
// a chord (a profile's shell, "Stay awake 15m").
function chordKeys(label) {
  let s = label.trim()
  const mods = []
  while (s.length > 1 && MAC_MODS[s[0]]) {
    mods.push(MAC_MODS[s[0]])
    s = s.slice(1)
  }
  if (!mods.length) {
    const m = /^((?:(?:ctrl|alt|shift|win|cmd)\+)+)(.+)$/i.exec(s)
    if (m) {
      for (const mod of m[1].split("+").filter(Boolean)) {
        const l = mod.toLowerCase()
        mods.push(l === "cmd" || l === "win" ? "meta" : l)
      }
      s = m[2]
    }
  }
  if (!s || /\s/.test(s)) return null
  if (!mods.length && !/^F\d{1,2}$/.test(s)) return null
  if ([...s].length > 5 && !NAMED[s.toLowerCase()]) return null
  return [...mods, keyName(s)].join("+")
}

// A command's hint as Kbd chords, one per alternative ("Ctrl+Shift+] ·
// Ctrl+Tab", "⌘⇧M / ⌃⇧M"); null when any part is not a chord, so the hint
// shows as plain text.
export function hintChords(hint) {
  if (!hint) return null
  const chords = hint.split(/\s+[·/]\s+/).map(chordKeys)
  return chords.every(Boolean) ? chords : null
}

