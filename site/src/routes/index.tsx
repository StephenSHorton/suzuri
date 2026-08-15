import { createFileRoute } from "@tanstack/react-router"
import { useEffect, useRef, useState } from "react"
import { CanvasEffect } from "@/components/CanvasEffect"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardDescription, CardTitle } from "@/components/ui/card"
import { useHic } from "@/lib/hic-context"
import { GITHUB, MS_STORE, RELEASES } from "@/lib/site"

export const Route = createFileRoute("/")({
  component: HomePage,
})

type GhAsset = { name: string; browser_download_url: string }
type GhRelease = { tag_name: string; html_url: string; assets?: GhAsset[] }

function pickMac(assets: GhAsset[]) {
  return (
    assets.find((x) => /darwin-arm64\.dmg$/i.test(x.name)) ||
    assets.find((x) => /darwin-arm64\.app\.zip$/i.test(x.name)) ||
    assets.find((x) => /darwin-arm64/i.test(x.name))
  )
}

function HomePage() {
  const hic = useHic()
  const { useHic: hicOn, browserHasHic, preferHic } = hic
  const [macHref, setMacHref] = useState(RELEASES)
  const [tag, setTag] = useState<string | null>(null)

  useEffect(() => {
    fetch("https://api.github.com/repos/StephenSHorton/suzuri/releases/latest")
      .then((r) => (r.ok ? r.json() : null))
      .then((rel: GhRelease | null) => {
        if (!rel?.tag_name) return
        setTag(rel.tag_name)
        setMacHref(pickMac(rel.assets ?? [])?.browser_download_url ?? rel.html_url)
      })
      .catch(() => {})
  }, [])

  const hint = !hicOn
    ? browserHasHic && !preferHic
      ? "html-in-canvas effects are off (footer toggle) — plain HTML preview."
      : "Full decrypt effect needs Chrome html-in-canvas (origin trial or flag). Content stays readable."
    : "Move your cursor over the panel to decrypt."

  return (
    <main className="mx-auto w-[min(920px,92vw)]">
      <section className="pt-14 pb-10 max-sm:pt-8">
        <p className="mb-3.5 text-[0.78rem] tracking-[0.12em] text-mute uppercase">
          terminal host · Windows &amp; macOS
        </p>
        <CanvasEffect kind="glitch" useHic={hicOn}>
          <h1 className="m-0 font-serif text-[clamp(2.1rem,5vw,3.1rem)] leading-[1.15] font-bold tracking-wide text-shadow-[0_0_40px_rgba(0,230,118,0.12)]">
            Your window.
            <br />
            Your PTY.
            <br />
            <em className="not-italic text-accent">Real terminal host.</em>
          </h1>
        </CanvasEffect>
        <p className="mb-7 max-w-xl text-mute">
          <strong className="text-foreground">suzuri（硯）</strong> means{" "}
          <em className="not-italic text-foreground">inkstone</em> — where ink is
          ground before writing. A native terminal host for{" "}
          <strong className="text-foreground">Windows</strong> and{" "}
          <strong className="text-foreground">macOS (Apple Silicon)</strong>:
          your window, ConPTY / POSIX PTY, VT cells, GPU chrome, Warp-style input.
          Not a TUI inside someone else’s emulator.
        </p>
        <div className="mb-2 max-w-xl rounded-[10px] border border-border bg-[rgba(10,18,12,0.88)] p-4 backdrop-blur-sm">
          <div className="mb-2.5 flex flex-wrap gap-3">
            <Button href={MS_STORE}>Download for Windows</Button>
            <Button href={macHref}>Download for macOS</Button>
            <Button href={GITHUB} variant="ghost">
              Source on GitHub
            </Button>
          </div>
          <p className="m-0 text-[0.82rem] text-mute">
            {tag ? (
              <>
                Latest <strong className="text-foreground">{tag}</strong> · Microsoft Store · macOS
                arm64 · MIT
              </>
            ) : (
              "Windows · Microsoft Store · macOS arm64 · MIT License"
            )}
          </p>
          <p className="mt-1.5 text-[0.78rem] text-mute opacity-80">
            Linux not yet supported as a host build.
          </p>
        </div>
        <HeroVideo />
      </section>

      <section className="grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-4 py-4 pb-10">
        <Card>
          <CardTitle>Host, not guest</CardTitle>
          <CardDescription>
            Owns the window, the PTY, selection, scrollback, and fonts. Shells run for real —
            PowerShell, cmd, zsh, bash — not a fake pipe.
          </CardDescription>
        </Card>
        <Card>
          <CardTitle>Native chrome</CardTitle>
          <CardDescription>
            Tabs, command palette, settings, themes, and help are GPU-composited over a live
            shell — Crush-inspired without the AI product.
          </CardDescription>
        </Card>
        <Card>
          <CardTitle>Warp-style bar</CardTitle>
          <CardDescription>
            Local line edit, multiline, history, echo suppression. Full-screen apps reclaim the
            bottom. Quiet prompts keep the shell clean.
          </CardDescription>
        </Card>
        <Card>
          <CardTitle>Polish that sticks</CardTitle>
          <CardDescription>
            Window placement, high-contrast themes, matrix/ripple intros, MCP diagnostics for
            agents, and GitHub Releases auto-update.
          </CardDescription>
        </Card>
      </section>

      <section className="pb-12" aria-labelledby="dossier-title">
        <div className="mb-3.5 flex flex-wrap items-baseline justify-between gap-2">
          <h2 id="dossier-title" className="m-0 text-base tracking-wide">
            Session dossier
          </h2>
          <p className="m-0 text-[0.78rem] text-mute">{hint}</p>
        </div>
        <CanvasEffect kind="decrypt" useHic={hicOn}>
          <div className="rounded-xl border border-border bg-[rgba(8,14,10,0.96)] px-5 py-4">
            <header className="mb-4 flex flex-wrap gap-2">
              <Badge>CLASSIFIED // SUZURI</Badge>
              <Badge dim>clearance · operator</Badge>
            </header>
            <dl className="m-0 grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-x-5">
              <div>
                <dt className="mb-0.5 text-[0.72rem] tracking-[0.1em] text-mute uppercase">
                  Codename
                </dt>
                <dd className="m-0">硯 · inkstone</dd>
              </div>
              <div>
                <dt className="mb-0.5 text-[0.72rem] tracking-[0.1em] text-mute uppercase">
                  Role
                </dt>
                <dd className="m-0">Native terminal host</dd>
              </div>
              <div>
                <dt className="mb-0.5 text-[0.72rem] tracking-[0.1em] text-mute uppercase">
                  Targets
                </dt>
                <dd className="m-0">Windows amd64 · macOS arm64</dd>
              </div>
              <div>
                <dt className="mb-0.5 text-[0.72rem] tracking-[0.1em] text-mute uppercase">
                  Shells
                </dt>
                <dd className="m-0">PowerShell · cmd · zsh · bash</dd>
              </div>
              <div>
                <dt className="mb-0.5 text-[0.72rem] tracking-[0.1em] text-mute uppercase">
                  Chrome
                </dt>
                <dd className="m-0">wgpu glass + Warp-style input</dd>
              </div>
              <div>
                <dt className="mb-0.5 text-[0.72rem] tracking-[0.1em] text-mute uppercase">
                  Input
                </dt>
                <dd className="m-0">Warp-style local line editor</dd>
              </div>
              <div className="sm:col-span-2">
                <dt className="mb-0.5 text-[0.72rem] tracking-[0.1em] text-mute uppercase">
                  Mission
                </dt>
                <dd className="m-0">
                  Own the window and the PTY. Render a real VT grid. Compose native UI over a
                  live shell — not a fake pipe, not a TUI guest inside someone else’s emulator.
                </dd>
              </div>
            </dl>
            <footer className="mt-4 flex flex-wrap justify-between gap-2 border-t border-dashed border-border pt-3 text-[0.78rem] text-mute">
              <span>// encrypt with cursor leave</span>
              <a href={GITHUB} className="text-accent no-underline hover:underline">
                open source · MIT
              </a>
            </footer>
          </div>
        </CanvasEffect>
      </section>

      <section className="pb-12">
        <h2 className="mt-0 mb-3 text-base">Essentials</h2>
        <table className="w-full max-w-md border-collapse text-[0.9rem] text-mute">
          <tbody>
            <tr className="border-b border-border">
              <td className="w-[45%] py-1.5 pr-2 whitespace-nowrap">
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  Ctrl
                </kbd>
                +
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  K
                </kbd>
              </td>
              <td>Command palette</td>
            </tr>
            <tr className="border-b border-border">
              <td className="py-1.5 pr-2">
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  Ctrl
                </kbd>
                +
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  ,
                </kbd>
              </td>
              <td>Settings</td>
            </tr>
            <tr className="border-b border-border">
              <td className="py-1.5 pr-2">
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  Ctrl
                </kbd>
                +
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  Shift
                </kbd>
                +
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  T
                </kbd>
              </td>
              <td>New tab</td>
            </tr>
            <tr className="border-b border-border">
              <td className="py-1.5 pr-2">
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  Ctrl
                </kbd>
                +
                <kbd className="rounded-sm border border-border bg-panel px-1.5 py-0.5 text-[0.78rem] text-foreground">
                  /
                </kbd>
              </td>
              <td>Help</td>
            </tr>
          </tbody>
        </table>
      </section>
    </main>
  )
}

function HeroVideo() {
  const videoRef = useRef<HTMLVideoElement>(null)
  const base = import.meta.env.BASE_URL

  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)")
    const apply = () => {
      if (reduce.matches) {
        video.pause()
        video.removeAttribute("autoplay")
        return
      }
      video.play().catch(() => {
        /* autoplay policy — poster remains */
      })
    }
    apply()
    reduce.addEventListener("change", apply)
    return () => reduce.removeEventListener("change", apply)
  }, [])

  return (
    <figure className="hero-shot">
      <video
        ref={videoRef}
        className="hero-shot-video"
        autoPlay
        muted
        loop
        playsInline
        disablePictureInPicture
        controlsList="nodownload nofullscreen noremoteplayback"
        preload="auto"
        poster={`${base}media/suzuri-poster.jpg`}
        width={1600}
        height={1018}
        aria-label="suzuri running a live terminal session"
      >
        <source src={`${base}media/suzuri.mp4`} type="video/mp4" />
      </video>
    </figure>
  )
}
