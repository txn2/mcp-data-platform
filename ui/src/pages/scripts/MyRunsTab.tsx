import { Activity } from "lucide-react";
import { useMyScriptRuns } from "@/api/portal/hooks/scripts";
import type { PortalScriptRun } from "@/api/portal/hooks/scripts";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatDuration } from "@/lib/formatDuration";
import { runStatusLabel, runStatusVariant, runWhen } from "./runFormat";

// MyRunsTab is every run of every script this person owns, newest first
// (#1405).
//
// It answers the question the per-script history cannot: not "how is this
// report going" but "how are my scripts going", which until now took opening
// each script in turn. The reason a run failed is in the row rather than
// behind it, because that reason is what decides which run anybody opens.
//
// A row opens the run itself — the script's page with that run's log, its
// parameters and what it produced already open — and the script's name opens
// the script.

export function MyRunsTab({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { data, isLoading, error } = useMyScriptRuns();
  const runs = data?.data ?? [];

  return (
    <SectionCard title="Runs">
      {isLoading && <p className="text-sm text-muted-foreground">Loading runs...</p>}
      {error && (
        <p className="text-sm text-muted-foreground">Your runs could not be loaded.</p>
      )}
      {!isLoading && !error && runs.length === 0 && (
        <EmptyState icon={Activity}>
          None of your scripts has run yet. A run happens when a schedule fires or when
          somebody asks for one, and a run always executes a saved version.
        </EmptyState>
      )}
      <CapNotice shown={runs.length} limit={data?.limit} />
      {runs.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Script</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Trigger</TableHead>
              <TableHead>When</TableHead>
              <TableHead>Duration</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {runs.map((run) => (
              <RunRow key={run.id} run={run} onNavigate={onNavigate} />
            ))}
          </TableBody>
        </Table>
      )}
    </SectionCard>
  );
}

// CapNotice says the listing was cut off. A page that filled its limit and said
// nothing would read as the whole history.
function CapNotice({ shown, limit }: { shown: number; limit?: number }) {
  if (!limit || shown < limit) return null;
  return (
    <p className="pb-2 text-xs text-muted-foreground">
      Showing the {limit} most recent runs across your scripts. Older ones are kept until
      this deployment's retention window ends, and each script's own page carries its full
      history.
    </p>
  );
}

// RunRow is one run: which script it belongs to, how it ended, why when it
// failed, and when it was. The error wraps rather than truncating — a
// half-sentence about a broken report is worse than the sentence.
function RunRow({
  run,
  onNavigate,
}: {
  run: PortalScriptRun;
  onNavigate: (path: string) => void;
}) {
  return (
    <TableRow
      className="cursor-pointer"
      onClick={() => onNavigate(`/scripts/${run.script_id}/runs/${run.id}`)}
    >
      <TableCell className="max-w-md">
        <button
          type="button"
          className="text-left font-medium hover:underline"
          onClick={(e) => {
            // The row opens the run; this opens the script it belongs to, which
            // is the other thing a reader wants from a listing that spans them.
            e.stopPropagation();
            onNavigate(`/scripts/${run.script_id}`);
          }}
        >
          {run.script_name || run.script_id}
        </button>
        {run.error && (
          <div className="mt-1 text-xs break-words whitespace-pre-wrap text-red-700 dark:text-red-300">
            {run.error}
          </div>
        )}
      </TableCell>
      <TableCell>
        <Badge variant={runStatusVariant(run.status)}>{runStatusLabel(run.status)}</Badge>
      </TableCell>
      <TableCell className="text-xs">{run.trigger}</TableCell>
      <TableCell className="text-xs">{runWhen(run)}</TableCell>
      <TableCell className="text-xs">
        {run.duration_ms > 0 ? formatDuration(run.duration_ms) : "—"}
      </TableCell>
    </TableRow>
  );
}
