import { CheckCircle2, Clock, ShieldAlert } from "lucide-react";
import type { PendingReview } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

// ScriptReviewQueue lists the versions waiting for a decision. It is the point
// of this page: everything else here is context for one of these rows.
//
// A row is a version nothing is executing — either a script that has never run
// unattended, or a correction to one that is still running the code it was
// meant to replace. The queue says which, because those are different costs.
//
// A script the execution gate would refuse anyway (disabled, deprecated,
// superseded) is not here: approving one changes nothing, so it is not a
// decision to put in front of anybody.
export function ScriptReviewQueue({
  pending,
  isLoading,
  selectedVersionID,
  onOpen,
}: {
  pending: PendingReview[];
  isLoading: boolean;
  selectedVersionID: string | null;
  onOpen: (row: PendingReview) => void;
}) {
  return (
    <SectionCard
      className={pending.length > 0 ? "border-amber-500/30 bg-amber-500/5" : undefined}
      title={
        <span className="flex items-center gap-2">
          Awaiting approval
          {pending.length > 0 && <Badge variant="warning">{pending.length}</Badge>}
        </span>
      }
    >
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading the review queue...</p>
      ) : pending.length === 0 ? (
        <EmptyState icon={CheckCircle2}>
          Nothing is waiting for approval. Every script the platform runs is running a
          version somebody approved.
        </EmptyState>
      ) : (
        <ul className="divide-y divide-border/60">
          {pending.map((row) => (
            <QueueRow
              key={row.version_id}
              row={row}
              selected={row.version_id === selectedVersionID}
              onOpen={() => onOpen(row)}
            />
          ))}
        </ul>
      )}
    </SectionCard>
  );
}

function QueueRow({
  row,
  selected,
  onOpen,
}: {
  row: PendingReview;
  selected: boolean;
  onOpen: () => void;
}) {
  return (
    <li className="flex items-start justify-between gap-4 py-2">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium break-words">
            {row.display_name || row.script_name}
          </span>
          <Badge variant="outline" className="font-mono">
            v{row.version}
          </Badge>
          {row.first_approval ? (
            <Badge variant="info">First approval</Badge>
          ) : (
            <Badge variant="warning">
              <ShieldAlert /> Change to a running script
            </Badge>
          )}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          Written by {row.author || "unknown"}
          {row.author_roles.length > 0 && (
            <>, who held {row.author_roles.join(", ")}</>
          )}
          {" · "}
          <span className="inline-flex items-center gap-1">
            <Clock className="size-3" aria-hidden />
            {waitingFor(row.created_at)}
          </span>
        </div>
        {row.description && (
          <div className="mt-0.5 text-xs break-words text-muted-foreground">
            {row.description}
          </div>
        )}
      </div>
      <Button size="sm" variant={selected ? "secondary" : "default"} onClick={onOpen}>
        Review
      </Button>
    </li>
  );
}

// waitingFor renders how long a decision has been outstanding, which is the
// number an operator is alerted on.
function waitingFor(createdAt: string, now: Date = new Date()): string {
  const created = new Date(createdAt);
  if (Number.isNaN(created.getTime())) return "waiting";
  const hours = Math.floor((now.getTime() - created.getTime()) / 3_600_000);
  if (hours < 1) return "waiting less than an hour";
  if (hours < 24) return `waiting ${hours} hour${hours === 1 ? "" : "s"}`;
  const days = Math.floor(hours / 24);
  return `waiting ${days} day${days === 1 ? "" : "s"}`;
}
