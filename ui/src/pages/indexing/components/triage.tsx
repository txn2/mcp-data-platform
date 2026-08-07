import { useMemo, useState } from "react";
import { AlertTriangle, CheckCircle2, ChevronDown, ChevronRight, RefreshCw, X } from "lucide-react";

import { type IndexFailedUnit } from "@/api/admin/indexjobs";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { relTime, errorSignature, failureKey } from "./helpers";

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
    <li className="rounded border border-destructive/20 bg-background/40 px-2 py-1.5 text-xs">
      <div className="flex items-center justify-between gap-2">
        {/* The unit's own name is the disclosure: a ghost Button stripped of
            its padding and hover fill, so the row reads as text while the
            primitive still supplies the focus ring and disabled semantics. */}
        <Button
          type="button"
          variant="ghost"
          size="xs"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="h-auto min-w-0 justify-start px-0 font-normal hover:bg-transparent"
        >
          {open ? (
            <ChevronDown className="text-muted-foreground" />
          ) : (
            <ChevronRight className="text-muted-foreground" />
          )}
          <span className="truncate font-mono">
            {unit.source_kind}/{unit.source_id}
          </span>
        </Button>
        <div className="flex shrink-0 items-center gap-1">
          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => onRetry(unit.source_kind, unit.source_id)}
            disabled={busy}
            title="Re-index this unit; the card clears once it succeeds"
            className="text-muted-foreground"
          >
            <RefreshCw className={retrying ? "animate-spin" : undefined} /> Retry
          </Button>
          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => onDismiss(unit.source_kind, unit.source_id)}
            disabled={busy}
            title="Dismiss: mark this failure resolved without re-indexing"
            className="text-muted-foreground"
          >
            <X /> Dismiss
          </Button>
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
          <pre className="overflow-x-auto whitespace-pre-wrap rounded bg-muted/60 p-2 text-[11px] text-destructive">
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
    // The panel re-polls every few seconds, so its banners are status updates
    // rather than assertive alerts: role="status" keeps a screen reader from
    // interrupting on every refresh.
    return (
      <Alert variant="warning" role="status">
        <AlertTriangle />
        <AlertDescription>
          Could not load failures; this list may be stale or incomplete.
        </AlertDescription>
      </Alert>
    );
  }
  if (units.length === 0) {
    return (
      <Alert variant="success" role="status">
        <CheckCircle2 />
        <AlertDescription>
          No open failures. A failure clears automatically once the unit is re-indexed
          successfully.
        </AlertDescription>
      </Alert>
    );
  }
  return (
    <div className="space-y-3">
      {groups.map(([sig, items]) => (
        // A triage group is a grouping of rows, not a notice, so it is a Card
        // tinted like the destructive Alert rather than an Alert itself: it
        // must not announce itself on every poll.
        <Card key={sig} className="gap-2 border-destructive/30 bg-destructive/5 p-3 shadow-none">
          <div className="flex items-start gap-2">
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
            <code className="break-all text-xs text-destructive">{sig}</code>
            <Badge variant="danger" className="ml-auto shrink-0">
              {items.length} unit{items.length === 1 ? "" : "s"}
            </Badge>
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
        </Card>
      ))}
    </div>
  );
}
