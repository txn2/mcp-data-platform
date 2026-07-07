import { useMemo, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Loader2,
  RefreshCw,
  X,
} from "lucide-react";
import { type IndexJob, type IndexFailedUnit } from "@/api/admin/indexjobs";
import { relTime, leaseRemaining, fmtClock, errorSignature, failureKey } from "./helpers";

export function InFlightPanel({ jobs }: { jobs: IndexJob[] }) {
  const running = jobs.filter((j) => j.status === "running");
  if (running.length === 0) {
    return <p className="py-4 text-center text-sm text-muted-foreground">No jobs in flight.</p>;
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
    return <p className="py-4 text-center text-sm text-muted-foreground">No jobs in retry backoff.</p>;
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

// FailedUnitRow renders one failing unit inside a triage group: its
// timestamps, last-success context, and Retry / Dismiss actions, with an
// expandable drill-in to the un-redacted error and the underlying job id.
function FailedUnitRow({
  unit,
  onRetry,
  onDismiss,
  retrying,
  dismissing,
}: {
  unit: IndexFailedUnit;
  onRetry: (kind: string, sourceID: string) => void;
  onDismiss: (kind: string, sourceID: string) => void;
  retrying: boolean;
  dismissing: boolean;
}) {
  const [open, setOpen] = useState(false);
  const busy = retrying || dismissing;
  return (
    <li className="rounded border border-red-500/20 bg-background/40 px-2 py-1.5 text-xs">
      <div className="flex items-center justify-between gap-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="flex min-w-0 items-center gap-1 text-left"
          aria-expanded={open}
        >
          {open ? (
            <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
          )}
          <span className="truncate font-mono">
            {unit.source_kind}/{unit.source_id}
          </span>
        </button>
        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            onClick={() => onRetry(unit.source_kind, unit.source_id)}
            disabled={busy}
            className="flex items-center gap-1 rounded border px-2 py-0.5 text-muted-foreground transition-colors hover:bg-accent disabled:opacity-50"
            title="Re-index this unit; the card clears once it succeeds"
          >
            <RefreshCw className={`h-3 w-3 ${retrying ? "animate-spin" : ""}`} /> Retry
          </button>
          <button
            type="button"
            onClick={() => onDismiss(unit.source_kind, unit.source_id)}
            disabled={busy}
            className="flex items-center gap-1 rounded border px-2 py-0.5 text-muted-foreground transition-colors hover:bg-accent disabled:opacity-50"
            title="Dismiss: mark this failure resolved without re-indexing"
          >
            <X className="h-3 w-3" /> Dismiss
          </button>
        </div>
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 pl-4 text-[11px] text-muted-foreground">
        <span>
          {unit.occurrences} failure{unit.occurrences === 1 ? "" : "s"} · {unit.attempts} attempts
        </span>
        <span>first seen {relTime(unit.first_failed_at)}</span>
        <span>last seen {relTime(unit.last_failed_at)}</span>
        {unit.last_succeeded_at ? (
          <span className="text-emerald-600 dark:text-emerald-400">
            last succeeded {relTime(unit.last_succeeded_at)}
          </span>
        ) : (
          <span>never succeeded</span>
        )}
      </div>
      {open && (
        <div className="mt-2 space-y-1 pl-4">
          <div className="text-[11px] text-muted-foreground">
            job #{unit.latest_job_id} · source id <code className="font-mono">{unit.source_id}</code>
          </div>
          <pre className="overflow-x-auto whitespace-pre-wrap rounded bg-muted/60 p-2 text-[11px] text-red-700 dark:text-red-300">
            {unit.last_error || "no error recorded"}
          </pre>
        </div>
      )}
    </li>
  );
}

export function FailureTriage({
  units,
  isError,
  onRetry,
  onDismiss,
  retryingKey,
  dismissingKey,
}: {
  units: IndexFailedUnit[];
  isError: boolean;
  onRetry: (kind: string, sourceID: string) => void;
  onDismiss: (kind: string, sourceID: string) => void;
  retryingKey: string | null;
  dismissingKey: string | null;
}) {
  const groups = useMemo(() => {
    const m = new Map<string, IndexFailedUnit[]>();
    for (const u of units) {
      const sig = errorSignature(u.last_error ?? "unknown error");
      const arr = m.get(sig) ?? [];
      arr.push(u);
      m.set(sig, arr);
    }
    return [...m.entries()].sort((a, b) => b[1].length - a[1].length);
  }, [units]);

  // A load error must NOT read as "all clear": failures fall back to an
  // empty list on error, which would otherwise render the green success
  // state and mask real failures while the index silently degrades.
  if (isError) {
    return (
      <p className="flex items-center justify-center gap-2 py-4 text-center text-sm text-amber-700 dark:text-amber-400">
        <AlertTriangle className="h-4 w-4" /> Could not load failures; this list may be stale or
        incomplete.
      </p>
    );
  }
  if (units.length === 0) {
    return (
      <p className="flex items-center justify-center gap-2 py-4 text-center text-sm text-emerald-600 dark:text-emerald-400">
        <CheckCircle2 className="h-4 w-4" /> No open failures. A failure clears automatically once
        the unit is re-indexed successfully.
      </p>
    );
  }
  return (
    <div className="space-y-3">
      {groups.map(([sig, items]) => (
        <div key={sig} className="rounded-md border border-red-500/30 bg-red-500/5 p-3">
          <div className="mb-2 flex items-start gap-2">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-red-500" />
            <code className="break-all text-xs text-red-700 dark:text-red-300">{sig}</code>
            <span className="ml-auto shrink-0 rounded-full bg-red-500/15 px-2 text-xs text-red-600 dark:text-red-300">
              {items.length} unit{items.length === 1 ? "" : "s"}
            </span>
          </div>
          <ul className="space-y-1.5">
            {items.map((u) => {
              const key = failureKey(u.source_kind, u.source_id);
              return (
                <FailedUnitRow
                  key={u.latest_job_id}
                  unit={u}
                  onRetry={onRetry}
                  onDismiss={onDismiss}
                  retrying={retryingKey === key}
                  dismissing={dismissingKey === key}
                />
              );
            })}
          </ul>
        </div>
      ))}
    </div>
  );
}

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
    <div className="rounded-lg border bg-card p-4">
      <div className="mb-3 flex items-baseline justify-between">
        <h2 className="text-sm font-medium">{title}</h2>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  );
}
