import type { GlassSpec, RGBA } from "kussetsu";
import { rgba } from "kussetsu";

/** Inkstone palette — shared between GPU chrome and terminal theme. */
export const C = {
  page: rgba("#050a07"),
  mist100: rgba("#e8f5ee"),
  mist300: rgba("#a8c4b4"),
  mist400: rgba("#6d8f7d", 0.95),
  mist500: rgba("#6d8f7d", 0.7),
  jade: rgba("#00e676"),
  jadeSoft: rgba("#3dff9a"),
  jadeDim: rgba("#00e676", 0.35),
  amber: rgba("#ffb74d"),
  danger: rgba("#ff8a80"),
  ink: rgba("#0a1410"),
  chipIdle: rgba("#12241b", 0.55),
  chipActive: rgba("#1a3d2c", 0.45),
  whiteSoft: rgba("#ffffff", 0.12),
} as const;

/** Default refractive glass for chrome panels. */
export const glassPanel: GlassSpec = {
  refraction: 0.1,
  blur: 10,
  tint: 0.22,
  tintColor: [0.08, 0.55, 0.32, 1] as RGBA,
  rim: 14,
  specular: 0.06,
  dispersion: 0.04,
};

/** Stronger frost/ice for the active tab. */
export const glassActive: GlassSpec = {
  refraction: 0.14,
  blur: 16,
  tint: 0.4,
  tintColor: [0.15, 0.85, 0.5, 1] as RGBA,
  rim: 18,
  specular: 0.12,
  dispersion: 0.06,
};

/** Layout constants — keep GPU chrome + terminal hole in sync. */
export const L = {
  titleH: 44,
  tabH: 36,
  pad: 12,
  gap: 10,
  warpH: 92,
  radius: 16,
  chipRadius: 12,
} as const;

export type HoleRect = { x: number; y: number; w: number; h: number };

/** Terminal cell pane rect (DOM hole under the glass frame). */
export function terminalHole(vw: number, vh: number): HoleRect {
  const x = L.pad;
  const y = L.titleH + L.tabH + L.pad + L.gap;
  const w = Math.max(80, vw - L.pad * 2);
  const h = Math.max(
    80,
    vh - L.titleH - L.tabH - L.warpH - L.pad * 3 - L.gap * 2,
  );
  return { x, y, w, h };
}

