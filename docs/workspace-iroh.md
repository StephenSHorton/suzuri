# Workspace Iroh sync (opt-in)

Local-first is the default. Channel messages live on one machine under the
suzuri config directory. **Share workspace** / **Join workspace** in chrome
opt into peer-to-peer sync of channel `messages.jsonl` between two workspace
roots, using the same iroh 1.0 stack as `suzuri-transfer`.

Handshake is the same product sentence as file transfer: one side listens
(ticket), the other pastes the ticket and joins. After that the two rooms
are one room. The sidecar also upserts message authors into `members.json`
so remote humans/agents show in the presence strip.

## Enable

CLI stays off unless you opt in:

```
SUZURI_WORKSPACE_IROH=1
```

(`true` / `yes` also work) or pass `--enable` to `suzuri-workspace-sync`.

If neither is set the binary prints a short error and exits 2.

Chrome **Share** / **Join** pass `--enable --json` for you (clicking Link is
the opt-in). Palette: **Share workspace (ticket)…**, **Join workspace…**,
**Disconnect workspace**. Shortcut: Ctrl+Shift+L / ⇧⌘L to share. The **Link**
chip (right of the presence strip) listens and copies a ticket; it turns
**Live** once a peer is connected. Click again to copy the ticket. Sync keeps
running if you close the pane; chrome exit (or **Disconnect workspace**)
stops it.

## What v1 syncs

| Synced | Not synced |
|--------|------------|
| `channels/<slug>/messages.jsonl` | `channels/<slug>/files/` |
| Creates missing channel dirs (`meta.json` + jsonl) | Live availability codes (idle/working/…) |
| Authors from incoming lines → `members.json` (by `from_id`) | `workspace.json` |
| | Path leases / tasks |

## Ticket = pairing

`listen` prints a **single-line JSON `EndpointAddr`** (iroh 1.0 encoding) on
stdout. `join` also accepts a `ticket: ` prefix. Anyone who has the ticket can
`join`. There is **no extra auth** (no contact book, no SAS). Treat the ticket
like a capability: do not post it publicly.

Each process keeps its own secret key as 32 raw bytes in
`<iroh-dir>/identity.secret` (default `--iroh-dir` is `<root>/.iroh`). Two
processes on one machine **must** use distinct dirs so they get distinct
endpoint ids — the per-root default already does that.

## Conflict policy

Append-only jsonl. Dedup by message `id`.

- If an id is already in the local file, the incoming line is **skipped**.
- Last-write does **not** rewrite history.
- Merge of two files is idempotent: unique ids from both sides are kept;
  the first-seen line for an id wins.

## Two-root local demo

Build the binary (iroh 1.0.1 needs rustc 1.91+):

```
cargo build -p suzuri-workspace-sync --manifest-path libs/transfer/Cargo.toml
```

```
# terminal 1
SUZURI_WORKSPACE_IROH=1 suzuri-workspace-sync listen --root /tmp/ws-a
```

```
# terminal 2 (paste ticket)
SUZURI_WORKSPACE_IROH=1 suzuri-workspace-sync join --root /tmp/ws-b --ticket '<ticket>'
```

Then drop a jsonl line into A's general channel and see it appear in B:

```
mkdir -p /tmp/ws-a/channels/general
echo '{"id":"msg_demo1","channel":"general","kind":"text","body":"hello from A"}' >> /tmp/ws-a/channels/general/messages.jsonl
```

Within about a second, `/tmp/ws-b/channels/general/messages.jsonl` should
contain the same id. A line posted on B flows back to A the same way.

Same-machine note: defaults (`<root>/.iroh`) already give A and B distinct
identities. Override with `--iroh-dir` if you need to.

Host wrapper (if `suzuri-workspace-sync` is next to `suzuri`, on `PATH`, or
`SUZURI_WORKSPACE_SYNC_BIN` is set):

```
SUZURI_WORKSPACE_IROH=1 suzuri workspace-sync listen --root /tmp/ws-a
```

You can also run the rust binary directly; the Go command is a thin exec.

Chrome spawns the same binary with `--enable --json`. Stdout is NDJSON:

| Event | Meaning |
|-------|---------|
| `ready` | `ticket` (JSON EndpointAddr string) + `role=listen` |
| `connecting` | join dial started |
| `connected` | `peers` count |
| `peer_left` | `peers` count after a drop |
| `error` | `message` |
| `stopped` | sidecar exiting |

Human traces stay on stderr. `SUZURI_WORKSPACE_SYNC_OUTPUT=json` is the same
as `--json`.

## Out of scope (later)

- File blobs (`files/`)
- Live presence / availability sync
- CRDTs / last-write-wins field merge
- Sync of `workspace.json` / tasks / leases
