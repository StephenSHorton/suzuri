declare module "@/vendor/canvas-ui/glyph-rain.js" {
  export function supportsHtmlInCanvas(): boolean
  export function createGlyphRain(
    els: { source: HTMLElement; content: HTMLElement; output: HTMLCanvasElement },
    options?: Record<string, unknown>,
  ): { resize?: () => void; destroy?: () => void } | null
}
declare module "@/vendor/canvas-ui/glitch.js" {
  export function createGlitch(
    els: { source: HTMLElement; content: HTMLElement; output: HTMLCanvasElement },
    options?: Record<string, unknown>,
  ): { resize?: () => void; destroy?: () => void } | null
}
declare module "@/vendor/canvas-ui/decrypt-reveal.js" {
  export function createDecryptReveal(
    els: { source: HTMLElement; content: HTMLElement; output: HTMLCanvasElement },
    options?: Record<string, unknown>,
  ): { resize?: () => void; destroy?: () => void } | null
}
