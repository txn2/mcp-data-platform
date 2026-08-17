import { useCallback, useMemo, useState } from "react";
import { useMySessions } from "@/api/portal/hooks";
import { Pager } from "@/components/patterns/Pager";
import {
  NO_SESSION_FILTERS,
  SessionFilters,
  type SessionFilterState,
} from "@/pages/sessions/SessionFilters";
import { SessionsTable } from "@/pages/sessions/SessionsTable";
import { windowStart } from "@/pages/sessions/window";
import { mySessionPath } from "./routes";

// MySessionsPage is the reader's own working history: every session they ran,
// each openable. The dashboard beside it answers how much and how often; this
// answers what happened last Tuesday, which no aggregate can.
//
// The list is the operator's list with the reader taken out of it — no user
// column, no user facet — because the server scopes every read here to the
// caller and a control over that scope could only mislead.

const PER_PAGE = 20;

export function MySessionsPage({
  onNavigate,
}: {
  onNavigate: (path: string) => void;
}) {
  const [page, setPage] = useState(1);
  const [filterState, setFilterState] =
    useState<SessionFilterState>(NO_SESSION_FILTERS);

  // Any filter change resets to the first page: page 4 of the old result set
  // is rarely a page of the new one.
  const changeFilters = useCallback((patch: Partial<SessionFilterState>) => {
    setFilterState((prev) => ({ ...prev, ...patch }));
    setPage(1);
  }, []);

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      kind: filterState.kind || undefined,
      hasAssets: filterState.hasAssets || undefined,
      hasFailures: filterState.hasFailures || undefined,
      startTime: windowStart(filterState.window),
    }),
    [page, filterState],
  );

  const { data, isLoading } = useMySessions(params);

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Every tool call carries the session that made it, so a session can be
        read back long after it ended — as far back as this deployment keeps
        audit history. The window below is how far back this list reads.
      </p>

      <SessionFilters
        value={filterState}
        onChange={changeFilters}
        showUserFacet={false}
      />

      <SessionsTable
        sessions={data?.data}
        isLoading={isLoading}
        showUser={false}
        onOpen={(sessionId) => onNavigate(mySessionPath(sessionId))}
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
