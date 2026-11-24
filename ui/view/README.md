# suzuri-view

suzuri's terminal view as a package. It paints `rsz-term` frames on a canvas and turns keys, IME text, paste, the mouse and the wheel into calls on a `host` object. It has no Tauri code: each app wires the host to its own IPC.

It is plain ESM with hand-written types (`index.d.ts`) and no dependencies (the Settings window and palette, below, take React and Astryx from the host). Each suzuri release carries it packed as `suzuri-view-<v>.tgz`, at the release's version, so it and `rsz-term` move in one suzuri tag; depend on it by that release URL. `feed` checks the frame's length (at least its 32-byte header) and format version (header byte 11). A frame too short or in another version is refused, the view shows why (for a version, "terminal format vN, this app knows vM"), and that frame is not acked.

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

## Settings window and command palette

suzuri's own Settings window and command palette, for a host whose page is React with Astryx (Hearth's terminal). suzuri itself mounts these same ones. They need no Tauri: the host passes in the settings, the choices and the commands, and gets back saves and runs. `react`, `react-dom`, `@astryxdesign/core` (0.7.12) and `lucide-react` are peer dependencies, optional because the terminal view above needs none of them. Both ship compiled to plain JS (`dist/`, built when the package is packed), so the host's bundler needs no JSX support for them. Mount them inside the host's Astryx `<Theme>`: they take its theme and leave their own look to it.

```js
import { SettingsWindow, TERMINAL_PARTS } from "suzuri-view/settings"
import { Palette } from "suzuri-view/palette"

// Open while mounted; unmount on onClose.
<SettingsWindow
  settings={settings}           // the settings now; a new object while open is a change made elsewhere
  meta={meta}                   // rsz-settings' meta(): fonts, cursors, limits, labels, path
  save={(patch) => ipc.saveSettings(patch)}   // resolves to the saved settings (or nothing)
  onClose={(kept, finished) => setOpen(false)}
  parts={TERMINAL_PARTS}        // font, size, cursor, the profile new tabs use, the profiles
  width={460}                   // default 600
/>

<Palette
  commands={[{ title: "New tab", category: "Tabs", hint: "Ctrl+Shift+T", id: "new-tab" }, ...]}
  onRun={(command) => run(command.id)}   // after the palette has closed and handed focus back
  onClose={() => setPaletteOpen(false)}
/>
```

- Every change saves at once through `save`. Cancel, Esc or a click outside puts back what changed on this screen (a `save` of only those keys); Done, Enter or Ctrl/Cmd+, keeps it. `finished` (onClose's second argument) resolves once the last save has landed, to `{ changed, settings }`. A `save` that throws shows "Couldn't save: …" in the footer.
- `parts` picks what shows, from `ALL_PARTS` (the default): `theme`, `mode`, `font`, `size`, `text_size`, `cursor` on Look; `new_tab_profile`, `notice`, `animate_unfocused`, `restore_sessions`, `profiles` on Session. A page with none of them is left out, and with one page left the tabs go. Only the keys of the parts shown are read and saved (`PART_KEYS`), so a host may pass only those.
- `themeIcon(id)` puts an icon beside each theme's name (suzuri: its colour swatch); `initialPage` and `onPageChange` keep the page between openings.
- The footer reads "Saved to" `meta.path` when the host passes a path.
- A palette command is `{ title, category?, hint?, ... }`; the rest is the host's and comes back to `onRun`. A hint that is a chord label ("Ctrl+Shift+T", "⌘⇧W", alternatives joined by " · ") shows as keys, anything else as text. Matching is every word of the query in the title, hint or category (`filterCommands`). Cmd/Ctrl + K or P closes it again (`isToggleKey` changes that).
- The rules alone, with no React: `suzuri-view/settings_rules.js` and `suzuri-view/palette_rules.js`.

## Differences from suzuri 0.10 behaviour

suzuri 0.10 had one hidden textarea and one paint loop for every pane in `ui/app.js`. Running each pane through its own view changes these, by design:

- **One IME textarea per view.** Each pane has its own textarea, so the keyboard moves between textareas when the focused pane changes, and an IME composition under way ends when focus moves to another pane or the pane's element is re-parented (a split, close, move or maximize rebuilds the layout). suzuri 0.10's single textarea never moved, so a composition survived those.
- **One `requestAnimationFrame` per view.** Each view schedules its own animation frame for its queued frames, instead of one shared loop painting every pane. Panes still paint in the same browser frame, each acking its own frames as before.
- **Bad frames show an error.** A frame shorter than its 32-byte header, or in another format version, is dropped with everything queued behind it, shown in the view's `.rsz-error` banner, and not acked. In suzuri 0.10 the decoder threw on a version mismatch inside the paint loop (logged, no banner, the queue not dropped), and a short frame was not checked at all.
- **`pane_cell_px` on open.** When `pane_open` answers, a new pane sends its cell pixels (`pane_cell_px`), and `pane_resize` too if the view's fit came out other than the size it opened at, because the view measured and fit before the pane had an id.

## Files

- `index.js`: `mount` and the TermView controller (types: `index.d.ts`)
- `frame.js`: the frame decoder and `Screen` (style and metrics per Screen); types `frame.d.ts`
- `react/Settings.jsx`, `react/Palette.jsx`: the Settings window and command palette (built to `dist/settings.js`, `dist/palette.js` by `build.mjs`); types `settings.d.ts`, `palette.d.ts`
- `settings_rules.js`, `palette_rules.js`: their rules (parts and pages, Cancel's restore, the filter, chord labels); types beside them
- `boxdraw.js`, `gdi_cells.js`: box glyphs drawn into the cell, and Go's Windows cell sizes
- `keys.js`, `kitty.js`: the key encoder; types `keys.d.ts`, `kitty.d.ts`
- `pointer.js`: selection, mouse reports and links (⌘- or Ctrl-click on macOS; on an alt screen that reports the mouse, a double or triple click still selects)
- `smooth.js`: eased scrollback scrolling (Go suzuri's k = 16 ease, in whole lines)
- `images.js`: inline images and Kitty graphics
- `view.css`: the look, themed through `--rsz-*` custom properties, and the Gohu `@font-face`
- `fonts/`: the bundled Gohu (GohuFont uni14 Nerd Font Mono) and its license, `COPYING-LICENSE`

The Gohu font ships in `fonts/` (suzuri's own UI loads it from there too). `GOHU_FONT.url` resolves next to `index.js` through `new URL(..., import.meta.url)`, and a bundler that follows that pattern copies the file. `suzuri-view/fonts/*` is also exported for a host that serves the file itself.

## Pane-tree rules

suzuri's tabs and splits follow three DOM-free modules, shipped here so another app splits, closes and tabs panes by the same rules. suzuri imports them from this package too.

- `suzuri-view/layout_ops.js`: the pane tree (`{pane}` leaves, `{dir: "h"|"v", a, b, ratio?}` splits) and its operations: `removePane`, `swapPanes`, `dockBeside`, `adjustRatio`, `equalize`, `rotate`, `swapSides`, `neighbor`, `dropEdge`, `dropChanges`. Each changes the tree in place.
- `suzuri-view/sash_ops.js`: dragging a divider: `sashRect`, `hitSash`, `ratioAt`, `equalizeRun`, and `resizeGate` to hold PTY resizes back during a drag.
- `suzuri-view/tab_rules.js`: `MAX_TABS`, `MAX_PANES`, the strip label (`tabTitle`, `stickyAt`), `windowTitle` and the zoomed-pane cycle (`zoomCycle`).

```js
import { dockBeside, removePane } from "suzuri-view/layout_ops.js"
let tree = { dir: "h", a: { pane: left }, b: { pane: right } }
tree = dockBeside(tree, right, left, "bottom") // right goes under left
tree = removePane(tree, left)                  // null once the last pane closes
```
