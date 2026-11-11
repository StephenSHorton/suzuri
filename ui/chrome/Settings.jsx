// The Settings screen (Ctrl+, / Cmd+,), drawn with Astryx: a Dialog with a
// TabList for the Look and Session pages, a horizontal-labels FormLayout of
// Selectors, a Slider, a SegmentedControl and a Switch, and the profile rows
// as TextInputs. ui/settings.js owns the state and the saving; this file
// only draws what it is given and reports what the person changed.
import { useEffect, useRef, useState } from "react"
import { Dialog, DialogHeader } from "@astryxdesign/core/Dialog"
import { Layout, LayoutContent, LayoutFooter, HStack, VStack, StackItem } from "@astryxdesign/core/Layout"
import { TabList, Tab } from "@astryxdesign/core/TabList"
import { FormLayout } from "@astryxdesign/core/FormLayout"
import { Field } from "@astryxdesign/core/Field"
import { Selector } from "@astryxdesign/core/Selector"
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

export const LABELS = {
  themes: {
    inkstone: "Inkstone", charmtone: "Charmtone", high_contrast: "High contrast", nord: "Nord",
    dracula: "Dracula", tokyo_night: "Tokyo Night", catppuccin: "Catppuccin", gruvbox: "Gruvbox",
    one_dark: "One Dark", solarized: "Solarized", rose_pine: "Rosé Pine", kanagawa: "Kanagawa",
    monokai: "Monokai", forest: "Forest", ocean: "Ocean", amber: "Amber",
  },
  cursors: { block: "Block", underline: "Underline", bar: "Bar" },
  ansi_maps: { none: "Off (stock ANSI colours)", soft: "Soft (blend into the theme)", full: "Full (theme palette)" },
}

const opts = (ids, labels) => (ids || []).map((id) => ({ value: id, label: labels?.[id] || id }))

// props: cfg, meta, page, status, onPage(page), onSet(patch), onProfiles(list),
// onAddProfile(), onDone(keep), selectLast (select the new profile's name).
export function SettingsView(props) {
  const { page, onPage, onDone, status, meta } = props

  // Keys stay in the screen: none reach the terminal or app.js's shortcuts.
  // Escape is left to bubble, because Astryx's layer stack answers it on the
  // document (closing an open Selector first, else the dialog: cancel).
  // Plain Tab bubbles too: the Dialog's focus trap listens on the document.
  const pathRef = useRef(null)
  const onKeyDown = (e) => {
    if (e.key === "Escape" || (e.key === "Tab" && !e.ctrlKey)) return
    e.stopPropagation()
    if ((e.metaKey || e.ctrlKey) && e.key === ",") {
      e.preventDefault()
      // Blur first, so a profile row being edited saves before the screen
      // unmounts (removing a focused input fires no blur).
      e.target?.blur?.()
      return onDone(true)
    }
    // Ctrl+Tab switches page (old suzuri used plain Tab, which now moves focus).
    if (e.ctrlKey && e.key === "Tab") { e.preventDefault(); return onPage(page === "look" ? "session" : "look") }
    if (e.key === "Enter" && !e.defaultPrevented && keepsEnter(e.target) === false) {
      e.preventDefault()
      onDone(true)
    }
  }

  return (
    <Dialog
      isOpen
      onOpenChange={(open) => { if (!open) onDone(false) }}
      purpose="form"
      width={600}
      maxHeight="85dvh"
      onKeyDown={onKeyDown}
    >
      <Layout
        header={
          <VStack gap={2}>
            <DialogHeader title="Settings" hasDivider={false} />
            <TabList value={page} onChange={onPage} role="tablist" aria-label="Settings pages" hasDivider isFullBleed>
              <Tab value="look" label="Look" panelId="rsz-settings-look" />
              <Tab value="session" label="Session" panelId="rsz-settings-session" />
            </TabList>
          </VStack>
        }
        content={
          <LayoutContent>
            {/* Not Astryx: a plain role="tabpanel" div, because TabList's tabs
                pattern asks the caller to supply the panel element (Tab.md). */}
            <div id={`rsz-settings-${page}`} role="tabpanel">
              {page === "look" ? <LookPage {...props} /> : <SessionPage {...props} />}
            </div>
          </LayoutContent>
        }
        footer={
          <LayoutFooter hasDivider>
            <HStack gap={3} vAlign="center" wrap="wrap">
              <StackItem size="fill">
                <VStack gap={1}>
                  <HStack gap={1.5} vAlign="center" wrap="wrap">
                    <Kbd keys="ctrl+tab" /><Text type="supporting">page</Text>
                    <Kbd keys="enter" /><Text type="supporting">done</Text>
                    <Kbd keys="esc" /><Text type="supporting">cancel</Text>
                  </HStack>
                  {/* Not Astryx: a span to anchor the Tooltip, since Text takes no ref. */}
                  <span ref={pathRef}>
                    <Text type="supporting" maxLines={1}>{status || `Saved to ${shortPath(meta.path)}`}</Text>
                  </span>
                  <Tooltip content={meta.path} anchorRef={pathRef} isEnabled={!status} />
                </VStack>
              </StackItem>
              <HStack gap={2}>
                <Button label="Cancel" variant="secondary" onClick={() => onDone(false)} />
                <Button label="Done" variant="primary" onClick={() => onDone(true)} />
              </HStack>
            </HStack>
          </LayoutFooter>
        }
      />
    </Dialog>
  )
}

function LookPage({ cfg, meta, onSet }) {
  const fonts = Array.from(new Set([cfg.font_face, ...(meta.fonts || [])]))
  // The slider shows the size while dragging and saves when it lets go.
  const [size, setSize] = useState(cfg.font_size_px)
  useEffect(() => setSize(cfg.font_size_px), [cfg.font_size_px])
  const saveSize = (v) => { if (v !== cfg.font_size_px) onSet({ font_size_px: v }) }
  return (
    <FormLayout direction="horizontal-labels">
      <Selector label="Font" options={opts(fonts)} value={cfg.font_face} hasSearch={fonts.length > 8}
        onChange={(v) => onSet({ font_face: v })} />
      <Slider label="Size" value={size} min={meta.font_min} max={meta.font_max} step={1}
        valueDisplay="text" formatValue={(v) => `${v}px`}
        onChange={setSize} onChangeEnd={saveSize} />
      <Selector label="Text size" description="The size of the text around the terminal: tabs, menus, settings and notes. Size above sets the terminal's."
        options={opts(meta.text_sizes, meta.text_size_labels)} value={cfg.ui_text_size} onChange={(v) => onSet({ ui_text_size: v })} />
      <FieldRow label="Cursor">
        <SegmentedControl label="Cursor" value={cfg.cursor} onChange={(v) => onSet({ cursor: v })}>
          {(meta.cursors || []).map((id) => <SegmentedControlItem key={id} value={id} label={LABELS.cursors[id] || id} />)}
        </SegmentedControl>
      </FieldRow>
      <Selector label="Terminal theme" description="Colours inside the terminal panes only; the window chrome is always Astryx Neutral."
        options={opts(meta.themes, LABELS.themes)} value={cfg.theme} onChange={(v) => onSet({ theme: v })} />
      <Selector label="ANSI map" description="How programs' ANSI colours map onto the terminal theme."
        options={opts(meta.ansi_maps, LABELS.ansi_maps)} value={cfg.shell_ansi_map} onChange={(v) => onSet({ shell_ansi_map: v })} />
    </FormLayout>
  )
}

function SessionPage({ cfg, meta, onSet, onProfiles, onAddProfile, selectLast }) {
  const names = cfg.profiles.map((p) => p.name)
  const saveRow = (i, p) => onProfiles(cfg.profiles.map((q, j) => (j === i ? p : q)))
  return (
    <VStack gap={4}>
      <FormLayout direction="horizontal-labels">
        <Selector label="New tabs use" options={opts(names)} value={cfg.active_profile} onChange={(v) => onSet({ active_profile: v })} />
        <Selector label="Notice" description="The screen corner notice cards stack in."
          options={opts(meta.notice_positions, meta.notice_labels)} value={cfg.notice_position} onChange={(v) => onSet({ notice_position: v })} />
        <Switch label="Animate unfocused" description="Off: spinners rest while another app is in front."
          value={cfg.animate_unfocused !== false} onChange={(on) => onSet({ animate_unfocused: on })} />
      </FormLayout>
      <Divider />
      <VStack gap={2}>
        <VStack gap={0.5}>
          <Text type="label">Profiles</Text>
          <Text type="supporting">Shell and folder for "New tab: …" in the palette.</Text>
        </VStack>
        {cfg.profiles.map((p, i) => (
          <ProfileRow key={i} index={i} profile={p}
            canDelete={cfg.profiles.length > 1}
            select={selectLast && i === cfg.profiles.length - 1}
            onSave={(np) => saveRow(i, np)}
            onDelete={() => onProfiles(cfg.profiles.filter((_, j) => j !== i))} />
        ))}
        <HStack>
          <Button label="Add profile" icon={<Icon icon={Plus} />} variant="secondary" size="sm" onClick={onAddProfile} />
        </HStack>
      </VStack>
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
function FieldRow({ label, children }) {
  return (
    <Field label={label} inputID={`rsz-settings-${label.toLowerCase()}`} isGroupLabel>
      <div id={`rsz-settings-${label.toLowerCase()}`}>{children}</div>
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

// The settings path with the home folder as ~, for the footer.
export function shortPath(p) {
  return String(p ?? "").replace(/^\/Users\/[^/]+|^\/home\/[^/]+|^[A-Za-z]:\\Users\\[^\\]+/, "~")
}
