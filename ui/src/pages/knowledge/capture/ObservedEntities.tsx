import { Database, TriangleAlert } from "lucide-react";
import type { ObservedEntity } from "@/api/admin/types";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { LabeledBlock } from "./fields";

// ObservedEntities is what the platform can see for itself about the entities a
// pending claim is about (#1219): what each one is queryable as, and how many
// rows it holds right now. It sits directly under the claim text so a reviewer
// certifying the claim reads it against the warehouse, not on its own word.
//
// The server sends this only for a pending insight whose URN resolved to an
// available table, so the block is absent — not empty — for a decided insight,
// an unresolvable entity, or a deployment with no query provider.
export function ObservedEntities({ observed }: { observed?: ObservedEntity[] }) {
  if (!observed || observed.length === 0) return null;
  return (
    <LabeledBlock label="Observed Now">
      <div className="space-y-2">
        {observed.map((entity) => (
          <ObservedEntityCard key={entity.urn} entity={entity} />
        ))}
      </div>
    </LabeledBlock>
  );
}

function ObservedEntityCard({ entity }: { entity: ObservedEntity }) {
  return (
    <div className="space-y-2 rounded border bg-card p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Database className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="break-all font-mono text-xs" title={entity.urn}>
          {entity.query_table || entity.urn}
        </span>
        {entity.connection && (
          <StatusBadge variant="neutral">{entity.connection}</StatusBadge>
        )}
      </div>
      <p className="text-xs text-muted-foreground">{rowLine(entity)}</p>
      {entity.conflict && (
        <Alert variant="warning">
          <TriangleAlert />
          <AlertTitle>Claim disagrees with the table</AlertTitle>
          <AlertDescription>
            <p>
              Claim states {entity.conflict.claimed_rows.toLocaleString()}; the table
              currently estimates {entity.conflict.observed_rows.toLocaleString()}.
            </p>
            <p>Advisory only — an estimate is an estimate, and the decision stands here.</p>
          </AlertDescription>
        </Alert>
      )}
    </div>
  );
}

// rowLine states what the platform observed. A connection that does not
// estimate row counts (the default, since COUNT(*) can scan a whole table) still
// tells the reviewer the entity exists and is queryable, which is the larger
// half of the question.
function rowLine(entity: ObservedEntity): string {
  if (entity.estimated_rows === undefined) {
    return "Queryable now. This connection does not estimate row counts.";
  }
  return `Queryable now, currently ~${entity.estimated_rows.toLocaleString()} rows.`;
}
