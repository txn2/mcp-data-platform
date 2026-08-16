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
import { formatWhen, runStatusLabel, runStatusVariant, runWhen } from "./runFormat";

// MyScriptsPage is what the people who own the automations see (#1290): every
// script they may see, what it is scheduled to do, and how its last run went.
//
// It reads only. Approving a version is an administrator's decision and stays
// on the admin surface; this page exists because an owner is frequently not an
// administrator and had no way to see their own automations at all.

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
function AutomationSummary({ rows }: { rows: PortalScriptRow[] }) {
  const executable = rows.filter((r) => !!r.script.approved_version_id).length;
  const scheduled = rows.filter((r) => r.schedule?.enabled).length;
  const failing = rows.filter((r) => r.last_run?.status === "failed").length;
  return (
    <div className="grid gap-4 sm:grid-cols-4">
      <SummaryTile label="Automations" value={rows.length} hint="you can see" />
      <SummaryTile
        label="Approved"
        value={executable}
        hint={executable === rows.length ? "all of them" : "the rest run nothing"}
      />
      <SummaryTile label="On a cadence" value={scheduled} hint="firing unattended" />
      <SummaryTile
        label="Last run failed"
        value={failing}
        hint={failing === 0 ? "nothing is broken" : "worth opening"}
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
        run repeatedly; it appears here once it exists, and runs once an administrator
        approves it.
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

// ScheduleCell reports the cadence and when it next fires. A paused schedule
// says so rather than showing a next fire that will not happen.
function ScheduleCell({ row }: { row: PortalScriptRow }) {
  if (!row.schedule) {
    return <span className="text-xs text-muted-foreground">On demand</span>;
  }
  return (
    <div className="text-xs">
      <div className="font-mono">{row.schedule.cron_spec}</div>
      <div className="text-muted-foreground">
        {row.schedule.enabled ? `next ${formatWhen(row.schedule.next_run_at)}` : "paused"}
      </div>
    </div>
  );
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
