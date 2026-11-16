# Performance baselines

## PTY Feed (replay corpus)

The replay corpus in `internal/ui/testdata/pty` holds raw PTY byte streams
(see its README). These benchmarks feed a stream through the same path a live
pane uses, in the chunks it was read in, and report bytes per second.

| Benchmark | Path | What it measures |
|---|---|---|
| `BenchmarkFeed_ClaudeStream` | `tab.ingestPTY` (protocol filters, screen snapshots, scrollback, vt10x) | Claude Code streaming a 50-line answer |
| `BenchmarkFeed_CatLarge` | `tab.ingestPTY` | `cat` of a 488 KB text file |
| `BenchmarkFeed_ClaudeStreamVT10xOnly` | `vt10x.Terminal.Write` only | The same Claude chunks with no pipeline around the parser |

Run:

```sh
go test ./internal/ui -run '^$' -bench BenchmarkFeed -benchtime 3s -count 5
```

### Baseline: vt10x, 2026-11-02

Apple M5 Max, macOS (darwin/arm64), Go 1.27.1, 5 runs of 3 s each. Medians:

| Benchmark | Stream | ns/op | MB/s | B/op | allocs/op |
|---|---|---|---|---|---|
| `BenchmarkFeed_ClaudeStream` | claude-stream, 58,226 B in 128 chunks | 14,295,394 | 4.07 | 3,449,420 | 110,714 |
| `BenchmarkFeed_CatLarge` | cat-large, 499,916 B in 489 chunks | 76,747,620 | 6.51 | 19,841,003 | 977,898 |
| `BenchmarkFeed_ClaudeStreamVT10xOnly` | claude-stream | 944,590 | 61.64 | 1,140,545 | 93,233 |

What the numbers say:

- The pipeline around vt10x costs about 15 times what vt10x does on Claude
  output (4.1 vs 61.6 MB/s). Most of it is per-chunk work that is
  proportional to the grid, not the bytes. `ingestPTY` copies the whole
  screen as text up to three times per chunk (`snapshotScreenText` before the
  write, for `modes.observe` and for `scrollback.noteScreen`).
- At 4 MB/s, a pane streaming Claude output at a few KB/s is far from the
  limit. Eight panes plus a `cat` of a large file is where it shows.

Windows numbers are still to be added on the PC (same command).

### Not yet measured

- The windows/amd64 baseline on the Windows PC.
- Per-frame paint cost. These benchmarks stop at the grid.
