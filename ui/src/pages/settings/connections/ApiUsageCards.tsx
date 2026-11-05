import { useConnectionUsedBy, useConnectionWarnings } from "@/api/admin/hooks";
import type { APIUpstreamWarning } from "@/api/admin/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { UsedByList } from "../catalogs/UsedBy";

// An api connection's Used by and Upstream warnings (#2485, #2486): what
// calls each of its operations, and what its upstream has said about them.

export function ConnectionUsedByCard({ connectionName }: { connectionName: string }) {
  const { data, isLoading } = useConnectionUsedBy(connectionName);
  const operations = data?.operations ?? [];
  return (
    <SectionCard title="Used by">
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : !data?.catalog_id ? (
        <p className="text-sm text-muted-foreground">
          This connection reads no catalog, so its operations are not known to list what uses them.
        </p>
      ) : operations.length === 0 ? (
        <p className="text-sm text-muted-foreground">No script, endpoint or prompt uses this connection&apos;s operations.</p>
      ) : (
        <ul className="divide-y">
          {operations.map((op) => (
            <li key={op.operation} className="space-y-1 py-2">
              <code className="font-mono text-xs">{op.operation}</code>
              <UsedByList callers={op.used_by} empty="" />
            </li>
          ))}
        </ul>
      )}
    </SectionCard>
  );
}

const KIND_LABEL: Record<APIUpstreamWarning["kind"], string> = {
  deprecation: "Deprecation",
  sunset: "Sunset",
  removed: "Removed",
};

export function UpstreamWarningsCard({ connectionName }: { connectionName: string }) {
  const { data, isLoading } = useConnectionWarnings(connectionName);
  const warnings = data?.warnings ?? [];
  return (
    <SectionCard title="Upstream warnings">
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : warnings.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          The upstream has not marked an operation deprecated or set to sunset, and no operation that answered before
          has stopped answering.
        </p>
      ) : (
        <ul className="divide-y">
          {warnings.map((w) => (
            <li key={`${w.operation}:${w.kind}`} className="flex flex-wrap items-center gap-2 py-2 text-xs">
              <Badge variant={w.kind === "removed" ? "destructive" : "secondary"}>{KIND_LABEL[w.kind]}</Badge>
              <code className="font-mono">{w.operation}</code>
              {w.detail && <span className="text-muted-foreground">{w.detail}</span>}
              <span className="ml-auto text-muted-foreground">
                last {new Date(w.last_seen_at).toLocaleString()}, {w.seen_count}×
              </span>
            </li>
          ))}
        </ul>
      )}
    </SectionCard>
  );
}
