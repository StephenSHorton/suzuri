// Pane activity: is a pane busy, blocked (waiting on you), idle or dead?
// Pure state, no DOM; ui/activity.js feeds it events and paints the result.
//
// Go suzuri guessed from a braille glyph in the title or any PTY output in
// the last 600 ms (2.5 s on the alt screen). That misfired: typing echo and
// prompt redraws spun the spinner, a title glyph stayed after its program
// exited, an agent's report never cleared, and nothing but a report could
// say "blocked". Here the signals are ranked, each clears when it goes stale,
// and the output guess only runs when nothing better speaks:
//   1. the agent's own report (AI control report_agent);
//   2. a bell or notification while you're not looking at the pane: blocked;
//   3. the program's title (Claude Code: ◐/◑ working, ✳ idle; Grok: braille)
//      or OSC 9;4 progress;
//   4. shell integration: at the prompt is idle, a command running for
//      CMD_RISE_MS is busy, unless it's interactive (it took your keys or
//      went full screen);
//   5. sustained output, with hysteresis so it doesn't flicker.

// The output guess: busy after this much steady output...
export const OUT_RISE_MS = 1000
// ...counting output this close together as one stretch...
export const OUT_GAP_MS = 1500
// ...and idle again after this much quiet.
export const OUT_QUIET_MS = 2500
// Output this soon after a key is the echo, not the program working.
export const ECHO_MS = 400
// A title glyph that changed this recently is animating.
export const SPIN_MS = 2000
// Without shell marks, a title spinner with no title change and no output
// this long is stale.
export const STALE_MS = 30000
// A command is busy only once it has run this long, so ls or cd never
// flash the spinner.
export const CMD_RISE_MS = 700
// "Command finished" only for commands that ran at least this long.
export const LONG_CMD_MS = 10000

const AGENT = ["idle", "working", "blocked", "done"]

// Glyphs programs put at the front of their title as status: braille
// spinners, geometric shapes, block meters, dingbat asterisks (Claude
// Code's ✳) and dots.
export function isStatusGlyph(ch) {
  const c = ch?.codePointAt(0) ?? 0
  return (c >= 0x2800 && c <= 0x28ff) || (c >= 0x25a0 && c <= 0x25ff) || (c >= 0x2580 && c <= 0x259f) ||
    (c >= 0x2722 && c <= 0x273d) || "•·∙…⋯".includes(ch)
}

// Spinner glyphs that mean busy even when they stop animating: braille
// (Grok) and the half circles Claude Code alternates (◐ ◑, every 960 ms,
// and only while its pane has focus, so a background one sits still).
const isSpinner = (ch) => {
  const c = ch?.codePointAt(0) ?? 0
  return (c > 0x2800 && c <= 0x28ff) || (c >= 0x25d0 && c <= 0x25d3)
}

// The title's leading status glyph, or "".
export function titleGlyph(title) {
  const ch = [...String(title || "").trimStart()][0]
  return ch && isStatusGlyph(ch) ? ch : ""
}

// The title without its leading status glyphs, so labels don't jitter with
// spinner frames. ASCII marks only as a badge ("* Grok"), never "xargs".
export function cleanTitle(title) {
  let s = [...String(title || "").trim()]
  for (;;) {
    if (s.length && (s[0] === " " || s[0] === "\t" || isStatusGlyph(s[0]))) s.shift()
    else if (s.length > 1 && "*+-~>".includes(s[0]) && (s[1] === " " || s[1] === "\t")) s.shift()
    else break
  }
  return s.join("").trim()
}

export function newActivity(now = 0) {
  return {
    dead: false,
    agent: "", // the report_agent state, until it's cleared or the prompt is back
    attention: false, // a bell or notification you haven't seen
    glyph: "", // the title's leading status glyph
    glyphAt: 0, // when that glyph last changed from one status glyph to another
    lastTitle: "",
    titleAt: now, // when the title last changed
    progress: 0, // OSC 9;4 state: 1 set, 2 error, 3 indeterminate, 4 paused
    shell: { integrated: false, program: false, prompts: 0 },
    cmdAt: null, // when the running command started (null: none)
    interactive: false, // the running command took keys or the alt screen
    inputAt: -Infinity,
    outAt: -Infinity, // last output, echo included (for staleness)
    runAt: -Infinity, // last output that wasn't echo
    runFrom: -Infinity, // start of the current stretch of output
  }
}

// The prompt is back: whatever the last program said about itself is over.
function promptBack(s) {
  s.agent = ""
  s.glyph = ""
  s.glyphAt = 0
  s.progress = 0
  s.cmdAt = null
  s.interactive = false
}

// Apply one event. `ctx.looking`: you can see the pane (window focused, its
// tab shown, the pane focused). `ctx.alt`: it's on the alt screen. Returns a
// notice to post ({title, body, failed}) or null.
export function step(s, ev, now, ctx = {}) {
  switch (ev.t) {
    case "exit":
      s.dead = true
      break
    case "agent": {
      const st = String(ev.state || "").toLowerCase().trim()
      s.agent = AGENT.includes(st) ? st : ""
      if (s.agent === "working" || s.agent === "blocked") s.attention = false
      break
    }
    case "title": {
      const g = titleGlyph(ev.title)
      if (ev.title !== s.lastTitle) s.titleAt = now
      s.lastTitle = ev.title
      if (g && s.glyph && g !== s.glyph) s.glyphAt = now
      s.glyph = g
      break
    }
    case "progress":
      s.progress = Number(ev.state) || 0
      break
    case "bell":
    case "notice":
      if (!ctx.looking) s.attention = true
      break
    case "seen":
      s.attention = false
      break
    case "input":
      s.inputAt = now
      s.attention = false
      if (s.shell.program) s.interactive = true
      break
    case "output": {
      const at = ev.at
      if (at <= s.outAt) break
      s.outAt = at
      if (at - s.inputAt >= 0 && at - s.inputAt < ECHO_MS) break
      // Once busy, a gap only ends the stretch when it's long enough to
      // read as idle; otherwise a pause would drop the spinner and need a
      // whole new second of output to bring it back.
      const steady = s.runAt - s.runFrom >= OUT_RISE_MS
      if (at - s.runAt > (steady ? OUT_QUIET_MS : OUT_GAP_MS)) s.runFrom = at
      s.runAt = at
      break
    }
    case "shell": {
      const was = s.shell
      const next = { integrated: !!ev.integrated, program: !!ev.program, prompts: ev.prompts | 0 }
      s.shell = next
      if (next.program && !was.program) {
        s.cmdAt = now
        s.interactive = !!ctx.alt
      }
      if ((was.program && !next.program) || next.prompts > was.prompts) promptBack(s)
      break
    }
    case "done": {
      // Comes before the shell's prompt status, so cmdAt is still set.
      const cmd = String(ev.command || "").trim()
      const ran = s.cmdAt != null ? now - s.cmdAt : 0
      if (cmd.startsWith("_suzuri_") || ran < LONG_CMD_MS || ctx.looking) return null
      const failed = ev.exit != null && ev.exit !== 0
      const body = ev.exit == null ? "finished" : failed ? `failed (exit ${ev.exit})` : "succeeded"
      return { title: [...(cmd || "Command")].slice(0, 120).join(""), body, failed }
    }
  }
  if (ctx.alt && s.shell.program) s.interactive = true
  return null
}

// What the title or progress bar says: "busy", "idle" or "".
function programSays(s, now) {
  if (s.progress === 1 || s.progress === 3) return "busy"
  if (!s.glyph) return ""
  if (isSpinner(s.glyph)) {
    // With shell marks the next prompt clears it; without, a spinner that
    // hasn't moved and a pane that hasn't printed for a while is left over.
    const stale = !s.shell.integrated && now - s.titleAt > STALE_MS && now - s.outAt > STALE_MS
    return stale ? "" : "busy"
  }
  // Claude Code's resting (and waiting-on-you) title.
  if (s.glyph === "✳") return "idle"
  // Any other status glyph only while it animates.
  if (s.glyphAt && now - s.glyphAt < SPIN_MS) return "busy"
  return ""
}

// "dead", "blocked", "busy" or "idle".
export function activity(s, now, ctx = {}) {
  if (s.dead) return "dead"
  if (s.agent === "working") return "busy"
  if (s.agent === "blocked" || s.attention) return "blocked"
  if (s.agent) return "idle"
  const said = programSays(s, now)
  if (said) return said
  const sh = s.shell
  if (sh.integrated && !sh.program) return "idle"
  // No cmdAt while program is only the foreground probe lagging behind a
  // prompt that's back (it catches up within 200 ms): no busy from that.
  if (sh.program && s.cmdAt != null && now - s.cmdAt >= CMD_RISE_MS && !s.interactive && !ctx.alt) return "busy"
  const steady = s.runAt - s.runFrom >= OUT_RISE_MS
  return steady && now - s.runAt < OUT_QUIET_MS ? "busy" : "idle"
}

// A tab's mark from its panes': blocked wins, then busy; dead when all are.
export function rollUp(states) {
  if (!states.length) return "idle"
  if (states.includes("blocked")) return "blocked"
  if (states.includes("busy")) return "busy"
  if (states.every((x) => x === "dead")) return "dead"
  return "idle"
}

// Braille "dots" spinner frames, as Go's strip used.
export const SPIN = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]
