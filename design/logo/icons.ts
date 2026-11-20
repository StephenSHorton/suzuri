// Writes every icon suzuri ships from one picked logo candidate (an
// N-name.svg here, written by build.ts), drawn by tile.ts the way Hearth's
// apps/web/scripts/icons.ts draws Hearth's: no image library, no artwork from
// anywhere else.
//
//   bun design/logo/icons.ts                  write them from the pick in pick.json
//   bun design/logo/icons.ts 1-gate           pick another candidate, then write
//   bun design/logo/icons.ts 8-glyph --small 6-small-glyph --small-up-to 32
//                                             the full pick, said in full
//   bun design/logo/icons.ts --small none     one drawing at every size
//   bun design/logo/icons.ts --check          fail when a written file differs
//                                             from what the pick draws
//
// Written:
//   app/icons/32x32.png, 128x128.png, 128x128@2x.png, icon.png (512)
//                         Tauri's bundle list (tauri.conf.json); the PNGs are
//                         the window icon on Linux
//   app/icons/icon.ico    16, 24, 32, 48, 64, 256: the Windows exe's icon (title
//                         bar, taskbar, Alt-Tab, Start menu, Explorer) and the
//                         installer's and uninstaller's
//   app/icons/icon.icns   16 to 1024 with every @2x: the Mac app (Dock, Finder,
//                         About panel, and the Dock icon of a dev run)
//   packaging/windows/installer-header.bmp (150x57), installer-sidebar.bmp
//                         (164x314): the NSIS installer's pages
//   design/logo/mark.svg, lockup.svg
//                         the picked mark and its lockup (README.md shows it)
//
// Two drawings: the picked mark, and below `smallUpTo` px a small-size mark
// (the gate's character alone reads where the gate's opening is too few
// pixels). The choice is by the pixels an entry has, so a 16@2x entry (32 px)
// is a small one and a 32@2x (64 px) is not.
//
// The Mac's icons sit on Apple's grid: the tile is 824 of 1024 px with the
// rest transparent, as every app in the Dock draws, so suzuri is the size of
// its neighbours there. Hearth's own .icns is full-bleed (no margin), which is
// why it looks larger beside them. Windows and the PNGs are full-bleed, as
// Windows icons are; so are the Mac's 16 and 32 px entries (Finder lists,
// menus), where a margin costs the character its legibility.
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { inflateSync } from "node:zlib";
import { cover, DARK, LIGHT, type Mark, markPolys, png, readMark, roundedSquare, tilePixels } from "./tile.ts";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..");
const icons = join(root, "app", "icons");
const windows = join(root, "packaging", "windows");

// ---------------------------------------------------------------- the pick

type Pick = { full: string; small: string | null; smallUpTo: number };
const pickFile = join(here, "pick.json");
const DEFAULT: Pick = { full: "8-glyph", small: "6-small-glyph", smallUpTo: 32 };

const args = process.argv.slice(2);
const check = args.includes("--check");
const flag = (name: string) => {
  const at = args.indexOf(name);
  if (at < 0) return undefined;
  const v = args[at + 1];
  if (!v || v.startsWith("--")) throw new Error(`${name} needs a value`);
  return v;
};
const positional = args.filter((a, i) => !a.startsWith("--") && !["--small", "--small-up-to"].includes(args[i - 1] ?? ""));

const saved: Pick = existsSync(pickFile) ? { ...DEFAULT, ...JSON.parse(readFileSync(pickFile, "utf8")) } : DEFAULT;
const changed = positional.length > 0 || flag("--small") !== undefined || flag("--small-up-to") !== undefined;
if (check && changed) throw new Error("--check checks the saved pick; give no candidate with it");
const small = flag("--small");
const pick: Pick = {
  full: (positional[0] ?? saved.full).replace(/\.svg$/, "").replace(/^.*\//, ""),
  small: small === undefined ? saved.small : small === "none" ? null : small.replace(/\.svg$/, "").replace(/^.*\//, ""),
  smallUpTo: Number(flag("--small-up-to") ?? saved.smallUpTo),
};

function load(id: string): Mark {
  const file = join(here, `${id}.svg`);
  if (!/^\d+-[a-z0-9-]+$/.test(id) || id.endsWith("-lockup") || !existsSync(file)) throw new Error(`no candidate ${id} (${file}); the candidates are the N-name.svg files build.ts writes`);
  return readMark(file, id);
}
const FULL = load(pick.full);
const SMALL = pick.small ? load(pick.small) : FULL;
const markFor = (px: number) => (px <= pick.smallUpTo ? SMALL : FULL);

// ---------------------------------------------------------------- drawings

// Apple's grid: an 824 px tile on the 1024 px canvas. Entries of 32 px and
// under stay full-bleed: inset, the 16 px tile is under 13 px and the
// character's strokes run together (tried: the small mark's pixel-row lock
// only holds on the full tile).
const MAC_INSET = 100 / 1024;
const MAC_FULL_BLEED_UP_TO = 32;

const drawn = new Map<string, Buffer>();
function tile(px: number, inset = 0): Buffer {
  const key = `${px}/${inset}`;
  if (!drawn.has(key)) drawn.set(key, png(px, px, tilePixels(px, markFor(px), inset)));
  return drawn.get(key)!;
}

// icon.ico: PNGs in a Windows icon directory.
function ico(sizes: number[]): Buffer {
  const images = sizes.map((s) => tile(s));
  const head = Buffer.alloc(6 + 16 * sizes.length);
  head.writeUInt16LE(1, 2);
  head.writeUInt16LE(sizes.length, 4);
  let at = head.length;
  sizes.forEach((size, i) => {
    const entry = 6 + 16 * i;
    head[entry] = size >= 256 ? 0 : size;
    head[entry + 1] = size >= 256 ? 0 : size;
    head.writeUInt16LE(1, entry + 4);
    head.writeUInt16LE(32, entry + 6);
    head.writeUInt32LE(images[i]!.length, entry + 8);
    head.writeUInt32LE(at, entry + 12);
    at += images[i]!.length;
  });
  return Buffer.concat([head, ...images]);
}

// icon.icns, by type and pixels: ic04 16 and ic05 32 as Apple's "ARGB"
// (what iconutil writes; macOS draws a PNG in the old icp4/icp5 slots as
// noise), then PNGs: ic11 32 (16@2x), ic12 64 (32@2x), ic07 128, ic13 256
// (128@2x), ic08 256, ic14 512 (256@2x), ic09 512, ic10 1024 (512@2x).
const ICNS: [string, number][] = [
  ["ic04", 16], ["ic05", 32], ["ic11", 32], ["ic12", 64], ["ic07", 128],
  ["ic13", 256], ["ic08", 256], ["ic14", 512], ["ic09", 512], ["ic10", 1024],
];
// "ARGB", then the A, R, G and B planes, each as literal runs of up to 128
// bytes (a count byte n < 128 means n + 1 bytes follow as they are).
function argb(rgba: Buffer): Buffer {
  const out: number[] = [0x41, 0x52, 0x47, 0x42];
  const n = rgba.length / 4;
  for (const ch of [3, 0, 1, 2]) {
    for (let i = 0; i < n; i += 128) {
      const len = Math.min(128, n - i);
      out.push(len - 1);
      for (let j = 0; j < len; j++) out.push(rgba[(i + j) * 4 + ch]!);
    }
  }
  return Buffer.from(out);
}
function icns(): Buffer {
  const parts = ICNS.map(([type, size]) => {
    const inset = size <= MAC_FULL_BLEED_UP_TO ? 0 : MAC_INSET;
    const body = type === "ic04" || type === "ic05" ? argb(tilePixels(size, markFor(size), inset)) : tile(size, inset);
    const head = Buffer.alloc(8);
    head.write(type, 0, "ascii");
    head.writeUInt32BE(8 + body.length, 4);
    return Buffer.concat([head, body]);
  });
  const head = Buffer.alloc(8);
  head.write("icns", 0, "ascii");
  head.writeUInt32BE(8 + parts.reduce((n, p) => n + p.length, 0), 4);
  return Buffer.concat([head, ...parts]);
}

// A w by h picture in one colour with RGBA layers laid over it at (x, y).
function picture(w: number, h: number, bg: readonly number[], layers: { rgba: Buffer; size: number; x: number; y: number }[]): Buffer {
  const out = Buffer.alloc(w * h * 3);
  for (let i = 0; i < w * h; i++) out.set(bg.slice(0, 3), i * 3);
  for (const { rgba, size, x, y } of layers) {
    for (let py = 0; py < size; py++) {
      for (let px = 0; px < size; px++) {
        const from = (py * size + px) * 4;
        const a = rgba[from + 3]! / 255;
        const to = ((y + py) * w + x + px) * 3;
        for (let c = 0; c < 3; c++) out[to + c] = Math.round(rgba[from + c]! * a + out[to + c]! * (1 - a));
      }
    }
  }
  return out;
}

// The mark alone in the light colour, no tile, `box` px across its 90 units.
function bareMark(box: number): Buffer {
  const m = FULL;
  const c = cover(box, markPolys(m, box), 8);
  const rgba = Buffer.alloc(box * box * 4);
  for (let i = 0; i < c.length; i++) rgba.set([...LIGHT, Math.round(255 * Math.min(1, c[i]!))], i * 4);
  return rgba;
}

// A 24-bit uncompressed BMP (bottom-up rows padded to 4 bytes): what NSIS's
// Modern UI takes for its header and welcome-page pictures.
function bmp(w: number, h: number, rgb: Buffer): Buffer {
  const stride = Math.ceil((w * 3) / 4) * 4;
  const out = Buffer.alloc(54 + stride * h);
  out.write("BM", 0, "ascii");
  out.writeUInt32LE(out.length, 2);
  out.writeUInt32LE(54, 10);
  out.writeUInt32LE(40, 14);
  out.writeInt32LE(w, 18);
  out.writeInt32LE(h, 22);
  out.writeUInt16LE(1, 26);
  out.writeUInt16LE(24, 28);
  out.writeUInt32LE(stride * h, 34);
  out.writeInt32LE(2835, 38);
  out.writeInt32LE(2835, 42);
  for (let y = 0; y < h; y++) {
    const row = 54 + (h - 1 - y) * stride;
    for (let x = 0; x < w; x++) {
      const from = (y * w + x) * 3;
      out[row + x * 3] = rgb[from + 2]!;
      out[row + x * 3 + 1] = rgb[from + 1]!;
      out[row + x * 3 + 2] = rgb[from]!;
    }
  }
  return out;
}

// The installer's header (150x57, on the white header band): the app tile at
// its left, where Modern UI puts the picture (Tauri sets no MUI_HEADERIMAGE_RIGHT).
function header(): Buffer {
  const size = 45;
  return bmp(150, 57, picture(150, 57, [255, 255, 255], [{ rgba: tilePixels(size, markFor(size)), size, x: 8, y: 6 }]));
}
// The welcome and finish pages' side picture (164x314): the mark on the tile's colour.
function sidebar(): Buffer {
  const box = 150;
  return bmp(164, 314, picture(164, 314, DARK, [{ rgba: bareMark(box), size: box, x: 7, y: 60 }]));
}

const svgOf = (id: string) => readFileSync(join(here, `${id}.svg`));

const FILES: Record<string, () => Buffer> = {
  [join(icons, "32x32.png")]: () => tile(32),
  [join(icons, "128x128.png")]: () => tile(128),
  [join(icons, "128x128@2x.png")]: () => tile(256),
  [join(icons, "icon.png")]: () => tile(512),
  [join(icons, "icon.ico")]: () => ico([16, 24, 32, 48, 64, 256]),
  [join(icons, "icon.icns")]: () => icns(),
  [join(windows, "installer-header.bmp")]: () => header(),
  [join(windows, "installer-sidebar.bmp")]: () => sidebar(),
  [join(here, "mark.svg")]: () => svgOf(pick.full),
  [join(here, "lockup.svg")]: () => svgOf(`${pick.full}-lockup`),
  [pickFile]: () => Buffer.from(`${JSON.stringify(pick, null, 2)}\n`),
};

// ---------------------------------------------------------------- --check

// What a PNG shows: its chunks with the picture data unpacked (two zlibs pack
// the same pixels into different bytes), as Hearth's --check does.
function shown(bytes: Buffer): string {
  if (bytes.readUInt32BE(0) !== 0x89504e47) return bytes.toString("base64");
  const parts: string[] = [];
  const idat: Buffer[] = [];
  for (let at = 8; at + 8 <= bytes.length; ) {
    const length = bytes.readUInt32BE(at);
    const type = bytes.toString("ascii", at + 4, at + 8);
    const data = bytes.subarray(at + 8, at + 8 + length);
    if (type === "IDAT") idat.push(data);
    else parts.push(type, data.toString("hex"));
    at += 12 + length;
  }
  return JSON.stringify([parts, idat.length ? inflateSync(Buffer.concat(idat)).toString("base64") : ""]);
}
function pieces(path: string, bytes: Buffer): string[] {
  if (path.endsWith(".ico")) {
    const n = bytes.readUInt16LE(4);
    return Array.from({ length: n }, (_, i) => {
      const e = 6 + 16 * i;
      const off = bytes.readUInt32LE(e + 12);
      return bytes.subarray(e, e + 8).toString("hex") + shown(bytes.subarray(off, off + bytes.readUInt32LE(e + 8)));
    });
  }
  if (path.endsWith(".icns")) {
    const found: string[] = [];
    for (let at = 8; at + 8 <= bytes.length; ) {
      const length = bytes.readUInt32BE(at + 4);
      found.push(bytes.toString("ascii", at, at + 4) + shown(bytes.subarray(at + 8, at + length)));
      at += length;
    }
    return found;
  }
  if (path.endsWith(".png")) return [shown(bytes)];
  return [bytes.toString("base64")];
}
function same(path: string, want: Buffer): boolean {
  if (!existsSync(path)) return false;
  try {
    const a = pieces(path, readFileSync(path));
    const b = pieces(path, want);
    return a.length === b.length && a.every((p, i) => p === b[i]);
  } catch {
    return false;
  }
}

let stale = 0;
for (const [path, make] of Object.entries(FILES)) {
  const bytes = make();
  if (check) {
    if (!same(path, bytes)) {
      console.error(`${path} is not what design/logo/icons.ts draws for the pick in pick.json; run it again.`);
      stale++;
    }
  } else {
    writeFileSync(path, bytes);
  }
}
if (stale) process.exit(1);
const what = `${pick.full}${pick.small ? `, ${pick.small} at ${pick.smallUpTo} px and under` : ""}`;
console.log(check ? `Every icon is what ${what} draws.` : `Wrote ${Object.keys(FILES).length} files from ${what}.`);
