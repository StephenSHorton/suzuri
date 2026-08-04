# Vendor — Canvas UI

Vanilla (framework-free) runtimes from [Canvas UI](https://canvasui.dev)
(`DavidHDev/canvas-ui`). Used on the suzuri GitHub Pages site.

| File | Component | Role on page |
|------|-----------|--------------|
| `glyph-rain.js` | Glyph Rain | Full-page rain backdrop |
| `glitch.js` | Glitch | Hero headline |
| `liquid.js` | Liquid | Download / CTA strip |
| `decrypt-reveal.js` | Decrypt Reveal | “Session dossier” panel |

License: MIT + Commons Clause (see `LICENSE-canvas-ui.txt`). Components are
embedded as part of this website, not redistributed as a library.

## html-in-canvas

Glitch, Liquid, and Decrypt Reveal look best with Chrome’s experimental
**html-in-canvas** API (origin trial on your domain, or
`chrome://flags/#canvas-draw-element`). Without it they fall back to plain HTML.
Glyph Rain still paints a WebGL overlay in that case.

## Publishing the site (separate from app releases)

App and site use **different tags**:

| What | Tag example | Workflow |
|------|-------------|----------|
| App binary release | `v0.9.78` | `release.yml` |
| GitHub Pages site | `v0.1.0-site` (or `site-v0.1.0`) | `pages.yml` |

```bash
# After docs/site changes are on master:
git tag v0.1.0-site
git push origin v0.1.0-site
```

The deploy stamps that tag into the footer (`site v0.1.0-site · <UTC time>`).
