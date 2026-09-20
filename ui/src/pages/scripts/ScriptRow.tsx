import { PrincipalLabel } from "@/components/PrincipalLabel";
import { Badge } from "@/components/ui/badge";
import { TableCell, TableRow } from "@/components/ui/table";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { scheduleLine, scheduleState } from "./cadence";
import { formatWhen, runStatusLabel, runStatusVariant, runWhen } from "./runFormat";

// One row of the scripts listing, and the four cells that need more than a
// string. Split out of ScriptListing for that file's line budget; the listing
// above it is the bar, the health line and the table, and this is what a row
// of that table says.

export function ScriptRow({
  row,
  basePath,
  onNavigate,
}: {
  row: PortalScriptRow;
  basePath: string;
  onNavigate: (path: string) => void;
}) {
  const { script } = row;
  return (
    // The row opens the script, which is how every other listing in the portal
    // opens a record (assets, collections, resources, prompts, insights).
    <TableRow
      className="cursor-pointer align-top"
      onClick={() => onNavigate(`${basePath}/${script.id}`)}
      data-testid={`script-row-${script.id}`}
    >
      <TableCell className="whitespace-normal">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium" data-testid="script-name">
            {script.display_name || script.name}
          </span>
          {/* The category appears once, beside the name. Tags leave the row
              entirely: a tag is how a script is FOUND, which is the facet
              above, not what a reader needs while scanning (#1795). */}
          {script.category && <Badge variant="muted">{script.category}</Badge>}
          <InertBadge row={row} />
        </div>
        <div className="font-mono text-xs text-muted-foreground">{script.name}</div>
      </TableCell>
      <TableCell className="text-xs">
        {script.owner_email ? (
          // The author reads as a person, the way every other people-bearing
          // table in the portal renders one, rather than as a bare email in a
          // text cell (#1795). The owner is identified by email here, which is
          // what a script record carries.
          <PrincipalLabel userId={script.owner_email} email={script.owner_email} />
        ) : (
          <span className="text-muted-foreground">nobody</span>
        )}
      </TableCell>
      <TableCell>
        <ScheduleCell row={row} />
      </TableCell>
      <TableCell>
        <LastRunCell row={row} />
      </TableCell>
      <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
        {formatWhen(script.updated_at)}
      </TableCell>
    </TableRow>
  );
}

// InertBadge marks a script that will execute nothing — disabled, or retired.
// It is a badge on the name rather than a column of its own (#1407): the
// column it replaces said "Runs v3" on almost every row, which is a fact about
// every healthy script and therefore not one worth a column. What a reader
// scans a listing for is the exception, and the version a run executes is on
// the script's own page.
function InertBadge({ row }: { row: PortalScriptRow }) {
  const { script } = row;
  if (script.enabled && script.status === "active") return null;
  return <Badge variant="muted">{script.enabled ? script.status : "disabled"}</Badge>;
}

// ScheduleCell reports the cadence and what it is doing, in the words the
// schedule editor states them in (#1358). This column is the surface a reader
// scans to answer "what is running and when", and a cron expression is not an
// answer to that question for the person whose report it is — so this column
// never shows one (#1405), and the editor is where an expression is read and
// written.
function ScheduleCell({ row }: { row: PortalScriptRow }) {
  const { schedule } = row;
  if (!schedule) {
    return <span className="text-xs text-muted-foreground">On demand</span>;
  }
  return (
    <div className="text-xs">
      <div>{scheduleLine(schedule.cron_spec, schedule.timezone)}</div>
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
