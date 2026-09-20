import { useMemo, useState } from "react";
import { FileCode2 } from "lucide-react";
import { useScriptListing } from "@/api/portal/hooks/scripts";
import type {
  PortalScriptRow,
  ScriptListFilter,
  ScriptListResponse,
} from "@/api/portal/hooks/scripts";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FilterSelect, type FilterOption } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SectionCard } from "@/components/patterns/SectionCard";
import { SortableHead } from "@/components/patterns/SortableHead";
import {
  ScopeFilter,
  SCRIPT_SCOPE_OPTIONS,
  getStoredScriptScope,
  storeScriptScope,
  type ScriptScope,
} from "@/components/ScopeFilter";
import {
  DEFAULT_SCRIPT_SORT,
  toggleSort,
  type ListSort,
  type ScriptSortKey,
} from "@/components/listSort";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useDebounced } from "@/lib/useDebounced";
import { ScriptRow } from "./ScriptRow";

// ScriptListing is the scripts table and everything that narrows it, on both
// surfaces: the scripts a person owns (#1290) and every script on the platform
// (#1407).
//
// It is ONE component because it is one listing — the server answers both from
// the same predicate, so a second table for administrators would be a second
// answer to the same question, drifting a column at a time.
//
// Everything that narrows it is one filter bar, and everything it is ordered by
// is the server's (#1795). Before that it carried four narrowing controls in
// three visual languages stacked above the table — three counting tiles, a
// search box, a row of category chips and an uncapped row of tag chips — and
// no ordering at all, because the store emitted `ORDER BY updated_at DESC` and
// took none from the caller. Two of those controls were also counting
// different populations: the chips counted the platform while the tiles
// counted the page the 200-row cap had returned.
//
// The listing reads only; what anyone does to a script is on the script's own
// page, which is the same page for both surfaces.

// SEARCH_DEBOUNCE_MS is how long typing settles before the listing is asked
// again. The search runs in the store, so this is a request per pause rather
// than one per keystroke.
const SEARCH_DEBOUNCE_MS = 250;

/** Audience is who is reading, which decides the wording. */
export type Audience = "owner" | "admin";

interface Props {
  audience: Audience;
  /** basePath is the section a row opens under: /scripts or /admin/scripts. */
  basePath: string;
  onNavigate: (path: string) => void;
}

/** The facets the bar narrows on, each applied by the server. */
interface Facets {
  owner: string;
  category: string;
  tag: string;
  status: string;
}

const NO_FACETS: Facets = { owner: "", category: "", tag: "", status: "" };

export function ScriptListing({ audience, basePath, onNavigate }: Props) {
  const state = useListingState(audience);

  return (
    <div className="space-y-4">
      <UnreadableListing failed={state.failed} audience={audience} />

      {state.corpus.length > 0 && (
        <ScriptFilterBar
          audience={audience}
          scope={state.scope}
          onScope={state.chooseScope}
          corpus={state.corpus}
          facets={state.facets}
          onFacets={state.setFacets}
          search={state.typed}
          onSearch={state.setTyped}
        />
      )}

      <ScriptHealth
        total={state.total}
        scheduled={state.scheduled}
        failing={state.failing}
        shown={state.rows.length}
        onlyFailing={state.onlyFailing}
        onToggleFailing={state.toggleFailing}
      />

      <SectionCard title={audience === "admin" ? "All scripts" : "Scripts"}>
        <ScriptsSection
          rows={state.shown}
          audience={audience}
          basePath={basePath}
          isLoading={state.isLoading}
          narrowed={state.narrowed}
          sort={state.sort}
          onSort={state.chooseSort}
          onNavigate={onNavigate}
        />
      </SectionCard>
    </div>
  );
}

/**
 * useListingState holds what the reader has asked for and what the server
 * answered.
 *
 * It is a hook rather than a dozen useStates in the component because the
 * component's job is to lay out four parts, and the ordering, the facets, the
 * scope and the two queries behind them are one piece of state that has to
 * stay consistent with itself.
 */
function useListingState(audience: Audience) {
  // An administrator's listing is the platform's by construction, so the scope
  // tabs are the non-admin's control and the stored choice is theirs.
  const [scope, setScope] = useState<ScriptScope>(() =>
    audience === "admin" ? "all" : getStoredScriptScope(),
  );
  const [facets, setFacets] = useState<Facets>(NO_FACETS);
  const [typed, setTyped] = useState("");
  const [sort, setSort] = useState<ListSort<ScriptSortKey>>(DEFAULT_SCRIPT_SORT);
  const [onlyFailing, setOnlyFailing] = useState(false);
  const search = useDebounced(typed.trim(), SEARCH_DEBOUNCE_MS);

  const filter = serverFilter(scope, facets, search, sort);
  const { data, isLoading, error } = useScriptListing(filter);

  // The facet vocabulary is read from the listing narrowed by SCOPE ALONE, not
  // from the rows on screen: filtering to one category would otherwise remove
  // every other category's option and leave a reader unable to switch.
  //
  // With nothing narrowed the two filters are the same object, so they are one
  // query key and this costs one request rather than two.
  const { data: all } = useScriptListing(vocabularyFilter(filter, scope, sort));

  const answer = listingAnswer(data, onlyFailing);
  return {
    scope,
    facets,
    setFacets,
    typed,
    setTyped,
    sort,
    onlyFailing,
    corpus: all?.data ?? [],
    ...answer,
    isLoading,
    failed: Boolean(error),
    narrowed: isNarrowed(filter) || onlyFailing,
    chooseScope: (next: ScriptScope) => {
      setScope(next);
      if (audience !== "admin") storeScriptScope(next);
    },
    chooseSort: (key: ScriptSortKey) => setSort((current) => toggleSort(current, key)),
    toggleFailing: () => setOnlyFailing((v) => !v),
  };
}

// UnreadableListing is the listing that could not be read at all, which is a
// different statement from an empty one and is made in the reader's own terms.
function UnreadableListing({ failed, audience }: { failed: boolean; audience: Audience }) {
  if (!failed) return null;
  return (
    <Alert variant="destructive">
      <AlertDescription>
        {audience === "admin"
          ? "The script listing could not be loaded, so this page cannot say what exists or what is scheduled. The server may be unavailable."
          : "Your scripts could not be loaded, so this page cannot say what is scheduled or what has been running. The server may be unavailable."}
      </AlertDescription>
    </Alert>
  );
}

/** hasFailedLastRun is the one narrowing the server cannot do. */
function hasFailedLastRun(row: PortalScriptRow): boolean {
  return row.last_run?.status === "failed";
}

/**
 * listingAnswer is what the server said, with the one narrowing it could not
 * apply applied.
 *
 * A failed last run is attached per page, and only for rows the caller owns,
 * so it cannot be a predicate; everything else on this page is one.
 */
function listingAnswer(data: ScriptListResponse | undefined, onlyFailing: boolean) {
  const rows = data?.data ?? [];
  return {
    rows,
    shown: onlyFailing ? rows.filter(hasFailedLastRun) : rows,
    total: data?.total ?? 0,
    scheduled: data?.scheduled ?? 0,
    failing: data?.failing ?? 0,
  };
}

// serverFilter renders the state of the bar as the query the server answers.
// An empty axis is LEFT OUT rather than set to undefined, so an unnarrowed
// listing is the smallest query.
function serverFilter(
  scope: ScriptScope,
  facets: Facets,
  search: string,
  sort: ListSort<ScriptSortKey>,
): ScriptListFilter {
  const filter: ScriptListFilter = { scope, sort: sort.key, dir: sort.dir };
  if (facets.category) filter.category = facets.category;
  if (facets.tag) filter.tag = facets.tag;
  if (facets.status) filter.status = facets.status;
  if (facets.owner) filter.owner = facets.owner;
  if (search) filter.search = search;
  return filter;
}

/**
 * vocabularyFilter is the query the facet options are read under: the scope and
 * the ordering, and none of the narrowing.
 *
 * When nothing is narrowed it returns the listing's own filter, so the two
 * hooks share a query key and the page makes one request instead of two.
 */
function vocabularyFilter(
  filter: ScriptListFilter,
  scope: ScriptScope,
  sort: ListSort<ScriptSortKey>,
): ScriptListFilter {
  if (!isNarrowed(filter)) return filter;
  return { scope, sort: sort.key, dir: sort.dir };
}

// isNarrowed reports whether anything was asked of the server BEYOND the scope
// and the ordering, which is what separates "nothing matched" from "there are
// no scripts".
function isNarrowed(filter: ScriptListFilter): boolean {
  return Boolean(
    filter.category || filter.tag || filter.status || filter.owner || filter.search,
  );
}

// ScriptHealth is the state of the listing in one line.
//
// It replaces three bordered tiles, which took about 90px of vertical space to
// say two numbers and a third that was just the row count (#1795). Only the
// failed count is emphatic and pressable, because it is the single thing a
// person opens this page to find; the others are plain text, because a number
// nobody acts on is not a control.
function ScriptHealth({
  total,
  scheduled,
  failing,
  shown,
  onlyFailing,
  onToggleFailing,
}: {
  total: number;
  scheduled: number;
  failing: number;
  shown: number;
  onlyFailing: boolean;
  onToggleFailing: () => void;
}) {
  if (total === 0) return null;
  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
      <span data-testid="script-health-total">
        {total} {total === 1 ? "script" : "scripts"}
      </span>
      {/* The page says so when it is not the whole answer. total counts the
          predicate; shown is what the cap returned. */}
      {shown < total && <span>· showing {shown}</span>}
      <span>· {scheduled} scheduled</span>
      {failing > 0 && (
        <>
          <span aria-hidden>·</span>
          <Button
            type="button"
            variant={onlyFailing ? "destructive" : "outline"}
            size="xs"
            aria-pressed={onlyFailing}
            onClick={onToggleFailing}
            data-testid="script-health-failing"
          >
            {failing} failed {failing === 1 ? "its" : "their"} last run
          </Button>
        </>
      )}
    </p>
  );
}

// ScriptFilterBar is one row of compact controls, which is the shape the
// assets browser established (AssetFilterBar). Every axis on it is applied by
// the server, so the answer is the same one an agent's list gets and it is not
// limited to the rows this page happened to load.
//
// Tag is a select rather than a row of chips. It is the least discriminating
// axis on the page and the only one with no bound on its vocabulary, so as
// chips it was a paragraph of pills between the search box and the first row.
// The options are ordered by count, exactly as the chips were.
function ScriptFilterBar({
  audience,
  scope,
  onScope,
  corpus,
  facets,
  onFacets,
  search,
  onSearch,
}: {
  audience: Audience;
  scope: ScriptScope;
  onScope: (scope: ScriptScope) => void;
  corpus: PortalScriptRow[];
  facets: Facets;
  onFacets: (facets: Facets) => void;
  search: string;
  onSearch: (value: string) => void;
}) {
  const options = useMemo(
    () => ({
      owner: facetOptions(corpus, (s) => (s.owner_email ? [s.owner_email] : []), "All authors"),
      category: facetOptions(corpus, (s) => (s.category ? [s.category] : []), "All categories"),
      tag: facetOptions(corpus, (s) => s.tags ?? [], "All tags"),
      status: facetOptions(corpus, (s) => (s.status ? [s.status] : []), "Any status"),
    }),
    [corpus],
  );

  const set = (patch: Partial<Facets>) => onFacets({ ...facets, ...patch });

  return (
    <div className="flex flex-wrap items-center gap-2">
      {/* An administrator's listing is every script by definition, so the
          scope tabs would be one face pressed against itself. */}
      {audience !== "admin" && (
        <ScopeFilter
          value={scope}
          onChange={onScope}
          options={SCRIPT_SCOPE_OPTIONS}
          label="Whose scripts"
        />
      )}
      <SearchInput
        value={search}
        onChange={(e) => onSearch(e.target.value)}
        placeholder="Search scripts..."
        aria-label="Search scripts"
        className="w-full sm:w-64"
      />
      <FilterSelect
        label="Filter by author"
        value={facets.owner}
        onChange={(owner) => set({ owner })}
        options={options.owner}
      />
      <FilterSelect
        label="Filter by category"
        value={facets.category}
        onChange={(category) => set({ category })}
        options={options.category}
      />
      <FilterSelect
        label="Filter by tag"
        value={facets.tag}
        onChange={(tag) => set({ tag })}
        options={options.tag}
      />
      <FilterSelect
        label="Filter by status"
        value={facets.status}
        onChange={(status) => set({ status })}
        options={options.status}
      />
    </div>
  );
}

// facetOptions is a facet's vocabulary, ordered by how many scripts carry each
// value and then alphabetically — the order the chips were in, so a reader
// finds the same values in the same places.
function facetOptions(
  rows: PortalScriptRow[],
  values: (script: PortalScriptRow["script"]) => string[],
  allLabel: string,
): FilterOption[] {
  const counts = new Map<string, number>();
  for (const row of rows) {
    for (const value of values(row.script)) {
      counts.set(value, (counts.get(value) ?? 0) + 1);
    }
  }
  const options = [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([value, count]) => ({ value, label: `${value} (${count})` }));
  return [{ value: "", label: allLabel }, ...options];
}

function ScriptsSection({
  rows,
  audience,
  basePath,
  isLoading,
  narrowed,
  sort,
  onSort,
  onNavigate,
}: {
  rows: PortalScriptRow[];
  audience: Audience;
  basePath: string;
  isLoading: boolean;
  narrowed: boolean;
  sort: ListSort<ScriptSortKey>;
  onSort: (key: ScriptSortKey) => void;
  onNavigate: (path: string) => void;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }
  if (rows.length === 0) {
    return <NothingToList audience={audience} narrowed={narrowed} />;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <SortableHead
            label="Script"
            sortKey="display_name"
            sortBy={sort.key}
            sortDir={sort.dir}
            onSort={onSort}
          />
          <SortableHead
            label="Author"
            sortKey="owner_email"
            sortBy={sort.key}
            sortDir={sort.dir}
            onSort={onSort}
          />
          <TableHead>Schedule</TableHead>
          {/* Last run carries no sort affordance: it is attached to this page
              after the query, so an ordering over it would order the page. */}
          <TableHead>Last run</TableHead>
          <SortableHead
            label="Updated"
            sortKey="updated_at"
            sortBy={sort.key}
            sortDir={sort.dir}
            onSort={onSort}
          />
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <ScriptRow
            key={row.script.id}
            row={row}
            basePath={basePath}
            onNavigate={onNavigate}
          />
        ))}
      </TableBody>
    </Table>
  );
}

// NothingToList is the empty listing, which is three different statements
// depending on who is reading and whether they narrowed it themselves.
function NothingToList({ audience, narrowed }: { audience: Audience; narrowed: boolean }) {
  if (narrowed) {
    return (
      <EmptyState icon={FileCode2}>
        No script you can see matches that. Clear the search or the filters above to see
        the rest.
      </EmptyState>
    );
  }
  if (audience === "admin") {
    return (
      <EmptyState icon={FileCode2}>
        No scripts have been authored yet. An agent creates one through the manage_script
        tool, or a person writes one on their own scripts page; it appears here as soon as
        it exists.
      </EmptyState>
    );
  }
  return (
    <EmptyState icon={FileCode2}>
      You have no scripts yet. Ask an agent to write one for a report or an export you
      run repeatedly. A script runs as soon as it is saved, under the access you hold.
    </EmptyState>
  );
}

