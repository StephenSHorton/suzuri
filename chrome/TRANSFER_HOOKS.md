# Transfer hooks (suzuri-chrome)

Native chrome transfer UX for the `suzuri-transfer` / `hato` sidecar. Product
references: `internal/chrome/transfer.go`, `internal/transfer/*`,
`libs/transfer/docs/machine-mode.md`.

## Modules

| File | Role |
|------|------|
| `src/transfer_ui.rs` | Glass modal state: open/close/tick, path/ticket buffer, status fields |
| `src/transfer_engine.rs` | Binary discovery, process spawn, NDJSON parse, background worker |

`TransferUi` is owned by `ChromeApp` (`app.rs`). The renderer only reads public
fields (`mode`, `buf`, `status`, `ticket`, animation helpers).

## Public API (`TransferUi`) — keep compatible with `app.rs`

| Method / field | Use |
|----------------|-----|
| `open`, `mode`, `buf`, `status`, `ticket` | Render + input |
| `phase`, `done_bytes`, `total_bytes` | Progress parity (optional in UI) |
| `open_send()` / `open_receive()` | Palette actions |
| `close()` | Esc / overlay dismiss (cancels in-flight engine) |
| `tick(dt)` | Animation + **drain engine channel** into status fields |
| `animated_modal_rect` / `content_ease` / `scrim_alpha` / `visible` | Layout |
| `insert_char` / `backspace` / `submit` | Keyboard while prompt open |

Do not rename these without updating `app.rs` and `renderer.rs`.

## Engine binary discovery

Same order as product host (`internal/transfer/resolve.go`) plus monorepo paths:

1. **`SUZURI_TRANSFER_BIN`** — absolute path to the engine
2. **Next to the running executable** — `suzuri-transfer`, then legacy `hato`
3. **Walk up from CWD** — `libs/transfer/target/{release,debug}/suzuri-transfer`
4. **Dev home fallback** — `~/projects/suzuri/libs/transfer/target/release/…`
5. **`PATH`** — `suzuri-transfer`, then `hato`

Missing binary → status line:

```text
suzuri-transfer not found — build libs/transfer or set SUZURI_TRANSFER_BIN
```

Build helper: `./tools/build-transfer.sh`.

## Process model

```
submit()
  └─ spawn_engine(Send|Receive, value)
        └─ thread "suzuri-transfer"
              Command: <bin> --json send <path>
                    or <bin> --json receive <ticket> <dir>
              env: HATO_CONFIG_DIR=…/suzuri/transfer/
                   HATO_OUTPUT=json
              stdout → NDJSON lines → mpsc::Sender<EngineUpdate>
tick(dt)
  └─ try_recv() → update phase / ticket / done / total / status
close() / Drop
  └─ cancel flag → SIGINT then SIGKILL on child
```

- **UI thread never blocks** on `Command::output()`.
- Send stays alive until cancel (ticket must remain shareable).
- Receive uses `~/Downloads` when present, else `$HOME`.

## NDJSON events (machine mode)

Stdout is **one JSON object per line** (`v:1`). Hosts ignore unknown fields.

| Event | Typical fields | UI phase |
|-------|----------------|----------|
| `ready` | `ticket`, `path`, `relays`, `ips` | `ready` — show ticket |
| `receiving` | (optional dir) | `receiving` |
| `progress` | `done`, `total` | `progress` — status bar text |
| `resumed` | `already_had` | folded into progress message |
| `done` | `total_bytes`, `out_dir` | `done` |
| `stopped` | | `stopped` (send cancel / SIGINT) |
| `error` | `code`, `message` | `error` |

Parser is best-effort (no serde); see `transfer_engine::parse_ndjson_line`.

## Config isolation

| Env | Role |
|-----|------|
| `HATO_CONFIG_DIR` | Engine identity root (default: app support `…/suzuri/transfer/`) |
| `HATO_OUTPUT=json` | Same as `--json` |
| `SUZURI_TRANSFER_BIN` | Force engine path |

Default config dir:

- macOS: `~/Library/Application Support/suzuri/transfer/`
- Windows: `%LOCALAPPDATA%\suzuri\transfer\`
- Linux: `${XDG_CONFIG_HOME:-~/.config}/suzuri/transfer/`

Sender and receiver on the **same machine** need distinct config dirs (distinct
iroh identities). Override with `HATO_CONFIG_DIR` for local loop tests.

## Wiring (host / app)

Palette (product names):

- **Send file (ticket)…** → `transfer.open_send()`
- **Receive ticket…** → `transfer.open_receive()`

Suggested key handling while `transfer.open`:

| Key | Action |
|-----|--------|
| printable | `insert_char` (ignored while job running) |
| Backspace | `backspace` |
| Enter | `submit` |
| Esc | `close` (cancels engine) |

Each frame: `transfer.tick(dt)` before render so progress text stays live.

## Parity vs product Go chrome

| Feature | Product (`transfer.go` + host) | Chrome track |
|---------|--------------------------------|--------------|
| Path/ticket prompt | yes | yes |
| NDJSON progress / ticket | yes | yes (status + `ticket` field) |
| Background engine | yes (`transferCtl`) | yes (`EngineJob` thread) |
| Missing binary message | toast | status line |
| Copy ticket (`c` / click) | yes | not yet (ticket shown; host can copy) |
| OS drag-drop send | yes | not yet |
| Progress bar glyph | yes | text `%` / bytes in `status` |

## Tests

```sh
cd chrome && cargo test --lib transfer_engine 2>/dev/null || \
  cargo test -p suzuri-chrome parse_
```

Unit tests cover NDJSON `ready` / `progress` / `error` parsing in
`transfer_engine.rs`.
