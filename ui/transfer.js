// File transfer UI (#286): send a file or folder for a ticket, or receive a
// ticket into Downloads. The engine is Go suzuri's `suzuri-transfer`, run by
// app/src/transfer.rs. Dropping files on the window starts a send.

import { openPanel, closePanel, isOpen, h } from "./panel.js"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

let active = 0 // generation of the transfer this panel shows
let view = null // { status, bar, ticketRow }

function fmtBytes(n) {
  if (n == null) return ""
  const u = ["B", "KB", "MB", "GB", "TB"]
  let i = 0
  while (n >= 1024 && i < u.length - 1) (n /= 1024), i++
  return `${n.toFixed(i ? 1 : 0)} ${u[i]}`
}

function showProgress(mode) {
  openPanel(mode === "send" ? "Send" : "Receive", (body) => {
    const status = h("div", { class: "rsz-status" }, "Starting…")
    const bar = h("progress", { max: "1", value: "0" })
    bar.hidden = true
    const ticketRow = h("div", { class: "rsz-row" })
    const cancel = h("button", { onclick: closePanel }, "Cancel")
    body.append(status, bar, ticketRow, h("div", { class: "rsz-row end" }, cancel))
    view = { status, bar, ticketRow, cancel }
  }, {
    // Like Go suzuri: closing the transfer panel (Esc, ✕, Cancel) stops a
    // transfer that is still running.
    onClose: () => {
      if (view && !view.finished) invoke("transfer_cancel")
      view = null
    },
  })
}

async function start(mode, value) {
  try {
    showProgress(mode)
    active = await invoke(mode === "send" ? "transfer_send" : "transfer_receive", mode === "send" ? { path: value } : { ticket: value })
  } catch (e) {
    if (view) view.status.textContent = String(e)
  }
}

export async function promptSend(initial = "") {
  const info = await invoke("transfer_info")
  openPanel("Send a file or folder", (body) => {
    const field = h("input", { type: "text", placeholder: "Path to a file or folder", value: initial })
    const go = h("button", { class: "primary", onclick: () => field.value.trim() && start("send", field.value.trim()) }, "Send")
    field.onkeydown = (e) => e.key === "Enter" && go.onclick()
    body.append(field, h("div", { class: "rsz-row end" }, go))
    if (!info.engine) body.append(h("div", { class: "rsz-status err" }, "suzuri-transfer was not found. Install Go suzuri or set SUZURI_TRANSFER_BIN."))
    body.append(h("div", { class: "rsz-hint" }, "Or drop a file on the window."))
  })
}

export async function promptReceive() {
  const info = await invoke("transfer_info")
  openPanel("Receive", (body) => {
    const field = h("input", { type: "text", placeholder: "Paste a ticket" })
    const go = h("button", { class: "primary", onclick: () => field.value.trim() && start("receive", field.value.trim()) }, "Receive")
    field.onkeydown = (e) => e.key === "Enter" && go.onclick()
    body.append(field, h("div", { class: "rsz-row end" }, go), h("div", { class: "rsz-hint" }, `Saves to ${info.receive_dir}`))
    if (!info.engine) body.append(h("div", { class: "rsz-status err" }, "suzuri-transfer was not found. Install Go suzuri or set SUZURI_TRANSFER_BIN."))
  })
}

listen("transfer", ({ payload: u }) => {
  if (u.generation !== active || !view) return
  const verb = u.mode === "send" ? "Sending" : "Receiving"
  let text = u.message || u.phase
  if (u.phase === "ready" && u.ticket) text = "Waiting for the receiver. Share this ticket:"
  else if (u.total) text = `${verb}: ${fmtBytes(u.done)} of ${fmtBytes(u.total)}`
  if (u.finished) text = u.ok ? (u.message || "Done.") : `Failed: ${u.message || u.phase}`
  view.status.textContent = text
  view.status.classList.toggle("err", u.finished && !u.ok)
  if (u.total) {
    view.bar.hidden = false
    view.bar.max = u.total
    view.bar.value = u.done || 0
  }
  if (u.ticket && !view.ticketRow.firstChild) {
    const t = h("input", { type: "text", readonly: "", value: u.ticket, class: "rsz-ticket" })
    const copy = h("button", { class: "primary", onclick: async () => {
      await navigator.clipboard.writeText(u.ticket)
      copy.textContent = "Copied"
    } }, "Copy")
    view.ticketRow.append(t, copy)
  }
  if (u.finished) {
    view.finished = true
    view.cancel.textContent = "Close"
  }
})

// Files dropped on the window: send the first one.
listen("tauri://drag-drop", ({ payload }) => {
  const p = payload?.paths?.[0]
  if (!p) return
  if (isOpen()) closePanel()
  start("send", p)
})
