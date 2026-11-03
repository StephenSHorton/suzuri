// Settings (⌘, / Ctrl+,): Go suzuri's Look and Session pages
// (internal/chrome/settings.go) as real form controls. Every change is
// saved at once (config_set) and previewed live; Cancel or Esc puts back
// what was there when the screen opened, as Go's Esc does.
//
// After each save the window gets a `rsz:config` event with the saved
// settings, so other parts (themes and fonts, #284) can apply them.

const { invoke } = window.__TAURI__.core

const LABELS = {
  themes: {
    inkstone: "Inkstone", charmtone: "Charmtone", high_contrast: "High contrast", nord: "Nord",
    dracula: "Dracula", tokyo_night: "Tokyo Night", catppuccin: "Catppuccin", gruvbox: "Gruvbox",
    one_dark: "One Dark", solarized: "Solarized", rose_pine: "Rosé Pine", kanagawa: "Kanagawa",
    monokai: "Monokai", forest: "Forest", ocean: "Ocean", amber: "Amber",
  },
  cursors: { block: "Block", underline: "Underline", bar: "Bar" },
  ansi_maps: { none: "Off (stock ANSI colours)", soft: "Soft (blend into the theme)", full: "Full (theme palette)" },
}

export function announce(cfg) {
  window.dispatchEvent(new CustomEvent("rsz:config", { detail: cfg }))
}

export class Settings {
  constructor(host, onClose) {
    this.onClose = onClose
    this.el = document.createElement("div")
    this.el.className = "overlay settings"
    this.el.hidden = true
    host.appendChild(this.el)
    this.page = "look"
    this.row = 0 // the highlighted row, Go's settings field
    this.el.addEventListener("keydown", (e) => this.onKey(e))
    this.el.addEventListener("mousedown", (e) => {
      if (e.target === this.el) { e.preventDefault(); this.done(true) }
    })
  }

  get isOpen() { return !this.el.hidden }

  async open() {
    const [cfg, meta] = await Promise.all([invoke("config_get"), invoke("config_meta")])
    this.meta = meta
    this.snap = structuredClone(cfg)
    this.cfg = cfg
    this.el.hidden = false
    this.row = 0
    this.render()
  }

  // keep=false restores the settings from when the screen opened.
  async done(keep) {
    if (this.el.hidden) return
    this.el.hidden = true
    if (!keep && JSON.stringify(this.snap) !== JSON.stringify(this.cfg)) {
      this.cfg = await invoke("config_set", { patch: this.snap })
      announce(this.cfg)
    }
    this.onClose?.()
  }

  // Go's keys: Up/Down pick a field, Left/Right change it, Tab/Shift+Tab
  // switch page, Enter keeps, Esc cancels. Typing in a profile box is left
  // alone (but Esc still cancels).
  onKey(e) {
    e.stopPropagation()
    const typing = e.target.tagName === "INPUT" && e.target.type !== "range"
    if (e.key === "Escape") { e.preventDefault(); return this.done(false) }
    if ((e.metaKey || e.ctrlKey) && e.key === ",") { e.preventDefault(); return this.done(true) }
    if (typing) return
    const rows = this.rows()
    switch (e.key) {
      case "Enter":
        if (e.target.tagName === "BUTTON") return
        e.preventDefault()
        return this.done(true)
      case "Tab":
        e.preventDefault()
        this.page = this.page === "look" ? "session" : "look"
        this.row = 0
        return this.render()
      case "ArrowUp":
      case "ArrowDown":
        e.preventDefault()
        this.row = (this.row + (e.key === "ArrowUp" ? -1 : 1) + rows.length) % rows.length
        return this.mark()
      case "ArrowLeft":
      case "ArrowRight":
        e.preventDefault()
        return this.nudge(rows[this.row], e.key === "ArrowLeft" ? -1 : 1)
    }
  }

  rows() {
    return [...this.el.querySelectorAll(".page > label")]
  }

  mark() {
    this.rows().forEach((r, i) => r.classList.toggle("cur", i === this.row))
    const ctl = this.rows()[this.row]?.querySelector("select, input")
    ctl?.focus({ preventScroll: false })
  }

  nudge(row, d) {
    const ctl = row?.querySelector("select, input[type=range]")
    if (!ctl) return
    if (ctl.tagName === "SELECT") {
      const n = ctl.options.length
      ctl.selectedIndex = (ctl.selectedIndex + d + n) % n
    } else {
      ctl.value = String(Number(ctl.value) + d)
      ctl.dispatchEvent(new Event("input"))
    }
    ctl.dispatchEvent(new Event("change"))
  }

  async set(patch) {
    try {
      this.cfg = await invoke("config_set", { patch })
      announce(this.cfg)
    } catch (err) {
      this.status(`Couldn't save: ${err}`)
    }
  }

  status(text) {
    const s = this.el.querySelector(".status")
    if (s) s.textContent = text
  }

  render() {
    const m = this.meta, c = this.cfg
    const opt = (ids, labels, v) => ids.map((id) => `<option value="${id}"${id === v ? " selected" : ""}>${esc(labels?.[id] || id)}</option>`).join("")
    const fonts = Array.from(new Set([c.font_face, ...(m.fonts || [])]))
    const look = `
      <label><span>Font</span><select data-k="font_face">${opt(fonts, null, c.font_face)}</select></label>
      <label><span>Size</span><span class="size"><input type="range" data-k="font_size_px" min="${m.font_min}" max="${m.font_max}" value="${c.font_size_px}"><b>${c.font_size_px}px</b></span></label>
      <label><span>Cursor</span><select data-k="cursor">${opt(m.cursors, LABELS.cursors, c.cursor)}</select></label>
      <label><span>Theme</span><select data-k="theme">${opt(m.themes, LABELS.themes, c.theme)}</select></label>
      <label><span>ANSI map</span><select data-k="shell_ansi_map">${opt(m.ansi_maps, LABELS.ansi_maps, c.shell_ansi_map)}</select></label>`
    const names = c.profiles.map((p) => p.name)
    const rows = c.profiles.map((p, i) => `
      <div class="prow" data-i="${i}">
        <input data-f="name" value="${esc(p.name)}" placeholder="Name" spellcheck="false">
        <input data-f="shell" value="${esc(p.shell || "")}" placeholder="Shell (empty: your login shell)" spellcheck="false">
        <input data-f="cwd" value="${esc(p.cwd || "")}" placeholder="Folder (empty: home)" spellcheck="false">
        <button class="del" title="Remove profile"${c.profiles.length < 2 ? " disabled" : ""}>×</button>
      </div>`).join("")
    const session = `
      <label><span>New tabs use</span><select data-k="active_profile">${opt(names, null, c.active_profile)}</select></label>
      <div class="profiles"><div class="phead">Profiles <small>shell and folder for "New tab: …" in the palette</small></div>${rows}
        <button class="add">Add profile</button></div>`
    this.el.innerHTML = `
      <div class="card" role="dialog" aria-label="Settings">
        <div class="top"><b>Settings</b>
          <nav>${["look", "session"].map((p) => `<button data-page="${p}" class="${p === this.page ? "on" : ""}">${p === "look" ? "Look" : "Session"}</button>`).join("")}</nav>
        </div>
        <div class="page">${this.page === "look" ? look : session}</div>
        <div class="foot"><span class="keys">↑↓ field · ←→ change · Tab page · Enter done · Esc cancel</span><span class="status" title="${esc(m.path)}">Saved to ${esc(shortPath(m.path))}</span>
          <button class="cancel">Cancel</button><button class="ok">Done</button></div>
      </div>`
    this.wire()
    this.row = Math.min(this.row, Math.max(0, this.rows().length - 1))
    this.mark()
  }

  wire() {
    const q = (s) => this.el.querySelectorAll(s)
    for (const b of q("nav button")) b.onclick = () => { this.page = b.dataset.page; this.row = 0; this.render() }
    this.rows().forEach((r, i) => r.addEventListener("mousedown", () => { this.row = i; this.rows().forEach((x, j) => x.classList.toggle("cur", j === i)) }))
    this.el.querySelector(".cancel").onclick = () => this.done(false)
    this.el.querySelector(".ok").onclick = () => this.done(true)
    for (const s of q("select[data-k]")) s.onchange = () => this.set({ [s.dataset.k]: s.value })
    const range = this.el.querySelector('input[type=range]')
    if (range) {
      range.oninput = () => { range.nextElementSibling.textContent = `${range.value}px` }
      range.onchange = () => this.set({ font_size_px: Number(range.value) })
    }
    const saveProfiles = async () => {
      const ps = [...q(".prow")].map((r) => {
        const p = { ...this.cfg.profiles[Number(r.dataset.i)] }
        for (const f of r.querySelectorAll("input")) {
          const v = f.value.trim()
          if (v || f.dataset.f === "name") p[f.dataset.f] = v
          else delete p[f.dataset.f]
        }
        return p
      })
      await this.set({ profiles: ps })
      this.render()
    }
    for (const r of q(".prow input")) r.onchange = saveProfiles
    for (const b of q(".prow .del")) b.onclick = async () => {
      const i = Number(b.closest(".prow").dataset.i)
      await this.set({ profiles: this.cfg.profiles.filter((_, j) => j !== i) })
      this.render()
    }
    const add = this.el.querySelector(".add")
    if (add) add.onclick = async () => {
      let n = this.cfg.profiles.length + 1
      while (this.cfg.profiles.some((p) => p.name.toLowerCase() === `profile ${n}`)) n++
      await this.set({ profiles: [...this.cfg.profiles, { name: `Profile ${n}` }] })
      this.render()
      this.el.querySelector(".prow:last-of-type input")?.select()
    }
  }
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"]/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[ch])
}

// The settings path with the home folder as ~, for the footer.
function shortPath(p) {
  return String(p).replace(/^\/Users\/[^/]+|^\/home\/[^/]+|^[A-Za-z]:\\Users\\[^\\]+/, "~")
}
