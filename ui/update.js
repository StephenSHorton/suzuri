// Update notice: the app checks its release feed and sends "update" when a
// newer build is out. Like Go suzuri, nothing installs until the person
// clicks Install; the app then downloads, verifies, installs and restarts.
// The palette's "Check for updates" checks now and says what it found.
import { checkMessage } from "./update_check.js"

const { invoke } = window.__TAURI__.core
const { listen } = window.__TAURI__.event

const box = document.createElement("div")
box.id = "update"
box.hidden = true
box.innerHTML = `<span class="msg"></span><button class="go">Install and restart</button><button class="later" title="Hide">✕</button>`
document.body.append(box)
const msg = box.querySelector(".msg")
const go = box.querySelector(".go")

let shown = null
let hideTimer = 0

// force: a manual check shows it again after ✕.
function offer(payload, force) {
  if (!force && shown === payload.version) return
  shown = payload.version
  clearTimeout(hideTimer)
  msg.textContent = `suzuri ${payload.version} is available (you have ${payload.current}).`
  go.hidden = false
  go.disabled = false
  go.textContent = "Install and restart"
  box.hidden = false
}

// A message with no Install button; ms > 0 hides it after that long.
function say(text, ms) {
  clearTimeout(hideTimer)
  msg.textContent = text
  go.hidden = true
  box.hidden = false
  if (ms) hideTimer = setTimeout(() => (box.hidden = true), ms)
}

listen("update", ({ payload }) => offer(payload))

let checking = false
async function checkNow() {
  if (checking) return
  checking = true
  say("Checking for updates…")
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
  else say(m.text, m.ms)
}

go.addEventListener("click", async () => {
  go.disabled = true
  go.textContent = "Installing…"
  try {
    await invoke("update_install")
  } catch (e) {
    msg.textContent = `Update failed: ${e}`
    go.textContent = "Retry"
    go.disabled = false
    // The failed update was dropped; fetch it again for the retry.
    invoke("update_check").catch(() => {})
  }
})
box.querySelector(".later").addEventListener("click", () => { clearTimeout(hideTimer); box.hidden = true })

window.rsz?.addCommands([
  { id: "check_updates", title: "Check for updates", category: "System", hint: "", run: () => checkNow() },
])
