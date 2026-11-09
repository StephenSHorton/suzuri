# suzuri-view

suzuri's terminal view as a package. It paints `rsz-term` frames on a canvas and turns keys, IME text, paste, the mouse and the wheel into calls on a `host` object. It has no Tauri code: each app wires the host to its own IPC.

It is plain ESM with hand-written types (`index.d.ts`) and no dependencies. Its version is the same as the `rsz-term` crate's, and both move in one suzuri tag. `feed` checks the frame's length (at least its 32-byte header) and format version (header byte 11). A frame too short or in another version is refused, the view shows why (for a version, "terminal format vN, this app knows vM"), and that frame is not acked.

Grapheme clusters (👍🏽, 👨‍👩‍👧, 🇯🇵, e + U+0301) come in an optional cluster table after the last row. Header bytes 30-31 hold its entry count (`nclusters` in `decodeHeader`), 0 when there is none, and the frame is then the same bytes as before. Each entry is `u16 y, u16 col, u8 w, u8 n, n × u32` codepoints, and the cell's run still carries the base codepoint (`crates/term/src/frame.rs` has the layout). This is additive, so the format version stays 1. An older suzuri-view ignores the table and draws the base glyph across the cluster's cells, as it always did. This one draws each cluster as one string, and emoji in the system colour-emoji fonts (Apple Color Emoji, Segoe UI Emoji, Noto Color Emoji), shrunk to their cells only when they would overflow.

## Embedding

```js
import { mount, GOHU_FONT } from "suzuri-view"
import "suzuri-view/view.css" // or a <link> to it; it also declares the Gohu @font-face

const view = mount(document.getElementById("term"), {
  write: (data) => ipc.write(id, data),
  paste: (text) => ipc.paste(id, text),          // null: read the clipboard yourself
  resize: (cols, rows) => ipc.resize(id, cols, rows),
  cellPx: (w, h) => ipc.cellPx(id, w, h),
  scroll: (delta) => ipc.scroll(id, delta),
  ack: (seq) => ipc.ack(id, seq),
  redraw: () => ipc.redraw(id),
  focus: (on) => ipc.focus(id, on),
  select: (op, col, row, right) => ipc.select(id, op, col, row, right),
  copy: () => ipc.copy(id),
  mouse: (ev) => ipc.mouse(id, ev),
  link: (col, row) => ipc.link(id, col, row),
  openUrl: (url) => ipc.openUrl(url),
  keyFilter: (e) => myShortcuts(e),               // optional: true = the app took the key
}, { padding: [6, 4] })

view.setStyle({
  bg: "#0c0c10", selection: "rgba(184, 160, 200, 0.38)", cursor: "#b8a0c8",
  font: GOHU_FONT, // the bundled Gohu; or { family, px, url?, face? } for another font
})
frames.onmessage = (buf) => view.feed(buf)   // paints on the next animation frame, then host.ack(seq)
msgs.onmessage = (msg) => view.message(msg)  // title, exit, img, imgdrop, imgpos, progress, ...
view.focus()

// later (React: in the effect's cleanup)
view.unmount()
```

Every view has its own style, cell metrics, textarea, image layer and image modal, so two terminals on one page can differ. `unmount()` removes every listener, animation frame and observer the view added; a large image the view opened stays up until it is clicked.

`setReadOnly(true)` stops writes, pastes, mouse reports, scrolling, selection and copy from reaching the host. After an `exit` message the view stops writes and pastes only: the history can still be scrolled, selected (a drag already under way) and copied, as in suzuri. A new press on an exited terminal is ignored.

### Hooks for an app with its own chrome

suzuri itself mounts every pane this way (`ui/app.js`, `ui/pane_input.js`), with a few more hooks, all optional:

- `host.keyFilter(e)`, `host.textFilter(text)`, `host.pasteFilter(clipboard)`: the app's shortcuts, IME text and paste events go to the app first; `true` means it took them. suzuri's input bar takes keys and text at a prompt, and an image pasted on the alt screen becomes a file path. A keyFilter that wants the view's copy/paste chords to stay the view's can ask `clipShortcut(e, isMac, view.hasSelection)`.
- `host.wheelKeys(data)`: the arrow keys the wheel sends on an alt screen that asked for them (default: `write`); suzuri sends them straight to the PTY, untimed.
- `host.onPaint(header, stats)`: after each paint and its ack (suzuri re-renders its input bar when the alt screen flips, and counts paint stats and input latency).
- `mount(el, host, { reportFocus: false })`: the textarea's focus and blur report nothing; the app reports which pane is focused itself.
- `mount(el, host, { ready: () => bool })`: while false the pointer ignores the mouse (no press, selection, mouse report or link hover); suzuri's panes are not ready until `pane_open` has answered.
- `mount(el, host, { caret: () => element })`: the IME textarea and preedit sit on that element (suzuri: the input bar's caret at a prompt), placed in viewport coordinates. `view.placeInput()` places them again after the element moved.
- `mount(el, host, { screen: (canvas) => screen })`: the Screen that paints and measures (suzuri's follow its one window style). `view.fit()` refits after a font change made that way.
- `mount(el, host, { paint: "external" })`: the app paints, e.g. in a worker after `view.canvas.transferControlToOffscreen()`. The view makes no canvas context, `feed` throws, and the app calls `view.painted(header)` for each painted frame (cursor, badge, the canvas's CSS size, `ack`, then `onPaint`).
- `mount(el, host, { framesPending: () => bool })`: while true an `imgpos` waits for the next painted frame instead of placing now (suzuri: a worker pane's frames wait until `pane_open` answers).
- `mount(el, host, { debouncePaste: false })`: every paste chord calls `host.paste(null)`; the host debounces its own clipboard paste (suzuri's one 280 ms debounce also covers the macOS Edit menu). Default: chords within 280 ms are one paste.
- `view.key(e)`: a keydown that landed elsewhere (suzuri: the page itself focused while a pane is) handled as the textarea's would be: `keyFilter`, the clipboard chords, the encoder.
- `view.modes` (the last frame's mode bits) and `view.hasSelection`.
- `view.scrollBy(lines)`: scroll the history (> 0 up), eased like the wheel. `mount(el, host, { smoothScroll: false })` makes the wheel and `scrollBy` call `host.scroll` at once. While scrolled back, a thin thumb (`.rsz-scrollbar .thumb`) shows on the right edge.

An error (a frame shorter than its 32-byte header, a frame in another format version, or a frame that fails to paint) shows in the view until the next frame paints or `setStyle` is called. When the view is unmounted while it has focus, it calls `host.focus(false)`.

## Differences from suzuri 0.10 behaviour

suzuri 0.10 had one hidden textarea and one paint loop for every pane in `ui/app.js`. Running each pane through its own view changes these, by design:

- **One IME textarea per view.** Each pane has its own textarea, so the keyboard moves between textareas when the focused pane changes, and an IME composition under way ends when focus moves to another pane or the pane's element is re-parented (a split, close, move or maximize rebuilds the layout). suzuri 0.10's single textarea never moved, so a composition survived those.
- **One `requestAnimationFrame` per view.** Each view schedules its own animation frame for its queued frames, instead of one shared loop painting every pane. Panes still paint in the same browser frame, each acking its own frames as before.
- **Bad frames show an error.** A frame shorter than its 32-byte header, or in another format version, is dropped with everything queued behind it, shown in the view's `.rsz-error` banner, and not acked. In suzuri 0.10 the decoder threw on a version mismatch inside the paint loop (logged, no banner, the queue not dropped), and a short frame was not checked at all.
- **`pane_cell_px` on open.** When `pane_open` answers, a new pane sends its cell pixels (`pane_cell_px`), and `pane_resize` too if the view's fit came out other than the size it opened at, because the view measured and fit before the pane had an id.

## Files

- `index.js`: `mount` and the TermView controller (types: `index.d.ts`)
- `frame.js`: the frame decoder and `Screen` (style and metrics per Screen); types `frame.d.ts`
- `boxdraw.js`, `gdi_cells.js`: box glyphs drawn into the cell, and Go's Windows cell sizes
- `keys.js`, `kitty.js`: the key encoder; types `keys.d.ts`, `kitty.d.ts`
- `pointer.js`: selection, mouse reports and links (⌘- or Ctrl-click on macOS; on an alt screen that reports the mouse, a double or triple click still selects)
- `smooth.js`: eased scrollback scrolling (Go suzuri's k = 16 ease, in whole lines)
- `images.js`: inline images and Kitty graphics
- `view.css`: the look, themed through `--rsz-*` custom properties, and the Gohu `@font-face`
- `fonts/`: the bundled Gohu (GohuFont uni14 Nerd Font Mono) and its license, `COPYING-LICENSE`

The Gohu font ships in `fonts/` (suzuri's own UI loads it from there too). `GOHU_FONT.url` resolves next to `index.js` through `new URL(..., import.meta.url)`, and a bundler that follows that pattern copies the file. `suzuri-view/fonts/*` is also exported for a host that serves the file itself.
