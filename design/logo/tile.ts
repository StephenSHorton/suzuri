// The drawing both design/logo/build.ts (the candidates sheet) and
// design/logo/icons.ts (the app's icons) use: SVG paths turned into polygons,
// sampled many times a pixel into Hearth's app tile, written as PNG with zlib.
// No image library, no font. Importing this writes nothing.
import { readFileSync } from "node:fs";
import { deflateSync } from "node:zlib";

// ---------------------------------------------------------------- paths

export type Pt = [number, number];
export type Cmd = { c: "M" | "L" | "Q" | "C" | "Z"; v: number[] };

// Absolute M L H V Q C Z only: all that the gate and the baked glyphs use.
export function parse(d: string): Cmd[] {
  const out: Cmd[] = [];
  let x = 0;
  let y = 0;
  for (const m of d.matchAll(/([MLHVQCZ])([^MLHVQCZ]*)/g)) {
    const c = m[1]!;
    const v = (m[2]!.match(/-?\d*\.?\d+/g) ?? []).map(Number);
    if (c === "Z") out.push({ c: "Z", v: [] });
    else if (c === "H") for (const n of v) out.push({ c: "L", v: [(x = n), y] });
    else if (c === "V") for (const n of v) out.push({ c: "L", v: [x, (y = n)] });
    else {
      const n = c === "Q" ? 4 : c === "C" ? 6 : 2;
      for (let i = 0; i + n <= v.length; i += n) {
        out.push({ c: c === "M" && i > 0 ? "L" : (c as Cmd["c"]), v: v.slice(i, i + n) });
        x = v[i + n - 2]!;
        y = v[i + n - 1]!;
      }
    }
  }
  return out;
}

// Closed outlines as polygons (curves cut into short lines).
export function flatten(cmds: Cmd[]): Pt[][] {
  const polys: Pt[][] = [];
  let cur: Pt[] = [];
  let x = 0;
  let y = 0;
  for (const { c, v } of cmds) {
    if (c === "M") {
      if (cur.length > 2) polys.push(cur);
      cur = [[v[0]!, v[1]!]];
    } else if (c === "L") cur.push([v[0]!, v[1]!]);
    else if (c === "Q") {
      for (let i = 1; i <= 10; i++) {
        const t = i / 10;
        const u = 1 - t;
        cur.push([u * u * x + 2 * u * t * v[0]! + t * t * v[2]!, u * u * y + 2 * u * t * v[1]! + t * t * v[3]!]);
      }
    } else if (c === "C") {
      for (let i = 1; i <= 14; i++) {
        const t = i / 14;
        const u = 1 - t;
        cur.push([
          u * u * u * x + 3 * u * u * t * v[0]! + 3 * u * t * t * v[2]! + t * t * t * v[4]!,
          u * u * u * y + 3 * u * u * t * v[1]! + 3 * u * t * t * v[3]! + t * t * t * v[5]!,
        ]);
      }
    } else {
      if (cur.length > 2) polys.push(cur);
      cur = [];
      continue;
    }
    x = v[v.length - 2]!;
    y = v[v.length - 1]!;
  }
  if (cur.length > 2) polys.push(cur);
  return polys;
}

// ---------------------------------------------------------------- the mark

// A mark: its paths on the 64-unit grid, and how many of those units span the
// app tile (Hearth's 90 unless said).
export type Mark = { id: string; paths: string[]; span?: number };

// A mark as build.ts writes it (N-name.svg): every <path d>, and the span from
// the root's data-tile-span when the mark has its own.
export function readMark(file: string, id: string): Mark {
  const svg = readFileSync(file, "utf8");
  const paths = [...svg.matchAll(/<path d="([^"]+)"/g)].map((m) => m[1]!);
  if (!paths.length) throw new Error(`${file} has no paths`);
  const span = /data-tile-span="([\d.]+)"/.exec(svg)?.[1];
  return { id, paths, ...(span ? { span: Number(span) } : {}) };
}

// ---------------------------------------------------------------- the app tile

// Hearth's recipe (apps/web/scripts/icons.ts): a rounded tile in the neutral
// theme's dark body colour, corner radius 22% of its side, the mark in the
// theme's light body colour; 90 of the mark's units span the tile, with the
// mark's point (32, 33) at the tile's middle.
export const DARK = [0x1b, 0x1b, 0x1b] as const;
export const LIGHT = [0xf1, 0xf1, 0xf1] as const;
export const MARK_SPAN = 90;
export const RADIUS = 0.22;

// How much of each pixel the polygons cover (nonzero winding), sampled s by s
// times a pixel, on a w by h canvas.
export function cover(size: number, polys: Pt[][], s: number, h = size): Float32Array {
  const w = size;
  const cov = new Float32Array(w * h);
  const edges: number[][] = [];
  for (const p of polys) {
    for (let i = 0; i < p.length; i++) {
      const a = p[i]!;
      const b = p[(i + 1) % p.length]!;
      if (a[1] === b[1]) continue;
      edges.push(a[1] < b[1] ? [a[0], a[1], b[0], b[1], 1] : [b[0], b[1], a[0], a[1], -1]);
    }
  }
  const each = 1 / (s * s);
  for (let sy = 0; sy < h * s; sy++) {
    const y = (sy + 0.5) / s;
    const hits: [number, number][] = [];
    for (const e of edges) {
      if (y >= e[1]! && y < e[3]!) hits.push([e[0]! + ((y - e[1]!) * (e[2]! - e[0]!)) / (e[3]! - e[1]!), e[4]!]);
    }
    hits.sort((a, b) => a[0] - b[0]);
    const row = Math.floor(sy / s) * w;
    let wind = 0;
    for (let i = 0; i + 1 < hits.length; i++) {
      wind += hits[i]![1];
      if (!wind) continue;
      const from = Math.max(0, Math.ceil(hits[i]![0] * s - 0.5));
      const to = Math.min(w * s, Math.ceil(hits[i + 1]![0] * s - 0.5));
      for (let k = from; k < to; k++) cov[row + Math.floor(k / s)] += each;
    }
  }
  return cov;
}

// A rounded square `size` across with its corner at (ox, oy).
export function roundedSquare(size: number, ox = 0, oy = 0): Pt[] {
  const r = RADIUS * size;
  const pts: Pt[] = [];
  const corners: Pt[] = [[size - r, r], [size - r, size - r], [r, size - r], [r, r]];
  corners.forEach(([cx, cy], q) => {
    for (let i = 0; i <= 24; i++) {
      const a = -Math.PI / 2 + (q + i / 24) * (Math.PI / 2);
      pts.push([ox + cx + r * Math.cos(a), oy + cy + r * Math.sin(a)]);
    }
  });
  return pts;
}

// The mark's polygons placed on a `box`-wide tile whose corner is (ox, oy).
export function markPolys(m: Mark, box: number, ox = 0, oy = 0): Pt[][] {
  const span = m.span ?? MARK_SPAN;
  return m.paths.flatMap((d) =>
    flatten(parse(d)).map((p) => p.map(([X, Y]): Pt => [ox + ((X - 32) / span + 0.5) * box, oy + ((Y - 33) / span + 0.5) * box])),
  );
}

// The tile's pixels, RGBA, on a `size` canvas. inset: the share of the side
// left empty (transparent) on each edge, for a platform whose icons sit on a
// grid smaller than the canvas (the Mac's). 0 is Hearth's full-bleed tile.
export function tilePixels(size: number, m: Mark, inset = 0): Buffer {
  const s = size <= 128 ? 8 : 4;
  const o = inset * size;
  const box = size - 2 * o;
  const bg = cover(size, [roundedSquare(box, o, o)], s);
  const mark = new Float32Array(size * size);
  // Each path on its own, then added: overlapping paths never cancel out.
  for (const d of m.paths) {
    const c = cover(size, markPolys({ ...m, paths: [d] }, box, o, o), s);
    for (let i = 0; i < c.length; i++) mark[i] = Math.min(1, mark[i]! + c[i]!);
  }
  const rgba = Buffer.alloc(size * size * 4);
  for (let i = 0; i < size * size; i++) {
    const a = Math.min(1, bg[i]!);
    const t = a ? Math.min(1, mark[i]! / a) : 0;
    for (let c = 0; c < 3; c++) rgba[i * 4 + c] = Math.round(DARK[c]! * (1 - t) + LIGHT[c]! * t);
    rgba[i * 4 + 3] = Math.round(255 * a);
  }
  return rgba;
}

// ---------------------------------------------------------------- PNG

const CRC = new Int32Array(256).map((_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c;
});
function crc32(bytes: Uint8Array): number {
  let c = -1;
  for (const b of bytes) c = CRC[(c ^ b) & 0xff]! ^ (c >>> 8);
  return (c ^ -1) >>> 0;
}
function chunk(type: string, data: Buffer): Buffer {
  const head = Buffer.alloc(8);
  head.writeUInt32BE(data.length, 0);
  head.write(type, 4, "ascii");
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(Buffer.concat([head.subarray(4), data])), 0);
  return Buffer.concat([head, data, crc]);
}
export function png(w: number, h: number, rgba: Buffer): Buffer {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr.set([8, 6, 0, 0, 0], 8);
  const rows = Buffer.alloc(h * (w * 4 + 1));
  for (let y = 0; y < h; y++) rgba.copy(rows, y * (w * 4 + 1) + 1, y * w * 4, (y + 1) * w * 4);
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(rows, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}
