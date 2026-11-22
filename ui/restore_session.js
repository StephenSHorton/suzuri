// Terminals back after a reboot or a logout (card hearth#916). The core
// saves the tabs and splits with each pane's folder, text and Claude Code
// conversation (app/src/restore.rs); a start with no kept shell to put
// back (ui/kept_layout.js) builds them again from that, a fresh shell in
// each folder under its old text. Nothing is run by itself: a pane that
// was running Claude Code gets `claude --resume <id>` in its input bar,
// for Enter. Pure, so node tests it (test/restore_session.test.mjs).

// What to build: tabs whose leaves are the saved panes ({cwd, text,
// claude, title}), the focused and zoomed one by position, and the tab
// that was in front.
export function savedPlan(session) {
  const tabs = []
  let active = 0
  for (const [ti, t] of (session?.tabs || []).entries()) {
    const ids = []
    const prune = (n) => {
      if (!n || typeof n !== "object") return null
      if (n.id != null) {
        ids.push(n.id)
        const leaf = {
          id: n.id,
          title: typeof n.title === "string" ? n.title : "",
          cwd: typeof n.cwd === "string" ? n.cwd : "",
          text: typeof n.text === "string" ? n.text : "",
          claude: resumeOf(n.claude),
        }
        // The shell it was started with (a Cmd tab's cmd.exe). None in a
        // save from before (or for the login shell): the active profile's.
        const command = commandOf(n.command)
        if (command) leaf.command = command
        return leaf
      }
      const a = prune(n.a), b = prune(n.b)
      if (!a || !b) return a || b
      const out = { dir: n.dir === "v" ? "v" : "h", a, b }
      if (typeof n.ratio === "number") out.ratio = n.ratio
      return out
    }
    const tree = prune(t.tree)
    if (!tree) continue
    if (ti === (session.active || 0)) active = tabs.length
    tabs.push({
      name: typeof t.name === "string" ? t.name : "",
      sticky: ids.length > 1 && typeof t.sticky === "string" ? t.sticky : "",
      tree,
      focused: ids.includes(t.focused) ? t.focused : ids[0],
      zoomed: ids.length > 1 && ids.includes(t.zoomed) ? t.zoomed : 0,
    })
  }
  return { tabs, active: Math.min(active, Math.max(0, tabs.length - 1)), savedAt: Number(session?.saved_at) || 0 }
}

// A saved launch argv: a list of strings, its first not empty.
function commandOf(c) {
  if (!Array.isArray(c) || !c.length || !c.every((x) => typeof x === "string") || !c[0].trim()) return null
  return c
}

// A Claude Code conversation to offer, when its id is one (it goes on a
// command line).
function resumeOf(c) {
  const id = c?.session
  if (typeof id !== "string" || !/^[0-9a-fA-F-]{1,64}$/.test(id)) return null
  return { session: id, cwd: typeof c.cwd === "string" ? c.cwd : "" }
}

// The command offered in the input bar.
export function resumeCommand(leaf) {
  return leaf?.claude ? `claude --resume ${leaf.claude.session}` : ""
}

const DIM = "\x1b[2m", OFF = "\x1b[0m"

// The text shown above the new shell: the old lines (control characters
// taken out, so nothing in them acts on the terminal), then a dim line
// saying they are restored, and the resume offer when there is one.
export function preamble(leaf, savedAt, when = formatWhen) {
  const lines = []
  const old = (leaf?.text || "").replace(/\r\n?/g, "\n").replace(/[\x00-\x08\x0b-\x1f\x7f\x9b]/g, "")
  if (old.trim()) lines.push(...old.split("\n"))
  const at = savedAt ? ` · saved ${when(savedAt)}` : ""
  lines.push(`${DIM}─── restored${at} ───${OFF}`)
  if (leaf?.claude) lines.push(`${DIM}Claude Code was running here. Press Enter to resume the conversation.${OFF}`)
  return lines.join("\r\n") + "\r\n"
}

function formatWhen(ms) {
  try {
    return new Date(ms).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })
  } catch {
    return new Date(ms).toISOString()
  }
}
