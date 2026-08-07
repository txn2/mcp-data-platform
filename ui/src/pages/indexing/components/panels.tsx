import { Loader2 } from "lucide-react";
import { type IndexJob } from "@/api/admin/indexjobs";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { leaseRemaining, fmtClock } from "./helpers";

export function InFlightPanel({ jobs }: { jobs: IndexJob[] }) {
  const running = jobs.filter((j) => j.status === "running");
  if (running.length === 0) {
    return <EmptyState className="py-6">No jobs in flight.</EmptyState>;
  }
  return (
    <ul className="space-y-2">
      {running.map((j) => (
        <li key={j.id} className="flex items-center justify-between gap-2 text-sm">
          <span className="flex min-w-0 items-center gap-2">
            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-blue-500" />
            <span className="truncate font-mono text-xs">
              {j.source_kind}/{j.source_id}
            </span>
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">
            {j.items_done > 0 ? `${j.items_done} items · ` : ""}
            {j.worker_id ? `${j.worker_id} · ` : ""}
            lease {leaseRemaining(j.lease_expires_at)}
          </span>
        </li>
      ))}
    </ul>
  );
}

export function RetryBackoffPanel({ jobs }: { jobs: IndexJob[] }) {
  const waiting = jobs.filter((j) => j.status === "pending" && j.attempts > 0);
  if (waiting.length === 0) {
    return <EmptyState className="py-6">No jobs in retry backoff.</EmptyState>;
  }
  return (
    <ul className="space-y-2">
      {waiting.map((j) => (
        <li key={j.id} className="flex items-center justify-between gap-2 text-sm">
          <span className="truncate font-mono text-xs">
            {j.source_kind}/{j.source_id}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">
            attempt {j.attempts} · next run {fmtClock(j.next_run_at)}
          </span>
        </li>
      ))}
    </ul>
  );
}

// Section is the dashboard's panel box: a SectionCard whose header action slot
// carries the one-line hint that qualifies what the panel is counting.
export function Section({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <SectionCard
      title={title}
      action={hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    >
      {children}
    </SectionCard>
  );
}
