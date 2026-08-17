import { useCallback, useMemo, useState } from "react";
import { useMyCalls } from "@/api/portal/hooks";
import { Pager } from "@/components/patterns/Pager";
import {
  CallFilters,
  NO_CALL_FILTERS,
  type CallFilterState,
} from "@/pages/calls/CallFilters";
import { CallsTable } from "@/pages/calls/CallsTable";
import { connectionsIn } from "@/pages/calls/CallsPage";
import { myCallPath } from "./routes";

// MyCallsPage is the reader's own working queries: every query and API call
// they ran, what each was for, and which of them answered something.
//
// It is the operator's catalog with the reader taken out of it — no user
// column, no user facet — because the server scopes every read here to the
// caller and a control over that scope could only mislead.

const PER_PAGE = 20;

export function MyCallsPage({
  onNavigate,
}: {
  onNavigate: (path: string) => void;
}) {
  const [page, setPage] = useState(1);
  const [filterState, setFilterState] = useState<CallFilterState>(NO_CALL_FILTERS);

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
      promotable: filterState.promotable || undefined,
      q: filterState.q || undefined,
    }),
    [page, filterState],
  );

  const { data, isLoading } = useMyCalls(params);

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        The queries and API calls you have run, kept with the reason stated for
        each. A call that something was built from reads as satisfied, and one
        that answered a question in conversation reads that way too once you
        capture it. Any of them can be published to the catalog so the next
        person finds the query instead of writing it again.
      </p>

      <CallFilters
        connections={connectionsIn(data?.data)}
        value={filterState}
        onChange={changeFilters}
        showUserFacet={false}
      />

      <CallsTable
        records={data?.data}
        isLoading={isLoading}
        showUser={false}
        onOpen={(id) => onNavigate(myCallPath(id))}
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
