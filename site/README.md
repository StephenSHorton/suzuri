# suzuri website

React 19 + Vite 8 (Rolldown) + TanStack Router + Tailwind 4 + shadcn-style UI.

Canvas UI (Glyph Rain, Glitch, Decrypt Reveal) is vendored in `src/vendor/canvas-ui`.

```bash
cd site
bun install
bun run dev                  # http://localhost:5173
SITE_BASE=/suzuri/ bun run build
```

Docs live at `/docs` in this app (TanStack routes + inkstone chrome). Protocol tables read `src/data/guest.v1.json`.
