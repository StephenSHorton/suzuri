// The notes view (#294) inside the modal panel (chrome/Panel.jsx). Three
// screens in one dialog: the list, the editor (name + body) and the delete
// question; Astryx says not to nest dialogs, so the question stays inline as
// Go asked it. notes.js owns the bank and saving (model); this edits the
// bank's objects in place and re-renders.
//
// Keys are documented in notes.js. Escape that a screen handles (editor back
// to the list, name cancels the rename, question cancels) calls
// preventDefault, so Astryx's layer stack leaves the dialog open; any other
// Escape closes the panel.
import { useEffect, useLayoutEffect, useReducer, useRef, useState } from "react"
import { Button } from "@astryxdesign/core/Button"
import { EmptyState } from "@astryxdesign/core/EmptyState"
import { HStack, VStack } from "@astryxdesign/core/Layout"
import { Icon } from "@astryxdesign/core/Icon"
import { Kbd } from "@astryxdesign/core/Kbd"
import { List, ListItem } from "@astryxdesign/core/List"
import { Text } from "@astryxdesign/core/Text"
import { TextArea } from "@astryxdesign/core/TextArea"
import { TextInput } from "@astryxdesign/core/TextInput"
import { Plus, Trash2 } from "lucide-react"
import { displayTitle, renameSeed, insertTab, onFirstLine } from "../notes_edit.js"
import { caretStyle, onFirstRow } from "../notes_caret.js"

const isBlank = (n) => !(n.title || "").trim() && !(n.body || "").trim()
const runes = (s) => [...s].length

// Focus after the browser has shown the dialog (showModal runs in the
// Dialog's effect, after ours), and after a screen switch has rendered.
const later = (f) => setTimeout(f, 0)

// The notes didn't load: say why, in the panel.
export function NotesError({ error }) {
  return <EmptyState title="Notes didn't load" description={error} icon={<Icon icon="error" size="lg" />} />
}

export function NotesView({ model }) {
  const bank = model.bank
  const [, redraw] = useReducer((x) => x + 1, 0)
  const [screen, setScreen] = useState({ name: "editor", nameFirst: false })
  const [pick, setPick] = useState(() => Math.max(0, bank.notes.findIndex((n) => n.id === bank.active_id)))
  const [status, setStatus] = useState(null)

  useEffect(() => {
    model.onStatus = setStatus
    return () => { if (model.onStatus === setStatus) model.onStatus = null }
  }, [model])

  const active = () => bank.notes.find((n) => n.id === bank.active_id) || bank.notes[0]
  const touch = (n) => { n.updated = model.now(); model.save(); redraw() }

  const showList = (n) => {
    if (n) setPick(bank.notes.indexOf(n))
    setScreen({ name: "list" })
  }
  const openNote = (i) => {
    bank.active_id = bank.notes[i].id
    model.save()
    setScreen({ name: "editor", nameFirst: false })
  }
  const newNote = () => {
    if (bank.notes.length >= model.maxNotes) return setStatus(`At most ${model.maxNotes} notes.`)
    const n = { id: model.newId(), title: "", body: "", updated: model.now() }
    bank.notes.push(n)
    bank.active_id = n.id
    model.save()
    setScreen({ name: "editor", nameFirst: true })
  }
  const rename = (i) => {
    bank.active_id = bank.notes[i].id
    setScreen({ name: "editor", nameFirst: true })
  }
  const askDelete = (i) => {
    if (i != null) bank.active_id = bank.notes[i].id
    setScreen({ name: "confirm" })
  }
  const doDelete = () => {
    const n = active()
    if (bank.notes.length <= 1) Object.assign(n, { title: "", body: "", updated: model.now() })
    else {
      const i = bank.notes.indexOf(n)
      bank.notes.splice(i, 1)
      bank.active_id = bank.notes[Math.min(i, bank.notes.length - 1)].id
      setPick((p) => Math.min(p, bank.notes.length - 1))
    }
    model.save(true)
    setScreen({ name: "list" })
  }

  const statusLine = status && (
    <Text type="supporting" className="notes-status" maxLines={1}>{status}</Text>
  )
  const actions = { showList, openNote, newNote, rename, askDelete, doDelete, touch }

  if (screen.name === "list")
    return <NotesList bank={bank} model={model} pick={pick} setPick={setPick} actions={actions} status={statusLine} />
  if (screen.name === "confirm")
    return <NotesConfirm note={active()} last={bank.notes.length <= 1} onYes={doDelete} onNo={() => showList()} />
  const n = active()
  return (
    <NotesEditor
      key={n.id}
      note={n}
      model={model}
      nameFirst={screen.nameFirst}
      canClear={isBlank(n) && bank.notes.length <= 1}
      actions={actions}
      status={statusLine}
    />
  )
}

// A key (or keys that do the same) and what it does, for the hint rows.
function Hint({ keys, children }) {
  const all = Array.isArray(keys) ? keys : [keys]
  return (
    <HStack gap={1} align="center">
      {all.map((k, i) => (
        <HStack key={k} gap={1} align="center">
          {i > 0 && <Text type="supporting">or</Text>}
          <Kbd keys={k} />
        </HStack>
      ))}
      <Text type="supporting">{children}</Text>
    </HStack>
  )
}

function NotesList({ bank, model, pick, setPick, actions, status }) {
  const box = useRef(null)
  const at = Math.max(0, Math.min(pick, bank.notes.length - 1))
  const full = bank.notes.length >= model.maxNotes

  useEffect(() => {
    const t = later(() => box.current?.focus())
    return () => clearTimeout(t)
  }, [])
  useLayoutEffect(() => {
    box.current?.querySelector('[aria-current="true"]')?.scrollIntoView({ block: "nearest" })
  }, [at])

  // Not Astryx: List has no keyboard cursor, so the list box keeps Go's keys
  // (↑↓ move the selected row; Enter, N, D, R ...) on its own keydown.
  const onKeyDown = (e) => {
    if (e.nativeEvent.isComposing) return
    const k = e.key.toLowerCase()
    const count = bank.notes.length
    if (k === "arrowup") setPick((at - 1 + count) % count)
    else if (k === "arrowdown") setPick((at + 1) % count)
    else if (k === "enter" || k === "arrowright" || k === "tab") actions.openNote(at)
    else if (k === "n" && !e.metaKey) actions.newNote()
    else if (k === "d" || k === "delete" || k === "backspace") actions.askDelete(at)
    else if (k === "f2" || k === "r") actions.rename(at)
    else return
    e.preventDefault()
  }

  return (
    <VStack gap={3} height="100%">
      <div ref={box} className="notes-list" tabIndex={0} onKeyDown={onKeyDown} aria-label="Notes">
        <List density="compact">
          {bank.notes.map((n, i) => (
            <ListItem
              key={n.id}
              label={displayTitle(n, 48)}
              endContent={<Text type="supporting" hasTabularNumbers>{(n.updated || "").slice(0, 10)}</Text>}
              isSelected={i === at}
              onClick={() => { setPick(i); actions.openNote(i) }}
            />
          ))}
        </List>
      </div>
      <HStack gap={3} align="center" wrap="wrap">
        <Button
          label="New note"
          icon={<Icon icon={Plus} />}
          onClick={actions.newNote}
          isDisabled={full}
          tooltip={full ? `At most ${model.maxNotes} notes.` : undefined}
        />
        <Text type="supporting" hasTabularNumbers>{bank.notes.length} of {model.maxNotes}</Text>
        <Hint keys="enter">open</Hint>
        <Hint keys="N">new</Hint>
        <Hint keys="D">delete</Hint>
        <Hint keys="R">rename</Hint>
        <Hint keys="escape">close</Hint>
        {status}
      </HStack>
    </VStack>
  )
}

function NotesConfirm({ note, last, onYes, onNo }) {
  const yes = useRef(null)
  useEffect(() => {
    const t = later(() => yes.current?.focus())
    return () => clearTimeout(t)
  }, [])
  const label = displayTitle(note)
  return (
    <VStack
      gap={4}
      onKeyDown={(e) => {
        if (e.key === "Escape") { e.preventDefault(); onNo() }
      }}
    >
      <Text type="body">
        {last ? `Clear “${label}”? The last note is emptied, not removed.` : `Delete “${label}”? This cannot be undone.`}
      </Text>
      <HStack gap={2} justify="end">
        <Button label="Cancel" variant="secondary" onClick={onNo} />
        <Button ref={yes} label={last ? "Clear" : "Delete"} variant="destructive" onClick={onYes} />
      </HStack>
    </VStack>
  )
}

function NotesEditor({ note: n, model, nameFirst, canClear, actions, status }) {
  const name = useRef(null)
  const text = useRef(null)
  // Renaming starts whenever the name gets focus: it's filled with the
  // stored or derived name, saves as you type, and Esc puts back the old one.
  const before = useRef(null)
  const [draft, setDraft] = useState(null)

  useEffect(() => {
    const t = later(() => {
      if (nameFirst || isBlank(n)) name.current?.focus()
      else if (text.current) {
        const f = text.current
        f.focus()
        f.setSelectionRange(f.value.length, f.value.length)
      }
    })
    return () => clearTimeout(t)
  }, [])

  // Not Astryx: the caret in the terminal's cursor shape (notes_caret.js).
  // TextArea and TextInput are native fields with no caret shape, so a
  // block or underline is drawn over a transparent native caret.
  useEffect(() => {
    const fields = [name.current, text.current].filter(Boolean)
    for (const f of fields) caretStyle(f, model.caret)
    return () => {
      for (const f of fields) for (const box of f.parentElement?.querySelectorAll(":scope > .notes-caret") || []) box.remove()
    }
  }, [])

  const setBody = (v) => { n.body = v; actions.touch(n) }

  const onNameFocus = () => {
    if (before.current !== null) return
    before.current = n.title || ""
    setDraft(renameSeed(n))
    later(() => name.current?.select())
  }
  const onNameBlur = () => { before.current = null; setDraft(null) }
  const onNameChange = (v) => {
    setDraft(v)
    n.title = v.trim() ? v : ""
    actions.touch(n)
  }
  const toBody = () => {
    const t = text.current
    if (!t) return
    t.focus()
    t.setSelectionRange(0, 0)
    t.scrollTop = 0
  }
  const onNameKey = (e) => {
    if (e.nativeEvent.isComposing) return
    if (e.key === "Enter" || e.key === "ArrowDown") {
      e.preventDefault()
      // As Go: what's in the field is the name, even an untouched seed.
      const t = (draft ?? n.title ?? "").trim()
      if (n.title !== t) { n.title = t; actions.touch(n) }
      toBody()
    } else if (e.key === "Escape") {
      e.preventDefault()
      if (before.current !== null && n.title !== before.current) { n.title = before.current; actions.touch(n) }
      toBody()
    }
  }

  // Tab through the text box's own editing, so ⌘Z undoes it.
  const typeTab = (t) => {
    if (document.execCommand?.("insertText", false, "\t")) return
    const r = insertTab(t.value, t.selectionStart, t.selectionEnd)
    setBody(r.value)
    later(() => t.setSelectionRange(r.caret, r.caret))
  }
  // ↑ leaves the body for the name on its first row, wrapped rows included.
  const atTop = (t) => {
    if (t.selectionStart !== t.selectionEnd || !onFirstLine(t.value, t.selectionStart)) return false
    try {
      return onFirstRow(t)
    } catch {
      return true
    }
  }
  const onBodyKey = (e) => {
    if (e.nativeEvent.isComposing) return
    const t = e.currentTarget
    const mod = model.isMac ? e.metaKey : e.ctrlKey
    if (e.key === "Escape") { e.preventDefault(); model.save(true); actions.showList(n) }
    else if (e.key === "F2") { e.preventDefault(); name.current?.focus() }
    else if (e.key === "Tab" && !mod && !e.altKey) { e.preventDefault(); typeTab(t) }
    else if (e.key === "ArrowUp" && !e.shiftKey && !mod && !e.altKey && atTop(t)) { e.preventDefault(); name.current?.focus() }
    else if (mod && e.key.toLowerCase() === "n" && !e.shiftKey) { e.preventDefault(); actions.newNote() }
  }

  const title = displayTitle(n)
  const over = runes(n.body || "") > model.maxRunes
  return (
    <VStack gap={3} height="100%">
      <TextInput
        ref={name}
        label="Name"
        isLabelHidden
        value={draft ?? n.title ?? ""}
        placeholder={title === "Untitled" ? "Name this note" : title}
        hasSpellCheck={false}
        onChange={onNameChange}
        onFocus={onNameFocus}
        onBlur={onNameBlur}
        onKeyDown={onNameKey}
        containerClassName="notes-title"
      />
      <TextArea
        ref={text}
        label="Note"
        isLabelHidden
        value={n.body || ""}
        placeholder="Write here. Saved as you type."
        hasSpellCheck={false}
        onChange={setBody}
        onKeyDown={onBodyKey}
        status={over ? { type: "warning", message: `Only the first ${model.maxRunes} characters are kept.` } : undefined}
        containerClassName="notes-body"
        controlClassName="notes-body-box"
      />
      <HStack gap={3} align="center" wrap="wrap">
        <Button label="All notes" icon={<Icon icon="chevronLeft" />} onClick={() => { model.save(true); actions.showList(n) }} />
        <Button label={canClear ? "Clear" : "Delete"} icon={<Icon icon={Trash2} />} onClick={() => actions.askDelete()} />
        <Hint keys="escape">list</Hint>
        <Hint keys={["F2", "up"]}>rename</Hint>
        <Hint keys="tab">indents</Hint>
        {status}
      </HStack>
    </VStack>
  )
}
