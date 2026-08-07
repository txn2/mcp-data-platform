import { Globe, Users } from "lucide-react";
import type { ShareSummary } from "@/api/portal/types";
import { cn } from "@/lib/utils";

/**
 * ShareIndicators says how far an item the caller owns has travelled: shared
 * with named users, published behind a public link, or both. It renders nothing
 * when the item has not left its owner, so a list of private items carries no
 * empty affordances.
 *
 * `className` places and dresses the row — the grid cards float it over the
 * thumbnail, the tables centre it in a column.
 */
export function ShareIndicators({
  summary,
  className,
}: {
  summary?: ShareSummary;
  className?: string;
}) {
  if (!summary?.has_user_share && !summary?.has_public_link) return null;
  return (
    <div className={cn("flex gap-1", className)}>
      {summary.has_user_share && (
        <span title="Shared with users">
          <Users className="size-3.5 text-muted-foreground" />
        </span>
      )}
      {summary.has_public_link && (
        <span title="Has public link">
          <Globe className="size-3.5 text-muted-foreground" />
        </span>
      )}
    </div>
  );
}
