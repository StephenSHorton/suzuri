// Update notice: the app checks its release feed and sends "update" when a
// newer build is out. Like Go suzuri, nothing installs until the person
// clicks Install; the app then downloads, verifies, installs and restarts.
// The offer is an Astryx Toast that stays until dismissed (chrome/Toasts.jsx);
// the palette's "Check for updates" checks now and says what it found.
import { createElement as h } from "react"
import { Button } from "@astryxdesign/core/Button"
import { checkMessage } from "./update_check.js"
import { hostToast } from "./host_toast.js"
import { showToast } from "./chrome/Toasts.jsx"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

let shown = null
// The update on offer and where its install is: "offer", "installing" or
// "failed" (with the error).
let offered = null
let state = "offer"
let failure = ""

// One toast, updated in place (same uniqueID) as the state changes.
function render() {
  if (!offered) return
  const body = state === "failed"
    ? `Update failed: ${failure}`
    : `suzuri ${offered.version} is available (you have ${offered.current}).`
  showToast({
    uniqueID: "update",
    collisionBehavior: "overwrite",
    isAutoHide: false,
    body,
    endContent: h(Button, {
      label: state === "installing" ? "Installing…" : state === "failed" ? "Retry" : "Install and restart",
      variant: "primary",
      size: "sm",
      isLoading: state === "installing",
      onClick: install,
    }),
  })
}

// force: a manual check shows it again after it was dismissed.
function offer(payload, force) {
  if (!force && shown === payload.version) return
  shown = payload.version
  offered = payload
  state = "offer"
  render()
}

listen("update", ({ payload }) => offer(payload))

let checking = false
async function checkNow() {
  if (checking) return
  checking = true
  // The check's news goes on notice cards (Go's update toasts); the toast
  // here is for the Install button.
  hostToast("Checking for updates…")
  let found = null, error = null, current = ""
  try {
    current = await window.__TAURI__.app.getVersion()
  } catch {}
  try {
    found = await invoke("update_check")
  } catch (e) {
    error = e
  }
  checking = false
  const m = checkMessage({ found, current, error })
  if (m.offer) offer(found, true)
  else hostToast(m.text)
}

async function install() {
  if (state === "installing") return
  state = "installing"
  render()
  try {
    await invoke("update_install")
  } catch (e) {
    state = "failed"
    failure = String(e)
    render()
    // The failed update was dropped; fetch it again for the retry.
    invoke("update_check").catch(() => {})
  }
}

window.rsz?.addCommands([
  { id: "check_updates", title: "Check for updates", category: "System", hint: "", run: () => checkNow() },
])
