# suzuri

## UI: React + Astryx

Everything around the terminal panes (title bar, tabs, palette, settings,
notices, dialogs) is React 19 with Meta's [Astryx](https://astryx.atmeta.com)
design system, theme Neutral in dark mode. The terminal panes, splits and
key routing stay plain JS (`ui/app.js`, `ui/view`); the two meet in
`ui/chrome/store.js`.

- **Pick from Astryx first.** The whole catalogue is in `docs/astryx/`
  (start at its README: every component, hook, page template and docs
  topic). Live search: `npm run astryx -- search <words>` in `ui/`. Never
  run a bare `npx astryx` (an unrelated package).
- Style with Astryx tokens (`--color-*`, `--spacing-*`, `--radius-*`), not
  hex values or loose pixel gaps.
- The CSP allows no outside sources: fonts and icons are bundled
  (Figtree via `@fontsource/figtree`, Lucide via `lucide-react`).
- Build: `npm ci && npm run build` in `ui/` (Vite to `ui/dist`, which Tauri
  embeds). `cargo tauri build` runs it first; a plain `cargo build` fails
  without `ui/dist`. UI tests: `npm test` in `ui/`.
- After an Astryx upgrade, regenerate the catalogue: `npm run astryx-catalog`.
