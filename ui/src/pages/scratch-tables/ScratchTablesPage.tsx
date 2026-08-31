import { useCallback, useMemo, useState } from "react";
import { Table2 } from "lucide-react";
import { useScratchTables } from "@/api/tables/hooks";
import type { ScratchTable, ScratchTableQuery } from "@/api/tables/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { Pager } from "@/components/patterns/Pager";
import { SearchInput } from "@/components/patterns/SearchInput";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ScratchTablesTable } from "./ScratchTablesTable";

// ScratchTablesPage lists the scratch tables a deployment has registered
// (#1472).
//
// It exists because the scratch schema is shared and nothing listed it.
// Everyone granted a connection sees every table in it, so a reader could
// query a table through Trino that the portal gave them no way to find, no way
// to identify the source of, and no way to tell was current -- the only
// surface was a panel inside one asset's or one resource's page, and finding
// out what was registered meant opening every file in turn.
//
// The listing is driven by the stored registrations, not by what can be
// registered, so a source of a format the register action does not yet accept
// appears here as soon as one exists.

const PER_PAGE = 25;

const KIND_OPTIONS = [
  { value: "", label: "All sources" },
  { value: "resource", label: "Resources" },
  { value: "asset", label: "Assets" },
];

export function ScratchTablesPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const [page, setPage] = useState(1);
  const [filters, setFilters] = useState<ScratchTableQuery>({});

  // Any filter change resets to the first page: page 3 of the old result set
  // is rarely a page of the new one.
  const changeFilters = useCallback((patch: Partial<ScratchTableQuery>) => {
    setFilters((prev) => ({ ...prev, ...patch }));
    setPage(1);
  }, []);

  const params = useMemo(() => queryFor(page, PER_PAGE, filters), [page, filters]);
  const { data, isLoading, isError } = useScratchTables(params);
  const total = data?.total ?? 0;

  return (
    <div className="space-y-4">
      <Filters value={filters} rows={data?.data} onChange={changeFilters} />

      <Listing
        rows={data?.data}
        isLoading={isLoading}
        isError={isError}
        filtered={isFiltered(filters)}
        onOpen={(id) => onNavigate(`/scratch-tables/${encodeURIComponent(id)}`)}
      />

      {total > PER_PAGE && (
        <Pager page={page} perPage={PER_PAGE} total={total} onPage={setPage} />
      )}
    </div>
  );
}

/** queryFor renders the request a page and a set of facets ask for. */
function queryFor(page: number, perPage: number, filters: ScratchTableQuery): ScratchTableQuery {
  return {
    page,
    perPage,
    connection: filters.connection || undefined,
    kind: filters.kind || undefined,
    q: filters.q || undefined,
  };
}

/** isFiltered reports whether the reader narrowed the listing themselves. */
function isFiltered(filters: ScratchTableQuery): boolean {
  return Boolean(filters.connection || filters.kind || filters.q);
}

// Filters is the listing's facet bar: free text over the qualified name, the
// kind of file a table was registered from, and which connection it lives on.
function Filters({
  value,
  rows,
  onChange,
}: {
  value: ScratchTableQuery;
  rows: ScratchTable[] | undefined;
  onChange: (patch: Partial<ScratchTableQuery>) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <SearchInput
        className="w-full sm:max-w-xs"
        inputClassName="h-8 text-sm"
        placeholder="Search by table name"
        aria-label="Search registered tables by name"
        value={value.q ?? ""}
        onChange={(e) => onChange({ q: e.target.value })}
      />
      <FilterSelect
        label="Filter by the kind of file the table was registered from"
        value={value.kind ?? ""}
        onChange={(kind) => onChange({ kind: kind as ScratchTableQuery["kind"] })}
        options={KIND_OPTIONS}
        className="w-40"
      />
      <FilterSelect
        label="Filter by connection"
        value={value.connection ?? ""}
        onChange={(connection) => onChange({ connection })}
        options={connectionOptions(rows, value.connection)}
        className="w-48"
      />
    </div>
  );
}

// Listing is the body: the refusal, the explanation, or the table.
//
// The three are separated because they say different things. A failed read is
// not an empty platform, and an empty platform is not an empty filter: a
// reader who narrowed the list to nothing needs their filters back, while one
// who has registered nothing needs to be told what a scratch table is.
function Listing({
  rows,
  isLoading,
  isError,
  filtered,
  onOpen,
}: {
  rows: ScratchTable[] | undefined;
  isLoading: boolean;
  isError: boolean;
  filtered: boolean;
  onOpen: (id: string) => void;
}) {
  if (isError) {
    return (
      <Alert variant="destructive" className="py-2">
        <AlertDescription>
          The registered tables could not be read. This is a failure to reach them rather than an
          empty list: whatever is registered is still queryable.
        </AlertDescription>
      </Alert>
    );
  }
  if (!isLoading && rows?.length === 0 && !filtered) {
    return <NothingRegistered />;
  }
  return <ScratchTablesTable rows={rows} isLoading={isLoading} onOpen={onOpen} />;
}

/**
 * NothingRegistered says what a scratch table is and where one is made, rather
 * than showing an empty table.
 *
 * Registering stays on the file's own page because it needs the file: the
 * platform reads the header row to learn the columns. So this explains rather
 * than offering an action it cannot complete.
 */
function NothingRegistered() {
  return (
    <EmptyState icon={Table2} className="py-10">
      <p className="font-medium text-foreground">No file is registered as a table yet.</p>
      <p className="mx-auto mt-1.5 max-w-lg">
        Registering points a query engine at a CSV already stored on the platform, so it can be
        joined to warehouse tables. Nothing is copied, and the table reads the file&rsquo;s current
        contents. Open a resource or an asset and use <em>Query as a table</em> on its page: the
        platform reads the file&rsquo;s header row to learn the columns, which is why registering
        happens there rather than here.
      </p>
    </EmptyState>
  );
}

/**
 * connectionOptions offers the connections present on the page as a facet.
 *
 * The listing has no connection index of its own, and the page a reader is
 * looking at is the honest source: a name that appears nowhere in the results
 * is a filter that can only empty the list. The connection already chosen is
 * kept whatever the page holds, so a facet cannot filter itself out of its own
 * dropdown.
 */
export function connectionOptions(
  rows: ScratchTable[] | undefined,
  selected: string | undefined,
): { value: string; label: string }[] {
  const names = new Set<string>();
  for (const row of rows ?? []) names.add(row.connection);
  if (selected) names.add(selected);
  return [
    { value: "", label: "All connections" },
    ...[...names].sort().map((name) => ({ value: name, label: name })),
  ];
}
