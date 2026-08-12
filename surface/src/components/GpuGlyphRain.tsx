import { Text, View, useFrame, useViewport, rgba, type RGBA } from "kussetsu";
import { useEffect, useMemo, useRef, useState } from "react";

/**
 * Real katakana/digit rain in the Kussetsu backdrop (glass can refract it).
 *
 * Perf notes:
 * - Positions update every rAF (not throttled) so motion is 60fps-smooth.
 * - Fixed glyph pool + stable keys (no mount/unmount churn).
 * - Short trails / capped drops keep Yoga + glyph draws cheap.
 */

const CHARSET =
  "ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ0123456789Z*+-<>¦=:.";

// Precomputed colors — avoid rgba() allocs every glyph every frame
const COL_HEAD = rgba("#dcffeb");
const COL_NEAR = rgba("#50ffa0");
const COL_TRAIL: RGBA[] = [
  rgba("#00e676", 0.5),
  rgba("#00e676", 0.38),
  rgba("#00e676", 0.28),
  rgba("#00e676", 0.2),
  rgba("#00e676", 0.14),
  rgba("#00e676", 0.1),
];

type Drop = {
  x: number;
  y: number;
  speed: number;
  chars: string[];
};

function randGlyph() {
  return CHARSET[(Math.random() * CHARSET.length) | 0] ?? "0";
}

type GpuGlyphRainProps = {
  cell?: number;
  /** Simultaneous falling columns */
  maxDrops?: number;
  /** Glyphs per column (including head) */
  trail?: number;
};

export function GpuGlyphRain({
  cell = 16,
  maxDrops = 36,
  trail = 9,
}: GpuGlyphRainProps) {
  const { width: vw, height: vh } = useViewport();
  const drops = useRef<Drop[]>([]);
  const scramble = useRef(0);
  const [, setTick] = useState(0);

  // Fixed slot count for stable React keys
  const slots = useMemo(
    () => maxDrops * trail,
    [maxDrops, trail],
  );

  useEffect(() => {
    if (vw < 8 || vh < 8) return;
    const cols = Math.max(8, Math.floor(vw / cell));
    const n = Math.min(maxDrops, Math.floor(cols * 0.5));
    const next: Drop[] = [];
    const used = new Set<number>();
    let guard = 0;
    while (next.length < n && guard++ < n * 10) {
      const ci = (Math.random() * cols) | 0;
      if (used.has(ci)) continue;
      used.add(ci);
      next.push({
        x: ci * cell + 2,
        y: Math.random() * vh,
        speed: 70 + Math.random() * 100,
        chars: Array.from({ length: trail }, randGlyph),
      });
    }
    // Pad to maxDrops so keys stay stable if we grow later
    while (next.length < maxDrops) {
      next.push({
        x: -9999,
        y: -9999,
        speed: 0,
        chars: Array.from({ length: trail }, () => " "),
      });
    }
    drops.current = next;
    setTick((t) => t + 1);
  }, [vw, vh, cell, maxDrops, trail]);

  useFrame((dt) => {
    const list = drops.current;
    if (!list.length) return;

    scramble.current += dt;
    const mut = scramble.current > 0.1;
    if (mut) scramble.current = 0;

    for (let di = 0; di < list.length; di++) {
      const d = list[di]!;
      if (d.speed <= 0) continue;
      d.y += d.speed * dt;
      if (d.y - trail * cell > vh) {
        d.y = -Math.random() * cell * trail;
        d.speed = 70 + Math.random() * 100;
        for (let i = 0; i < trail; i++) d.chars[i] = randGlyph();
      } else if (mut) {
        // Mutate mid-trail only (not every cell) — cheaper, still lively
        const i = 1 + ((Math.random() * (trail - 1)) | 0);
        d.chars[i] = randGlyph();
      }
    }

    // Every display frame — was ~28fps before (main jitter source)
    setTick((t) => t + 1);
  });

  if (vw < 8 || vh < 8) return null;

  const list = drops.current;
  // Fixed-length children array for stable reconciler shape
  const children = new Array(slots);
  let k = 0;
  for (let di = 0; di < maxDrops; di++) {
    const d = list[di];
    for (let i = 0; i < trail; i++, k++) {
      if (!d || d.speed <= 0) {
        children[k] = (
          <Text
            key={k}
            style={{ absolute: { x: -9999, y: -9999 }, fontSize: 1, color: COL_TRAIL[0] }}
          >
            {" "}
          </Text>
        );
        continue;
      }
      const gy = d.y - i * cell;
      // Park offscreen instead of omitting — keeps the child list length fixed
      if (gy < -cell || gy > vh + cell) {
        children[k] = (
          <Text
            key={k}
            style={{ absolute: { x: -9999, y: -9999 }, fontSize: 1, color: COL_TRAIL[0] }}
          >
            {" "}
          </Text>
        );
        continue;
      }
      const color: RGBA =
        i === 0 ? COL_HEAD : i < 3 ? COL_NEAR : COL_TRAIL[Math.min(i - 3, COL_TRAIL.length - 1)]!;
      children[k] = (
        <Text
          key={k}
          style={{
            absolute: { x: d.x, y: gy },
            fontSize: cell - 2,
            fontWeight: i === 0 ? 700 : 500,
            color,
          }}
        >
          {d.chars[i]!}
        </Text>
      );
    }
  }

  return (
    <View
      style={{
        absolute: { x: 0, y: 0 },
        width: vw,
        height: vh,
      }}
    >
      {children}
    </View>
  );
}
