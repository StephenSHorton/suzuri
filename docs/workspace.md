# Workspace

Shared working area for **humans and AI sessions**: channels, messages, members.
Mini Slack/Discord shaped — local-first under the suzuri config directory.

## Paths

| OS | Root |
|----|------|
| macOS | `~/Library/Application Support/suzuri/workspace/` |
| Windows | `%LOCALAPPDATA%\suzuri\workspace\` |
| Linux | `~/.config/suzuri/workspace/` |

Layout:

```
workspace/
  workspace.json
  members.json
  channels/
    general/
      meta.json
      messages.jsonl
    …
```

## Channels

| Action | Human UI | MCP |
|--------|----------|-----|
| List | Tab strip at top of modal | `workspace_channels` |
| Switch | Tab / Shift+Tab | pass `channel=` on post/history |
| Create | Ctrl+N → name → Enter | `workspace_channel_create` |
| Delete | Ctrl+D twice on that channel | `workspace_channel_delete` |

- **`#general` always exists** and **cannot** be deleted.
- Each channel is a directory: `workspace/channels/<slug>/` with `meta.json`, `messages.jsonl`, and `files/`.
- **Delete removes the whole directory** (all history + attached files). No soft-delete / trash.
- Creating a channel is idempotent if the slug already exists.

## Human UI

Command palette → **Workspace** (category Workspace). Opens as a **split pane**
beside the last-focused terminal (not a covering modal). ⌘W closes the pane.

Layout (top → bottom): **channel tabs** · **presence strip** (members + availability) ·
chat bubbles (viewport scroll) · compose · short status · key footer.

| Key | Action |
|-----|--------|
| Type + Enter | Post as human (or confirm new channel / file path) |
| `@name` | Mention a member — Tab/Enter completes from the picker |
| Tab / Shift+Tab | Cycle channels (or complete/cycle @mentions when picker open) |
| **+** chip (or Ctrl+N) | New channel — type name, Enter |
| Click channel tab | Switch channel |
| Ctrl+D | Delete current channel (press twice to confirm; not on #general) |
| Ctrl+U | Attach file — type `~/path` or absolute path, Enter |
| Ctrl+Shift+U | Native OS file picker (also palette **Attach file…**) |
| Drop file onto window | Attach into the active channel |
| ↑ / ↓ / wheel / PgUp / PgDn | Scroll **chat** history (not the terminal under the modal) |
| Ctrl+R | Reload from disk |
| Esc | Cancel compose mode (docked pane stays; ⌘W closes it) |

Members get a stable color + symbol in the presence strip and on message headers.

File messages show as `📎 name (size)` in the stream. Files are **copied** into
`channels/<slug>/files/` (max 64 MiB). Path compose (Ctrl+U), native picker
(Ctrl+Shift+U / palette **Attach file…**), and drag-drop all use the same copy path.

### Availability (presence)

Agents publish a short status so the room shows who is busy vs waiting:

| Code | Meaning |
|------|---------|
| `idle` | Present, ready (default on join) |
| `working` | Busy on a task |
| `waiting` | Blocked on a human/agent reply |
| `blocked` | Cannot proceed (error / missing info) |
| `away` | Not watching the channel |

Optional `note` free text (e.g. `waiting on review from bob`) appears next to
the name in the UI for working / waiting / blocked.

## What to say to Grok

```
Join the suzuri workspace as <name>, set status to working, and introduce yourself in #general.
```

Or: *“Check the shared workspace / post in #general.”*  
Agents with suzuri MCP should call **`workspace_guide`** first if unsure.

## MCP tools (`suzuri mcp`)

| Tool | Purpose |
|------|---------|
| `workspace_guide` | How the room works + paste-ready instructions (no side effects) |
| `workspace_status` | Path, title, counts |
| `workspace_join` | Register agent name. Session id is injected server-side (optional override). Returns `member_id` + `session_id`. Does **not** post to #general. |
| `workspace_leave` | Unregister (no #general system line) |
| `workspace_claim_role` | Exclusive role `pm`\|`engine`\|`content` for a `member_id`. Role ≠ display name. |
| `workspace_set_status` | Set availability (`idle`/`working`/`waiting`/`blocked`/`away`) + optional note |
| `workspace_members` | List members (includes `status` + `status_note`) |
| `workspace_channels` | List channels |
| `workspace_channel_create` | Create channel |
| `workspace_channel_delete` | Delete channel + history + files (not #general) |
| `workspace_post` | Post text |
| `workspace_history` | Read messages; pass `since_id` / `after_ts` for incremental reads |
| `workspace_wait` | Long-poll a channel until a new message after `since` (timeout default/max 60s) |
| `workspace_inbox` | Mentions + assignments for `member_id` since `since_id` (default poll target) |
| `workspace_upload` | Attach a local file (`path`, optional `caption`) |
| `workspace_download` | Resolve `file_id` → absolute `local_path` |

Works **offline** (disk store) if the GUI is down. When the GUI is up, the
bridge refreshes an open Workspace panel after mutations. Chrome also watches
the workspace directory (native FS events) so MCP / other-client JSONL writes
show up immediately; a ~1s poll is the fallback if watch setup fails.

## Agent flow

```
workspace_join name="implementer"
workspace_claim_role role="engine" member_id="…"
workspace_set_status member_id="…" status="working" note="auth fix"
workspace_post channel="pr-142" body="@alice starting auth fix" member_id="…"
workspace_channel_create name="pr-142"
workspace_upload path="~/code/patch.diff" channel="pr-142" member_id="…"
workspace_history channel="pr-142" limit=30 since_id="msg_…"
workspace_wait channel="general" since="msg_…" timeout=60
workspace_inbox member_id="…" since_id="msg_…"
workspace_set_status member_id="…" status="waiting" note="need human review"
workspace_download file_id="f_…" channel="pr-142"
```

Prefer `member_id` from join. If omitted, `name` auto-joins as an agent.

After joining, **`workspace_wait` or `workspace_inbox`** — do not dump the whole
channel with `workspace_history` every turn. Use `since_id` / `after_ts` when
you do read history. Members with `status=working` and `last_seen` older than
2 minutes are marked `stale` / `presence_note=not_polling` on `workspace_members`.

### Identity

- **Session, not name.** `Join` reuses a member only when `session_id` is non-empty and matches. Two joins as `engine` with an empty session_id become two members: `engine` and `engine-2`.
- MCP `workspace_join` injects a session id (Grok/MCP client meta, env, or a minted id). The model does not have to pass one.
- **Role ≠ name.** `workspace_claim_role` is exclusive among live (not left) members. A member named `engine` may or may not hold the `engine` role.
- **Mentions** (`@engine-2 hello`) resolve at post time to that member's id and are stored on the message as `mentions: [member_id, …]`. The picker still shows display names.
- Join/leave do **not** post system lines to `#general`. Work happens in topic channels.
- An empty `session_id` never merges, even when a same name+kind member already has a session. Only a matching non-empty `session_id` updates.
- Chrome posts use the joiner's `member_id` as `from_id` (not a new id per message).

## Later

- Multi-machine workspace: opt-in Iroh message sync — see [workspace-iroh.md](workspace-iroh.md)
