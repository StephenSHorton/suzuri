// suzuri-view/palette: suzuri's command palette, drawn with Astryx
// CommandPalette: the commands grouped by category, each row its title and
// its shortcut as Kbd badges (or the hint as plain text when it is not a
// chord, like a profile's shell). Up/Down move, Enter runs, Esc closes;
// ⌘K / Ctrl+K (or P) again closes it too.
//
// No Tauri: the host gives its list of commands and a run callback, and
// mounts it inside an Astryx <Theme>.
import { useEffect, useMemo, useRef } from "react"
import {
  CommandPalette,
  CommandPaletteInput,
  useCommandPaletteContext,
} from "@astryxdesign/core/CommandPalette"
import { HStack } from "@astryxdesign/core/HStack"
import { Text } from "@astryxdesign/core/Text"
import { Kbd } from "@astryxdesign/core/Kbd"
import { filterCommands, hintChords } from "../palette_rules.js"

export { filterCommands, hintChords }

// The query field, plus two things the old palette did that CommandPalette
// leaves out: the first result is highlighted, so typing then Enter runs it,
// and the open chord toggles the palette shut.
// Not Astryx: CommandPalette starts with no highlight and clears it on every
// keystroke, and has no close chord; this wraps CommandPaletteInput through
// its exported context (no custom DOM or CSS) to keep both behaviours.
function PaletteInput({ onToggle, isToggleKey, placeholder }) {
  const ctx = useCommandPaletteContext()
  const count = ctx?.selectableItems.length ?? 0
  const highlighted = ctx?.highlightedIndex ?? -1
  useEffect(() => {
    if (ctx && highlighted < 0 && count > 0) ctx.setHighlightedIndex(0)
  }, [ctx, highlighted, count, ctx?.selectableItems])
  const onKeyDown = (e) => {
    // An IME composition owns its keys (Enter commits it, not a command).
    if (e.nativeEvent.isComposing || e.keyCode === 229) return
    if ((isToggleKey || defaultToggle)(e)) {
      e.preventDefault()
      onToggle()
      return
    }
    if (e.key === "Enter" && ctx && ctx.highlightedIndex < 0 && count > 0) {
      e.preventDefault()
      ctx.selectItem(ctx.selectableItems[0].value)
      ctx.onClose()
    }
  }
  return <CommandPaletteInput placeholder={placeholder} onKeyDown={onKeyDown} />
}

// suzuri's open chords: ⌘K / Ctrl+K, or P.
const defaultToggle = (e) => (e.metaKey || e.ctrlKey) && /^[kp]$/i.test(e.key)

function Hint({ hint }) {
  if (!hint) return null
  const chords = hintChords(hint)
  if (!chords) {
    return (
      <Text type="supporting" maxLines={1} hasTruncateTooltip={false}>
        {hint}
      </Text>
    )
  }
  return (
    <HStack gap={1} align="center">
      {chords.map((keys, i) => (
        <Kbd key={i} keys={keys} />
      ))}
    </HStack>
  )
}

function Row({ item }) {
  return (
    <HStack gap={3} align="center" justify="between" width="100%">
      <Text maxLines={1} hasTruncateTooltip={false}>
        {item.label}
      </Text>
      <Hint hint={item.command.hint} />
    </HStack>
  )
}

// props (palette.d.ts has them typed):
//   commands      [{ title, category?, hint?, ... }]: the list, in order;
//                 a hint that is a chord label shows as keys
//   onRun(command)  the chosen command, called after the palette has closed
//                 and handed focus back, so a command may take the focus
//   onClose()     the palette is done (chosen or dismissed): unmount it
//   isToggleKey(e)  a keydown that closes it again (default ⌘/Ctrl + K or P)
//   placeholder   the query field's hint (default "Type a command…")
export function Palette({ commands, onRun, onClose, isToggleKey, placeholder = "Type a command…" }) {
  const done = useRef(false)
  const finish = (command) => {
    if (done.current) return
    done.current = true
    onClose?.()
    // The palette is a modal <dialog>: until React removes it everything
    // else is inert (a focus() there does nothing), and the removal hands
    // focus back. Run after both, so a command that takes the focus (rename
    // pane or tab) keeps it.
    if (command) requestAnimationFrame(() => setTimeout(() => onRun?.(command)))
  }
  return (
    <PaletteView items={commands} onDone={finish} onToggle={() => finish(null)}
      isToggleKey={isToggleKey} placeholder={placeholder} />
  )
}

// The bare view: items, onDone(item | null) when it closes, with the
// command to run or null for a dismissal; onToggle when the open chord
// comes again.
export function PaletteView({ items, onDone, onToggle, isToggleKey, placeholder = "Type a command…" }) {
  const chosen = useRef(null)
  // Not Astryx: a hand-made SearchSource rather than createStaticSource, so
  // matching stays filterCommands' (every word across title, hint, category).
  const source = useMemo(() => {
    // Ids are positions: the same command id can appear more than once
    // (Move pane to tab: 1, 2 …, each its own arg).
    const all = items.map((command, i) => ({
      id: String(i),
      label: command.title,
      command,
      auxiliaryData: command.category ? { group: command.category } : undefined,
    }))
    return {
      bootstrap: () => all,
      search: (q) => {
        const keep = new Set(filterCommands(items, q))
        return all.filter((r) => keep.has(r.command))
      },
    }
  }, [items])

  return (
    <CommandPalette
      isOpen
      label="Command palette"
      searchSource={source}
      input={<PaletteInput onToggle={onToggle} isToggleKey={isToggleKey} placeholder={placeholder} />}
      renderItem={(item) => <Row item={item} />}
      emptySearchText="No matches"
      emptyBootstrapText="No commands"
      onValueChange={(id) => {
        chosen.current = items[Number(id)] ?? null
      }}
      onOpenChange={(open) => {
        if (!open) onDone(chosen.current)
      }}
    />
  )
}
