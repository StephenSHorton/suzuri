// Builds suzuri's logo candidates: the Hearth gate with the character 硯
// (suzuri, an inkstone) in it. Written beside this file:
//   N-name.svg          each candidate mark (64x64, one colour, plain paths)
//   N-name-lockup.svg   the mark with the word "suzuri"
//   compare.html        every candidate large, as the app tile at 1024, 128,
//                       64, 48, 32, 24 and 16 px, on light and dark, beside the
//                       Hearth icon and suzuri's current icon
//
//   bun design/logo/build.ts                  write them
//   bun design/logo/build.ts --tiles <dir>    also write each tile as PNG files
//                                             and small.png (the 16 and 32 px
//                                             tiles, real size and 8x) in <dir>
//
// The character's outline is baked from an openly licensed font into
// glyph/*.svg (README.md says which, and how), so nothing here needs a font.
// The word is lettering drawn as strokes with round caps, like Hearth's.
// Everything is currentColor: one colour, whatever the page puts it on.
//
// The small tiles on the sheet are real pixels, drawn here the way Hearth's
// apps/web/scripts/icons.ts draws its icons (shapes sampled many times a
// pixel, written as PNG with zlib; no image library): a vector shrunk by the
// browser would flatter them. The drawing itself is tile.ts, shared with
// icons.ts, which writes the app's icons from the picked candidate.
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { type Cmd, type Mark, flatten, MARK_SPAN, parse, png, RADIUS, tilePixels } from "./tile.ts";

const here = dirname(fileURLToPath(import.meta.url));

const num = (n: number) => String(Math.round(n * 100) / 100);

// ---------------------------------------------------------------- the character

// A baked 硯: its outline and the box its ink fills (font units, y down).
type Face = "noto-sans-jp-700" | "zen-old-mincho-900";
const faces = new Map<Face, { cmds: Cmd[]; x0: number; y0: number; x1: number; y1: number }>();
function face(name: Face) {
  if (!faces.has(name)) {
    const d = /<path d="([^"]+)"/.exec(readFileSync(join(here, "glyph", `${name}.svg`), "utf8"))![1]!;
    const cmds = parse(d);
    const pts = flatten(cmds).flat();
    faces.set(name, {
      cmds,
      x0: Math.min(...pts.map((p) => p[0])),
      y0: Math.min(...pts.map((p) => p[1])),
      x1: Math.max(...pts.map((p) => p[0])),
      y1: Math.max(...pts.map((p) => p[1])),
    });
  }
  return faces.get(name)!;
}

// The character drawn so its ink fills a box `w` wide (and `h` high when
// given: a little squeeze or stretch, never more than a few percent),
// centred on cx, standing on `foot`.
function glyph(name: Face, cx: number, foot: number, w: number, h = 0): string {
  const g = face(name);
  const sx = w / (g.x1 - g.x0);
  const sy = h ? h / (g.y1 - g.y0) : sx;
  const tx = cx - (sx * (g.x0 + g.x1)) / 2;
  const ty = foot - sy * g.y1;
  return g.cmds
    .map(({ c, v }) => c + v.map((n, i) => num(i % 2 ? n * sy + ty : n * sx + tx)).join(" "))
    .join("");
}

// ---------------------------------------------------------------- the gate

// Hearth's gate, to the unit (hearth/design/logo/hearth.svg): a top beam 9
// thick, bowed 4 in the middle, ends slanting in by 2; a lower beam 5 thick
// reaching 4 past the posts; two posts 7 wide.
const HEARTH_GATE = "M2 8Q32 16 62 8L60 17H53V27H57V32H53V58H46V32H18V58H11V32H7V27H11V17H4Z";
// Hearth's flame (its half circle written as two curves, for the sampler).
const HEARTH_FLAME = "M32 34.5C32 34.5 40 43.3 40 49.7C40 54.12 36.42 57.7 32 57.7C27.58 57.7 24 54.12 24 49.7C24 43.3 32 34.5 32 34.5Z";

// The same gate with other measures. l..r: the top beam's ends; top: its top
// at the ends; post: the left post's left edge (the right one mirrors it);
// lower: the lower beam's top; over: how far it passes the posts; foot: where
// the posts stand, or 0 for the gate's head alone (both beams, the posts only
// between them).
function gate(o: { l: number; top: number; post: number; lower: number; over: number; foot: number }): string {
  const r = 64 - o.l;
  const under = o.top + 9;
  const pl = o.post;
  const pr = 64 - o.post - 7;
  const lb = o.lower + 5;
  const head = `M${o.l} ${o.top}Q32 ${o.top + 8} ${r} ${o.top}L${r - 2} ${under}H${pr + 7}V${o.lower}H${pr + 7 + o.over}V${lb}`;
  if (!o.foot) {
    return `${head}H${pl - o.over}V${o.lower}H${pl}V${under}H${o.l + 2}ZM${pl + 7} ${under}V${o.lower}H${pr}V${under}Z`;
  }
  return `${head}H${pr + 7}V${o.foot}H${pr}V${lb}H${pl + 7}V${o.foot}H${pl}V${lb}H${pl - o.over}V${o.lower}H${pl}V${under}H${o.l + 2}Z`;
}
// The top beam alone.
const beam = (l: number, top: number) => `M${l} ${top}Q32 ${top + 8} ${64 - l} ${top}L${62 - l} ${top + 9}H${l + 2}Z`;

// ---------------------------------------------------------------- candidates

// A mark (tile.ts): its paths on the 64-unit grid, and how many of those
// units span the app tile (Hearth's 90 unless said).
type Candidate = Mark & { name: string; line: string; small?: boolean };

// The wide gate: the top beam 4 wider, the posts 4.5 further out each side,
// the lower beam 6 higher, the feet 3 lower. Opening 37 by 35 (Hearth's: 28 by 26).
const WIDE = gate({ l: 0, top: 5, post: 6.5, lower: 21, over: 3, foot: 61 });

const CANDIDATES: Candidate[] = [
  {
    id: "1-gate",
    name: "Hearth gate, 硯 for the flame",
    line: "The plain reading: Hearth's gate untouched, the character (a bold gothic) standing where the flame is.",
    paths: [HEARTH_GATE, glyph("noto-sans-jp-700", 32, 58, 24, 22.8)],
  },
  {
    id: "2-gate-mincho",
    name: "Hearth gate, brush-style 硯",
    line: "The same, with a mincho face: closer to ink and brush, thinner cross strokes.",
    paths: [HEARTH_GATE, glyph("zen-old-mincho-900", 32, 58, 24, 22.8)],
  },
  {
    id: "3-wide-gate",
    name: "Wide gate",
    line: "The same beams and posts, the posts moved out and the lower beam raised: the character is a third bigger.",
    paths: [WIDE, glyph("noto-sans-jp-700", 32, 61, 32, 31)],
  },
  {
    id: "4-wide-gate-mincho",
    name: "Wide gate, brush-style 硯",
    line: "The wide gate with the mincho face.",
    paths: [WIDE, glyph("zen-old-mincho-900", 32, 61, 32, 31)],
  },
  {
    id: "5-roof",
    name: "Beam over 硯",
    line: "Only the bowed top beam, as a roof over a large character.",
    paths: [beam(2, 6), glyph("noto-sans-jp-700", 32, 60, 42)],
  },
  {
    // Sized and placed for the pixels: on a 16 px tile the character is 12.6 px
    // wide and the three bars of its 目 fall on pixel rows 3, 5 and 7 (twice
    // that at 32), which keeps them apart.
    id: "6-small-glyph",
    name: "Small sizes: 硯 alone",
    line: "No gate: the character filling the tile, placed on the pixel rows. For 32 px and under only.",
    paths: [glyph("noto-sans-jp-700", 32, 60.8, 63)],
    span: 80,
    small: true,
  },
  {
    // The owner's pick (2026-11-20): no gate at all, the character alone on
    // the tile, with about the margin Hearth's gate has.
    id: "8-glyph",
    name: "硯 alone",
    line: "No gate: the character alone on the dark tile, with room around it.",
    paths: [glyph("noto-sans-jp-700", 32, 57.3, 52)],
  },
  {
    id: "7-small-gate-stone",
    name: "Small sizes: gate and inkstone",
    line: "No character: Hearth's gate over a plain inkstone slab. For 32 px and under only.",
    paths: [HEARTH_GATE, "M22 40H42V58H22ZM25.5 43.5V50H38.5V43.5Z"],
    small: true,
  },
];

// The sets worth seeing whole: a full mark, and the small-size mark it hands
// over to at `upTo` px and under.
const PAIRS: { full: string; small: string; upTo: number }[] = [
  { full: "8-glyph", small: "6-small-glyph", upTo: 32 },
  { full: "3-wide-gate", small: "6-small-glyph", upTo: 32 },
  { full: "1-gate", small: "6-small-glyph", upTo: 32 },
];

const STYLE = `<style>svg{color:#1c1917}@media(prefers-color-scheme:dark){svg{color:#f5f5f4}}</style>`;
const body = (paths: string[]) => paths.map((d) => `<path d="${d}"/>`).join("\n");

// "suzuri" in Hearth's lettering: baseline y=20, x-height 12, 2.8 thick,
// round caps. The r is Hearth's own; the u is its h's arch turned over.
const WORD = `<g fill="none" stroke="currentColor" stroke-width="2.800" stroke-linecap="round" stroke-linejoin="round">
<path d="M8.300 9.900A4.200 3 0 1 0 4.500 14A4.200 3 0 1 1 0.700 18.100"/>
<path d="M16 8V14C16 18 18.500 20 22 20C25 20 28 18 28 14M28 8V20"/>
<path d="M35 8H44L35 20H44"/>
<path d="M51 8V14C51 18 53.500 20 57 20C60 20 63 18 63 14M63 8V20"/>
<path d="M70 8V20M70 14C70 10 72.500 8 76 8"/>
<path d="M83 8V20M83 2.300V2.400"/>
</g>`;
const WORD_W = 83;
const LOCK_W = Math.ceil(60 + WORD_W * 1.4 + 8);
const lockInner = (b: string) => `<g transform="scale(0.75)">${b}</g>
<g transform="translate(60 6) scale(1.400)">${WORD}</g>`;

for (const c of CANDIDATES) {
  writeFileSync(
    join(here, `${c.id}.svg`),
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"${c.span ? ` data-tile-span="${c.span}"` : ""} fill="currentColor" stroke="none">${STYLE}
${body(c.paths)}
</svg>
`,
  );
  // Mark 48 high at the left; the word's x-height centred beside it.
  writeFileSync(
    join(here, `${c.id}-lockup.svg`),
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${LOCK_W} 48" fill="currentColor" stroke="none">${STYLE}
${lockInner(body(c.paths))}
</svg>
`,
  );
}

const drawn = new Map<string, Buffer>();
function tilePng(m: Mark, size: number): Buffer {
  const key = `${m.id}@${size}`;
  if (!drawn.has(key)) drawn.set(key, png(size, size, tilePixels(size, m)));
  return drawn.get(key)!;
}

// ---------------------------------------------------------------- compare.html

const HEARTH: Mark = { id: "hearth", paths: [HEARTH_GATE, HEARTH_FLAME] };
// The sizes drawn as real pixels; 1024 is shown as a vector, a quarter size.
const SIZES = [128, 64, 48, 32, 24, 16];
const ZOOM = 6;

const mark = (paths: string[], px: number) =>
  `<svg viewBox="0 0 64 64" width="${px}" height="${px}" fill="currentColor" stroke="none" aria-hidden="true">${body(paths)}</svg>`;
const lock = (paths: string[], h: number) =>
  `<svg viewBox="0 0 ${LOCK_W} 48" height="${h}" fill="currentColor" stroke="none" aria-hidden="true">${lockInner(body(paths))}</svg>`;
function tileSvg(m: Mark, px: number): string {
  const span = m.span ?? MARK_SPAN;
  return `<svg viewBox="0 0 ${span} ${span}" width="${px}" height="${px}" aria-hidden="true"><rect width="${span}" height="${span}" rx="${num(span * RADIUS)}" fill="#1b1b1b"/><g transform="translate(${span / 2 - 32} ${span / 2 - 33})" fill="#f1f1f1">${body(m.paths)}</g></svg>`;
}
const tileImg = (m: Mark, size: number, zoom = 1) =>
  `<img alt="" width="${size * zoom}" height="${size * zoom}"${zoom > 1 ? ` class="px"` : ""} src="data:image/png;base64,${tilePng(m, size).toString("base64")}">`;
const fig = (label: string, inner: string) => `<figure>${inner}<figcaption>${label}</figcaption></figure>`;

// The tile at every size on a light and on a dark desktop; `beside` adds the
// Hearth tile next to it at 48, as the two would sit in a Dock or a taskbar.
const tiles = (pick: (size: number) => Mark, beside = true) =>
  ["l", "d"]
    .map(
      (tone) => `<div class="strip ${tone}">
    ${SIZES.map((size) => fig(String(size), tileImg(pick(size), size))).join("")}
    ${fig(`32, ${ZOOM}x`, tileImg(pick(32), 32, ZOOM))}${fig(`16, ${ZOOM}x`, tileImg(pick(16), 16, ZOOM))}
    ${beside ? fig("beside Hearth, 48", `<span class="dock">${tileImg(HEARTH, 48)}${tileImg(pick(48), 48)}</span>`) : ""}
  </div>`,
    )
    .join("\n");

const row = (c: Candidate) => `
<section class="row">
  <header><span class="n">${parseInt(c.id)}</span><div><h2>${c.name}</h2><p>${c.line}</p></div></header>
  <div class="big">
    <div class="pane l">${mark(c.paths, 150)}${lock(c.paths, 40)}</div>
    <div class="pane d">${mark(c.paths, 150)}${lock(c.paths, 40)}</div>
    <div class="pane t">${fig("1024, shown a quarter size", tileSvg(c, 256))}</div>
  </div>
  ${tiles(() => c)}
</section>`;

const byId = (id: string) => CANDIDATES.find((c) => c.id === id)!;
const pairRow = (p: (typeof PAIRS)[number]) => `
<section class="row">
  <header><span class="n">${parseInt(p.full)}+${parseInt(p.small)}</span><div><h2>${byId(p.full).name}, and 硯 alone at ${p.upTo} px and under</h2><p>Candidate ${parseInt(p.full)} from 48 px up, candidate ${parseInt(p.small)} below: the icons as they would ship.</p></div></header>
  ${tiles((size) => (size <= p.upTo ? byId(p.small) : byId(p.full)))}
</section>`;

const now = (tone: string) => `<div class="strip ${tone}">
    ${fig("128", `<img alt="" width="128" height="128" src="../../app/icons/128x128.png">`)}
    ${fig("32", `<img alt="" width="32" height="32" src="../../app/icons/32x32.png">`)}
    ${fig("16 (the 32, halved)", `<img alt="" width="16" height="16" src="../../app/icons/32x32.png">`)}
    ${fig(`32, ${ZOOM}x`, `<img alt="" class="px" width="${32 * ZOOM}" height="${32 * ZOOM}" src="../../app/icons/32x32.png">`)}
  </div>`;
const reference = `
<section class="row">
  <header><span class="n">H</span><div><h2>For reference: Hearth</h2><p>Hearth's mark and its icon, drawn by the same code as every tile on this page.</p></div></header>
  <div class="big">
    <div class="pane l">${mark(HEARTH.paths, 150)}</div>
    <div class="pane d">${mark(HEARTH.paths, 150)}</div>
    <div class="pane t">${fig("1024, shown a quarter size", tileSvg(HEARTH, 256))}</div>
  </div>
  ${tiles(() => HEARTH, false)}
</section>
<section class="row">
  <header><span class="n">S</span><div><h2>For reference: suzuri now</h2><p>The icon in app/icons now, which icons.ts writes from the pick in pick.json (before it: a green 硯 on a black square, and Tauri's own logo in icon.icns).</p></div></header>
  <div class="big">
    <div class="pane t">${fig("256 (the largest PNG there)", `<img alt="" width="256" height="256" src="../../app/icons/128x128@2x.png">`)}</div>
  </div>
  ${now("l")}
  ${now("d")}
</section>`;

const full = CANDIDATES.filter((c) => !c.small);
const small = CANDIDATES.filter((c) => c.small);
const html = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>suzuri: logo candidates</title>
<style>
:root{--ink:#1c1917;--paper:#f1f1f1;--ink-d:#f5f5f4;--paper-d:#1b1b1b}
*{box-sizing:border-box}
body{margin:0;padding:28px;background:#e7e5e4;color:#1c1917;font:14px/1.4 system-ui,-apple-system,sans-serif}
h1{font-size:22px;margin:0 0 4px}
h3{font-size:15px;margin:26px 0 10px;text-transform:uppercase;letter-spacing:.05em;color:#57534e}
.lead{margin:0 0 6px;color:#57534e;max-width:100ch}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(940px,1fr));gap:18px}
.row{background:#fafaf9;border:1px solid #d6d3d1;border-radius:14px;padding:16px;min-width:0}
header{display:flex;gap:12px;align-items:flex-start;margin-bottom:12px}
.n{flex:none;min-width:28px;height:28px;padding:0 7px;border-radius:14px;background:#1c1917;color:#fff;display:grid;place-items:center;font-weight:700}
h2{margin:0;font-size:17px}header p{margin:2px 0 0;color:#57534e}
.big{display:flex;gap:10px;margin-bottom:2px}
.pane{border-radius:10px;padding:18px;display:flex;flex-direction:column;align-items:flex-start;justify-content:space-between;gap:16px}
.pane.l,.pane.d{flex:1}
.pane svg{flex:none}
.l{background:var(--paper);color:var(--ink)}
.d{background:var(--paper-d);color:var(--ink-d)}
.pane.t{background:#cfd4da;padding:14px}
.strip{display:flex;gap:18px;align-items:flex-end;padding:12px 14px;border-radius:10px;margin-top:8px}
.strip.l{background:#dfe3e8}.strip.d{background:#2a2c30}
figure{margin:0;display:flex;flex-direction:column;align-items:center;gap:6px}
figcaption{font-size:11px;color:#78716c;white-space:nowrap}
.d figcaption{color:#a8a29e}
img,figure svg{display:block}
.px{image-rendering:pixelated}
.dock{display:flex;gap:8px;padding:6px 8px;border-radius:12px;background:rgba(127,127,127,.28)}
</style></head><body>
<h1>suzuri: logo candidates</h1>
<p class="lead">The Hearth gate with 硯 in it. For each candidate: the mark, and the mark with the word, on light and dark; the app tile (Hearth's own: dark rounded square, light mark) at 1024; then the tile drawn as real pixels at 128, 64, 48, 32, 24 and 16, with the 32 and 16 enlarged ${ZOOM} times so every pixel shows.</p>
<h3>Reference</h3>
<div class="grid">${reference}</div>
<h3>The mark</h3>
<div class="grid">${full.map(row).join("\n")}</div>
<h3>For 32 px and under</h3>
<div class="grid">${small.map(row).join("\n")}</div>
<h3>As a set</h3>
<div class="grid">${PAIRS.map(pairRow).join("\n")}</div>
</body></html>
`;
writeFileSync(join(here, "compare.html"), html);
console.log(`Wrote ${CANDIDATES.length} marks, their lockups and compare.html in ${here}`);

// ---------------------------------------------------------------- --tiles <dir>

const at = process.argv.indexOf("--tiles");
if (at > 0) {
  const dir = process.argv[at + 1];
  if (!dir) throw new Error("--tiles needs a folder");
  mkdirSync(dir, { recursive: true });
  const all: Mark[] = [HEARTH, ...CANDIDATES];
  const sizes = [1024, ...SIZES];
  for (const m of all) for (const size of sizes) writeFileSync(join(dir, `${m.id}-${size}.png`), tilePng(m, size));
  // small.png: four marks a row; for each, the 32 px tile 8x and real size,
  // then the 16 px tile 8x and real size, on a mid grey.
  const cell = 32 * 8 + 24;
  const band = 24 + 256 + 12 + 32 + 12 + 128 + 12 + 16;
  const w = 4 * cell + 24;
  const h = Math.ceil(all.length / 4) * band + 24;
  const sheet = Buffer.alloc(w * h * 4);
  for (let i = 0; i < w * h; i++) sheet.set([0x80, 0x84, 0x8a, 255], i * 4);
  const blit = (pixels: Buffer, size: number, zoom: number, ox: number, oy: number) => {
    for (let y = 0; y < size * zoom; y++) {
      for (let x = 0; x < size * zoom; x++) {
        const from = (Math.floor(y / zoom) * size + Math.floor(x / zoom)) * 4;
        const to = ((oy + y) * w + ox + x) * 4;
        const a = pixels[from + 3]! / 255;
        for (let c = 0; c < 3; c++) sheet[to + c] = Math.round(pixels[from + c]! * a + sheet[to + c]! * (1 - a));
      }
    }
  };
  all.forEach((m, i) => {
    const ox = 24 + (i % 4) * cell;
    const oy = 24 + Math.floor(i / 4) * band;
    const p32 = tilePixels(32, m);
    const p16 = tilePixels(16, m);
    blit(p32, 32, 8, ox, oy);
    blit(p32, 32, 1, ox, oy + 256 + 12);
    blit(p16, 16, 8, ox, oy + 256 + 12 + 32 + 12);
    blit(p16, 16, 1, ox, oy + 256 + 12 + 32 + 12 + 128 + 12);
  });
  writeFileSync(join(dir, "small.png"), png(w, h, sheet));
  console.log(`Wrote ${all.length * sizes.length} tiles and small.png in ${dir}`);
}
