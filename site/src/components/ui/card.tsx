import * as React from "react"
import { cn } from "@/lib/utils"

function Card({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "rounded-lg border border-border bg-panel/90 backdrop-blur-sm p-5 transition-colors hover:border-[#245a3a]",
        className,
      )}
      {...props}
    />
  )
}

function CardTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return (
    <h2
      className={cn("m-0 mb-2 text-[0.95rem] font-semibold text-accent", className)}
      {...props}
    />
  )
}

function CardDescription({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p className={cn("m-0 text-[0.88rem] text-mute", className)} {...props} />
  )
}

export { Card, CardTitle, CardDescription }
