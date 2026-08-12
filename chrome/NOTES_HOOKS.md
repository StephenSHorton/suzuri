# Notes bank — app / renderer hooks

Primary module: [`src/notes.rs`](src/notes.rs)  
Pure bank ops: [`src/notes_ops.rs`](src/notes_ops.rs)  
Product refs: `internal/notes/` (disk bank + MCP offline ops); UI in this crate

## Product shape

- Disk file: `notes.json` next to product config  
  - macOS: `~/Library/Application Support/suzuri/notes.json`
- JSON: `{ "active_id": "<id>", "notes": [ { "id", "title", "body", "updated?" } ] }`
- Bank never empty; deleting the last note **clears** title/body instead of removing it.
- Max bank: 48 notes; body capped at 64Ki runes.

## `NotesState` API (stable for `app.rs`)

| Method / field | Role |
|----------------|------|
| `open` / `close` / `toggle` / `tick` | Overlay lifecycle; **close flushes + saves** |
| `visible` / `content_ease` / `scrim_alpha` / `animated_modal_rect` | Animation + modal geometry |
| `body` / `title` / `cursor` / `focus` | Live editor buffers |
| `bank()` / `active_index()` / `active_id()` | List data |
| `select(i)` / `new_note()` / `delete_active()` | Bank mutations (dirty) |
| `try_click(x,y,win_w,win_h)` | List / + New / Delete / title / body hit-test |
| `insert_char` / `backspace` / `move_cursor` | Edit **title or body** per `focus` |
| `set_focus` / `cycle_focus` | Title ↔ Body; typing always hits the focused field |
| `undo` / `redo` / `can_undo` / `can_redo` | Body-only history stack (`BODY_HIST_LIMIT` ≈ 50) |
| `layout(win_w,win_h)` / `refresh_layout` | Shared hit-test geometry |
| `is_dirty()` / `save()` / `snapshot()` | Persistence |
| `with_path(path)` | Injectable store for tests |
| `display_title_for(i)` / `active_display_title()` | List labels (title → first body line → Untitled) |
| `display_lines()` | Text fallback summary |

## Layout helpers (paint + hit-test)

Use **one** geometry source so clicks match glass panels:

```text
NotesState::layout(win_w, win_h) -> NotesLayout {
  modal, list, list_rows[], new_row, delete_row, title, body
}
```

Constants: `NOTES_PAD`, `NOTES_LIST_W`, `NOTES_ROW_H`, `NOTES_TITLE_H`, `NOTES_GAP`.

- **Renderer panels**: frost rects for `list`, `title`, `body` from `layout()`.
- **Renderer labels**: row titles via `display_title_for`; caret only on focused field.
- **App click**: `notes.try_click(cursor.x, cursor.y, win_w, win_h)` while notes modal is open (already wired). Outside-click still dismisses via `pointer_in_open_modal`.

## Keyboard (app)

Already routed when `notes.open` and no super/ctrl:

- Printable → `insert_char` (title if `focus == Title`, else body)
- Backspace / ← / → → `backspace` / `move_cursor`
- Enter in title → commit focus to body; in body → newline
- Tab / Shift-Tab → `cycle_focus` Title ↔ Body

With super/ctrl while notes open:

- ⌘Z / Ctrl+Z → `undo` (body)
- ⇧⌘Z / Ctrl+Shift+Z → `redo` (body)

Optional later:

- Cmd/Ctrl+Backspace or a confirm dialog → `delete_active` (UI row already calls it on click)

## Dirty / save

- Edits and active-id changes set `dirty`.
- `close()` / Esc path: `flush_active` + atomic write (`notes.json.tmp` → rename).
- Always write on close so `active_id` stays product-compatible even if body unchanged.

## Body undo / redo

- Stack of `{text, cursor}` snapshots pushed **before** each body mutation (`insert_char` / `backspace` on body).
- Coalesces consecutive identical snapshots; new edit after undo clears redo.
- Cleared on `select` / `new_note` / `delete_active` (document switch).
- Title edits do not push; undo still restores body and moves focus to Body.
- Product ref: `internal/textedit/history.go` + `notesPushUndo` / `notesUndo` / `notesRedo`.

## Tests

```bash
cd chrome && cargo test notes
```

- `notes_ops`: create / update / delete / find / full bank / display title  
- `notes`: temp-path round-trip, multi-note bank, layout hit regions, parse product JSON, body undo/redo, title/body focus  

## Out of scope (parity polish later)

- Drag-select, word/line click counts  
- Full list-only screen (chrome keeps split list+editor)  
- Confirm dialog before delete (product has confirm; chrome deletes on list row click)  
