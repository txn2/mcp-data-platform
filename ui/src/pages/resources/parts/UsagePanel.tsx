import { Activity } from "lucide-react";
import { SectionCard } from "@/components/patterns/SectionCard";
import type { ResourceUsage } from "@/api/resources/types";

// SURFACE_LABELS names the doors a resource's content is served through. The
// keys are the server's surface values (pkg/resource.Surface*).
const SURFACE_LABELS: Record<string, string> = {
  mcp_read: "Agent read",
  fetch: "Search fetch",
  rest_download: "Portal download",
};

// NEVER_READ_DAYS is how old a never-read resource must be before the panel
// flags it. Below it, "no reads yet" says nothing: nobody has had the chance.
const NEVER_READ_DAYS = 30;

function daysSince(iso: string): number {
  return (Date.now() - new Date(iso).getTime()) / 86_400_000;
}

// UsagePanel shows how much a resource is actually used: reads over the last 30
// and 90 days broken down by surface, and when it was last read. It answers the
// question a library cannot answer for itself — is anything using this? — so a
// curator can retire dead weight instead of only ever accumulating.
//
// Counts come from the read audit trail and are therefore bounded by the
// deployment's audit retention; last-read is stored on the resource itself and
// outlives it. With audit disabled the server sends no usage and the panel
// renders nothing rather than an empty scoreboard implying zero reads.
export function UsagePanel({
  usage,
  lastReadAt,
  createdAt,
}: {
  usage?: ResourceUsage;
  lastReadAt?: string;
  createdAt: string;
}) {
  if (!usage) {
    return null;
  }

  const lastRead = lastReadAt ?? usage.last_read_at;
  const surfaces = Object.entries(usage.by_surface_30d ?? {}).filter(([, n]) => n > 0);
  const stale = !lastRead && daysSince(createdAt) >= NEVER_READ_DAYS;

  return (
    <SectionCard
      data-testid="resource-usage"
      title={
        <span className="flex items-center gap-1.5">
          <Activity className="h-3 w-3 text-muted-foreground" />
          Usage
        </span>
      }
    >
      <div className="grid grid-cols-3 gap-3">
        <div>
          <p className="text-lg font-semibold leading-none" data-testid="usage-reads-30d">
            {usage.reads_30d}
          </p>
          <p className="mt-0.5 text-xs text-muted-foreground">reads / 30d</p>
        </div>
        <div>
          <p className="text-lg font-semibold leading-none" data-testid="usage-reads-90d">
            {usage.reads_90d}
          </p>
          <p className="mt-0.5 text-xs text-muted-foreground">reads / 90d</p>
        </div>
        <div>
          <p className="text-sm font-medium leading-none" data-testid="usage-last-read">
            {lastRead ? new Date(lastRead).toLocaleDateString() : "Never"}
          </p>
          <p className="mt-0.5 text-xs text-muted-foreground">last read</p>
        </div>
      </div>

      {surfaces.length > 0 && (
        <ul className="mt-3 space-y-1">
          {surfaces.map(([surface, count]) => (
            <li key={surface} className="flex items-center justify-between text-xs text-muted-foreground">
              <span>{SURFACE_LABELS[surface] ?? surface}</span>
              <span className="tabular-nums">{count}</span>
            </li>
          ))}
        </ul>
      )}

      {stale && (
        // A readout about the resource in view, not an alert: it is re-read
        // every time a different resource is opened, and Alert's role="alert"
        // would announce each one.
        <p
          className="mt-3 text-xs text-amber-600 dark:text-amber-400"
          data-testid="usage-never-read"
        >
          Never read since it was uploaded over {NEVER_READ_DAYS} days ago.
        </p>
      )}
    </SectionCard>
  );
}
