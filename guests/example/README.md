# suzuri-guest-example

Sample **suzuri guest** plugin. Chrome never links this crate — it is a
separate process that speaks line-delimited JSON over one TCP connection
to `127.0.0.1`.

It sends `hello` on connect, stays up on `spawn` / `resize` / `focus`,
echoes `navigate` as `title` + `url`, and exits on `kill` or socket close.
When chrome sends `fb.path` + size, it paints a `SZFB` file (dark well,
jade rail, URL-tinted bar). Chrome blits those pixels into the mosaic
well — the guest does not open its own window.

JSON is scanned by hand.

## Build

```bash
cd guests/example
cargo test
cargo build --release
```

Binary: `target/release/suzuri-guest-example`.

Chrome starts it with `--suzuri-guest --port N` (or `SUZURI_GUEST_PORT`).

## Manifest

Drop a JSON file in the product guests directory. Point `command` at the
binary you built.

| OS | Path |
| --- | --- |
| macOS | `~/Library/Application Support/suzuri/guests/example.json` |
| Windows | `%LOCALAPPDATA%\suzuri\guests\example.json` |
| Linux | `~/.config/suzuri/guests/example.json` |

```json
{
  "id": "example",
  "name": "Example",
  "command": "/path/to/suzuri-guest-example",
  "protocol": 1,
  "capabilities": ["pane", "navigate"]
}
```

Restart suzuri, then open a guest pane from the palette.
