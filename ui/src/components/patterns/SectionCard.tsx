import { useState } from "react";
import { ChevronRight } from "lucide-react";
import { Card, CardAction, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

// SectionCard is the one way a page section is boxed: a titled card with the
// section's own action (edit, add, create) in its header, so an affordance is
// always attached to the thing it acts on instead of floating on the page.
// Dashed borders are reserved for EmptyState; a section's container is solid.
//
// A section may also fold (#1407). A page whose sections are each a form or a
// document does not fit on a screen, and the reader scrolling past a schedule
// builder to reach the code is paying for a section they open once a month. A
// folded section still states what it holds — the summary line stands in for
// the content, so folding hides the controls and never the fact.
export function SectionCard({
  title,
  action,
  className,
  children,
  collapsible = false,
  defaultOpen = true,
  summary,
  ...props
  // `title` is the section's heading, not the div's tooltip, so the DOM
  // attribute of that name is dropped from the forwarded props.
}: Omit<React.ComponentProps<typeof Card>, "title"> & {
  title: React.ReactNode;
  // The section-scoped action, rendered in the header's action slot.
  action?: React.ReactNode;
  /** collapsible turns the heading into the control that folds the section. */
  collapsible?: boolean;
  /** defaultOpen is the state a collapsible section starts in. */
  defaultOpen?: boolean;
  /**
   * summary is what the header says in place of the content while the section
   * is folded. A section that folds away without one is a title a reader has
   * to open to learn anything from.
   */
  summary?: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const shown = !collapsible || open;
  return (
    <Card className={cn("gap-3 py-4", className)} {...props}>
      <CardHeader className="px-4">
        {collapsible ? (
          <button
            type="button"
            aria-expanded={open}
            onClick={() => setOpen(!open)}
            className="flex min-w-0 items-center gap-2 text-left"
          >
            <ChevronRight
              aria-hidden
              className={cn(
                "size-3.5 shrink-0 text-muted-foreground transition-transform",
                open && "rotate-90",
              )}
            />
            {/* A real heading, not CardTitle's div, for the same reason the
                static case uses one: section titles are landmarks assistive
                tech (and the tests) navigate by. */}
            <h3 className="shrink-0 text-sm leading-none font-medium">{title}</h3>
            {!open && summary && (
              <span className="truncate text-xs text-muted-foreground">{summary}</span>
            )}
          </button>
        ) : (
          <h3 className="text-sm leading-none font-medium">{title}</h3>
        )}
        {action && <CardAction className="self-center">{action}</CardAction>}
      </CardHeader>
      {shown && <CardContent className="px-4">{children}</CardContent>}
    </Card>
  );
}
