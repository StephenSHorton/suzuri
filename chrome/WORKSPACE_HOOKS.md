# Workspace chat hooks (suzuri-chrome)

Track B / Wave 3 Track K of product parity. Implementation lives in:

| Module | Role |
|--------|------|
| `src/workspace_store.rs` | File-backed store (channels, members, messages, file attach) |
| `src/workspace_ui.rs` | Split-pane / modal state + compose + presence + +Agent + topic pin + hit-test |
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

On `WorkspaceUi::open()` chrome calls `join_self()` so `$USER` is registered as a human if not already present (updates `last_seen` when already joined). `join_self` uses `join_quiet` — **the UI does not post a “joined” system line to `#general`**.

Presence chips are **one per member** (never unique-by-name), so `engine` and `engine-2` both show. Extra annotations:

- **not polling** — `status=working` and `last_seen` older than 2 minutes
- **fuse** — this member has an empty `session_id` and another member has the exact same display name

`+ Agent` (right of the strip, or palette **Add agent…**) picks `pm` / `engine` / `content` and copies a kickoff snippet (call `workspace_guide` first, then `workspace_join` → `workspace_claim_role` → `workspace_wait`). Chrome does **not** launch a Grok process.

Connect bar (under the topic pin): **Share** copies an iroh ticket; **Join** pastes theirs. Sentence on the bar explains the current step. Live → **Copy ticket** / **Disconnect**. Palette / ⇧⌘L still work. Sidecar: `workspace_sync.rs`. Product notes: `docs/workspace-iroh.md`.

Channel `meta.json` already has `topic`. Chrome pins it above the chat (does not scroll) and can set it via the pin row, palette **Set channel topic…**, or Ctrl+Shift+T. Writes go through `WorkspaceStore::set_channel_topic` (same atomic `meta.json` rewrite as other workspace files).

## Public API — `WorkspaceUi`

Preserve these for `app` / `renderer`:

### Glass modal

| Method | Notes |
|--------|--------|
| `open` field | Logical open flag |
| `visible()` | `open \|\| present > 0.01` (closing animation) |
| `open()` | Load data (modal path / tests) |
| `dock(pane_id)` | Host in a split-pane leaf (product default) |
| `close()` | Close + clear draft; undocks |
| `toggle()` | |
| `tick(dt)` | Spring `present` + scrim `overlay`; native FS watch + 1s poll reload while open |
| `content_ease()` | Smoothstep of `present` |
| `scrim_alpha()` | Overlay dim `[0, 0.5]` (0 when docked) |
| `animated_modal_rect(win_w, win_h)` | Large centered card (pop-out / tests) |
| `card_rect(win_w, win_h)` | Pane glass when docked, else modal |
| `is_docked()` / `is_modal()` | Surface mode |

### Fields painted by renderer

- `channel: String` — active slug
- `channels: Vec<String>` — list (`general` first)
- `messages: Vec<WsMessage>` — history (oldest → newest)
- `members: Vec<WsMember>` — presence list for paint
- `channel_topic: String` — pinned topic for the active channel
- `draft: String` — compose buffer
- `status: String` — ephemeral feedback
- `mode: ComposeMode` — `Message` \| `NewChannel` \| `AttachPath` \| `PickAgentRole` \| `SetTopic` \| `JoinTicket`
- `scroll: usize` — messages hidden from the end

`WsMessage { id, channel, from, from_kind, kind, body, ts, file, mentions }` — `from` is display name (`from_name` on disk); `file` is `Option<WsFileRef>` for attaches; `mentions` is member ids resolved from `@name` at post time.

`WsMember { id, name, kind, session_id, role, status, status_note, joined_at, last_seen }` — `presence()` returns status or `idle`. `role` is `pm`\|`engine`\|`content` (exclusive; not the display name).

### Presence / members

| Method | Behavior |
|--------|----------|
| `join_self()` | `store.join($USER, "human", "chrome-local:$USER")` — stable session so reopen does not mint a new human; no #general join line |
| `members_strip_text()` / `members_strip_chips()` | One chip per member (humans first; never collapse same display name) |
| `refresh()` | Reload channels + messages + members + topic; status = `"refreshed"` |
| `reload_from_disk(announce)` | Soft reload (no status thrash when `announce=false`); preserves stick-to-bottom |
| `cycle_status()` | Local human: idle→working→waiting→blocked→away→idle via `set_status` |
| `self_status()` | Current local human presence code |
| `presence_strip_rect` | Click target for cycle (title strip; excludes +Agent) |
| `add_agent_chip_rect` / `begin_add_agent` / `pick_agent_role` | +Agent → role → clipboard kickoff |
| `topic_pin_rect` / `begin_set_topic` | Pinned topic above chat; writes `meta.json` |
| `take_pending_clipboard()` | Host copies kickoff after click/Enter |

Store: `list_members`, `join` / `join_quiet`, `claim_role`, `set_status`, `channel_topic`, `set_channel_topic`, `normalize_availability`, `normalize_role`, `next_availability`, `member_chip`, `presence_chip`, `member_is_stale`, `resolve_mentions`, `agent_kickoff_text`.

`join` reuses a member only on a non-empty `session_id` match. Empty session always creates a new member; taken names get a suffix (`engine`, `engine-2`). Join/leave do **not** post system lines to `#general`. `join_quiet` is the same (identity never announces).

### Auto-refresh while open

Native recursive FS watch (`notify` crate — FSEvents / inotify / ReadDirectoryChanges) on the **workspace root**. A background thread sets an atomic dirty flag; `tick` reloads after a short debounce (`WATCH_DEBOUNCE_SECS`, ~50ms) so MCP / other clients’ JSONL posts appear immediately without thrashing the status line or scroll.

If watch setup fails, the existing ~1s poll (`AUTO_REFRESH_INTERVAL_SECS`) is the refresh path. While watch is healthy the 1s poll stays as a safety net (skipped on a tick that already reloaded from watch).

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
`from_id` is the joiner's member id (`join_self` / `post_as`) — never a freshly minted id per message.
`@name` mentions are resolved to member ids and stored on the line as `mentions`. `@name` tokens are resolved to member ids and stored as `mentions`. The picker lists unique members (name + suffix); `engine` and `engine-2` are distinct `@targets`.

### File attach

| Method | Behavior |
|--------|----------|
| `attach_path(path)` | Copy into `channels/<slug>/files/`, post `kind=file` message |
| `begin_attach()` | Compose mode = path entry (Enter uploads) |
| `pick_and_attach()` | Native OS file dialog (`rfd`) then `attach_path`; cancel is a no-op |

- Path string (Ctrl+U), native picker (Ctrl+Shift+U / palette **Attach file…**), and drop all call `attach_path`.
- `app` wires `WindowEvent::DroppedFile` when workspace is open → `attach_path` (first path).
- Picker is blocking (`rfd::FileDialog::pick_file`) — only from an explicit attach action.
- Upload cap: 64 MiB (product `maxUploadBytes`).
- Store API: `WorkspaceStore::upload(channel, src_path, from_name, from_kind, caption)`.

### Hit-test helpers

Layout constants (logical px, shared with paint):

```text
MODAL_PAD          = 14
CHANNEL_LIST_W     = 140
CHANNEL_ROW_H      = 28
CHANNEL_LIST_TOP   = (legacy) first channel row; compact panes hide the rail
COMPOSE_H          = 44
PRESENCE_STRIP_H   = 18               // members line above messages
TOPIC_PIN_H        = 16               // pinned topic (does not scroll)
ADD_AGENT_CHIP_W   = 64               // +Agent at right of presence strip
CONNECT_BAR_H      = 22               // Share/Join bar under topic pin
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

1. Palette action `OpenWorkspace` → close other overlays → `session.split_focused_widget(Vertical, Workspace)` + `workspace_ui.dock(id)` (or focus existing)
2. Esc: if `mode != Message` → `cancel_mode()`; docked pane stays (⌘W closes). Modal path still `close()`
3. Enter → `send()` (completes `@mention` when picker open); printable → `insert_char`; Backspace → `backspace`
4. Tab / Shift+Tab → `tab()` (mention cycle when picker open, else `cycle_channel`); ↑/↓ → scroll
5. Ctrl+R → `refresh()`; Ctrl+Shift+A → `cycle_status()`; Ctrl+N new channel; Ctrl+U attach path; Ctrl+Shift+U / palette `WorkspaceAttachFile` → `pick_and_attach()`; Ctrl+Shift+T / palette `WorkspaceSetTopic` → topic; palette `WorkspaceAddAgent` / +Agent chip → role kickoff; Ctrl+Shift+L / palette `WorkspaceShare` / Link chip → listen + copy ticket; palette `WorkspaceJoin` → paste ticket; palette `WorkspaceDisconnect` → stop sidecar; Ctrl+D ×2 → `request_delete_channel()` (blocks `#general`)
6. Pointer inside docked pane / `card_rect` → keep focused; `try_click` for channel rail / presence / connect bar Share·Join / +Agent / topic pin
7. Outside click on a modal overlay → `close_all_overlays` (does not close a docked workspace pane)
8. `DroppedFile` while workspace open → `workspace_ui.attach_path(path)` (before transfer)
9. Mailbox `refresh_workspace` → soft reload if open

## Build

```bash
cd chrome && cargo test && cargo build --release
```

## Out of scope (later)

Go Join merge rules, MCP `workspace_wait` / inbox, and task/lease store logic — other slices.
