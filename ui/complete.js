// Tab completion and ghost text for the input bar (#295). A port of Go
// suzuri's internal/ui/complete.go: Tab completes the token at the caret from
// the file system or the whole line from history, Tab / Shift+Tab walk the
// matches, and the ghost text after the caret (zsh-autosuggestions style)
// shows the newest history line the buffer starts, else the first path
// match. Right-arrow at the end of the line accepts it.
//
// The webview can't read directories, so path matches come from `fs`:
//   fs.sep          "/" or "\\": the separator a token without one gets
//   fs.list(dir)    the entries [{ name, dir }] of the token's directory part
//                   `dir` ("" = the shell's cwd, "~/x", "/abs", "C:\\x",
//                   "sub/"), null when it can't be read, or undefined while
//                   it is being fetched (DirCache below, over complete_list
//                   in app/src/complete.rs)
// Everything here is pure and synchronous, so `node --test` covers it.

const SEPS = new Set([" ", "\t", "\n", "\r", "|", "&", ";", "<", ">"])
export const isTokenSep = (c) => SEPS.has(c)
const isSpace = (c) => c === " " || c === "\t" || c === "\n" || c === "\r"
const lower = (s) => s.toLowerCase()
const MAX_HISTORY_MATCHES = 40

// A pending lookup: complete() changes nothing and names the directory.
export const PENDING = "pending"

export function lineStartAt(runes, cursor) {
  let i = Math.min(cursor, runes.length)
  while (i > 0 && runes[i - 1] !== "\n") i--
  return i
}

// [start, end) of the token holding the caret, its text up to the caret, and
// whether it is the line's first word.
export function tokenAtCursor(runes, cursor) {
  cursor = Math.max(0, Math.min(cursor, runes.length))
  const ls = lineStartAt(runes, cursor)
  let start = cursor
  while (start > ls && !isTokenSep(runes[start - 1])) start--
  let end = cursor
  while (end < runes.length && runes[end] !== "\n" && !isTokenSep(runes[end])) end++
  const prefix = runes.slice(start, cursor).join("")
  let firstWord = true
  for (let i = ls; i < start; i++) {
    if (!isSpace(runes[i])) {
      firstWord = false
      break
    }
  }
  return { start, end, prefix, firstWord }
}

// The part of match after cur, when match starts with cur (ignoring case).
export function ghostRemainder(cur, match) {
  if (cur === match || !match) return ""
  const cr = [...cur]
  const mr = [...match]
  if (mr.length <= cr.length) return ""
  if (lower(mr.slice(0, cr.length).join("")) !== lower(cur)) return ""
  return mr.slice(cr.length).join("")
}

const trimEOL = (s) => s.replace(/[\r\n]+$/, "")

// History lines that start with linePrefix (ignoring case), newest first,
// without repeats or the line itself; at most 40.
export function historyCompletions(history, linePrefix) {
  linePrefix = trimEOL(linePrefix)
  if (linePrefix.trim() === "") return []
  const pl = lower(linePrefix)
  const seen = new Set()
  const out = []
  for (let i = history.length - 1; i >= 0; i--) {
    const h = history[i]
    if (h === linePrefix || !lower(h).startsWith(pl) || seen.has(lower(h))) continue
    seen.add(lower(h))
    out.push(h)
    if (out.length >= MAX_HISTORY_MATCHES) break
  }
  return out
}

// The untyped rest of the newest history line that starts with linePrefix.
export function historyGhostRemainder(history, linePrefix) {
  linePrefix = trimEOL(linePrefix)
  if (linePrefix.trim() === "") return ""
  const pl = lower(linePrefix)
  for (let i = history.length - 1; i >= 0; i--) {
    const h = history[i]
    if (h === linePrefix || !lower(h).startsWith(pl)) continue
    return ghostRemainder(linePrefix, h)
  }
  return ""
}

export function tokenLooksPath(token) {
  if (!token) return false
  if (token.startsWith("~") || token.startsWith(".")) return true
  if (/[/\\]/.test(token)) return true
  return /^\p{L}:/u.test(token)
}

const lastSlash = (s) => Math.max(s.lastIndexOf("/"), s.lastIndexOf("\\"))
const endsWithSlash = (s) => s.endsWith("/") || s.endsWith("\\")
const isWindowsAbs = (s) => /^\p{L}:/u.test(s) || s.startsWith("\\\\")
const isAbs = (s) => s.startsWith("/") || isWindowsAbs(s)

// Splits a path token: dirPart (kept as typed, to rebuild the token), base
// (the name filter), dir (the directory to list, for fs.list) and sep (the
// separator the token uses, else the platform's).
export function splitPathToken(token, defaultSep) {
  let sep = defaultSep
  if (token.includes("/") && !token.includes("\\")) sep = "/"
  else if (token.includes("\\")) sep = "\\"
  const raw = token.startsWith('"') ? token.slice(1) : token

  if (raw === "~") return { dirPart: "~" + sep, base: "", dir: "~", sep }
  if (raw.startsWith("~/") || raw.startsWith("~\\")) {
    const rest = raw.slice(2)
    if (endsWithSlash(raw)) return { dirPart: raw, base: "", dir: raw, sep }
    const slash = lastSlash(rest)
    if (slash < 0) return { dirPart: "~" + sep, base: rest, dir: "~", sep }
    const sub = rest.slice(0, slash)
    const dirPart = sep === "/" ? "~/" + sub.replaceAll("\\", "/") + "/" : "~\\" + sub.replaceAll("/", "\\") + "\\"
    return { dirPart, base: rest.slice(slash + 1), dir: "~/" + sub.replaceAll("\\", "/"), sep }
  }

  if (isAbs(raw)) {
    if (endsWithSlash(raw)) return { dirPart: raw, base: "", dir: raw, sep }
    const slash = lastSlash(raw)
    // "C:" alone: list the drive.
    if (slash < 0) return { dirPart: "", base: raw, dir: raw + "\\", sep }
    return { dirPart: raw.slice(0, slash + 1), base: raw.slice(slash + 1), dir: raw.slice(0, slash + 1), sep }
  }

  if (raw === "") return { dirPart: "", base: "", dir: "", sep }
  if (endsWithSlash(raw)) return { dirPart: raw, base: "", dir: raw, sep }
  const slash = lastSlash(raw)
  if (slash < 0) return { dirPart: "", base: raw, dir: "", sep }
  return { dirPart: raw.slice(0, slash + 1), base: raw.slice(slash + 1), dir: raw.slice(0, slash + 1), sep }
}

function joinToken(dirPart, name, sep) {
  if (!dirPart) return name
  return endsWithSlash(dirPart) ? dirPart + name : dirPart + sep + name
}

// Token replacements from the token's directory: hidden entries only when
// the name typed starts with ".", directories with the separator after
// them, quoted when they hold a space or tab, sorted ignoring case. null
// while fs is still fetching the directory.
export function pathCompletions(fs, token) {
  const { dirPart, base, dir, sep } = splitPathToken(token, fs.sep)
  const entries = fs.list(dir)
  if (entries === undefined) return null
  if (!entries) return []
  const bl = lower(base)
  const names = []
  for (const e of entries) {
    if (e.name.startsWith(".") && !base.startsWith(".")) continue
    if (base && !lower(e.name).startsWith(bl)) continue
    let full = joinToken(dirPart, e.dir ? e.name + sep : e.name, sep)
    if (/[ \t]/.test(full)) full = `"${full}"`
    names.push(full)
  }
  return names.sort((a, b) => (lower(a) < lower(b) ? -1 : lower(a) > lower(b) ? 1 : 0))
}

// What Tab completes: { matches, kindPath }, or null while a directory is
// being fetched. History (the whole line) and paths (the token), in Go
// suzuri's order.
export function collectCompletions(fs, history, linePrefix, token, firstWord) {
  const pathLike = tokenLooksPath(token)
  const wantPath = pathLike || !firstWord
  const typed = linePrefix.trim() !== ""
  const paths = () => pathCompletions(fs, token)

  if (typed) {
    const hm = historyCompletions(history, linePrefix)
    if (hm.length) {
      if (pathLike) {
        const pm = paths()
        if (pm === null) return null
        if (pm.length) return { matches: pm, kindPath: true }
      }
      if (firstWord || !pathLike) return { matches: hm, kindPath: false }
    }
  }
  if (wantPath || firstWord) {
    const pm = paths()
    if (pm === null) return null
    if (pm.length) return { matches: pm, kindPath: true }
  }
  if (typed) {
    const hm = historyCompletions(history, linePrefix)
    if (hm.length) return { matches: hm, kindPath: false }
  }
  return { matches: [], kindPath: false }
}

// ---- the bar's Tab cycle. bar is an InputBar (bar.js); its edits clear
// bar.comp.

function currentToken(bar) {
  const { tokenStart: start, kindPath } = bar.comp
  if (start < 0 || start > bar.runes.length) return ""
  let end = start
  while (end < bar.runes.length && (kindPath ? !isTokenSep(bar.runes[end]) : bar.runes[end] !== "\n")) end++
  return bar.runes.slice(start, end).join("")
}

function inSession(bar) {
  const c = bar.comp
  if (!c || !c.matches.length || c.idx < 0 || c.idx >= c.matches.length) return false
  if (c.tokenStart < 0 || c.tokenStart > bar.runes.length) return false
  return currentToken(bar) === c.matches[c.idx]
}

function applyMatch(bar) {
  const { tokenStart: start, kindPath, matches, idx } = bar.comp
  let end = start
  while (end < bar.runes.length && (kindPath ? !isTokenSep(bar.runes[end]) : bar.runes[end] !== "\n")) end++
  const mr = [...matches[idx]]
  bar.runes.splice(start, end - start, ...mr)
  bar.cursor = start + mr.length
  return true
}

// Tab (reverse: Shift+Tab). True when the buffer changed, false when there
// was nothing to complete, PENDING when fs is fetching a directory (nothing
// changed; try again once it has).
export function complete(bar, fs, reverse = false) {
  bar.leaveHistory()
  bar.clamp()
  if (inSession(bar)) {
    const n = bar.comp.matches.length
    bar.comp.idx = (bar.comp.idx + (reverse ? n - 1 : 1)) % n
    return applyMatch(bar)
  }
  const { start, prefix, firstWord } = tokenAtCursor(bar.runes, bar.cursor)
  const linePrefix = bar.runes.slice(lineStartAt(bar.runes, bar.cursor), bar.cursor).join("")
  const got = collectCompletions(fs, bar.history, linePrefix, prefix, firstWord)
  if (got === null) return PENDING
  const { matches, kindPath } = got
  if (!matches.length) {
    bar.comp = null
    return false
  }
  // History completes the line from its start, a path the token. (Go
  // suzuri started both at the token, so Tab on "git sta" gave "git git
  // status"; the ghost already worked on the whole line.)
  const tokenStart = kindPath ? start : lineStartAt(bar.runes, bar.cursor)
  bar.comp = { tokenStart, matches, kindPath, idx: reverse && matches.length > 1 ? matches.length - 1 : 0 }
  applyMatch(bar)
  // One match: no cycle, so the next Tab looks again (into a directory).
  if (matches.length === 1) bar.comp = null
  return true
}

// The ghost text after the caret, or "". Only at the end of a non-empty
// buffer that isn't browsing history. While Tab-cycling it previews the next
// match; else the newest history line the line starts; else the first path
// match for the token (none for an empty first word).
export function ghostSuffix(bar, fs) {
  if (!bar.runes.length || bar.cursor !== bar.runes.length || bar.histIdx >= 0) return ""
  if (inSession(bar) && bar.comp.matches.length > 1) {
    const m = bar.comp.matches[(bar.comp.idx + 1) % bar.comp.matches.length]
    return ghostRemainder(currentToken(bar), m)
  }
  const linePrefix = bar.runes.slice(lineStartAt(bar.runes, bar.cursor), bar.cursor).join("")
  const rem = historyGhostRemainder(bar.history, linePrefix)
  if (rem) return rem
  const { start, prefix, firstWord } = tokenAtCursor(bar.runes, bar.cursor)
  if (prefix === "" && firstWord) return ""
  const got = collectCompletions(fs, bar.history, linePrefix, prefix, firstWord)
  if (!got || !got.matches.length) return ""
  const cur = got.kindPath ? bar.runes.slice(start, bar.cursor).join("") : linePrefix
  return ghostRemainder(cur, got.matches[0])
}

// Right-arrow at the end of the line: take the ghost text. True when the
// buffer changed (else the caller moves right).
export function acceptGhost(bar, fs) {
  const g = ghostSuffix(bar, fs)
  if (!g) return false
  bar.comp = null
  bar.leaveHistory()
  bar.runes.push(...g)
  bar.cursor = bar.runes.length
  return true
}

// A cache of directory listings for fs.list, per cwd: a listing is good
// for ttl ms, and a stale one is still served while it is fetched again.
// fetch(dir, cwd) returns a promise of entries (or null for a directory
// that can't be read); onLoad() runs when one arrives (repaint the ghost).
// Set cwd to the shell's before each use.
export class DirCache {
  constructor({ sep, fetch, onLoad = () => {}, ttl = 2000, now = () => Date.now(), max = 64 }) {
    this.sep = sep
    this.fetch = fetch
    this.onLoad = onLoad
    this.ttl = ttl
    this.now = now
    this.max = max
    this.cwd = ""
    this.map = new Map() // cwd \0 dir -> { at, entries, pending }
  }

  list(dir) {
    const key = this.cwd + "\0" + dir
    const e = this.map.get(key)
    if (!e || (!e.pending && this.now() - e.at > this.ttl)) this.load(dir)
    const cur = this.map.get(key)
    return cur && "entries" in cur ? cur.entries : undefined
  }

  // Resolves when every fetch in flight has landed.
  settled() {
    return Promise.all([...this.map.values()].map((e) => e.pending).filter(Boolean))
  }

  // Fetches dir (relative to cwd) unless a fetch is in flight; resolves when
  // it is in the cache.
  load(dir) {
    const cwd = this.cwd
    const key = cwd + "\0" + dir
    const e = this.map.get(key)
    if (e && e.pending) return e.pending
    const pending = Promise.resolve()
      .then(() => this.fetch(dir, cwd))
      .catch(() => null)
      .then((entries) => {
        this.map.set(key, { at: this.now(), entries: entries || null })
        if (this.map.size > this.max) this.map.delete(this.map.keys().next().value)
        this.onLoad()
      })
    this.map.set(key, { ...(e || {}), pending })
    return pending
  }
}
