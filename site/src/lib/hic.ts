import { HIC_PREF_KEY } from "@/lib/site"

export function readHicPref(): boolean {
  try {
    const stored = localStorage.getItem(HIC_PREF_KEY)
    if (stored === "0") return false
    if (stored === "1") return true
  } catch {
    /* private mode */
  }
  return true
}

export function writeHicPref(on: boolean) {
  try {
    localStorage.setItem(HIC_PREF_KEY, on ? "1" : "0")
  } catch {
    /* ignore */
  }
}
