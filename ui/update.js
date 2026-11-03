// Update notice: the app checks its release feed and sends "update" when a
// newer build is out. Like Go suzuri, nothing installs until the person
// clicks Install; the app then downloads, verifies, installs and restarts.
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
listen("update", ({ payload }) => {
  if (shown === payload.version) return
  shown = payload.version
  msg.textContent = `suzuri ${payload.version} is available (you have ${payload.current}).`
  go.disabled = false
  go.textContent = "Install and restart"
  box.hidden = false
})

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
box.querySelector(".later").addEventListener("click", () => (box.hidden = true))
