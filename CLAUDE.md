# suzuri

## UI: React + Astryx

Everything around the terminal panes (title bar, tabs, palette, settings,
the bell's notice history, dialogs) is React 19 with Meta's
[Astryx](https://astryx.atmeta.com) design system, in one of its seven themes (Settings › Look › Theme
and Mode, default Neutral dark; `ui/chrome/themes.js`). The terminal's
colours and the notice cards' follow the theme: `ui/theme_palette.js`
derives them from the resolved tokens and `ui/appearance.js` hands them to
Rust (`appearance_set`). Notice cards themselves are native desktop cards from rust-toast
(`app/src/notices.rs`, look in `app/src/toast_look.rs`). The terminal panes, splits and
key routing stay plain JS (`ui/app.js`, `ui/view`); the two meet in
`ui/chrome/store.js`.

- **Pick from Astryx first.** The whole catalogue is in `docs/astryx/`
  (start at its README: every component, hook, page template and docs
  topic). Live search: `npm run astryx -- search <words>` in `ui/`. Never
  run a bare `npx astryx` (an unrelated package).
- Style with Astryx tokens (`--color-*`, `--spacing-*`, `--radius-*`), not
  hex values or loose pixel gaps.
- The CSP allows no outside sources: fonts and icons are bundled
  (each theme's faces via `@fontsource/*`, Lucide via `lucide-react`);
  never import a theme's `fonts.css` (it pulls Google Fonts). Theme packages
  are pinned to `@astryxdesign/core`'s exact version.
- Build: `npm ci && npm run build` in `ui/` (Vite to `ui/dist`, which Tauri
  embeds). `cargo tauri build` runs it first; a plain `cargo build` fails
  without `ui/dist`. UI tests: `npm test` in `ui/`.
- After an Astryx upgrade, regenerate the catalogue: `npm run astryx-catalog`.
