import { createFileRoute, Link, Outlet } from "@tanstack/react-router"

export const Route = createFileRoute("/docs")({
  component: DocsLayout,
})

const links = [
  { to: "/docs", label: "Introduction", exact: true },
  { to: "/docs/install", label: "Install a guest" },
  { to: "/docs/manifest", label: "Manifest" },
  { to: "/docs/protocol", label: "Protocol" },
  { to: "/docs/example", label: "Example plugin" },
  { to: "/docs/faq", label: "FAQ" },
] as const

function DocsLayout() {
  return (
    <div className="mx-auto flex w-[min(920px,92vw)] flex-col gap-10 pt-10 pb-20 md:flex-row">
      <aside className="md:w-48 md:shrink-0">
        <p className="mb-3 text-[0.72rem] tracking-[0.12em] text-mute uppercase">Docs</p>
        <nav className="flex flex-col gap-2 text-sm">
          {links.map((l) => (
            <Link
              key={l.to}
              to={l.to}
              activeOptions={{ exact: "exact" in l && l.exact }}
              className="text-mute no-underline hover:text-accent [&.active]:text-accent"
            >
              {l.label}
            </Link>
          ))}
        </nav>
      </aside>
      <article className="min-w-0 flex-1 text-[0.95rem] leading-relaxed text-mute [&_a]:text-accent [&_code]:rounded-sm [&_code]:bg-panel [&_code]:px-1 [&_h1]:mb-4 [&_h1]:font-serif [&_h1]:text-3xl [&_h1]:text-foreground [&_h2]:mt-8 [&_h2]:mb-2 [&_h2]:text-lg [&_h2]:text-foreground [&_li]:mb-1 [&_p]:mb-4 [&_strong]:text-foreground [&_ul]:mb-4 [&_ul]:list-disc [&_ul]:pl-5">
        <Outlet />
      </article>
    </div>
  )
}
