// Command palette (⌘K / Ctrl+K): Go suzuri's filter-and-run list
// (internal/chrome/palette.go). A plain substring match over title, chord
// and category; Up/Down to move, Enter to run, Esc to close.

export function filterCommands(items, query) {
  const q = query.trim().toLowerCase()
  if (!q) return items.slice()
  // Every word must appear somewhere ("move 2" finds "Move pane to tab: 2 …").
  const words = q.split(/\s+/)
  return items.filter((it) => {
    const hay = `${it.title} ${it.hint || ""} ${it.category || ""}`.toLowerCase()
    return words.every((w) => hay.includes(w))
  })
}

export class Palette {
  constructor(host, onClose) {
    this.onClose = onClose
    this.el = document.createElement("div")
    this.el.className = "overlay palette"
    this.el.hidden = true
    this.el.innerHTML = `<div class="card"><input class="filter" placeholder="Type a command…" spellcheck="false" autocomplete="off"><div class="list" role="listbox"></div></div>`
    host.appendChild(this.el)
    this.filter = this.el.querySelector(".filter")
    this.list = this.el.querySelector(".list")
    this.items = []
    this.view = []
    this.index = 0
    this.filter.addEventListener("input", () => { this.index = 0; this.refresh() })
    this.filter.addEventListener("keydown", (e) => this.onKey(e))
    this.el.addEventListener("mousedown", (e) => {
      if (e.target === this.el) { e.preventDefault(); this.close() }
    })
  }

  get isOpen() { return !this.el.hidden }

  open(items, run) {
    this.items = items
    this.run = run
    this.filter.value = ""
    this.index = 0
    this.el.hidden = false
    this.refresh()
    this.filter.focus()
  }

  close() {
    if (this.el.hidden) return
    this.el.hidden = true
    this.onClose?.()
  }

  onKey(e) {
    e.stopPropagation()
    if (e.isComposing) return
    const n = this.view.length
    switch (e.key) {
      case "Escape": e.preventDefault(); return this.close()
      case "ArrowDown": e.preventDefault(); if (n) this.index = (this.index + 1) % n; return this.paint()
      case "ArrowUp": e.preventDefault(); if (n) this.index = (this.index - 1 + n) % n; return this.paint()
      case "PageDown": e.preventDefault(); this.index = Math.min(n - 1, this.index + 10); return this.paint()
      case "PageUp": e.preventDefault(); this.index = Math.max(0, this.index - 10); return this.paint()
      case "Enter": e.preventDefault(); return this.choose(this.view[this.index])
    }
    // ⌘K / Ctrl+K again closes it, like a toggle.
    if ((e.metaKey || e.ctrlKey) && (e.key === "k" || e.key === "p")) { e.preventDefault(); this.close() }
  }

  choose(it) {
    if (!it) return
    this.close()
    this.run(it)
  }

  refresh() {
    this.view = filterCommands(this.items, this.filter.value)
    if (this.index >= this.view.length) this.index = Math.max(0, this.view.length - 1)
    this.paint()
  }

  paint() {
    const frag = document.createDocumentFragment()
    if (!this.view.length) {
      const d = document.createElement("div")
      d.className = "empty"
      d.textContent = "No matches"
      frag.appendChild(d)
    }
    this.view.forEach((it, i) => {
      const row = document.createElement("div")
      row.className = "row" + (i === this.index ? " sel" : "")
      row.setAttribute("role", "option")
      row.innerHTML = `<span class="title"></span><span class="cat"></span><span class="hint"></span>`
      row.querySelector(".title").textContent = it.title
      row.querySelector(".cat").textContent = it.category || ""
      row.querySelector(".hint").textContent = it.hint || ""
      row.addEventListener("mousemove", () => { if (this.index !== i) { this.index = i; this.paint() } })
      row.addEventListener("mousedown", (e) => { e.preventDefault(); this.choose(it) })
      frag.appendChild(row)
    })
    this.list.replaceChildren(frag)
    this.list.querySelector(".sel")?.scrollIntoView({ block: "nearest" })
  }
}
