export function supportsHtmlInCanvas(): boolean
export function createGlyphRain(
  els: { source: HTMLElement; content: HTMLElement; output: HTMLCanvasElement },
  options?: Record<string, unknown>,
): { resize?: () => void; destroy?: () => void } | null
