# Workspace Iroh sync (opt-in)

Local-first is the default. Channel messages live on one machine under the
suzuri config directory. This slice adds **opt-in** peer-to-peer sync of
channel `messages.jsonl` between two workspace roots, using the same iroh 1.0
stack as `suzuri-transfer`.

It is **not** product UX (no chrome panel, no auto-start, not in release
packaging). Run the Rust binary directly.

## Enable

Sync stays off unless you opt in:

```
SUZURI_WORKSPACE_IROH=1
```

(`true` / `yes` also work) or pass `--enable` to `suzuri-workspace-sync`.

If neither is set the binary prints a short error and exits 2.

## What v1 syncs

| Synced | Not synced |
|--------|------------|
| `channels/<slug>/messages.jsonl` | `members.json` |
| Creates missing channel dirs (`meta.json` + jsonl) | `channels/<slug>/files/` |
| | Presence / availability |
| | `workspace.json` |

## Ticket = pairing

`listen` prints a **single line** to stdout:

```
ticket: <JSON EndpointAddr>
```

Anyone who has the ticket can `join`. There is **no extra auth** (no contact
book, no SAS). Treat the ticket like a capability: do not post it publicly.

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

Build the binary:

```
cargo build -p suzuri-workspace-sync --manifest-path libs/transfer/Cargo.toml
BIN=libs/transfer/target/debug/suzuri-workspace-sync
```

Terminal 1 (listen). The `ticket: …` line is greppable on stdout:

```
# terminal 1
$BIN listen --root /tmp/ws-a --enable
```

Terminal 2 (paste the JSON after `ticket: `):

```
# terminal 2
$BIN join --root /tmp/ws-b --ticket '<json>' --enable
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

Go host wiring is intentionally skipped in this slice — run the rust binary.

## Out of scope (this slice)

- Chrome / workspace-panel UX
- File blobs (`files/`)
- Presence
- CRDTs / last-write-wins field merge
- Sync of members or workspace metadata
- Release packaging / `suzuri workspace-sync` host wrapper
