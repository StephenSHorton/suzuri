import { useEffect, useRef } from "react"
import { createGlyphRain } from "@/vendor/canvas-ui/glyph-rain.js"

export function GlyphRain() {
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const root = rootRef.current
    if (!root) return
    const source = root.querySelector<HTMLCanvasElement>(".glyph-rain-source")
    const empty = root.querySelector<HTMLElement>(".glyph-rain-empty")
    const output = root.querySelector<HTMLCanvasElement>(".glyph-rain-output")
    if (!source || !empty || !output) return

    const rain = createGlyphRain(
      { source, content: empty, output },
      {
        charset:
          "ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ0123456789:.=*+-<>|",
        cell: 14,
        color: [0.0, 0.55, 0.28],
        headColor: [0.91, 1.0, 0.94],
        speed: 0.18,
        speedVariance: 0.55,
        density: 0.14,
        trail: 0.7,
        glow: 1.6,
        mutate: 0.35,
        flicker: 0.12,
        layers: 2,
        dim: 0.12,
        light: 0,
        lightRadius: 220,
        lightHeight: 160,
        relief: 0,
        stir: 0.55,
        stirRadius: 280,
        settle: 0.95,
      },
    )
    if (!rain) root.classList.add("glyph-rain-failed")
    return () => {
      rain?.destroy?.()
    }
  }, [])

  return (
    <div
      ref={rootRef}
      className="glyph-rain-backdrop pointer-events-none fixed inset-0 z-0 overflow-hidden"
      aria-hidden
      data-mode="overlay"
    >
      <canvas className="glyph-rain-source absolute inset-0 block size-full" />
      <div className="glyph-rain-empty hidden" />
      <canvas className="glyph-rain-output absolute inset-0 block size-full mix-blend-screen opacity-90" />
    </div>
  )
}
