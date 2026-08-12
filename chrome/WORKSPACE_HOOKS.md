# Workspace chat hooks (suzuri-chrome)

Track B / Wave 3 Track K of product parity. Implementation lives in:

| Module | Role |
|--------|------|
| `src/workspace_store.rs` | File-backed store (channels, members, messages, file attach) |
| `src/workspace_ui.rs` | Glass modal state + compose + presence + hit-test |
| `src/app.rs` | Keys, palette action, click routing, drop → attach |
| `src/renderer.rs` | Paint labels inside animated modal (incl. members strip) |

Product references: `internal/workspace/` (Go store); UI paint in this crate.

## Data layout

macOS root:

```text
~/Library/Application Support/suzuri/workspace/
  workspace.json
  members.json
  channels/<slug>/
    meta.json
    messages.jsonl
    files/<file_id>_<safe_name>
```

Linux / other: `$XDG_CONFIG_HOME/suzuri/workspace` or `~/.config/suzuri/workspace`.

`messages.jsonl` lines are product-shaped JSON objects:

```json
{"id":"msg_…","channel":"general","ts":"2026-08-07T17:58:03Z","from_id":"m_…","from_name":"alice","from_kind":"human","kind":"text","body":"hello"}
```

File messages add `"kind":"file"` and a nested `file` object:

```json
{"kind":"file","body":"notes.txt","file":{"id":"f_…","name":"notes.txt","bytes":12,"sha256":"","rel_path":"channels/general/files/f_…_notes.txt"}}
```

Chrome also **reads** the older minimal shape `{"from","body","ts"}` for forward compatibility.

Human display name: `$USER` (fallback `$USERNAME`, then `"human"`).

### Members / presence

`members.json` is a JSON array of product `Member` objects:

```json
[{"id":"m_…","name":"alice","kind":"human","status":"idle","joined_at":"…","last_seen":"…"}]
```

Availability codes (simple presence for paint): `idle` · `working` · `waiting` · `blocked` · `away`.

On `WorkspaceUi::open()` chrome calls `join_self()` so `$USER` is registered as a human if not already present (updates `last_seen` when already joined).

## Public API — `WorkspaceUi`

Preserve these for `app` / `renderer`:

### Glass modal

| Method | Notes |
|--------|--------|
| `open` field | Logical open flag |
| `visible()` | `open \|\| present > 0.01` (closing animation) |
| `open()` | Open + join self + reload channels/history/members |
| `close()` | Close + clear draft |
| `toggle()` | |
| `tick(dt)` | Spring `present` + scrim `overlay` |
| `content_ease()` | Smoothstep of `present` |
| `scrim_alpha()` | Overlay dim `[0, 0.5]` |
| `animated_modal_rect(win_w, win_h)` | Centered card with scale-in |

### Fields painted by renderer

- `channel: String` — active slug
- `channels: Vec<String>` — list (`general` first)
- `messages: Vec<WsMessage>` — history (oldest → newest)
- `members: Vec<WsMember>` — presence list for paint
- `draft: String` — compose buffer
- `status: String` — ephemeral feedback
- `mode: ComposeMode` — `Message` \| `NewChannel` \| `AttachPath`
- `scroll: usize` — messages hidden from the end

`WsMessage { id, channel, from, from_kind, kind, body, ts, file }` — `from` is display name (`from_name` on disk); `file` is `Option<WsFileRef>` for attaches.

`WsMember { id, name, kind, session_id, status, status_note, joined_at, last_seen }` — `presence()` returns status or `idle`.

### Presence / members

| Method | Behavior |
|--------|----------|
| `join_self()` | `store.join($USER, "human", "")` |
| `members_strip_text()` | One-line chip list (humans first; soft cap + overflow) |
| `refresh()` | Reload channels + messages + members; status = `"refreshed"` |
| `reload_from_disk(announce)` | Soft reload (no status thrash when `announce=false`); preserves stick-to-bottom |
| `cycle_status()` | Local human: idle→working→waiting→blocked→away→idle via `set_status` |
| `self_status()` | Current local human presence code |
| `presence_strip_rect` | Click target for cycle (title strip) |

Store: `list_members`, `join`, `set_status`, `normalize_availability`, `next_availability`, `member_chip`.

### Auto-refresh while open

`WorkspaceUi::tick` accumulates time; every ~1s (`AUTO_REFRESH_INTERVAL_SECS`) while `open`, calls `reload_from_disk(false)` so MCP / other clients’ JSONL posts appear without thrashing the status line or scroll.

Manual: **Ctrl+R** (⌘R) while open → `refresh()`. Mailbox: `refresh_workspace` → `CommandAction::RefreshWorkspace` (soft no-op if closed).

### Status cycle

- **Ctrl+Shift+A** (⇧⌘A) while workspace open
- Palette: **Cycle workspace status**
- Click presence strip

### Channels

| Method | Behavior |
|--------|----------|
| `select_channel(name)` | Switch + reload history |
| `create_channel(name)` | Ensure dir/meta/jsonl; select |
| `request_delete_channel()` | Ctrl+D ×2 confirm; blocks `#general` |
| `confirm_delete_channel()` | Store delete + select `#general` |
| `begin_new_channel()` | Compose mode = name entry |
| `cancel_mode()` | Back to message compose |
| `cycle_channel(delta)` | Tab-style prev/next |
| `refresh()` | Reload from disk |

### Compose

| Method | Behavior |
|--------|----------|
| `insert_char` / `backspace` | Draft edit |
| `send()` | Complete `@mention` if picker open; else post / create / attach |
| `tab(shift)` | Cycle `@mention` picker when open; else `cycle_channel` |

Posts append one product JSONL line under `channels/<slug>/messages.jsonl`.

### File attach

| Method | Behavior |
|--------|----------|
| `attach_path(path)` | Copy into `channels/<slug>/files/`, post `kind=file` message |
| `begin_attach()` | Compose mode = path entry (Enter uploads) |

- No OS file dialog required — path string is enough.
- `app` wires `WindowEvent::DroppedFile` when workspace is open → `attach_path` (first path).
- Upload cap: 64 MiB (product `maxUploadBytes`).
- Store API: `WorkspaceStore::upload(channel, src_path, from_name, from_kind, caption)`.

### Hit-test helpers

Layout constants (logical px, shared with paint):

```text
MODAL_PAD          = 14
CHANNEL_LIST_W     = 140
CHANNEL_ROW_H      = 28
CHANNEL_LIST_TOP   = MODAL_PAD + 18   // first row under title
COMPOSE_H          = 44
PRESENCE_STRIP_H   = 18               // members line above messages
```

| Method | Returns |
|--------|---------|
| `channel_list_rect(win_w, win_h)` | Left rail |
| `channel_row_rect(i, …)` | Row for channel index `i` |
| `new_channel_row_rect(…)` | “+ New” under last channel |
| `channel_at(x, y, …)` | `Some(slug)` if hit |
| `hits_new_channel(x, y, …)` | “+ New” hit |
| `try_click(x, y, …)` | Select / begin create / absorb click inside modal |

`app` should call `try_click` on mouse-up when the workspace modal is open (do **not** dismiss on interior clicks). Channel list geometry is unchanged by the presence strip (strip paints in the message pane only).

### Scroll helpers

- `visible_messages(max)` — slice for paint
- `scroll_up` / `scroll_down`

## App wiring checklist

1. Palette action `OpenWorkspace` → close other overlays → `workspace_ui.open()`
2. Esc: if `mode != Message` → `cancel_mode()`; else `close()`
3. Enter → `send()` (completes `@mention` when picker open); printable → `insert_char`; Backspace → `backspace`
4. Tab / Shift+Tab → `tab()` (mention cycle when picker open, else `cycle_channel`); ↑/↓ → scroll
5. Ctrl+R → `refresh()`; Ctrl+Shift+A → `cycle_status()`; Ctrl+N new channel; Ctrl+U attach path; Ctrl+D ×2 → `request_delete_channel()` (blocks `#general`)
6. Pointer inside `animated_modal_rect` → keep open; `try_click` for channel rail / presence strip
7. Outside click → `close()` (via `close_all_overlays`)
8. `DroppedFile` while workspace open → `workspace_ui.attach_path(path)` (before transfer)
9. Mailbox `refresh_workspace` → soft reload if open

## Build

```bash
cd chrome && cargo test && cargo build --release
```

## Out of scope (later)

- Native FS watch (polling ~1s is enough for MCP attach)
- OS file picker dialog (path string + drop is enough)
