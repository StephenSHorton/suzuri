export const GITHUB = "https://github.com/StephenSHorton/suzuri"
export const RELEASES = `${GITHUB}/releases/latest`
export const MS_STORE = "https://apps.microsoft.com/detail/9PJ735V6JKN3"
export const CANVAS_UI = "https://canvasui.dev"

export const HIC_PREF_KEY = "suzuri-site-hic"

export function siteBase(): string {
  const raw = import.meta.env.BASE_URL || "/"
  return raw.endsWith("/") ? raw.slice(0, -1) : raw
}
