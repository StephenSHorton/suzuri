# libs/transfer — P2P file transfer engine

Rust/iroh engine used by the **suzuri** host for peer-to-peer file send/receive.

| Binary | Role |
|--------|------|
| `suzuri-transfer` | Preferred name when shipped next to suzuri |
| `hato` | Same code (legacy name; still builds) |

The Go host shells out with `--json` (NDJSON). Protocol: [`docs/machine-mode.md`](docs/machine-mode.md). Product UX: [`docs/transfer.md`](../../docs/transfer.md).

## Crates

| Crate | Role |
|-------|------|
| `hato-core` | send/receive, identity, contacts, offers |
| `hato-cli` | CLI entrypoints |
| `hato-code` | SPAKE2 short codes / pair |
| `hato-mailbox` | WebSocket rendezvous (dev / self-host) |

Crate names still say `hato_*` for historical path continuity; rename later if desired.

## Build

```sh
# from repo root
cargo build --release --manifest-path libs/transfer/Cargo.toml -p hato-cli
# → libs/transfer/target/release/suzuri-transfer  (and hato)

# dev helper (copies next to a local suzuri binary)
./tools/build-transfer.sh
```

## Config

Engine still honors `HATO_CONFIG_DIR` / `HATO_MAILBOX` (set by the host to `…/suzuri/transfer/`).

## History

Imported from [StephenSHorton/hato](https://github.com/StephenSHorton/hato) (standalone app discontinued). Source of truth is this tree.
