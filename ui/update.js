// Update notice: the app checks its release feed and sends "update" when a
// newer build is out. Like Go suzuri, nothing installs until the person
// says so: the offer is a desktop card (rust-toast, app/src/update.rs
// update_offer) with Install and Skip buttons. Install downloads, verifies,
// installs and restarts; a failure is said on a card and offered again.
// The palette's "Check for updates" checks now and says what it found.
import { checkMessage } from "./update_check.js"
import { hostToast } from "./host_toast.js"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

// The version last offered: a skipped offer stays skipped until a newer
// version, or a manual check.
let shown = null

// force: a manual check shows it again after it was skipped.
function offer(payload, force) {
  if (!force && shown === payload.version) return
  shown = payload.version
  invoke("update_offer", { version: payload.version, current: payload.current ?? "" }).catch(() => {})
}

listen("update", ({ payload }) => offer(payload))

let checking = false
async function checkNow() {
  if (checking) return
  checking = true
  // The check's news goes on notice cards (Go's update toasts).
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

window.rsz?.addCommands([
  { id: "check_updates", title: "Check for updates", category: "System", hint: "", run: () => checkNow() },
])
