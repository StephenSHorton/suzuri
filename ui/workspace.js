// Workspace panel (#286): channels, history and compose for the shared
// human/agent room. Reads and writes go through Go suzuri's store via
// `suzuri mcp` (app/src/workspace.rs), the same path agents use.

import { openPanel, h } from "./panel.js"

const { invoke } = window.__TAURI__.core
const call = (tool, args = {}) => invoke("workspace_call", { tool, args })

let state = null // { channel, me, lastId, timer, view }

function fmtTime(ts) {
  const d = new Date(ts)
  return isNaN(d) ? "" : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
}

// Reuse the local human's existing member (Go suzuri joined it before);
// join only when there is none, so reopening never adds "name-2".
async function whoAmI() {
  const me = await invoke("workspace_me")
  const r = await call("workspace_members")
  const humans = (r.members || []).filter((m) => m.kind === "human")
  const mine = humans.find((m) => m.name === me.name) || humans.find((m) => m.name === me.short)
  if (mine) return { id: mine.id, name: me.name }
  const j = await call("workspace_join", { name: me.name, kind: "human", session_id: me.session_id })
  return { id: j.member_id || j.member?.id, name: me.name }
}

function msgRow(m) {
  const who = h("span", { class: "ws-from" + (m.from_kind === "agent" ? " agent" : "") }, m.from_name || m.from_id || "?")
  const body = m.kind === "file" && m.file ? `📎 ${m.file.name}` : m.body
  return h("div", { class: "ws-msg" + (m.kind === "system" ? " sys" : "") }, h("span", { class: "ws-ts" }, fmtTime(m.ts)), " ", who, " ", h("span", { class: "ws-body" }, body))
}

async function loadHistory(fresh) {
  const s = state
  if (!s) return
  const args = { channel: s.channel, limit: 120 }
  if (!fresh && s.lastId) args.since_id = s.lastId
  let r
  try {
    r = await call("workspace_history", args)
  } catch (e) {
    s.view.status.textContent = String(e)
    return
  }
  if (state !== s) return
  if (fresh) s.view.log.replaceChildren()
  const msgs = r.messages || []
  if (!msgs.length) return
  const atEnd = s.view.log.scrollHeight - s.view.log.scrollTop - s.view.log.clientHeight < 40
  for (const m of msgs) s.view.log.append(msgRow(m))
  s.lastId = msgs[msgs.length - 1].id
  if (fresh || atEnd) s.view.log.scrollTop = s.view.log.scrollHeight
}

async function loadSide() {
  const s = state
  const [ch, mem] = await Promise.all([call("workspace_channels"), call("workspace_members")])
  if (state !== s) return
  s.view.channels.replaceChildren(
    ...(ch.channels || []).map((c) =>
      h("button", { class: "ws-chan" + (c.id === s.channel ? " on" : ""), title: c.topic || "", onclick: () => switchChannel(c.id) }, "#" + c.name),
    ),
  )
  s.view.members.replaceChildren(
    ...(mem.members || [])
      .filter((m) => !m.stale)
      .map((m) => h("div", { class: "ws-member" }, `${m.kind === "agent" ? "◆" : "●"} ${m.name}`, h("span", { class: "ws-ts" }, ` ${m.status || "idle"}`))),
  )
}

function switchChannel(id) {
  state.channel = id
  state.lastId = null
  loadSide()
  loadHistory(true)
}

async function post() {
  const s = state
  const text = s.view.compose.value.trim()
  if (!text) return
  s.view.compose.value = ""
  try {
    const r = await call("workspace_post", { channel: s.channel, body: text, member_id: s.me.id, name: s.me.name, kind: "human" })
    if (r && r.ok === false) throw r.error || "post failed"
    s.view.status.textContent = ""
  } catch (e) {
    s.view.status.textContent = String(e)
    s.view.compose.value = text
  }
  loadHistory(false)
}

export function openWorkspace() {
  openPanel(
    "Workspace",
    async (body) => {
      const channels = h("div", { class: "ws-channels" })
      const members = h("div", { class: "ws-members" })
      const log = h("div", { class: "ws-log" })
      const compose = h("textarea", { rows: "2", placeholder: "Message (Enter to send, Shift+Enter for a new line)" })
      const status = h("div", { class: "rsz-status err" })
      compose.onkeydown = (e) => {
        if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
          e.preventDefault()
          post()
        }
      }
      body.append(h("div", { class: "ws-grid" }, h("div", { class: "ws-side" }, channels, members), h("div", { class: "ws-main" }, log, compose, status)))
      state = { channel: "general", me: null, lastId: null, view: { channels, members, log, compose, status } }
      const s = state
      try {
        s.me = await whoAmI()
        await loadSide()
        await loadHistory(true)
      } catch (e) {
        status.textContent = String(e)
      }
      // Agents post through their own `suzuri mcp`; poll for their messages.
      s.timer = setInterval(() => {
        loadHistory(false)
      }, 1500)
      s.sideTimer = setInterval(loadSide, 10000)
    },
    {
      wide: true,
      onClose: () => {
        if (state) clearInterval(state.timer), clearInterval(state.sideTimer)
        state = null
      },
    },
  )
}
