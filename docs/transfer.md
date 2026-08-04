# Transfer

Peer-to-peer file transfer inside suzuri. The **engine** is Rust (`suzuri-transfer`, same code as [hato](https://github.com/StephenSHorton/hato)); the **host** is Go.

## CLI

| Command | Behavior |
|---------|----------|
| `suzuri send <path>` | Import + serve; prints ticket on stdout; Ctrl+C stops |
| `suzuri receive <ticket> [dir]` | Download into `dir` (default `.`); resumable |
| `suzuri transfer version` | Engine binary path + config dir |
| `suzuri transfer me` | Local display name + endpoint id |

## Finding the engine

1. `SUZURI_TRANSFER_BIN` (absolute path)
2. Sibling of the running `suzuri` binary (`suzuri-transfer` or legacy `hato`)
3. `PATH`

Dev example:

```sh
cargo build -p hato-cli --manifest-path ~/projects/hato/Cargo.toml
cp ~/projects/hato/target/debug/suzuri-transfer ./suzuri-transfer
go build -o suzuri ./cmd/suzuri
./suzuri send ./file.bin
```

## Config

| Env | Role |
|-----|------|
| `HATO_CONFIG_DIR` | Override engine config root (default: `<suzuri config>/transfer/`) |
| `SUZURI_TRANSFER_BIN` | Force engine path |

Default config dir:

- macOS: `~/Library/Application Support/suzuri/transfer/`
- Windows: `%LOCALAPPDATA%\suzuri\transfer\`

Sender and receiver on the **same machine** need different config dirs (distinct identities). For two terminals on one Mac:

```sh
HATO_CONFIG_DIR=/tmp/suzuri-a suzuri send ./f
HATO_CONFIG_DIR=/tmp/suzuri-b suzuri receive "$TICKET" /tmp/out
```

## Protocol

Host ↔ engine uses NDJSON (`--json`). See [hato machine-mode](https://github.com/StephenSHorton/hato/blob/main/docs/machine-mode.md).

## GUI (command palette)

| Command | Flow |
|---------|------|
| **Send file (ticket)…** | Path prompt → prepare → ticket panel (`c` copy, `esc` stop) |
| **Receive ticket…** | Ticket prompt → download into `~/Downloads` (or home) with progress |

Keep suzuri open while serving. Engine missing → toast from host when start fails.

## Status

- [x] CLI send / receive (raw tickets)
- [x] Palette / progress panel in the GUI
- [ ] Bundle `suzuri-transfer` in release installers + multi-file updater
- [ ] Contacts / pair / listen (needs mailbox)
- [ ] Short codes
