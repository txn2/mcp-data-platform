import { Card, CardAction, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

// SectionCard is the one way a page section is boxed: a titled card with the
// section's own action (edit, add, create) in its header, so an affordance is
// always attached to the thing it acts on instead of floating on the page.
// Dashed borders are reserved for EmptyState; a section's container is solid.
export function SectionCard({
  title,
  action,
  className,
  children,
  ...props
  // `title` is the section's heading, not the div's tooltip, so the DOM
  // attribute of that name is dropped from the forwarded props.
}: Omit<React.ComponentProps<typeof Card>, "title"> & {
  title: React.ReactNode;
  // The section-scoped action, rendered in the header's action slot.
  action?: React.ReactNode;
}) {
  return (
    <Card className={cn("gap-3 py-4", className)} {...props}>
      <CardHeader className="px-4">
        {/* A real heading, not CardTitle's div: section titles are landmarks
            assistive tech (and the tests) navigate by. */}
        <h3 className="text-sm leading-none font-medium">{title}</h3>
        {action && <CardAction className="self-center">{action}</CardAction>}
      </CardHeader>
      <CardContent className="px-4">{children}</CardContent>
    </Card>
  );
}
