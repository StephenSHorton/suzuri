// Settings (⌘, / Ctrl+,): Go suzuri's Look and Session pages
// (internal/chrome/settings.go) as Astryx form controls. The window itself
// is suzuri-view's (view/react/Settings.jsx), the same one Hearth's terminal
// mounts; this module wires it to Tauri (config_get, config_meta,
// config_set) and keeps the API app.js uses. Every change is saved at once
// and previewed live; Cancel or Esc puts back what was there when the
// window opened, as Go's Esc does.
//
// After each save the window gets a `rsz:config` event with the saved
// settings, so other parts (themes and fonts, #284) can apply them.

import { createElement as h } from "react"
import { flushSync } from "react-dom"
import { hostToast } from "./host_toast.js"
import { mountOverlay, unmountOverlay } from "./chrome/overlays.js"
import { SettingsWindow } from "./view/react/Settings.jsx"
import { Swatch } from "./chrome/Swatch.jsx"

const { invoke } = window.__TAURI__.core

export function announce(cfg) {
  window.dispatchEvent(new CustomEvent("rsz:config", { detail: cfg }))
}

// Settings another program changed (another window, Hearth, a hand edit):
// the shared file's watch (app/src/config.rs) sends them, and every part
// applies them as after a save here.
window.__TAURI__.event.listen("rsz-config", ({ payload }) => announce(payload))

const themeIcon = (id) => h(Swatch, { id })

export class Settings {
  // host is the old #overlays element; the window now renders through the
  // React overlay host, so it is kept only for app.js's call.
  constructor(_host, onClose) {
    this.onClose = onClose
    this.open_ = false
    this.page = "look"
    this.serial = 0
    // An open window shows changes made elsewhere too (it keeps them on Cancel).
    window.addEventListener("rsz:config", (e) => {
      if (!this.open_ || e.detail === this.cfg) return
      this.cfg = e.detail
      this.render()
    })
  }

  get isOpen() { return this.open_ }

  async open() {
    const [cfg, meta] = await Promise.all([invoke("config_get"), invoke("config_meta")])
    this.meta = meta
    this.cfg = cfg
    this.open_ = true
    this.serial++
    this.render()
  }

  // Saves a patch; the window shows a failure in its footer.
  async save(patch) {
    try {
      this.cfg = await invoke("config_set", { patch })
    } catch (err) {
      hostToast("save failed")
      throw err
    }
    announce(this.cfg)
    return this.cfg
  }

  closed(keep, finished) {
    if (!this.open_) return
    this.open_ = false
    // Unmount now, so the dialog has let go of focus before the terminal takes it.
    flushSync(() => unmountOverlay("settings"))
    if (keep) this.onClose?.()
    finished.then(({ changed }) => {
      // Go toasts each save; ours save as they change, so say it once on
      // Done, after a save still in flight (a profile row saves on blur).
      if (keep && changed) hostToast("settings saved")
      if (!keep) this.onClose?.()
    })
  }

  render() {
    if (!this.open_) return
    mountOverlay("settings", h(SettingsWindow, {
      // A fresh window each open, so its snapshot is this opening's.
      key: this.serial,
      settings: this.cfg,
      meta: this.meta,
      save: (patch) => this.save(patch),
      onClose: (keep, finished) => this.closed(keep, finished),
      themeIcon,
      initialPage: this.page,
      onPageChange: (page) => { this.page = page },
    }))
  }
}
