# Workspace chat hooks (suzuri-chrome)

Track B of product parity. Implementation lives in:

| Module | Role |
|--------|------|
| `src/workspace_store.rs` | File-backed store (channels + `messages.jsonl`) |
| `src/workspace_ui.rs` | Glass modal state + compose + hit-test |
| `src/app.rs` | Keys, palette action, click routing |
| `src/renderer.rs` | Paint labels inside animated modal |

Product references: `internal/workspace/`, `internal/chrome/workspace.go`.

## Data layout

macOS root:

```text
~/Library/Application Support/suzuri/workspace/
  workspace.json
  members.json
  channels/<slug>/
    meta.json
    messages.jsonl
```

Linux / other: `$XDG_CONFIG_HOME/suzuri/workspace` or `~/.config/suzuri/workspace`.

`messages.jsonl` lines are product-shaped JSON objects:

```json
{"id":"msg_…","channel":"general","ts":"2026-08-07T17:58:03Z","from_id":"m_…","from_name":"alice","from_kind":"human","kind":"text","body":"hello"}
```

Chrome also **reads** the older minimal shape `{"from","body","ts"}` for forward compatibility.

Human display name: `$USER` (fallback `$USERNAME`, then `"human"`).

## Public API — `WorkspaceUi`

Preserve these for `app` / `renderer`:

### Glass modal

| Method | Notes |
|--------|--------|
| `open` field | Logical open flag |
| `visible()` | `open \|\| present > 0.01` (closing animation) |
| `open()` | Open + reload channels/history |
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
- `draft: String` — compose buffer
- `status: String` — ephemeral feedback
- `mode: ComposeMode` — `Message` \| `NewChannel`
- `scroll: usize` — messages hidden from the end

`WsMessage { id, channel, from, from_kind, kind, body, ts }` — `from` is display name (`from_name` on disk).

### Channels

| Method | Behavior |
|--------|----------|
| `select_channel(name)` | Switch + reload history |
| `create_channel(name)` | Ensure dir/meta/jsonl; select |
| `begin_new_channel()` | Compose mode = name entry |
| `cancel_mode()` | Back to message compose |
| `cycle_channel(delta)` | Tab-style prev/next |
| `refresh()` | Reload from disk |

### Compose

| Method | Behavior |
|--------|----------|
| `insert_char` / `backspace` | Draft edit |
| `send()` | Post text **or** create channel (by mode) |

Posts append one product JSONL line under `channels/<slug>/messages.jsonl`.

### Hit-test helpers

Layout constants (logical px, shared with paint):

```text
MODAL_PAD        = 14
CHANNEL_LIST_W   = 140
CHANNEL_ROW_H    = 28
CHANNEL_LIST_TOP = MODAL_PAD + 18   // first row under title
COMPOSE_H        = 44
```

| Method | Returns |
|--------|---------|
| `channel_list_rect(win_w, win_h)` | Left rail |
| `channel_row_rect(i, …)` | Row for channel index `i` |
| `new_channel_row_rect(…)` | “+ New” under last channel |
| `channel_at(x, y, …)` | `Some(slug)` if hit |
| `hits_new_channel(x, y, …)` | “+ New” hit |
| `try_click(x, y, …)` | Select / begin create / absorb click inside modal |

`app` should call `try_click` on mouse-up when the workspace modal is open (do **not** dismiss on interior clicks).

### Scroll helpers

- `visible_messages(max)` — slice for paint
- `scroll_up` / `scroll_down`

## App wiring checklist

1. Palette action `OpenWorkspace` → close other overlays → `workspace_ui.open()`
2. Esc: if `mode != Message` → `cancel_mode()`; else `close()`
3. Enter → `send()`; printable → `insert_char`; Backspace → `backspace`
4. Pointer inside `animated_modal_rect` → keep open; `try_click` for channel rail
5. Outside click → `close()` (via `close_all_overlays`)

## Build

```bash
cd chrome && cargo build --release
```

## Out of scope (later)

- Members presence strip / `@` mentions
- File attach (`workspace.Upload`)
- Delete channel
- Live FS watch / MCP refresh polling
