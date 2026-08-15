import { cn } from "@/lib/utils"

function Badge({
  className,
  dim,
  ...props
}: React.ComponentProps<"span"> & { dim?: boolean }) {
  return (
    <span
      className={cn(
        "inline-block rounded-full border px-2 py-1 text-[0.72rem] uppercase tracking-[0.08em]",
        dim
          ? "border-border text-mute bg-transparent"
          : "border-[#1f6b3f] text-accent bg-accent/10",
        className,
      )}
      {...props}
    />
  )
}

export { Badge }
