// The modal panel over the panes (ui/panel.js), used by notes: an Astryx
// Dialog with a DialogHeader (title + close button) and one content region.
// panel.js mounts it through chrome/overlays.js and owns the open state, so
// app.js can keep asking panel.js's isOpen().
//
// purpose="form": Escape closes (through Astryx's layer stack, which stands
// down when a field already handled the key with preventDefault), a stray
// click on the backdrop does not.
import { createContext, useContext, useState } from "react"
import { createPortal } from "react-dom"
import { Dialog, DialogHeader } from "@astryxdesign/core/Dialog"
import { Layout, LayoutContent } from "@astryxdesign/core/Layout"

// The header's start and end slots (DialogHeader startContent / endContent),
// as DOM nodes the content fills through PanelHeader: a screen puts its
// back button before the title and its actions before the close button.
const Slots = createContext(null)

export function PanelHeader({ start, end }) {
  const s = useContext(Slots)
  if (!s) return null
  return (
    <>
      {start && s.start && createPortal(start, s.start)}
      {end && s.end && createPortal(end, s.end)}
    </>
  )
}

// A wide panel is an editor: a fixed height its content fills.
const WIDE_HEIGHT = "min(560px, calc(100dvh - 96px))"

export function Panel({ title, wide, onClose, children }) {
  const onOpenChange = (open) => { if (!open) onClose() }
  const [start, setStart] = useState(null)
  const [end, setEnd] = useState(null)
  return (
    <Dialog
      isOpen
      onOpenChange={onOpenChange}
      purpose="form"
      width={wide ? 860 : 520}
      maxHeight={wide ? WIDE_HEIGHT : undefined}
      style={wide ? { height: WIDE_HEIGHT } : undefined}
      className="rsz-panel"
      // The terminal takes the keyboard back on a mousedown it sees
      // (app.js); a press in the panel is the panel's.
      onMouseDown={(e) => e.stopPropagation()}
    >
      <Layout
        header={
          <DialogHeader
            title={title}
            onOpenChange={onOpenChange}
            startContent={<span ref={setStart} className="rsz-panel-slot rsz-panel-start" />}
            endContent={<span ref={setEnd} className="rsz-panel-slot" />}
          />
        }
        content={
          <LayoutContent isScrollable={false} label={title}>
            <Slots.Provider value={{ start, end }}>{children}</Slots.Provider>
          </LayoutContent>
        }
      />
    </Dialog>
  )
}
