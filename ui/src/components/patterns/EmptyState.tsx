import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

// EmptyState is the one rendering of "there is nothing here": a dashed outline
// around a centered message and, when the reader can fix the emptiness, the
// action that does. The dashed border is reserved for this meaning — warnings
// and notices use Alert, sections use SectionCard — so the outline itself says
// "empty" before the copy is read.
export function EmptyState({
  icon: Icon,
  action,
  className,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  icon?: LucideIcon;
  action?: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center gap-2 rounded-lg border border-dashed px-4 py-8 text-center text-sm text-muted-foreground",
        className,
      )}
      {...props}
    >
      {Icon && <Icon aria-hidden className="size-5" />}
      <div>{children}</div>
      {action}
    </div>
  );
}
