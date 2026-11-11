// The notification history under the bell (ui/bell.js owns the state):
// an Astryx Popover anchored on the title bar's bell button, a List of past
// notices (time and text), and an EmptyState when there are none.
//
// Keys: Up/Down/Home/End move the selection (and the focus with it), Enter
// reveals the pane that posted it, Escape closes. The Popover owns the click
// on the bell (it toggles through onOpenChange), the outside-click close and
// the focus trap.
import { useEffect, useRef } from "react"
import { Popover } from "@astryxdesign/core/Popover"
import { List, ListItem } from "@astryxdesign/core/List"
import { EmptyState } from "@astryxdesign/core/EmptyState"
import { Text } from "@astryxdesign/core/Text"
import { Icon } from "@astryxdesign/core/Icon"
import { BellOff } from "lucide-react"
import { noticeTime, noticeText, moveIndex } from "../notice_list.js"

const NAV_KEYS = ["ArrowDown", "ArrowUp", "Home", "End"]

function rowButton(root, i) {
  return root?.querySelector(`[data-bell-row="${i}"] button`)
}

function History({ items, index, onIndex, onChoose, onClose }) {
  const ref = useRef(null)

  // app.js gives the keyboard back to the terminal on any mousedown in the
  // page; a press in here keeps it (the old popover did the same).
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const stop = (e) => e.stopPropagation()
    el.addEventListener("mousedown", stop)
    return () => el.removeEventListener("mousedown", stop)
  }, [])

  // Keep the selected row in view as the list refreshes.
  useEffect(() => {
    rowButton(ref.current, index)?.scrollIntoView?.({ block: "nearest" })
  }, [index, items])

  // The first open of a session shows the empty state until the history
  // arrives, so the Popover's autofocus lands on its surface, outside the
  // key handler below. When rows appear, put the focus on the selected one
  // so the arrow keys and Enter work without a Tab first.
  const hasRows = items.length > 0
  useEffect(() => {
    const el = ref.current
    if (!el || !hasRows) return
    const active = el.ownerDocument.activeElement
    if (active && !el.contains(active) && active.contains(el)) {
      rowButton(el, index)?.focus({ preventScroll: true })
    }
  }, [hasRows])

  const onKeyDown = (e) => {
    if (e.nativeEvent.isComposing) return
    if (e.key === "Escape") {
      e.preventDefault()
      e.stopPropagation()
      return onClose()
    }
    if (e.key === "Enter") {
      e.preventDefault()
      e.stopPropagation()
      return onChoose(items[index])
    }
    if (NAV_KEYS.includes(e.key)) {
      e.preventDefault()
      e.stopPropagation()
      const next = moveIndex(index, e.key, items.length)
      onIndex(next)
      rowButton(ref.current, next)?.focus({ preventScroll: true })
    }
  }

  // Tab (or a click) moving focus to another row selects it.
  const onFocus = (e) => {
    const row = e.target.closest?.("[data-bell-row]")
    if (row) {
      const i = Number(row.dataset.bellRow)
      if (i !== index) onIndex(i)
    }
  }

  return (
    <div ref={ref} className="rsz-bell" onKeyDown={onKeyDown} onFocus={onFocus}>
      {items.length === 0 ? (
        <EmptyState isCompact title="No notifications this session" icon={<Icon icon={BellOff} color="secondary" />} />
      ) : (
        // Not Astryx: a plain scroll box, because ScrollableArea has no max
        // height and the popover would otherwise grow with 40 notices.
        <div className="rsz-bell-scroll">
          <List density="compact" header={<Text type="supporting">Notifications</Text>}>
            {items.map((it, i) => {
              const text = noticeText(it)
              return (
                <ListItem
                  key={`${it.at}-${i}`}
                  data-bell-row={i}
                  title={text}
                  isSelected={i === index}
                  startContent={
                    <Text type="supporting" hasTabularNumbers>
                      {noticeTime(it.at)}
                    </Text>
                  }
                  label={text}
                  onClick={() => onChoose(it)}
                />
              )
            })}
          </List>
        </div>
      )}
    </div>
  )
}

// The bell button the popover hangs from: the title bar's data-chrome="bell"
// chip. Read once; the title bar keeps that element for the app's lifetime.
const anchor = { current: null }

export function BellPopover({ isOpen, items, index, onOpenChange, onIndex, onChoose, onClose }) {
  if (!anchor.current) anchor.current = document.querySelector('[data-chrome="bell"]')
  if (!anchor.current) return null
  return (
    <Popover
      anchorRef={anchor}
      isOpen={isOpen}
      onOpenChange={onOpenChange}
      label="Notifications"
      placement="below"
      alignment="end"
      width={380}
      padding={0}
      content={<History items={items} index={index} onIndex={onIndex} onChoose={onChoose} onClose={onClose} />}
    />
  )
}
