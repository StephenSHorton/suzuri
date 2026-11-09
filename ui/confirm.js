// A yes/no box in the palette's card style (Go suzuri's confirm modal,
// chrome/model.go: "Quit suzuri?" on closing the last tab). While it is up it
// takes every key: Enter (or the close chord again) says yes, Esc says no.

// A keydown to "yes", "no" or null (ignored).
export function confirmKey(e, isMac) {
  // A held key (⌘W closing pane after pane) must not answer the box.
  if (e.repeat) return null
  const k = (e.key || "").toLowerCase()
  if (k === "escape") return "no"
  if (k === "enter") return "yes"
  // ⌘W / ⌘⇧W on macOS, Ctrl+Shift+W on Windows: the chord that asked.
  if (k === "w" && (isMac ? e.metaKey : e.ctrlKey && e.shiftKey)) return "yes"
  return null
}

let openBox = null

export const isOpen = () => !!openBox

// Resolves true for yes, false for no. A second ask while one is up gets
// the first one's answer.
export function confirm({ title, body, yes = "OK", no = "Cancel" }) {
  if (openBox) return openBox.answer
  const isMac = navigator.platform.startsWith("Mac")
  const wrap = document.createElement("div")
  wrap.className = "overlay rsz-confirm"
  wrap.innerHTML = `<div class="card" role="alertdialog"><b></b><p></p><div class="row"><button class="no"></button><button class="yes"></button></div></div>`
  wrap.querySelector("b").textContent = title
  wrap.querySelector("p").textContent = body
  wrap.querySelector(".no").textContent = no
  wrap.querySelector(".yes").textContent = yes
  let done
  const answer = new Promise((r) => (done = r))
  const finish = (v) => {
    window.removeEventListener("keydown", onKey, true)
    wrap.remove()
    openBox = null
    done(v)
  }
  const onKey = (e) => {
    e.preventDefault()
    e.stopImmediatePropagation()
    const a = confirmKey(e, isMac)
    if (a) finish(a === "yes")
  }
  window.addEventListener("keydown", onKey, true)
  wrap.addEventListener("mousedown", (e) => {
    e.stopPropagation()
    if (e.target === wrap) finish(false)
  })
  wrap.querySelector(".no").onclick = () => finish(false)
  wrap.querySelector(".yes").onclick = () => finish(true)
  ;(document.getElementById("overlays") || document.body).append(wrap)
  openBox = { answer }
  return answer
}
