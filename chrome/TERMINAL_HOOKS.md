# Terminal hooks — selection + OSC titles

Track D wiring notes for `app.rs` (and any future host). Logic lives in library
modules; the app should stay a thin event → state bridge.

## Modules

| Module | Role |
|--------|------|
| `selection::Selection` | Drag model, `contains`, `text` extraction |
| `cells::CellGrid` | Absolute rows (`viewport_to_abs`, `line_text_abs`, …) |
| `ansi::AnsiDecoder` | `take_cwd()`, **`take_title()`** (OSC 0/2) |
| `session::ChromeSession` | `set_cwd`, **`set_pane_title`** (pane only) |

Do **not** rewrite `app.rs` wholesale — add a `Selection` field (per focused pane
or one shared for the active leaf) and call the hooks below.

---

## 1. OSC titles (after PTY drain)

Today cwd is applied after feed:

```rust
rt.ansi.feed(grid, chunk.as_bytes());
if let Some(cwd) = rt.ansi.take_cwd() {
    self.session.set_cwd(id, cwd);
}
// NEW — same site:
if let Some(title) = rt.ansi.take_title() {
    self.session.set_pane_title(id, title);
}
```

Rules:

- OSC `0` / `2` (and `1`) set `pending_title`; empty payloads are ignored.
- Titles rename the **pane**, not the chrome tab strip (product multi-pane rule).
- Solo tabs may still *display* the pane title in the strip if the UI already
  mirrors `pane.title` — that is a render choice, not a session write to `Tab.title`.

Sequences:

- `ESC ] 0 ; <title> BEL` or `… ST` (`ESC \`)
- `ESC ] 2 ; <title> BEL`

---

## 2. Mouse → cell coordinates

Given terminal pane rect `term` (logical px) and char metrics `(cell_w, cell_h)`:

```text
col = floor((x - term.x) / cell_w).clamp(0, cols - 1)
row = floor((y - term.y) / cell_h).clamp(0, rows - 1)   // viewport row
abs = grid.viewport_to_abs(row)
pos = selection::clamp_pos(grid, col, abs)
```

Only handle hits inside the **cell well** of the focused pane (not warp strip,
tabs, or overlays). If `overlay_open()`, skip selection.

---

## 3. Selection drag model

Hold one `Selection` (recommend: on the app or keyed by `pane_id`).

| Event | Action |
|-------|--------|
| Left **down** in terminal | `sel.begin(pos)` (or multi-click: word/line — see below) |
| Left **move** while dragging | `sel.update(pos)` |
| Left **up** | `sel.end()` |
| Click elsewhere / Escape | `sel.clear()` |
| Scroll wheel while selecting | still absolute rows — update via new `viewport_to_abs` under cursor if desired |

```rust
// mouse down
self.selection.begin(pos);
// mouse move (primary button held)
self.selection.update(pos);
// mouse up
self.selection.end();
```

### Optional multi-click (product parity)

| Clicks | API |
|--------|-----|
| 1 | `begin` / single cell |
| 2 | `selection.select_word(grid, pos)` |
| 3 | `selection.select_line(abs_row, grid.cols())` |

Track click count with a short window (~500 ms) and same-cell tolerance.

---

## 4. Copy to clipboard

When the user presses **Cmd+C** / **Ctrl+C** and a selection is active:

```rust
if !self.selection.is_empty() {
    if let Some(grid) = self.session.grid(focus_pane_id) {
        let text = self.selection.text(grid);
        if !text.is_empty() {
            if let Some(cb) = self.clipboard.as_mut() {
                let _ = cb.set_text(text);
            }
            // Do NOT send ^C to the PTY while a selection is present.
            return;
        }
    }
}
// else: existing interrupt / key handling
```

Paste remains the existing `paste_clipboard` path (Cmd+V / Ctrl+Shift+V).

Right-click copy is optional: if selection non-empty, copy; else paste (product).

---

## 5. Paint highlight (renderer) — done

`Renderer::render` takes `&Selection`; `chrome_labels` passes it only to the
**focused** pane’s `push_pane_cells`. Empty selection is a no-op
(`is_empty` / `contains` false).

Per visible viewport row:

```rust
let abs = grid.viewport_to_abs(view_row);
// contiguous runs where selection.contains(col, abs)
// → TextLabel::mono("█"×n, jade @ ~0.32 alpha) under glyphs
```

Implementation: `push_selection_row` in `renderer.rs` (glyph-block underlay,
same pipeline as ANSI cell bg — no glass/shader fill pass).

### Still open (app / host polish)

| Hook | Notes |
|------|--------|
| Multi-click word/line | `select_word` / `select_line` APIs exist |
| Right-click copy-or-paste | product: copy if selection, else paste |
| Clear on focus / resize / alt-screen | avoid stale ranges across panes |
| Extend while scrolling | re-map cursor via `viewport_to_abs` mid-drag |

---

## 6. Suggested `ChromeApp` fields

```rust
selection: selection::Selection,
// optional: multi-click tracker (count, last cell, Instant)
```

Clear selection on: tab/pane focus change, grid resize, alt-screen enter/leave
(optional), or any key that produces PTY input other than copy/paste shortcuts.

---

## 7. Tests

Library coverage (no app wiring required):

```bash
cd chrome && cargo test selection::
cargo test ansi::tests::osc_
cargo test cells::
cargo test
cargo build --release
```

---

## API cheat sheet

```rust
use suzuri_chrome::selection::{clamp_pos, CellPos, Selection};
use suzuri_chrome::ansi::AnsiDecoder;
use suzuri_chrome::session::ChromeSession;

// Selection
sel.begin(CellPos { col, abs_row });
sel.update(CellPos { col, abs_row });
sel.end();
let s = sel.text(grid);          // newlines between rows
let hit = sel.contains(col, abs_row);

// Grid
let abs = grid.viewport_to_abs(view_row);
let view = grid.abs_to_viewport(abs); // Option<u16>
let line = grid.line_text_abs(abs);

// OSC
let title = ansi.take_title();   // Option<String>
session.set_pane_title(pane_id, title);
```
