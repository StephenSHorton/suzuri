// The command palette's view (palette.js keeps the API app.js uses): Astryx
// CommandPalette, the commands grouped by category, each row its title and
// its shortcut as Kbd badges (or the hint as plain text when it is not a
// chord, like a profile's shell). Up/Down move, Enter runs, Esc closes;
// ⌘K / Ctrl+K (or P) again closes it too.
import { useEffect, useMemo, useRef } from "react"
import {
  CommandPalette,
  CommandPaletteInput,
  useCommandPaletteContext,
} from "@astryxdesign/core/CommandPalette"
import { HStack } from "@astryxdesign/core/HStack"
import { Text } from "@astryxdesign/core/Text"
import { Kbd } from "@astryxdesign/core/Kbd"
import { filterCommands, hintChords } from "../palette.js"

// The query field, plus two things the old palette did that CommandPalette
// leaves out: the first result is highlighted, so typing then Enter runs it,
// and the open chord toggles the palette shut.
function PaletteInput({ onToggle }) {
  const ctx = useCommandPaletteContext()
  const count = ctx?.selectableItems.length ?? 0
  const highlighted = ctx?.highlightedIndex ?? -1
  useEffect(() => {
    if (ctx && highlighted < 0 && count > 0) ctx.setHighlightedIndex(0)
  }, [ctx, highlighted, count, ctx?.selectableItems])
  const onKeyDown = (e) => {
    if ((e.metaKey || e.ctrlKey) && /^[kp]$/i.test(e.key)) {
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
  return <CommandPaletteInput placeholder="Type a command…" onKeyDown={onKeyDown} />
}

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

// items: the commands ({title, category?, hint?, ...}); onDone(item | null)
// when it closes, with the command to run or null for a dismissal.
export function PaletteView({ items, onDone, onToggle }) {
  const chosen = useRef(null)
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
      input={<PaletteInput onToggle={onToggle} />}
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
