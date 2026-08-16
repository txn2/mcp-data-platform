import { useCallback, useMemo, useState } from "react";
import { useAuditFilters, useSessions } from "@/api/admin/hooks";
import { Pager } from "@/components/patterns/Pager";
import {
  NO_SESSION_FILTERS,
  SessionFilters,
  type SessionFilterState,
} from "./SessionFilters";
import { SessionsTable } from "./SessionsTable";
import { windowStart } from "./window";

// SessionsPage is the admin's list of sessions. A session is not a stored
// row — it is every audit event sharing one session id — so this list reaches
// as far back as audit retention, including sessions whose live row has long
// since expired.

const PER_PAGE = 20;

export function SessionsPage({
  onNavigate,
}: {
  onNavigate: (path: string) => void;
}) {
  const [page, setPage] = useState(1);
  const [filterState, setFilterState] =
    useState<SessionFilterState>(NO_SESSION_FILTERS);
  const { data: auditFilters } = useAuditFilters();

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
      userId: filterState.userId || undefined,
      kind: filterState.kind || undefined,
      hasAssets: filterState.hasAssets || undefined,
      hasFailures: filterState.hasFailures || undefined,
      startTime: windowStart(filterState.window),
    }),
    [page, filterState],
  );

  const { data, isLoading } = useSessions(params);

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Every tool call carries the session that made it. A session is read back
        from those calls, so it outlives the session record itself; the window
        below is how far back this list reads, up to the audit retention period.
      </p>

      <SessionFilters
        filters={auditFilters}
        value={filterState}
        onChange={changeFilters}
      />

      <SessionsTable
        sessions={data?.data}
        isLoading={isLoading}
        onOpen={(sessionId) =>
          onNavigate(`/admin/sessions/${encodeURIComponent(sessionId)}`)
        }
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
