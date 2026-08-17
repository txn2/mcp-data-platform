import { useCallback, useMemo, useState } from "react";
import { useAuditFilters, useCalls } from "@/api/admin/hooks";
import { Pager } from "@/components/patterns/Pager";
import { CallFilters, NO_CALL_FILTERS, type CallFilterState } from "./CallFilters";
import { CallsTable } from "./CallsTable";

// CallsPage is the operator's catalog of recorded calls: every query and API
// invocation the platform ran, whoever ran it, with what came of each.
//
// It is one list with a review view rather than two pages: the queue is the
// same records narrowed to the ones that answered something and not yet acted
// on. A separate review page would drift from the catalog it reviews.

const PER_PAGE = 20;

export function CallsPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const [page, setPage] = useState(1);
  const [filterState, setFilterState] = useState<CallFilterState>(NO_CALL_FILTERS);
  const { data: auditFilters } = useAuditFilters();

  // Any filter change resets to the first page: page 4 of the old result set
  // is rarely a page of the new one.
  const changeFilters = useCallback((patch: Partial<CallFilterState>) => {
    setFilterState((prev) => ({ ...prev, ...patch }));
    setPage(1);
  }, []);

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      kind: filterState.kind || undefined,
      connection: filterState.connection || undefined,
      outcome: filterState.outcome || undefined,
      userId: filterState.userId || undefined,
      promotable: filterState.promotable || undefined,
      q: filterState.q || undefined,
    }),
    [page, filterState],
  );

  const { data, isLoading } = useCalls(params);

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Every query and API invocation the platform ran, with the reason its
        caller stated and what came of the result. An outcome is derived on each
        read from what cites the call, so it is never stale: a record becomes
        satisfied the moment an asset, an export, or a capture names it.
      </p>

      <CallFilters
        filters={auditFilters}
        connections={connectionsIn(data?.data)}
        value={filterState}
        onChange={changeFilters}
      />

      <CallsTable
        records={data?.data}
        isLoading={isLoading}
        onOpen={(id) => onNavigate(`/admin/calls/${encodeURIComponent(id)}`)}
      />

      {(data?.total ?? 0) > PER_PAGE && (
        <Pager
          page={page}
          perPage={PER_PAGE}
          total={data?.total ?? 0}
          onPage={setPage}
        />
      )}
    </div>
  );
}

/**
 * connectionsIn offers the connections present on the page as a facet. The
 * catalog has no connection index of its own, and the page a reader is looking
 * at is the honest source: a name that appears nowhere in the results is a
 * filter that can only empty the list.
 */
export function connectionsIn(records?: { connection?: string }[]): string[] {
  const names = new Set<string>();
  for (const record of records ?? []) {
    if (record.connection) names.add(record.connection);
  }
  return [...names].sort();
}
