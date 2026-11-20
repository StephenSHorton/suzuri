# suzuri logo: candidates

The Hearth gate (a torii-like gate: bowed top beam, lower beam, two posts) with
硯 (U+786F, *suzuri*, an inkstone) where Hearth has its flame. This folder is
candidates, a sheet to compare them on, and the script that draws every icon
suzuri ships from the picked one.

## The app's icons: one command

    bun design/logo/icons.ts 8-glyph

writes every icon from that candidate (and `6-small-glyph` at 32 px and
under, the default small mark), and saves the pick in `pick.json`. To swap,
run it with another candidate's name, e.g. `bun design/logo/icons.ts 1-gate`;
`--small <name>` or `--small none` and `--small-up-to <px>` change the small
mark. With no name it draws the saved pick again; `--check` fails when a
written file is not what the pick draws.

| Written | Where it shows |
| --- | --- |
| `app/icons/icon.icns` (16 to 1024, every @2x) | Mac: Dock, Finder, Cmd-Tab, About panel, and the Dock icon of a dev run (Tauri sets it from this file) |
| `app/icons/icon.ico` (16, 24, 32, 48, 64, 256) | Windows: the exe (title bar, taskbar, Alt-Tab, Start menu, Explorer, shortcuts) and the installer's and uninstaller's icon |
| `app/icons/32x32.png`, `128x128.png`, `128x128@2x.png`, `icon.png` | Tauri's bundle list; the window icon where there is no .ico or .icns |
| `packaging/windows/installer-header.bmp`, `installer-sidebar.bmp` | The NSIS installer's header and its welcome and finish pages |
| `mark.svg`, `lockup.svg` | The picked mark and lockup (the repo README shows the lockup) |

The Mac's entries above 32 px sit on Apple's grid (an 824 px tile on 1024,
the rest transparent), so suzuri is the size of the other apps in the Dock;
Hearth's own icon is full-bleed and looks bigger beside it. The Windows
icons, the PNGs and the Mac's 16 and 32 px entries are full-bleed.

Open `compare.html` in a browser to see everything.

## What is here

| File | What |
| --- | --- |
| `N-name.svg` | A candidate mark: 64x64, one colour (`currentColor`), plain paths, no font needed. |
| `N-name-lockup.svg` | The mark with the word "suzuri" (lettering drawn as round-cap strokes, as Hearth's word is). |
| `compare.html` | Every candidate large, with the word, and as the app tile at 1024, 128, 64, 48, 32, 24 and 16 px on light and dark, beside the Hearth icon and suzuri's icon now. |
| `build.ts` | Writes all of the above. `bun design/logo/build.ts` |
| `tile.ts` | The drawing `build.ts` and `icons.ts` share: paths to pixels, the tile, PNG. |
| `icons.ts`, `pick.json` | The app's icons, from the picked candidate (above). |
| `glyph/*.svg` | The outline of 硯 baked from each font, in font units. `build.ts` reads these. |
| `glyph/bake.py` | How the outlines were baked from the font files. |
| `glyph/OFL-*.txt` | The fonts' licence, with their copyright lines. |

The candidates and their measures are the `CANDIDATES` table in `build.ts`;
the marks are written from it, so change the table and run it again rather
than editing an `N-name.svg` by hand.

## The candidates

| | Mark | What |
| --- | --- | --- |
| 1 | `1-gate` | Hearth's gate untouched, a bold gothic 硯 where the flame is. The plain reading of "like the Hearth logo, with the character in the centre". |
| 2 | `2-gate-mincho` | The same with a mincho (brush-style) 硯. |
| 3 | `3-wide-gate` | The same beams and posts, the posts moved out and the lower beam raised, so the character is a third bigger. |
| 4 | `4-wide-gate-mincho` | The wide gate with the mincho 硯. |
| 5 | `5-roof` | Only the bowed top beam, over a large 硯. |
| 6 | `6-small-glyph` | For 32 px and under: 硯 alone, sized and placed so its strokes fall on the pixel rows. |
| 7 | `7-small-gate-stone` | For 32 px and under: Hearth's gate over a plain inkstone slab, no character. |
| 8 | `8-glyph` | The owner's pick: 硯 alone on the tile, no gate, with room around it. |

## What the small sizes showed

The tile is Hearth's (`hearth/apps/web/scripts/icons.ts`): 90 mark units span
it, so one unit is 0.18 px at 16 px. 硯 has twelve strokes and needs
about 11 px to be read at all.

- Between Hearth's posts the character gets 24 units: 8.5 px on a 32 px tile,
  4 px on a 16 px tile. Candidates 1 and 2 are a grey block under the gate at
  32, and at 16 they are hard to tell from Hearth's own icon.
- The wide gate gives it 32 units: 11 px at 32 (the character can be made out,
  softly), 17 px at 48 (clear). At 16 it is a grey block in a frame.
- The mincho face loses its thin cross strokes first: fine at 128 and up,
  weaker than the gothic at every size under that.
- The beam alone (5) keeps the character readable down to 32, but with the
  posts gone it reads as one unfamiliar character with a stroke on top, more
  than as Hearth's gate.
- Only the character alone (6) is readable at 16. It is drawn on a tile where
  80 units span the side instead of 90, which makes the three counters of 目
  exactly two pixels apart at 16.
- The gothic is weight 700, not the heaviest: at 900 the counters of 目 are
  a pixel tall or less at 48 and 64 and the character closes into a block.
- Gate and inkstone (7) is the cleanest set of pixels at 16 and keeps the gate,
  but it has no character, and beside Hearth's icon the two are easy to mix up.

So no single drawing holds from 1024 down to 16. The sheet's last section shows
the two sets that do: a gate mark from 48 px up, and 硯 alone at 32 and under.

## The fonts

The outlines in `glyph/` come from two fonts under the SIL Open Font License
1.1. The font files are not in this repo; only the one character's outline is.

| Outline | Font | Copyright |
| --- | --- | --- |
| `noto-sans-jp-700.svg` | Noto Sans JP 2.004, the variable font instanced at weight 700 | Copyright 2014-2021 Adobe (http://www.adobe.com/), with Reserved Font Name 'Source' |
| `zen-old-mincho-900.svg` | Zen Old Mincho Black 1.500 | Copyright 2021 The Zen Old Mincho Project Authors (https://github.com/googlefonts/zen-oldmincho) |

Downloaded on 2026-11-20 from the google/fonts repository at commit
`869789e84da92782c144d83d69b64cec54905b5f`:

| File | sha256 |
| --- | --- |
| `ofl/notosansjp/NotoSansJP[wght].ttf` | `c2f3b4d463500a2ddcd3849cded1fceeb9fd6d1c32e6cbecd568453ba50fc68f` |
| `ofl/zenoldmincho/ZenOldMincho-Black.ttf` | `84a80d8bca79d7d9478935045b216ed003ad40fdea5fd9116d524eb26e872cdc` |

(`https://raw.githubusercontent.com/google/fonts/<commit>/<file>`; the licence
texts in `glyph/` are the `OFL.txt` beside each font there.)

To bake them again, with fonttools installed:

    python design/logo/glyph/bake.py "NotoSansJP[wght].ttf" ZenOldMincho-Black.ttf

The outline is the font's own, unchanged but for rounding to whole font units;
`build.ts` only scales and places it (stretched about 4% taller, at most, to
fill a gate's opening).
Neither font's name, nor the reserved name 'Source', is used for anything here.

## The gate and the word

The gate is Hearth's, to the unit (`hearth/design/logo/hearth.svg`); the wide
gate uses the same beam, bow, post and lower-beam thicknesses with other
positions. The word reuses Hearth's `r`; `s`, `u`, `z` and `i` are drawn to the
same measures (x-height 12, stroke 2.8, round caps).
