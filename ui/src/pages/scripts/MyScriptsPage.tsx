import { FileCode2 } from "lucide-react";
import { useMyScripts } from "@/api/portal/hooks/scripts";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { cadenceLine, scheduleState } from "./cadence";
import { formatWhen, runStatusLabel, runStatusVariant, runWhen } from "./runFormat";

// MyScriptsPage is what the people who own the automations see (#1290): every
// script they may see, what it is scheduled to do, and how its last run went.
//
// It reads only; what an owner does to a script is on the script's own page.
// This listing exists because an owner is frequently not an administrator and
// had no way to see their own automations at all.

interface Props {
  onNavigate: (path: string) => void;
}

export function MyScriptsPage({ onNavigate }: Props) {
  const { data, isLoading, error } = useMyScripts();
  const rows = data?.data ?? [];

  return (
    <div className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>
            Your scripts could not be loaded, so this page cannot say what is scheduled or
            what has been running. The server may be unavailable.
          </AlertDescription>
        </Alert>
      )}

      {rows.length > 0 && <AutomationSummary rows={rows} />}

      <SectionCard title="Scripts">
        <ScriptsSection rows={rows} isLoading={isLoading} onNavigate={onNavigate} />
      </SectionCard>
    </div>
  );
}

// AutomationSummary is the state of this caller's automations in four numbers,
// computed from the listing itself rather than from a second query (#1307): how
// many there are, how many the platform will actually run, how many are firing
// on a cadence, and how many last ended badly.
//
// The last one is the number somebody opens this page for. A report that has
// been failing every morning is otherwise a red badge in a table they have to
// read row by row.
//
// Each caption states what its number counts and the population it counts over
// (#1360), because the four populations are NOT the same one. Every visible
// script carries an approval and a cadence, but a last run is the owner's and
// the administrator's reading and is absent from a row this caller does not
// own, so the failure count is over a strictly smaller set than the three
// beside it and has to say so.
//
// The approved caption names the approval and not runnability, because it
// counts scripts with an approved version and a disabled or deprecated one
// still has one. The row says which, and this number must not claim more than
// it counts.
function AutomationSummary({ rows }: { rows: PortalScriptRow[] }) {
  const executable = rows.filter((r) => !!r.script.approved_version_id).length;
  const scheduled = rows.filter((r) => r.schedule?.enabled).length;
  const owned = rows.filter((r) => r.owned).length;
  const failing = rows.filter((r) => r.last_run?.status === "failed").length;
  return (
    <div className="grid gap-4 sm:grid-cols-4">
      <SummaryTile label="Automations" value={rows.length} hint="scripts visible to you" />
      <SummaryTile
        label="Approved"
        value={executable}
        hint="have a version the platform may execute"
      />
      <SummaryTile label="On a cadence" value={scheduled} hint="run on a schedule, unattended" />
      <SummaryTile
        label="Last run failed"
        value={failing}
        hint={`of the ${owned} you own`}
        alarming={failing > 0}
      />
    </div>
  );
}

function SummaryTile({
  label,
  value,
  hint,
  alarming,
}: {
  label: string;
  value: number;
  hint: string;
  alarming?: boolean;
}) {
  return (
    <SectionCard title={label}>
      <div className="space-y-0.5">
        <div
          className={cn(
            "text-2xl font-semibold tabular-nums",
            alarming && "text-red-700 dark:text-red-300",
          )}
        >
          {value}
        </div>
        <div className="text-xs text-muted-foreground">{hint}</div>
      </div>
    </SectionCard>
  );
}

// ScriptsSection is the listing's three states: loading, nothing to show, and
// the table.
function ScriptsSection({
  rows,
  isLoading,
  onNavigate,
}: {
  rows: PortalScriptRow[];
  isLoading: boolean;
  onNavigate: (path: string) => void;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading scripts...</p>;
  }
  if (rows.length === 0) {
    return (
      <EmptyState icon={FileCode2}>
        You have no scripts yet. Ask an agent to write one for a report or an export you
        run repeatedly. A script only you can see runs as soon as it is saved, under the
        access you hold; one you share with others is reviewed first.
      </EmptyState>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Script</TableHead>
          <TableHead>Owner</TableHead>
          <TableHead>Executing</TableHead>
          <TableHead>Schedule</TableHead>
          <TableHead>Last run</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <ScriptRow key={row.script.id} row={row} onNavigate={onNavigate} />
        ))}
      </TableBody>
    </Table>
  );
}

function ScriptRow({
  row,
  onNavigate,
}: {
  row: PortalScriptRow;
  onNavigate: (path: string) => void;
}) {
  const { script } = row;
  return (
    // The row opens the script, which is how every other listing in the portal
    // opens a record (assets, collections, resources, prompts, insights).
    <TableRow className="cursor-pointer" onClick={() => onNavigate(`/scripts/${script.id}`)}>
      <TableCell>
        <div className="font-medium">{script.display_name || script.name}</div>
        <div className="font-mono text-xs text-muted-foreground">{script.name}</div>
      </TableCell>
      <TableCell className="text-xs">{script.owner_email || "—"}</TableCell>
      <TableCell>
        <ApprovalState row={row} />
      </TableCell>
      <TableCell>
        <ScheduleCell row={row} />
      </TableCell>
      <TableCell>
        <LastRunCell row={row} />
      </TableCell>
    </TableRow>
  );
}

// ApprovalState reports the execution gate from the listing's own record: a
// script with no approved version runs nothing, whatever else is true of it.
function ApprovalState({ row }: { row: PortalScriptRow }) {
  const { script } = row;
  if (!script.approved_version_id) {
    return <Badge variant="muted">Nothing approved</Badge>;
  }
  if (!script.enabled || script.status === "deprecated") {
    return (
      <span className="flex flex-wrap items-center gap-1.5">
        <Badge variant="success">Approved</Badge>
        <Badge variant="muted">{script.enabled ? script.status : "disabled"}</Badge>
      </span>
    );
  }
  return <Badge variant="success">Approved v{script.version}</Badge>;
}

// ScheduleCell reports the cadence and what it is doing, in the words the
// schedule editor states them in (#1358). This column is the surface an owner
// scans to answer "what is running and when", and a cron expression is not an
// answer to that question for the person whose report it is. An expression the
// builder cannot express falls back to the expression, which is all there is to
// say about it.
function ScheduleCell({ row }: { row: PortalScriptRow }) {
  const { schedule } = row;
  if (!schedule) {
    return <span className="text-xs text-muted-foreground">On demand</span>;
  }
  const line = cadenceLine(schedule.cron_spec, schedule.timezone);
  return (
    <div className="text-xs">
      <div className={line.verbatim ? "font-mono" : undefined}>{line.text}</div>
      <div className="text-muted-foreground">{scheduleWhen(schedule)}</div>
    </div>
  );
}

// scheduleWhen is the schedule's state in the few words this column has. The
// editor says the same thing at greater length; both read it off scheduleState,
// so neither can call a paused schedule due.
function scheduleWhen(schedule: NonNullable<PortalScriptRow["schedule"]>): string {
  const state = scheduleState(schedule);
  switch (state.kind) {
    case "paused":
      return "Paused";
    case "idle":
      return "No fire due";
    case "due":
      return `Next ${formatWhen(state.at)}`;
  }
}

// LastRunCell reports the most recent run. A script the caller does not own
// carries none: a run is the owner's and the administrator's reading, and so is
// the fact that one failed.
function LastRunCell({ row }: { row: PortalScriptRow }) {
  if (!row.owned) {
    return <span className="text-xs text-muted-foreground">—</span>;
  }
  if (!row.last_run) {
    return <span className="text-xs text-muted-foreground">Never run</span>;
  }
  return (
    <div className="flex flex-col gap-1 text-xs">
      <Badge variant={runStatusVariant(row.last_run.status)}>
        {runStatusLabel(row.last_run.status)}
      </Badge>
      <span className="text-muted-foreground">{runWhen(row.last_run)}</span>
    </div>
  );
}
