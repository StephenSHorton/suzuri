export function createDecryptReveal(
  els: { source: HTMLElement; content: HTMLElement; output: HTMLCanvasElement },
  options?: Record<string, unknown>,
): { resize?: () => void; destroy?: () => void } | null
