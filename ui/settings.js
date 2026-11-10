// Settings (⌘, / Ctrl+,): Go suzuri's Look and Session pages
// (internal/chrome/settings.go) as Astryx form controls (chrome/Settings.jsx).
// Every change is saved at once (config_set) and previewed live; Cancel or
// Esc puts back what was there when the screen opened, as Go's Esc does.
//
// After each save the window gets a `rsz:config` event with the saved
// settings, so other parts (themes and fonts, #284) can apply them.

import { createElement as h } from "react"
import { flushSync } from "react-dom"
import { hostToast } from "./host_toast.js"
import { mountOverlay, unmountOverlay } from "./chrome/overlays.js"
import { SettingsView } from "./chrome/Settings.jsx"

const { invoke } = window.__TAURI__.core

export function announce(cfg) {
  window.dispatchEvent(new CustomEvent("rsz:config", { detail: cfg }))
}

export class Settings {
  // host is the old #overlays element; the screen now renders through the
  // React overlay host, so it is kept only for app.js's call.
  constructor(_host, onClose) {
    this.onClose = onClose
    this.open_ = false
    this.page = "look"
    this.status = ""
    this.selectLast = false
  }

  get isOpen() { return this.open_ }

  async open() {
    const [cfg, meta] = await Promise.all([invoke("config_get"), invoke("config_meta")])
    this.meta = meta
    this.snap = structuredClone(cfg)
    this.cfg = cfg
    this.status = ""
    this.selectLast = false
    this.open_ = true
    this.render()
  }

  // keep=false restores the settings from when the screen opened.
  async done(keep) {
    if (!this.open_) return
    this.open_ = false
    // Unmount now, so the dialog has let go of focus before the terminal takes it.
    flushSync(() => unmountOverlay("settings"))
    if (keep) {
      this.onClose?.()
      // Go toasts each save; ours save as they change, so say it once on Done,
      // after a save still in flight (a profile row saves on blur) has landed.
      await this.saving
      if (changed(this.snap, this.cfg)) hostToast("settings saved")
      return
    }
    // Let a save in flight land first, so it can't overwrite the restore.
    await this.saving
    if (JSON.stringify(this.snap) !== JSON.stringify(this.cfg)) {
      // The window frame saves itself meanwhile; don't put back a stale one.
      const { window: _frame, ...snap } = this.snap
      this.cfg = await invoke("config_set", { patch: snap })
      announce(this.cfg)
    }
    this.onClose?.()
  }

  set(patch) {
    this.saving = this.save(patch)
    return this.saving
  }

  async save(patch) {
    try {
      this.cfg = await invoke("config_set", { patch })
      this.status = ""
      announce(this.cfg)
    } catch (err) {
      this.status = `Couldn't save: ${err}`
      hostToast("save failed")
    }
    this.render()
  }

  async addProfile() {
    let n = this.cfg.profiles.length + 1
    while (this.cfg.profiles.some((p) => p.name.toLowerCase() === `profile ${n}`)) n++
    await this.set({ profiles: [...this.cfg.profiles, { name: `Profile ${n}` }] })
    this.selectLast = true
    this.render()
    this.selectLast = false
  }

  render() {
    if (!this.open_) return
    mountOverlay("settings", h(SettingsView, {
      cfg: this.cfg,
      meta: this.meta,
      page: this.page,
      status: this.status,
      selectLast: this.selectLast,
      onPage: (page) => { this.page = page; this.render() },
      onSet: (patch) => this.set(patch),
      onProfiles: (profiles) => this.set({ profiles }),
      onAddProfile: () => this.addProfile(),
      onDone: (keep) => this.done(keep),
    }))
  }
}

// Settings that differ, leaving out the window frame (it saves itself).
function changed(a, b) {
  const { window: _a, ...x } = a || {}
  const { window: _b, ...y } = b || {}
  return JSON.stringify(x) !== JSON.stringify(y)
}
