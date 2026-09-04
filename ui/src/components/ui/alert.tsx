import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

const alertVariants = cva(
  // The content track is minmax(0,1fr), not 1fr: an `fr` track takes its
  // automatic minimum from its content, so one nowrap child -- a Button, which
  // is `whitespace-nowrap shrink-0` by default -- sets a floor wider than the
  // column the alert sits in, and every sibling paragraph is then laid out at
  // that width and clipped by the column mid-word (#1617).
  "relative grid w-full grid-cols-[0_minmax(0,1fr)] items-start gap-y-0.5 rounded-lg border px-4 py-3 text-sm has-[>svg]:grid-cols-[calc(var(--spacing)*4)_minmax(0,1fr)] has-[>svg]:gap-x-3 [&>svg]:size-4 [&>svg]:translate-y-0.5 [&>svg]:text-current",
  {
    variants: {
      variant: {
        default: "bg-card text-card-foreground",
        destructive:
          "bg-card text-destructive *:data-[slot=alert-description]:text-destructive/90 [&>svg]:text-current",
        // Semantic notices alongside destructive, tinted the same way the badge
        // variants are so a warning banner and a warning pill agree on colour.
        warning:
          "border-amber-500/30 bg-amber-500/5 text-amber-700 dark:text-amber-300 *:data-[slot=alert-description]:text-current [&>svg]:text-current",
        success:
          "border-emerald-500/30 bg-emerald-500/5 text-emerald-700 dark:text-emerald-300 *:data-[slot=alert-description]:text-current [&>svg]:text-current",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function Alert({
  className,
  variant,
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof alertVariants>) {
  return (
    <div
      data-slot="alert"
      role="alert"
      className={cn(alertVariants({ variant }), className)}
      {...props}
    />
  )
}

function AlertTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-title"
      className={cn(
        "col-start-2 line-clamp-1 min-h-4 font-medium tracking-tight",
        className
      )}
      {...props}
    />
  )
}

function AlertDescription({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-description"
      className={cn(
        // min-w-0 for the same reason the track above is minmax(0,1fr): this is
        // itself a grid, so its own auto column would otherwise be widened to
        // the min-content width of a nowrap child.
        "col-start-2 grid min-w-0 justify-items-start gap-1 text-sm text-muted-foreground [&_p]:leading-relaxed",
        className
      )}
      {...props}
    />
  )
}

export { Alert, AlertTitle, AlertDescription }
