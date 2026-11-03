# PTY replay corpus

Raw PTY output recorded with `tools/ptyrec`. `TestPTYReplayGoldens`
(`internal/ui/pty_replay_test.go`) feeds each stream through `tab.ingestPTY`
at full speed, in the chunks it was read in, and compares the final grid,
cursor, alt-screen bit and scrollback to `<name>.golden`.
`BenchmarkFeed_*` uses the same streams (docs/perf.md).

Each stream is three files:

- `<name>.bin` holds the bytes exactly as read from the PTY.
- `<name>.json` has `cols` and `rows` (the starting size), `chunks` (the
  length of each read, in order), and `resizes`. A resize
  `{"chunk": i, "offset": B, "cols": N, "rows": M}` applies before chunk
  `i`, which is the same point as byte offset `B`.
- `<name>.golden` is the expected end state.

| Stream | Source |
|---|---|
| `claude-idle` | Claude Code v2.1.316 started and left idle at its prompt |
| `claude-stream` | Claude Code streaming a 50-line answer |
| `claude-diff` | Claude Code editing `hello.py` and showing the diff |
| `claude-resize` | Claude Code answering, then resized 100x30 → 60x30 → 60x20 → 100x26 |
| `claude-wipe-synth` | Synthetic: the older Claude Code inline resize wipe (`CSI 3J`), from the `claudeInlineFrame` and `claudeResizeWipe` helpers. The current release redraws with `CSI 2K` per row instead (see `claude-resize`) |
| `grok-signin` | Grok's TUI at its device sign-in screen (8 s of the animated alt-screen page). This Mac's Grok isn't signed in, so there's no streaming Grok session yet |
| `vim` | `vim -u NONE`: number, insert, search, scroll, `:vsplit` |
| `htop` | `htop` showing only itself, re-sorted once |
| `cat-large` | `cat` of a generated 12,000-line UTF-8 file |

All were recorded at 100x30 on macOS (Claude Code ran with `--model haiku`).

## Recording

```sh
go build -o /tmp/ptyrec ./tools/ptyrec
/tmp/ptyrec -out internal/ui/testdata/pty/NAME -cols 100 -rows 30 \
  -script steps.txt -- program args...
```

The script steps are `send "<go-quoted>"`, `sleep 2s`, `quiet <idle> <max>`,
`expect "<text>" <max>` and `resize <cols> <rows>`. See the comment at the top
of `tools/ptyrec/main.go`.

`ptyrec` replaces the working directory and the user name with same-length
placeholders. Use `-scrub old=new` for anything else, and grep the `.bin` for
personal paths, names and emails before committing.

After adding a stream, or after an intended change to the ingest path:

```sh
go test ./internal/ui -run TestPTYReplay -update-replay
```

Then read the golden diff.

## What the goldens show about today's path

The scrollback section is empty in every golden. `scrollback.noteScreen`
only detects a scroll when the screen's old bottom row reappears one row up.
Output that ends in a newline leaves a blank bottom row, so the match never
holds, and lines scrolled off by `ingestPTY` alone never reach history. The
goldens pin that as the vt10x baseline. They are not a statement that it is
right.
