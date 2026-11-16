# Mac live check (card suzuri#3)

Run on macOS 26 (Apple silicon), debug build, a private `HOME`, windows
captured one at a time with `screencapture -l <window id>` (never the display).
Pictures are in `docs/img/mac-live-check/`.

## vim with syntax colours: works

`vim -u NONE -n --cmd 'set shm+=FWsI' --cmd 'syntax on'` on a `.rs` file as the
pane's command: comments, keywords, types, numbers and strings each get their
colour (`vim-syntax.png`).

## Real IME (Japanese, Romaji): two bugs found and fixed

How: Japanese – Romaji was enabled and selected with the Text Input Source
API (`TISEnableInputSource` / `TISSelectInputSource`), the window was brought
to the front (an IME only acts on the app that is active), and
`scripts/keypost.swift` posted real key codes (n i h o n [n]) to the pid with
`RSZ_UI_TEST=imereal RSZ_TRACE=1`. The input sources were switched back after.

What the trace showed: WebKit delivers every key the IME takes as a `keydown`
with `keyCode` 229 and `isComposing` false (the composition events come
first), and on commit it sends a plain `input` (`insertText`, not composing)
with no `compositionend`.

1. **Raw letters leaked.** Only `isComposing` was checked, so "nihon" went
   into the input bar as plain keys while the IME composed にほん; Return then
   submitted `nihon` to the shell. Fix: ignore `keyCode === 229` keydowns
   (`ui/view/index.js` keydown, `ui/pane_input.js` pageKeys).
   `ime-before-composing.png` (`nihon` + the preedit) and
   `ime-before-after-return.png` (`zsh: command not found: nihon`).
2. **The preedit stayed on screen after a commit** (no `compositionend`), so
   the bar showed 日本 twice, the second one underlined. Fix: an `input` that
   is not composing ends the preedit too. `ime-after-committed.png`: 日本 in
   the bar, caret after it, nothing else.

Not settled: typing "nihon", Space (convert), Return did not commit in this
set-up (Space only turned the pending `n` into ん, Return then did nothing).
"nihonn" + Return commits. Worth a try with a real keyboard.

## Kitty image placement: explicit placement right, cursor not advanced (bug)

`scripts/kitty-cursor-repro.sh` draws three scenes (`kitty-placement.png`).

- Scene A: an image at (row 3, col 5) lands there, in a default 10 x 5 cell box
  (the image is fitted in the box and centred, so it looks a row lower), and
  text at explicit rows is unaffected.
- Scene B: text printed straight after the image makes the image vanish.
- Scene C: the same text one column over: the image stays.

Cause: after an image suzuri leaves the cursor on the image's top-left cell
(`Pane::place` in `crates/term/src/pane.rs`, "Go leaves the cursor alone"),
Kitty moves it to the right of the image on its last row. Text that follows
(icat, chafa, yazi, ranger all rely on this) is written over the image's top
row, and over the cell that carries the image's anchor mark, which drops the
image. Filed as a follow-up card.

## Slow blank start: explained and fixed

`split()` in `ui/app.js` waited for `requestAnimationFrame` before sizing the
pane and starting its shell. A WKWebView in a hidden window (app hidden,
another Space, fully covered) gets no animation frames, so no shell started
until the window came back: the pane stayed blank (30-90 s in the card).
Measured with the same build: window hidden at launch, no shell after 60 s;
after the fix the shell starts in 2 s (`startup-fixed-after-hidden.png` is the
window right after it was shown, sized to fit). The fix races the frame against
a 100 ms timer.
