// The title bar: the 硯 mark, the tabs, + , and on the right only the bell
// and the caffeine cup (then, on Windows, the window's own buttons). Built
// from Astryx (theme Neutral, Lucide icons); see docs/astryx for the kit.
//
// The bar drags the window (data-tauri-drag-region on the bar, the spacer
// and the mark); every control stops that by not carrying the attribute.
// Presses keep the terminal's keyboard: controls prevent mousedown focus.
import { useSyncExternalStore, useEffect, useRef, useState } from "react"
import { IconButton } from "@astryxdesign/core/IconButton"
import { ToggleButton } from "@astryxdesign/core/ToggleButton"
import { Icon } from "@astryxdesign/core/Icon"
import { Badge } from "@astryxdesign/core/Badge"
import { TextInput } from "@astryxdesign/core/TextInput"
import { HStack } from "@astryxdesign/core/HStack"
import { Bell, BellDot, Coffee, Plus, SquareTerminal, X } from "lucide-react"
import { chrome } from "./store.js"
import { Caption } from "./Caption.jsx"

const isMac = navigator.platform.startsWith("Mac")
const useChrome = () => useSyncExternalStore(chrome.subscribe, chrome.get)
const keepFocus = (e) => e.preventDefault()

// The rename box: Astryx's TextInput, small and auto-focused, its label
// hidden (the tab is the context). Enter keeps, Escape drops, a blur keeps;
// every key stops here so none reaches the terminal. The span takes the
// presses on the control's padding too, which the tab would otherwise
// read as a click on itself.
function RenameBox({ tab }) {
  const ref = useRef(null)
  const [value, setValue] = useState(tab.label)
  const finished = useRef(false)
  useEffect(() => {
    ref.current?.focus()
    ref.current?.select()
  }, [])
  const done = (keep) => {
    if (finished.current) return
    finished.current = true
    chrome.actions.finishRename(tab.id, keep ? (ref.current?.value ?? value).trim() : null)
  }
  return (
    <span className="rsz-tab-rename" onMouseDown={(e) => e.stopPropagation()}>
      <TextInput
        ref={ref}
        label="Tab name"
        isLabelHidden
        size="sm"
        hasAutoFocus
        hasSpellCheck={false}
        value={value}
        onChange={setValue}
        onEnter={() => done(true)}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key === "Escape") done(false)
        }}
        onBlur={() => done(true)}
        width="100%"
      />
    </span>
  )
}

// One terminal tab. Astryx's Tab is page navigation with no close control,
// so a terminal tab is composed from Astryx parts: an Icon, the label, a
// Badge for the pane count and a ghost IconButton to close.
function TerminalTab({ tab }) {
  return (
    <div
      className="rsz-tab"
      role="tab"
      aria-selected={tab.active}
      data-active={tab.active || undefined}
      data-tab={tab.id}
      title={tab.title}
      onMouseDown={(e) => {
        if (e.target.closest("button, input")) return
        e.preventDefault()
        if (e.button === 1) return chrome.actions.closeTab(tab.id)
        chrome.actions.showTab(tab.id)
      }}
      onDoubleClick={(e) => {
        if (!e.target.closest("button")) chrome.actions.startRename(tab.id)
      }}
    >
      {tab.activity ? (
        <span className="rsz-tab-act" data-a={tab.activity.kind} title={tab.activity.title}>
          {tab.activity.text}
        </span>
      ) : (
        <Icon icon={SquareTerminal} size="sm" color={tab.active ? "primary" : "secondary"} />
      )}
      {tab.renaming ? <RenameBox tab={tab} /> : <span className="rsz-tab-label">{tab.label}</span>}
      {tab.panes > 1 && <Badge label={String(tab.panes)} variant="neutral" />}
      <span className="rsz-tab-close" onMouseDown={keepFocus}>
        <IconButton label="Close tab" icon={<Icon icon={X} />} variant="ghost" size="sm" onClick={() => chrome.actions.closeTab(tab.id)} />
      </span>
    </div>
  )
}

function CaffeineButton({ status }) {
  const on = !!status?.active
  const timed = on && status.remaining_s != null
  const tip = on
    ? `Caffeine on${timed ? ` for ${status.label}` : ""}: the computer stays awake (click to turn off)`
    : "Caffeine off: the computer may sleep (click to keep it awake)"
  return (
    <span className="rsz-chip" data-on={on || undefined} onMouseDown={keepFocus}>
      {/* An on/off control: Astryx's ToggleButton shows the pressed state. */}
      <ToggleButton label="Caffeine" icon={<Icon icon={Coffee} />} isIconOnly={!timed} isPressed={on} tooltip={tip} onPressedChange={() => chrome.actions.toggleCaffeine()}>
        {timed ? status.label : undefined}
      </ToggleButton>
    </span>
  )
}

export function TitleBar() {
  const s = useChrome()
  const unread = s.unread > 0
  return (
    // An Astryx HStack as the <header>: it passes id and the data attributes
    // through, so the bar itself still drags the window. Its height, the
    // macOS traffic-light inset and its place over the panes are chrome.css.
    <HStack as="header" id="bar" className="rsz-bar" gap={1} vAlign="center" data-tauri-drag-region data-inactive={s.inactive || undefined}>
      <span className="rsz-mark" data-tauri-drag-region>
        硯
      </span>
      <nav className="rsz-tabs" role="tablist" aria-label="Tabs">
        {s.tabs.map((t) => (
          <TerminalTab key={t.id} tab={t} />
        ))}
      </nav>
      <span onMouseDown={keepFocus}>
        <IconButton label="New tab" icon={<Icon icon={Plus} />} variant="ghost" size="sm" tooltip={`New tab (${isMac ? "⌘T" : "Ctrl+Shift+T"})`} onClick={() => chrome.actions.newTab()} />
      </span>
      <span className="rsz-spacer" data-tauri-drag-region />
      <span className="rsz-chip" data-chrome="bell" onMouseDown={keepFocus}>
        <IconButton
          label={unread ? `Notifications (${s.unread} unread)` : "Notifications"}
          icon={<Icon icon={unread ? BellDot : Bell} color={unread ? "accent" : "secondary"} />}
          variant="ghost"
          tooltip={s.bellOpen ? undefined : "Notifications"}
          onClick={() => chrome.actions.toggleBell()}
        />
        {unread && <span className="rsz-chip-count">{s.unread > 9 ? "9+" : s.unread}</span>}
      </span>
      <CaffeineButton status={s.caffeine} />
      {!isMac && <Caption maximized={s.maximized} />}
    </HStack>
  )
}
