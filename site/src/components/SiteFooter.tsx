import { useEffect, useState } from "react"
import { Link } from "@tanstack/react-router"
import { CANVAS_UI, GITHUB } from "@/lib/site"
import { writeHicPref } from "@/lib/hic"

type VersionJson = {
  version?: string
  built_at?: string
  source?: string
  commit?: string
}

export function SiteFooter({
  browserHasHic,
  preferHic,
}: {
  browserHasHic: boolean
  preferHic: boolean
}) {
  const [ver, setVer] = useState<VersionJson | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    const stamped = import.meta.env.VITE_SITE_VERSION as string | undefined
    if (stamped && !stamped.includes("__SITE_")) {
      setVer({
        version: stamped,
        built_at: import.meta.env.VITE_SITE_BUILT as string | undefined,
      })
      return
    }
    fetch(`${import.meta.env.BASE_URL}version.json?_=${Date.now()}`)
      .then((r) => (r.ok ? r.json() : null))
      .then((j: VersionJson | null) => {
        if (!j?.version) {
          setMissing(true)
          return
        }
        setVer(j)
      })
      .catch(() => setMissing(true))
  }, [])

  return (
    <footer className="mx-auto w-[min(920px,92vw)] border-t border-border pt-6 pb-10 text-[0.82rem] text-mute">
      <p>
        MIT © Stephen S. Horton ·{" "}
        <a href={GITHUB} className="text-accent no-underline hover:underline">
          StephenSHorton/suzuri
        </a>
        {" · "}
        <Link to="/privacy" className="text-accent no-underline hover:underline">
          Privacy
        </Link>
      </p>
      <p className="mt-1.5 opacity-75">
        Effects by{" "}
        <a href={CANVAS_UI} className="text-accent no-underline hover:underline">
          Canvas UI
        </a>{" "}
        (Glyph Rain, Glitch, Decrypt Reveal) · GohuFont uni14 Nerd Font Mono is
        bundled under WTFPL.
      </p>
      <p className="mt-2.5 text-[0.78rem] tracking-wide">
        site{" "}
        <strong className="font-semibold text-accent">
          {missing ? "local / unstamped" : ver?.version ?? "…"}
        </strong>
        {ver?.built_at ? (
          <span className="ml-1 opacity-75">· {ver.built_at}</span>
        ) : null}
      </p>
      <p className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 text-[0.78rem]">
        <label
          className={`inline-flex items-center gap-2 ${browserHasHic ? "cursor-pointer" : "cursor-not-allowed opacity-65"}`}
          title={
            browserHasHic
              ? "When off, Glitch and Decrypt use plain HTML."
              : "This browser does not expose html-in-canvas."
          }
        >
          <input
            type="checkbox"
            className="peer sr-only"
            checked={browserHasHic && preferHic}
            disabled={!browserHasHic}
            onChange={(e) => {
              writeHicPref(e.target.checked)
              location.reload()
            }}
          />
          <span className="relative h-[1.15rem] w-[2.1rem] shrink-0 rounded-full border border-border bg-[#1a2e22] after:absolute after:top-px after:left-0.5 after:size-[0.85rem] after:rounded-full after:bg-mute after:transition-transform peer-checked:border-[#1f6b3f] peer-checked:bg-accent/20 peer-checked:after:translate-x-[0.9rem] peer-checked:after:bg-accent peer-focus-visible:outline peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-accent" />
          <span>html-in-canvas effects</span>
        </label>
        <span
          className={
            !browserHasHic
              ? "text-mute"
              : preferHic
                ? "text-accent"
                : "text-[#c9a227]"
          }
        >
          {!browserHasHic
            ? "browser: unavailable"
            : preferHic
              ? "on"
              : "off · plain HTML preview"}
        </span>
      </p>
    </footer>
  )
}
