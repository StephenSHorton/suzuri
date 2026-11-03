# suzuri-view

suzuri's terminal view as a package. It paints `rsz-term` frames on a canvas and turns keys, IME text, paste, the mouse and the wheel into calls on a `host` object. It has no Tauri code: each app wires the host to its own IPC.

It is plain ESM with hand-written types (`index.d.ts`) and no dependencies. Its version is the same as the `rsz-term` crate's, and both move in one suzuri tag. `feed` checks the frame format version (header byte 11). A frame in another version is refused and the view shows "terminal format vN, this app knows vM". That frame is not acked.

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

Every view has its own style, cell metrics, textarea, image layer and image modal, so two terminals on one page can differ. `unmount()` removes every listener, animation frame and observer the view added.

`setReadOnly(true)` stops writes, pastes, mouse reports, scrolling, selection and copy from reaching the host. After an `exit` message the view stops writes and pastes only: the history can still be scrolled, selected (a drag already under way) and copied, as in suzuri. A new press on an exited terminal is ignored.

An error (a frame in another format version, or a frame that fails to paint) shows in the view until the next frame paints or `setStyle` is called. When the view is unmounted while it has focus, it calls `host.focus(false)`.

## Files

- `index.js`: `mount` and the TermView controller (types: `index.d.ts`)
- `frame.js`: the frame decoder and `Screen` (style and metrics per Screen); types `frame.d.ts`
- `boxdraw.js`, `gdi_cells.js`: box glyphs drawn into the cell, and Go's Windows cell sizes
- `keys.js`, `kitty.js`: the key encoder; types `keys.d.ts`, `kitty.d.ts`
- `pointer.js`: selection, mouse reports and links
- `images.js`: inline images and Kitty graphics
- `view.css`: the look, themed through `--rsz-*` custom properties, and the Gohu `@font-face`
- `fonts/`: the bundled Gohu (GohuFont uni14 Nerd Font Mono) and its license, `COPYING-LICENSE`

The Gohu font ships in `fonts/` (suzuri's own UI loads it from there too). `GOHU_FONT.url` resolves next to `index.js` through `new URL(..., import.meta.url)`, and a bundler that follows that pattern copies the file. `suzuri-view/fonts/*` is also exported for a host that serves the file itself.
