// suzuri-view/settings: suzuri's Settings window (Ctrl+, / Cmd+,), drawn with
// Astryx: a Dialog with a TabList for the Look and Session pages, a
// horizontal-labels FormLayout of Selectors, a Slider, a SegmentedControl and
// a Switch, and the profile rows as TextInputs.
//
// No Tauri: the host gives the settings, the choices (rsz-settings' meta())
// and a save callback, and mounts it inside an Astryx <Theme>. Every change
// is saved at once; Cancel or Esc puts back what was there when it opened.
// A host can show only some parts (`parts`, e.g. TERMINAL_PARTS).
import { useEffect, useRef, useState } from "react"
import { Dialog, DialogHeader } from "@astryxdesign/core/Dialog"
import { Layout, LayoutContent, LayoutFooter, HStack, VStack, StackItem } from "@astryxdesign/core/Layout"
import { TabList, Tab } from "@astryxdesign/core/TabList"
import { FormLayout } from "@astryxdesign/core/FormLayout"
import { Field } from "@astryxdesign/core/Field"
import { Selector, SelectorOption } from "@astryxdesign/core/Selector"
import { Slider } from "@astryxdesign/core/Slider"
import { SegmentedControl, SegmentedControlItem } from "@astryxdesign/core/SegmentedControl"
import { Switch } from "@astryxdesign/core/Switch"
import { TextInput } from "@astryxdesign/core/TextInput"
import { Button } from "@astryxdesign/core/Button"
import { IconButton } from "@astryxdesign/core/IconButton"
import { Icon } from "@astryxdesign/core/Icon"
import { Text } from "@astryxdesign/core/Text"
import { Kbd } from "@astryxdesign/core/Kbd"
import { Divider } from "@astryxdesign/core/Divider"
import { Tooltip } from "@astryxdesign/core/Tooltip"
import { Plus, Trash2 } from "lucide-react"
import {
  ALL_PARTS, TERMINAL_PARTS, LOOK_PARTS, SESSION_PARTS, PART_KEYS,
  pagesFor, foldOutside, restorePatch, changed, newProfileName, shortPath,
} from "../settings_rules.js"

export { ALL_PARTS, TERMINAL_PARTS, LOOK_PARTS, SESSION_PARTS, PART_KEYS, pagesFor, foldOutside, restorePatch, changed, newProfileName, shortPath }

export const LABELS = {
  cursors: { block: "Block", underline: "Underline", bar: "Bar" },
  // One line each, from Astryx's own theme list (docs/astryx/topics/use-a-theme.md).
  theme_notes: {
    neutral: "Restrained grays, minimal and quiet",
    stone: "Warm stone and slate",
    matcha: "Earthy greens",
    chocolate: "Warm browns and cozy beige",
    butter: "Golden yellows with blue accents",
    gothic: "Deep blue-grays, dark only",
    y2k: "Periwinkle, pinks and lime",
  },
}

const opts = (ids, labels) => (ids || []).map((id) => ({ value: id, label: labels?.[id] || id }))

// props (settings.d.ts has them typed):
//   settings      the settings now; a new object here while open is shown,
//                 and what it changed, other than this window's own saves
//                 coming back, is kept by Cancel
//   meta          the choices and limits (rsz-settings' meta())
//   save(patch)   saves; resolves to the saved settings (or nothing: the
//                 window then merges the patch itself); a throw shows in the footer
//   onClose(kept, finished)  the window is done: unmount it. finished
//                 resolves once any save in flight (and Cancel's restore)
//                 has landed, to { changed, settings, error? }; it never
//                 rejects (error: Cancel's restore failed)
//   parts         the parts to show (default ALL_PARTS)
//   width         the dialog's width in px (default 600)
//   themeIcon(id) an icon beside each theme's name (suzuri: its swatch)
//   initialPage, onPageChange(page)   the page shown ("look" or "session")
export function SettingsWindow({
  settings, meta, save, onClose, parts = ALL_PARTS, width = 600,
  themeIcon, initialPage, onPageChange,
}) {
  const pages = pagesFor(parts)
  const [page, setPageState] = useState(() =>
    pages.some((p) => p.id === initialPage) ? initialPage : pages[0]?.id)
  const setPage = (p) => { setPageState(p); onPageChange?.(p) }

  const [cfg, setCfg] = useState(settings)
  const [status, setStatus] = useState("")
  const [selectLast, setSelectLast] = useState(false)
  // Refs for what the async saves read: the latest settings, the snapshot
  // Cancel puts back, the save in flight, and whether the window is done.
  const cur = useRef(settings)
  const snap = useRef(null)
  if (snap.current === null) snap.current = structuredClone(settings)
  const saving = useRef(Promise.resolve())
  const closed = useRef(false)
  const seen = useRef(settings)
  // The last value saved from here for each key (foldOutside).
  const asked = useRef({})

  const apply = (next) => {
    cur.current = next
    if (!closed.current) setCfg(next)
  }

  // A change made elsewhere while open: show it, and keep it on Cancel.
  useEffect(() => {
    if (settings === seen.current) return
    seen.current = settings
    if (closed.current || settings === cur.current) return
    snap.current = foldOutside(snap.current, cur.current, settings, asked.current)
    apply(settings)
  }, [settings])

  useEffect(() => { if (selectLast) setSelectLast(false) }, [selectLast])

  const set = (patch) => {
    Object.assign(asked.current, structuredClone(patch))
    const p = (async () => {
      try {
        const next = await save(patch)
        apply(next ?? { ...cur.current, ...patch })
        if (!closed.current) setStatus("")
      } catch (err) {
        if (!closed.current) setStatus(`Couldn't save: ${err}`)
      }
    })()
    saving.current = p
    return p
  }

  const addProfile = async () => {
    const profiles = cur.current.profiles || []
    await set({ profiles: [...profiles, { name: newProfileName(profiles) }] })
    if (!closed.current) setSelectLast(true)
  }

  // keep=false puts back the settings from when the window opened.
  const done = (keep) => {
    if (closed.current) return
    closed.current = true
    const finished = (async () => {
      // Let a save in flight land first, so it can't overwrite the restore.
      await saving.current
      if (keep) return { changed: changed(snap.current, cur.current), settings: cur.current }
      const patch = restorePatch(snap.current, cur.current)
      if (!patch) return { changed: false, settings: cur.current }
      Object.assign(asked.current, structuredClone(patch))
      try {
        const next = (await save(patch)) ?? { ...cur.current, ...patch }
        cur.current = next
        return { changed: true, settings: next }
      } catch (error) {
        // The restore failed: the settings stay as this window left them.
        return { changed: true, settings: cur.current, error }
      }
    })()
    onClose?.(keep, finished)
  }

  // Keys stay in the window: none reach the terminal or the app's shortcuts.
  // Escape is left to bubble, because Astryx's layer stack answers it on the
  // document (closing an open Selector first, else the dialog: cancel).
  // Plain Tab bubbles too: the Dialog's focus trap listens on the document.
  const pathRef = useRef(null)
  const onKeyDown = (e) => {
    if (e.key === "Escape" || (e.key === "Tab" && !e.ctrlKey)) return
    e.stopPropagation()
    if ((e.metaKey || e.ctrlKey) && e.key === ",") {
      e.preventDefault()
      // Blur first, so a profile row being edited saves before the window
      // unmounts (removing a focused input fires no blur).
      e.target?.blur?.()
      return done(true)
    }
    // Ctrl+Tab switches page (old suzuri used plain Tab, which now moves focus).
    if (e.ctrlKey && e.key === "Tab") {
      e.preventDefault()
      const i = pages.findIndex((p) => p.id === page)
      return setPage(pages[(i + 1) % pages.length].id)
    }
    if (e.key === "Enter" && !e.defaultPrevented && keepsEnter(e.target) === false) {
      e.preventDefault()
      done(true)
    }
  }

  const shown = pages.find((p) => p.id === page) || pages[0]
  const pageProps = {
    cfg, meta, show: new Set(shown?.parts || []), themeIcon, selectLast,
    onSet: set,
    onProfiles: (profiles) => set({ profiles }),
    onAddProfile: addProfile,
  }
  const path = meta?.path ? String(meta.path) : ""

  return (
    <Dialog
      isOpen
      onOpenChange={(open) => { if (!open) done(false) }}
      purpose="form"
      width={width}
      maxHeight="85dvh"
      onKeyDown={onKeyDown}
    >
      <Layout
        header={
          <VStack gap={2}>
            <DialogHeader title="Settings" hasDivider={false} />
            {pages.length > 1 && (
              <TabList value={page} onChange={setPage} role="tablist" aria-label="Settings pages" hasDivider isFullBleed>
                {pages.map((p) => <Tab key={p.id} value={p.id} label={p.label} panelId={`rsz-settings-${p.id}`} />)}
              </TabList>
            )}
          </VStack>
        }
        content={
          <LayoutContent>
            {/* Not Astryx: a plain role="tabpanel" div, because TabList's tabs
                pattern asks the caller to supply the panel element (Tab.md). */}
            <div id={`rsz-settings-${shown?.id}`} role="tabpanel">
              {shown?.id === "look" ? <LookPage {...pageProps} /> : shown ? <SessionPage {...pageProps} /> : null}
            </div>
          </LayoutContent>
        }
        footer={
          <LayoutFooter hasDivider>
            <HStack gap={3} vAlign="center" wrap="wrap">
              <StackItem size="fill">
                <VStack gap={1}>
                  <HStack gap={1.5} vAlign="center" wrap="wrap">
                    {pages.length > 1 && <><Kbd keys="ctrl+tab" /><Text type="supporting">page</Text></>}
                    <Kbd keys="enter" /><Text type="supporting">done</Text>
                    <Kbd keys="esc" /><Text type="supporting">cancel</Text>
                  </HStack>
                  {(status || path) && (
                    <>
                      {/* Not Astryx: a span to anchor the Tooltip, since Text takes no ref. */}
                      <span ref={pathRef}>
                        <Text type="supporting" maxLines={1}>{status || `Saved to ${shortPath(path)}`}</Text>
                      </span>
                      <Tooltip content={path} anchorRef={pathRef} isEnabled={!status && !!path} />
                    </>
                  )}
                </VStack>
              </StackItem>
              <HStack gap={2}>
                <Button label="Cancel" variant="secondary" onClick={() => done(false)} />
                <Button label="Done" variant="primary" onClick={() => done(true)} />
              </HStack>
            </HStack>
          </LayoutFooter>
        }
      />
    </Dialog>
  )
}

function LookPage({ cfg, meta, show, onSet, themeIcon }) {
  const fonts = Array.from(new Set([cfg.font_face, ...(meta.fonts || [])].filter(Boolean)))
  // The slider shows the size while dragging and saves when it lets go.
  const [size, setSize] = useState(cfg.font_size_px)
  useEffect(() => setSize(cfg.font_size_px), [cfg.font_size_px])
  const saveSize = (v) => { if (v !== cfg.font_size_px) onSet({ font_size_px: v }) }
  const themes = (meta.ui_themes || []).map((id) => ({
    value: id,
    label: meta.ui_theme_labels?.[id] || id,
    description: LABELS.theme_notes[id],
    icon: themeIcon?.(id),
  }))
  return (
    <FormLayout direction="horizontal-labels">
      {show.has("theme") && (
        <Selector label="Theme" description="Astryx's look for the whole window: the chrome, the terminal's colours and the notice cards."
          options={themes} value={cfg.ui_theme} onChange={(v) => onSet({ ui_theme: v })}
          renderValue={(o) => <SelectorOption label={o.label} icon={o.icon} layout="inline" />} />
      )}
      {show.has("mode") && (
        <FieldRow label="Mode">
          <SegmentedControl label="Mode" value={cfg.ui_mode} onChange={(v) => onSet({ ui_mode: v })}>
            {(meta.ui_modes || []).map((id) => <SegmentedControlItem key={id} value={id} label={meta.ui_mode_labels?.[id] || id} />)}
          </SegmentedControl>
        </FieldRow>
      )}
      {show.has("font") && (
        <Selector label="Font" options={opts(fonts)} value={cfg.font_face} hasSearch={fonts.length > 8}
          onChange={(v) => onSet({ font_face: v })} />
      )}
      {show.has("size") && (
        <Slider label="Size" value={size} min={meta.font_min} max={meta.font_max} step={1}
          valueDisplay="text" formatValue={(v) => `${v}px`}
          onChange={setSize} onChangeEnd={saveSize} />
      )}
      {show.has("text_size") && (
        <Selector label="Text size" description="The size of the text around the terminal: tabs, menus, settings and notes. Size above sets the terminal's."
          options={opts(meta.text_sizes, meta.text_size_labels)} value={cfg.ui_text_size} onChange={(v) => onSet({ ui_text_size: v })} />
      )}
      {show.has("cursor") && (
        <FieldRow label="Cursor">
          <SegmentedControl label="Cursor" value={cfg.cursor} onChange={(v) => onSet({ cursor: v })}>
            {(meta.cursors || []).map((id) => <SegmentedControlItem key={id} value={id} label={LABELS.cursors[id] || id} />)}
          </SegmentedControl>
        </FieldRow>
      )}
    </FormLayout>
  )
}

function SessionPage({ cfg, meta, show, onSet, onProfiles, onAddProfile, selectLast }) {
  const profiles = cfg.profiles || []
  const names = profiles.map((p) => p.name)
  const saveRow = (i, p) => onProfiles(profiles.map((q, j) => (j === i ? p : q)))
  const form = ["new_tab_profile", "notice", "animate_unfocused", "restore_sessions"].some((p) => show.has(p))
  return (
    <VStack gap={4}>
      {form && (
        <FormLayout direction="horizontal-labels">
          {show.has("new_tab_profile") && (
            <Selector label="New tabs use" options={opts(names)} value={cfg.active_profile} onChange={(v) => onSet({ active_profile: v })} />
          )}
          {show.has("notice") && (
            <Selector label="Notice" description="The screen corner notice cards stack in."
              options={opts(meta.notice_positions, meta.notice_labels)} value={cfg.notice_position} onChange={(v) => onSet({ notice_position: v })} />
          )}
          {show.has("animate_unfocused") && (
            <FieldRow label="Animate unfocused" description="Off: spinners rest while another app is in front.">
              <Switch label="Animate unfocused" isLabelHidden
                value={cfg.animate_unfocused !== false} onChange={(on) => onSet({ animate_unfocused: on })} />
            </FieldRow>
          )}
          {show.has("restore_sessions") && (
            <FieldRow label="Bring back after a reboot" description="Tabs, folders and recent text return after a restart. Off deletes what is saved.">
              <Switch label="Bring back after a reboot" isLabelHidden
                value={cfg.restore_sessions !== false} onChange={(on) => onSet({ restore_sessions: on })} />
            </FieldRow>
          )}
        </FormLayout>
      )}
      {form && show.has("profiles") && <Divider />}
      {show.has("profiles") && (
        <VStack gap={2}>
          <VStack gap={0.5}>
            <Text type="label">Profiles</Text>
            <Text type="supporting">Shell and folder for "New tab: …" in the palette.</Text>
          </VStack>
          {profiles.map((p, i) => (
            <ProfileRow key={i} index={i} profile={p}
              canDelete={profiles.length > 1}
              select={selectLast && i === profiles.length - 1}
              onSave={(np) => saveRow(i, np)}
              onDelete={() => onProfiles(profiles.filter((_, j) => j !== i))} />
          ))}
          <HStack>
            <Button label="Add profile" icon={<Icon icon={Plus} />} variant="secondary" size="sm" onClick={onAddProfile} />
          </HStack>
        </VStack>
      )}
    </VStack>
  )
}

// One profile: name, shell and folder. Edits stay local while focus is in
// the row and save when it leaves (or on Enter), as the old change events did.
function ProfileRow({ index, profile, canDelete, select, onSave, onDelete }) {
  const fromProfile = () => ({ name: profile.name || "", shell: profile.shell || "", cwd: profile.cwd || "" })
  const [draft, setDraft] = useState(fromProfile)
  // A save, or a delete above this row, brings a new profile: show it.
  const saved = JSON.stringify(profile)
  useEffect(() => setDraft(fromProfile()), [saved])
  const row = useRef(null)
  useEffect(() => {
    if (!select) return
    const input = row.current?.querySelector("input")
    input?.focus()
    input?.select()
  }, [select])
  const save = () => {
    const p = { ...profile }
    for (const f of ["name", "shell", "cwd"]) {
      const v = draft[f].trim()
      if (v || f === "name") p[f] = v
      else delete p[f]
    }
    if (JSON.stringify(p) !== JSON.stringify(profile)) onSave(p)
  }
  const field = (f, label, placeholder) => (
    <StackItem size="fill">
      <TextInput label={`Profile ${index + 1} ${label}`} isLabelHidden size="sm" placeholder={placeholder}
        value={draft[f]} hasSpellCheck={false} autoCapitalize="off" hasAutoCorrect={false}
        onChange={(v) => setDraft((d) => ({ ...d, [f]: v }))} onEnter={save} />
    </StackItem>
  )
  return (
    <HStack ref={row} gap={2} vAlign="center"
      onBlur={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) save() }}>
      {field("name", "name", "Name")}
      {field("shell", "shell", "Shell (empty: your login shell)")}
      {field("cwd", "folder", "Folder (empty: home)")}
      <IconButton label="Remove profile" tooltip="Remove profile" icon={<Icon icon={Trash2} />} variant="ghost" size="sm"
        isDisabled={!canDelete} onClick={onDelete} />
    </HStack>
  )
}

// A labelled row in the horizontal-labels FormLayout for a control that has
// no label of its own (SegmentedControl only takes an aria-label).
// Not Astryx: the inner div only carries the id Field's label points at;
// SegmentedControl takes no id prop.
function FieldRow({ label, description, children }) {
  const id = `rsz-settings-${label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`
  return (
    <Field label={label} description={description} inputID={id} isGroupLabel>
      <div id={id}>{children}</div>
    </Field>
  )
}

// Enter keeps the settings, except where the focused control uses Enter
// itself: text boxes, buttons, tabs and Selector triggers/options.
function keepsEnter(t) {
  if (!t || !t.tagName) return false
  if (t.tagName === "BUTTON" || t.tagName === "TEXTAREA") return true
  if (t.tagName === "INPUT" && t.type !== "checkbox" && t.type !== "range") return true
  const role = t.getAttribute?.("role")
  return role === "combobox" || role === "tab" || role === "option" || role === "radio" || role === "listbox"
}
