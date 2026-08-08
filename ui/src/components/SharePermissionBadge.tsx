import type { SharePermission } from "@/api/portal/types";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/** Viewer / Editor pill shown on items shared with the current user. */
export function SharePermissionBadge({
  permission,
  className,
}: {
  permission: SharePermission;
  // Set where the pill sits in a line of prose rather than in a card's badge
  // row, and needs its own leading space (ActiveShares).
  className?: string;
}) {
  const editor = permission === "editor";
  return (
    <Badge variant={editor ? "info" : "muted"} className={cn("px-1.5", className)}>
      {editor ? "Editor" : "Viewer"}
    </Badge>
  );
}
