// The yes/no box (ui/confirm.js) as an Astryx AlertDialog. confirm.js owns
// the keys (Enter / the close chord = yes, Esc = no) through its window
// capture listener; this view only answers clicks.
import { AlertDialog } from "@astryxdesign/core/AlertDialog"

export function ConfirmDialog({ isOpen, title, body, yes, no, onAnswer }) {
  return (
    <AlertDialog
      isOpen={isOpen}
      onOpenChange={(open) => { if (!open) onAnswer(false) }}
      title={title}
      description={body}
      actionLabel={yes}
      cancelLabel={no}
      onAction={() => onAnswer(true)}
    />
  )
}
