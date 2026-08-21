import { useState } from "react";
import { FileCode2 } from "lucide-react";
import { useMyScripts } from "@/api/portal/hooks/scripts";
import type { PortalScriptRow, ScriptListFilter } from "@/api/portal/hooks/scripts";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FilterChip } from "@/components/FilterChip";
import { SearchInput } from "@/components/patterns/SearchInput";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useDebounced } from "@/lib/useDebounced";
import { cn } from "@/lib/utils";
import { MyRunsTab } from "./MyRunsTab";
import { scheduleLine, scheduleState } from "./cadence";
import { formatWhen, runStatusLabel, runStatusVariant, runWhen } from "./runFormat";
import { ScriptFacetBadges } from "./ScriptFacetBadges";

// MyScriptsPage is what the people who own the scripts see (#1290): their
// scripts, what each is scheduled to do, and how its last run went.
//
// Every script here is the reader's own, so there is no owner column to read
// (#1404); the administrator's listing keeps one, where whose script it is is
// the fact worth showing.
//
// Two tabs, because there are two questions (#1405): what do I have, and how
// have they been running. The second one used to take opening every script in
// turn to answer.
//
// The listing itself reads only; what an owner does to a script is on the
// script's own page. It exists because an owner is frequently not an
// administrator and had no way to see their own scripts at all.

interface Props {
  onNavigate: (path: string) => void;
}

// SEARCH_DEBOUNCE_MS is how long typing settles before the listing is asked
// again. The search runs in the store, so this is a request per pause rather
// than one per keystroke.
const SEARCH_DEBOUNCE_MS = 250;

export function MyScriptsPage({ onNavigate }: Props) {
  return (
    <Tabs defaultValue="scripts" className="gap-4">
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
      >
        <TabsTrigger
          value="scripts"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Scripts
        </TabsTrigger>
        <TabsTrigger
          value="runs"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Runs
        </TabsTrigger>
      </TabsList>

      <TabsContent value="scripts">
        <ScriptsTab onNavigate={onNavigate} />
      </TabsContent>

      <TabsContent value="runs">
        <MyRunsTab onNavigate={onNavigate} />
      </TabsContent>
    </Tabs>
  );
}

// ScriptsTab is the listing and everything that narrows it.
//
// Two kinds of narrowing meet here, and they are different in kind rather than
// in style. The search and the facet chips are query predicates: they are
// applied by the server, over every script the caller owns rather than over
// the page of them this listing holds. The tiles narrow the rows already in
// hand, because what they count — a schedule, a failed last run — travels with
// each row and needs no second request to know.
function ScriptsTab({ onNavigate }: Props) {
  const [category, setCategory] = useState<string | undefined>();
  const [tag, setTag] = useState<string | undefined>();
  const [typed, setTyped] = useState("");
  const [tile, setTile] = useState<TileFilter>(null);
  const search = useDebounced(typed.trim(), SEARCH_DEBOUNCE_MS);

  const filter = serverFilter(category, tag, search);
  const { data, isLoading, error } = useMyScripts(filter);
  // The facet vocabulary is read from the UNFILTERED listing, not from the rows
  // on screen: filtering to one category would otherwise remove every other
  // category's chip and leave a reader unable to switch. With nothing filtered
  // the two queries are the same key and this costs one request.
  const { data: all } = useMyScripts();
  const corpus = all?.data ?? [];
  const rows = data?.data ?? [];
  const shown = rows.filter((row) => matchesTile(row, tile));
  const narrowed = isNarrowed(filter) || tile !== null;

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

      {rows.length > 0 && <ScriptTiles rows={rows} tile={tile} onTile={setTile} />}

      {/* Nothing to narrow is nothing to narrow WITH: a caller who owns no
          script is offered no search box over their own emptiness. The bar is
          read off the unfiltered listing, so a search that matched nothing
          keeps the control that clears it. */}
      {corpus.length > 0 && (
        <ScriptFilters
          rows={corpus}
          category={category}
          tag={tag}
          search={typed}
          onCategory={setCategory}
          onTag={setTag}
          onSearch={setTyped}
        />
      )}

      <SectionCard title="Scripts">
        <ScriptsSection
          rows={shown}
          isLoading={isLoading}
          narrowed={narrowed}
          onNavigate={onNavigate}
        />
      </SectionCard>
    </div>
  );
}

// serverFilter is the three axes the server narrows on, with an empty axis
// LEFT OUT rather than set to undefined: an unfiltered listing is then the
// empty object, which is the query key the facet vocabulary is already read
// under, so narrowing nothing costs one request rather than two.
function serverFilter(
  category: string | undefined,
  tag: string | undefined,
  search: string,
): ScriptListFilter {
  const filter: ScriptListFilter = {};
  if (category) filter.category = category;
  if (tag) filter.tag = tag;
  if (search) filter.search = search;
  return filter;
}

// isNarrowed reports whether anything was asked of the server, which is what
// separates "nothing matched" from "you have no scripts".
function isNarrowed(filter: ScriptListFilter): boolean {
  return Object.keys(filter).length > 0;
}

/**
 * TileFilter is which tile is pressed, null for none. The three are one
 * exclusive group: "Scripts" is the whole listing, so pressing it is how a
 * reader clears one of the other two.
 */
type TileFilter = "scheduled" | "failing" | null;

// matchesTile applies the pressed tile to a row.
//
// "Scheduled" is a script that HAS a cadence, paused or not: the word says so,
// and a page that counted only the firing ones would report "Scheduled 0" over
// a row reading "Every 30 minutes / Paused". A paused report is also what
// somebody presses this tile looking for, and the row itself says which are
// firing.
function matchesTile(row: PortalScriptRow, tile: TileFilter): boolean {
  if (tile === "scheduled") return !!row.schedule;
  if (tile === "failing") return row.last_run?.status === "failed";
  return true;
}

// ScriptFilters is how a reader narrows the listing: free text over what a
// script is called and what it says about itself, and the shelves it is filed
// on — the category it carries (#1369) and its tags.
//
// Every axis is applied by the server, so the answer is the same one an agent's
// list gets and it is not limited to the rows this page happened to load. The
// axes combine, so a category and a word together are the scripts that are
// both — which is what a reader who typed one and pressed the other means, and
// it is also what the API does.
function ScriptFilters({
  rows,
  category,
  tag,
  search,
  onCategory,
  onTag,
  onSearch,
}: {
  rows: PortalScriptRow[];
  category?: string;
  tag?: string;
  search: string;
  onCategory: (value: string | undefined) => void;
  onTag: (value: string | undefined) => void;
  onSearch: (value: string) => void;
}) {
  const categories = facetValues(rows, (s) => (s.category ? [s.category] : []));
  const tags = facetValues(rows, (s) => s.tags ?? []);
  return (
    <div className="space-y-2">
      <SearchInput
        value={search}
        onChange={(e) => onSearch(e.target.value)}
        placeholder="Search scripts by name or description..."
        aria-label="Search scripts"
      />
      {categories.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          {/* The knowledge pages pattern: an "All" chip, then one chip per
              shelf. Pressing the active chip clears the axis, which is the
              same thing "All" does and is what a reader tries first. */}
          <FilterChip label="All" active={!category} onClick={() => onCategory(undefined)} />
          {categories.map((value) => (
            <FilterChip
              key={value.name}
              label={value.name}
              count={value.count}
              active={category === value.name}
              onClick={() => onCategory(category === value.name ? undefined : value.name)}
            />
          ))}
        </div>
      )}
      {tags.length > 0 && (
        // Tags are a second axis rather than a second set of shelves, so they
        // carry the label that says which axis they are and no "All" chip of
        // their own.
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs text-muted-foreground">Tag</span>
          {tags.map((value) => (
            <FilterChip
              key={value.name}
              label={value.name}
              count={value.count}
              active={tag === value.name}
              onClick={() => onTag(tag === value.name ? undefined : value.name)}
            />
          ))}
        </div>
      )}
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

// ScriptTiles is the state of this caller's scripts in three numbers, each of
// which is also the control that shows the scripts it counted (#1405). A
// number an owner opens this page for — the reports that failed this morning —
// is otherwise a red badge they have to find by reading the table row by row.
//
// The numbers are counted over the listing as filtered, so a tile and a
// category pressed together agree with the table under them.
function ScriptTiles({
  rows,
  tile,
  onTile,
}: {
  rows: PortalScriptRow[];
  tile: TileFilter;
  onTile: (tile: TileFilter) => void;
}) {
  const scheduled = rows.filter((r) => matchesTile(r, "scheduled")).length;
  const failing = rows.filter((r) => matchesTile(r, "failing")).length;
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <FilterTile
        label="Scripts"
        value={rows.length}
        active={tile === null}
        onClick={() => onTile(null)}
      />
      <FilterTile
        label="Scheduled"
        value={scheduled}
        active={tile === "scheduled"}
        onClick={() => onTile(tile === "scheduled" ? null : "scheduled")}
      />
      <FilterTile
        label="Failing"
        value={failing}
        active={tile === "failing"}
        onClick={() => onTile(tile === "failing" ? null : "failing")}
        alarming={failing > 0}
      />
    </div>
  );
}

// FilterTile is one number that is also the control that shows what it counted.
// It is a button rather than a card with a click handler, so it is reachable by
// keyboard and states whether it is pressed.
function FilterTile({
  label,
  value,
  active,
  alarming,
  onClick,
}: {
  label: string;
  value: number;
  active: boolean;
  alarming?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "bg-card text-card-foreground rounded-xl border px-4 py-3 text-left transition-colors",
        "hover:bg-accent/50 focus-visible:ring-ring/50 focus-visible:ring-[3px] focus-visible:outline-none",
        active && "border-primary ring-primary/30 ring-[2px]",
      )}
    >
      <div className="text-sm leading-none font-medium">{label}</div>
      <div
        className={cn(
          "mt-2 text-2xl font-semibold tabular-nums",
          alarming && "text-red-700 dark:text-red-300",
        )}
      >
        {value}
      </div>
    </button>
  );
}

// ScriptsSection is the listing's three states: loading, nothing to show, and
// the table.
function ScriptsSection({
  rows,
  isLoading,
  narrowed,
  onNavigate,
}: {
  rows: PortalScriptRow[];
  isLoading: boolean;
  /** narrowed distinguishes "nothing matched" from "you have no scripts". */
  narrowed: boolean;
  onNavigate: (path: string) => void;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading scripts...</p>;
  }
  if (rows.length === 0 && narrowed) {
    return (
      <EmptyState icon={FileCode2}>
        No script you can see matches that. Clear the search, the chips, or the tile above
        to see the rest.
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
