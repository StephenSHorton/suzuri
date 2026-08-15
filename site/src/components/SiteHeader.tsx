import { Link } from "@tanstack/react-router"
import { GITHUB, RELEASES } from "@/lib/site"

export function SiteHeader() {
  return (
    <header className="mx-auto flex w-[min(920px,92vw)] items-center justify-between pt-6 pb-2">
      <Link to="/" className="m-0 flex items-center gap-2 font-semibold tracking-wide text-foreground no-underline">
        <span className="font-serif text-[1.35em] text-accent">硯</span>
        suzuri
      </Link>
      <nav className="text-[0.9rem]">
        <Link to="/docs" className="ml-5 text-mute no-underline hover:text-accent">
          Docs
        </Link>
        <a href={GITHUB} className="ml-5 text-mute no-underline hover:text-accent">
          GitHub
        </a>
        <a href={RELEASES} className="ml-5 text-mute no-underline hover:text-accent">
          Download
        </a>
      </nav>
    </header>
  )
}
