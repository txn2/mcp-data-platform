import { useState } from "react";
import { FileCode2 } from "lucide-react";
import { useMyScripts } from "@/api/portal/hooks/scripts";
import type { PortalScriptRow, ScriptListFilter } from "@/api/portal/hooks/scripts";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FilterChip } from "@/components/FilterChip";
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
import { ScriptFacetBadges } from "./ScriptFacetBadges";

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
  const [filter, setFilter] = useState<ScriptListFilter>({});
  const { data, isLoading, error } = useMyScripts(filter);
  // The facet vocabulary is read from the UNFILTERED listing, not from the rows
  // on screen: filtering to one category would otherwise remove every other
  // category's chip and leave a reader unable to switch. With nothing filtered
  // the two queries are the same key and this costs one request.
  const { data: all } = useMyScripts();
  const rows = data?.data ?? [];
  const filtered = !!(filter.category || filter.tag);

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

      {rows.length > 0 && !filtered && <AutomationSummary rows={rows} />}

      <ScriptFacets rows={all?.data ?? []} filter={filter} onFilter={setFilter} />

      <SectionCard title="Scripts">
        <ScriptsSection
          rows={rows}
          isLoading={isLoading}
          filtered={filtered}
          onNavigate={onNavigate}
        />
      </SectionCard>
    </div>
  );
}

// ScriptFacets is how a reader narrows the listing: the categories scripts are
// filed under and the tags they carry (#1369). The narrowing is applied by the
// server, so it is the same answer an agent's list gets and it is not limited
// to the rows this page happened to load.
//
// Only one value per axis is selectable, and pressing an active chip clears it.
// The two axes combine, so a category and a tag together are the scripts that
// are both — which is what a reader pressing two chips means, and it is also
// what the API does.
function ScriptFacets({
  rows,
  filter,
  onFilter,
}: {
  rows: PortalScriptRow[];
  filter: ScriptListFilter;
  onFilter: (filter: ScriptListFilter) => void;
}) {
  const categories = facetValues(rows, (s) => (s.category ? [s.category] : []));
  const tags = facetValues(rows, (s) => s.tags ?? []);
  if (categories.length === 0 && tags.length === 0) {
    return null;
  }
  // A cleared axis is REMOVED from the filter rather than set to undefined, so
  // the unfiltered filter is the empty object the page started with — and the
  // query it keys stays the one the facet vocabulary is already read under.
  const setAxis = (axis: keyof ScriptListFilter, value: string | undefined) => {
    const next = { ...filter };
    if (value) {
      next[axis] = value;
    } else {
      delete next[axis];
    }
    onFilter(next);
  };
  return (
    <SectionCard title="Filter">
      <div className="space-y-2">
        <FacetRow
          label="Category"
          values={categories}
          active={filter.category}
          onToggle={(value) => setAxis("category", value)}
        />
        <FacetRow
          label="Tag"
          values={tags}
          active={filter.tag}
          onToggle={(value) => setAxis("tag", value)}
        />
      </div>
    </SectionCard>
  );
}

// FacetRow is one axis of chips, absent when nothing carries that axis: a
// deployment where nobody has filed a script should not be shown an empty
// "Category" strip explaining that.
function FacetRow({
  label,
  values,
  active,
  onToggle,
}: {
  label: string;
  values: FacetValue[];
  active?: string;
  onToggle: (value: string | undefined) => void;
}) {
  if (values.length === 0) {
    return null;
  }
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      {values.map((value) => (
        <FilterChip
          key={value.name}
          label={value.name}
          count={value.count}
          active={active === value.name}
          onClick={() => onToggle(active === value.name ? undefined : value.name)}
        />
      ))}
    </div>
  );
}

// FacetValue is one chip: the value and how many scripts carry it.
interface FacetValue {
  name: string;
  count: number;
}

// facetValues counts one axis over the listing, ordered by how many scripts
// carry each value and then alphabetically, so the shelves a reader actually
// uses come first and the order is stable between renders.
function facetValues(
  rows: PortalScriptRow[],
  values: (script: PortalScriptRow["script"]) => string[],
): FacetValue[] {
  const counts = new Map<string, number>();
  for (const row of rows) {
    for (const value of values(row.script)) {
      counts.set(value, (counts.get(value) ?? 0) + 1);
    }
  }
  return [...counts.entries()]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
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
// script carries a lifecycle and a cadence, but a last run is the owner's and
// the administrator's reading and is absent from a row this caller does not
// own, so the failure count is over a strictly smaller set than the three
// beside it and has to say so.
function AutomationSummary({ rows }: { rows: PortalScriptRow[] }) {
  const running = rows.filter((r) => r.script.enabled && r.script.status === "active").length;
  const scheduled = rows.filter((r) => r.schedule?.enabled).length;
  const owned = rows.filter((r) => r.owned).length;
  const failing = rows.filter((r) => r.last_run?.status === "failed").length;
  return (
    <div className="grid gap-4 sm:grid-cols-4">
      <SummaryTile label="Automations" value={rows.length} hint="scripts visible to you" />
      <SummaryTile
        label="In service"
        value={running}
        hint="enabled and active; a run executes the latest saved version"
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
  filtered,
  onNavigate,
}: {
  rows: PortalScriptRow[];
  isLoading: boolean;
  /** filtered distinguishes "nothing matched" from "you have no scripts". */
  filtered: boolean;
  onNavigate: (path: string) => void;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading scripts...</p>;
  }
  if (rows.length === 0 && filtered) {
    return (
      <EmptyState icon={FileCode2}>
        No script you can see carries that category and tag. Clear the filter above to see
        the rest.
      </EmptyState>
    );
  }
  if (rows.length === 0) {
    return (
      <EmptyState icon={FileCode2}>
        You have no scripts yet. Ask an agent to write one for a report or an export you
        run repeatedly. A script runs as soon as it is saved, under the access you hold.
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
        <ScriptFacetBadges category={script.category} tags={script.tags} className="mt-1" />
      </TableCell>
      <TableCell className="text-xs">{script.owner_email || "—"}</TableCell>
      <TableCell>
        <ExecutionState row={row} />
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

// ExecutionState reports the run gate from the listing's own record: a
// disabled or retired script runs nothing, whatever else is true of it.
function ExecutionState({ row }: { row: PortalScriptRow }) {
  const { script } = row;
  if (!script.enabled || script.status !== "active") {
    return <Badge variant="muted">{script.enabled ? script.status : "disabled"}</Badge>;
  }
  return <Badge variant="success">Runs v{script.version}</Badge>;
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
