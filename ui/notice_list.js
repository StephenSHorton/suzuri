// The bell's labels and history rows (pure, for ui/bell.js and its tests).
// Go: chrome renderBellChip and renderNoticeList.

// "🔔", or "🔔 3" with unread notices; Go caps the number at 9.
export function bellLabel(unread) {
  const n = Number(unread) || 0
  if (n <= 0) return ""
  return n > 9 ? "9+" : String(n)
}

// Unix ms as local "15:04".
export function noticeTime(ms) {
  const d = new Date(ms)
  const pad = (n) => String(n).padStart(2, "0")
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// "title — body", or just the title.
export function noticeText(item) {
  const title = item.title || ""
  return item.body ? `${title} — ${item.body}` : title
}

// Up/Down/Home/End over n rows, wrapping like the palette.
export function moveIndex(i, key, n) {
  if (!n) return 0
  switch (key) {
    case "ArrowDown": return (i + 1) % n
    case "ArrowUp": return (i - 1 + n) % n
    case "Home": return 0
    case "End": return n - 1
  }
  return i
}
