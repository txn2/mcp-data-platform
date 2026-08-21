import { Activity } from "lucide-react";
import { useScriptRunListing } from "@/api/portal/hooks/scripts";
import type { PortalScriptRun } from "@/api/portal/hooks/scripts";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatDuration } from "@/lib/formatDuration";
import type { Audience } from "./ScriptListing";
import { runStatusLabel, runStatusVariant, runWhen } from "./runFormat";

// ScriptRunsList is every run of every script the reader may see, newest first
// (#1405), on both surfaces: the runs of the scripts a person owns, and every
// run on the platform for an administrator (#1407).
//
// It answers the question the per-script history cannot: not "how is this
// report going" but "how are the scripts going", which until now took opening
// each script in turn. The reason a run failed is in the row rather than
// behind it, because that reason is what decides which run anybody opens.
//
// A row opens the run itself — the script's page with that run's log, its
// parameters and what it produced already open — and the script's name opens
// the script.

interface Props {
  audience: Audience;
  /** basePath is the section a row opens under: /scripts or /admin/scripts. */
  basePath: string;
  onNavigate: (path: string) => void;
  /**
   * scriptId narrows the listing to one script, which is what a metric that
   * names a script links to (#1407). The narrowing is the server's, so the row
   * cap counts that script's runs rather than everything's.
   */
  scriptId?: string;
  /** scriptName is what the narrowed listing calls the script it was narrowed to. */
  scriptName?: string;
  /** onClearScript removes the narrowing; absent when there is none to remove. */
  onClearScript?: () => void;
}

export function ScriptRunsList({
  audience,
  basePath,
  onNavigate,
  scriptId,
  scriptName,
  onClearScript,
}: Props) {
  const { data, isLoading, error } = useScriptRunListing(scriptId);
  const runs = data?.data ?? [];

  return (
    <SectionCard
      title={audience === "admin" ? "Recent runs" : "Runs"}
      action={<ClearNarrowing scriptId={scriptId} onClearScript={onClearScript} />}
    >
      <NarrowedNotice scriptId={scriptId} scriptName={scriptName} />
      <ListingState isLoading={isLoading} failed={!!error} empty={runs.length === 0} audience={audience} />
      <CapNotice shown={runs.length} limit={data?.limit} audience={audience} />
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
              <RunRow key={run.id} run={run} basePath={basePath} onNavigate={onNavigate} />
            ))}
          </TableBody>
        </Table>
      )}
    </SectionCard>
  );
}

// ClearNarrowing is the way back to every script, present only while the
// listing is narrowed to one: a control that undoes nothing is a control a
// reader has to work out the meaning of.
function ClearNarrowing({
  scriptId,
  onClearScript,
}: {
  scriptId?: string;
  onClearScript?: () => void;
}) {
  if (!scriptId || !onClearScript) return null;
  return (
    <Button size="xs" variant="outline" onClick={onClearScript}>
      Show every script
    </Button>
  );
}

// NarrowedNotice names the script the listing was narrowed to, so a short list
// reads as one script's history rather than as a quiet platform.
function NarrowedNotice({ scriptId, scriptName }: { scriptId?: string; scriptName?: string }) {
  if (!scriptId) return null;
  return (
    <p className="pb-2 text-xs text-muted-foreground">
      Narrowed to <span className="font-medium">{scriptName || scriptId}</span>.
    </p>
  );
}

// ListingState is what the section says before it has runs to show: still
// loading, could not be read, or nothing has run — three different statements,
// kept apart so the section cannot render two of them at once.
function ListingState({
  isLoading,
  failed,
  empty,
  audience,
}: {
  isLoading: boolean;
  failed: boolean;
  empty: boolean;
  audience: Audience;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading runs...</p>;
  }
  if (failed) {
    return (
      <p className="text-sm text-muted-foreground">The run history could not be loaded.</p>
    );
  }
  if (!empty) return null;
  return (
    <EmptyState icon={Activity}>
      {audience === "admin"
        ? "Nothing has run yet. A run happens when a schedule fires or somebody asks for one, and a run always executes a saved version."
        : "None of your scripts has run yet. A run happens when a schedule fires or when somebody asks for one, and a run always executes a saved version."}
    </EmptyState>
  );
}

// CapNotice says the listing was cut off. A page that filled its limit and said
// nothing would read as the whole history.
function CapNotice({
  shown,
  limit,
  audience,
}: {
  shown: number;
  limit?: number;
  audience: Audience;
}) {
  if (!limit || shown < limit) return null;
  return (
    <p className="pb-2 text-xs text-muted-foreground">
      Showing the {limit} most recent runs across{" "}
      {audience === "admin" ? "every script" : "your scripts"}. Older ones are kept until
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
  basePath,
  onNavigate,
}: {
  run: PortalScriptRun;
  basePath: string;
  onNavigate: (path: string) => void;
}) {
  return (
    <TableRow
      className="cursor-pointer"
      onClick={() => onNavigate(`${basePath}/${run.script_id}/runs/${run.id}`)}
    >
      <TableCell className="max-w-md">
        <button
          type="button"
          className="text-left font-medium hover:underline"
          onClick={(e) => {
            // The row opens the run; this opens the script it belongs to, which
            // is the other thing a reader wants from a listing that spans them.
            e.stopPropagation();
            onNavigate(`${basePath}/${run.script_id}`);
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
