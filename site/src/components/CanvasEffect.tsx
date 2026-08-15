import { useEffect, useRef, type ReactNode } from "react"
import { createDecryptReveal } from "@/vendor/canvas-ui/decrypt-reveal.js"
import { createGlitch } from "@/vendor/canvas-ui/glitch.js"

type Kind = "glitch" | "decrypt"

type Instance = { resize?: () => void; destroy?: () => void } | null

const glitchOpts = {
  intensity: 0.85,
  interval: 2.4,
  duration: 0.35,
  slices: 18,
  shift: 22,
  rgbShift: 3,
  blocks: 0.35,
  noise: 0.28,
}

const decryptOpts = {
  radius: 320,
  softness: 0.55,
  cell: 11,
  aspect: 0.72,
  color: "#00e676",
  background: "#050806",
  colored: 0.85,
  brightness: 1.15,
  legibility: 1,
  scramble: 0.14,
  scrambleSpeed: 8,
  edgeWidth: 0.24,
  edgeFlicker: 0.95,
  edgeGlow: 2,
  edgeTint: 0.85,
  aberration: 8,
  passthrough: 0.06,
  threshold: 0.025,
  smoothing: 0.16,
}

export function CanvasEffect({
  kind,
  useHic,
  className,
  children,
}: {
  kind: Kind
  useHic: boolean
  className?: string
  children: ReactNode
}) {
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const root = rootRef.current
    if (!root) return
    const source = root.querySelector<HTMLCanvasElement>(".cui-source")
    const content = root.querySelector<HTMLElement>(".cui-content")
    const output = root.querySelector<HTMLCanvasElement>(".cui-output")
    if (!source || !content || !output) return

    root.dataset.mode = useHic ? "html-in-canvas" : "fallback"
    root.classList.toggle("cui-failed", !useHic)

    if (!useHic) {
      source.style.display = "none"
      return
    }

    source.style.display = ""
    if (content.parentElement !== source) source.appendChild(content)

    const create = kind === "glitch" ? createGlitch : createDecryptReveal
    const opts = kind === "glitch" ? glitchOpts : decryptOpts
    const instance: Instance = create({ source, content, output }, opts)
    if (!instance) {
      root.classList.add("cui-failed")
      root.dataset.mode = "fallback"
      return
    }

    const syncHeight = () => {
      const h = Math.ceil(
        Math.max(
          content.scrollHeight,
          content.offsetHeight,
          content.getBoundingClientRect().height,
          1,
        ),
      )
      root.style.height = `${h}px`
      source.style.height = `${h}px`
      output.style.height = `${h}px`
      instance.resize?.()
    }
    const ro = new ResizeObserver(() => {
      requestAnimationFrame(() => requestAnimationFrame(syncHeight))
    })
    ro.observe(content)
    requestAnimationFrame(() => requestAnimationFrame(syncHeight))

    return () => {
      ro.disconnect()
      instance.destroy?.()
      if (content.parentElement === source) {
        root.insertBefore(content, output)
      }
    }
  }, [kind, useHic])

  return (
    <div
      ref={rootRef}
      className={`cui-mount relative isolate w-full ${kind === "glitch" ? "cui-glitch mb-5" : "cui-decrypt"} ${className ?? ""}`}
      data-label={kind === "glitch" ? "glitch" : "decrypt-reveal"}
    >
      <canvas className="cui-source absolute inset-0 z-0 block size-full" />
      <div className="cui-content relative z-[1] w-full">{children}</div>
      <canvas className="cui-output pointer-events-none absolute inset-0 z-[2] block size-full" aria-hidden />
    </div>
  )
}
