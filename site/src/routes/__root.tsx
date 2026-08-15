import { createRootRoute, Outlet } from "@tanstack/react-router"
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools"
import { useMemo } from "react"
import { GlyphRain } from "@/components/GlyphRain"
import { SiteFooter } from "@/components/SiteFooter"
import { SiteHeader } from "@/components/SiteHeader"
import { HicProvider } from "@/lib/hic-context"
import { readHicPref } from "@/lib/hic"
import { supportsHtmlInCanvas } from "@/vendor/canvas-ui/glyph-rain.js"

export const Route = createRootRoute({
  component: RootLayout,
})

function RootLayout() {
  const browserHasHic = useMemo(() => supportsHtmlInCanvas(), [])
  const preferHic = useMemo(() => readHicPref(), [])
  const useHic = browserHasHic && preferHic

  return (
    <HicProvider value={{ browserHasHic, preferHic, useHic }}>
      <GlyphRain />
      <div className="page relative z-[1] min-h-dvh">
        <SiteHeader />
        <Outlet />
        <SiteFooter browserHasHic={browserHasHic} preferHic={preferHic} />
      </div>
      {import.meta.env.DEV ? <TanStackRouterDevtools /> : null}
    </HicProvider>
  )
}
