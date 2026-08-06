import type { SharePermission } from "@/api/portal/types";
import { Badge } from "@/components/ui/badge";

/** Viewer / Editor pill shown on items shared with the current user. */
export function SharePermissionBadge({ permission }: { permission: SharePermission }) {
  const editor = permission === "editor";
  return (
    <Badge variant={editor ? "info" : "muted"} className="px-1.5">
      {editor ? "Editor" : "Viewer"}
    </Badge>
  );
}
