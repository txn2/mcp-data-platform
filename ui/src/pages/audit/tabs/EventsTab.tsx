import { useState, useMemo, useCallback } from "react";
import { useAuditEvents, useAuditFilters, useToolTitleMap } from "@/api/admin/hooks";
import { Pager } from "@/components/patterns/Pager";
import { EventDrawer } from "@/components/EventDrawer";
import type { AuditEvent, AuditSortColumn, SortOrder } from "@/api/admin/types";
import { EventFilters, type EventFilterState } from "../events/EventFilters";
import { EventsTable } from "../events/EventsTable";
import { downloadEvents } from "../events/exportEvents";

const PER_PAGE = 20;

const NO_FILTERS: EventFilterState = {
  search: "",
  userId: "",
  toolName: "",
  toolkitKind: "",
  source: "",
  success: "",
};

export function EventsTab({ onNavigate }: { onNavigate?: (path: string) => void }) {
  const [page, setPage] = useState(1);
  const [filterState, setFilterState] = useState<EventFilterState>(NO_FILTERS);
  const [sortBy, setSortBy] = useState<AuditSortColumn>("timestamp");
  const [sortOrder, setSortOrder] = useState<SortOrder>("desc");
  const [selectedEvent, setSelectedEvent] = useState<AuditEvent | null>(null);
  const titleMap = useToolTitleMap();

  const { data: filters } = useAuditFilters();

  // Any filter change resets to the first page: page 4 of the old result set
  // is rarely a page of the new one, and an empty table reads as "no matches".
  const changeFilters = useCallback((patch: Partial<EventFilterState>) => {
    setFilterState((prev) => ({ ...prev, ...patch }));
    setPage(1);
  }, []);

  const handleSort = useCallback(
    (column: AuditSortColumn) => {
      if (sortBy === column) {
        setSortOrder((prev) => (prev === "asc" ? "desc" : "asc"));
      } else {
        setSortBy(column);
        setSortOrder(column === "timestamp" ? "desc" : "asc");
      }
      setPage(1);
    },
    [sortBy],
  );

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      userId: filterState.userId || undefined,
      toolName: filterState.toolName || undefined,
      toolkitKind: filterState.toolkitKind || undefined,
      source: filterState.source || undefined,
      search: filterState.search || undefined,
      sortBy,
      sortOrder,
      success: filterState.success === "" ? null : filterState.success === "true",
    }),
    [page, filterState, sortBy, sortOrder],
  );

  const { data, isLoading } = useAuditEvents(params);

  return (
    <div className="space-y-4">
      <EventFilters
        filters={filters}
        value={filterState}
        onChange={changeFilters}
        onExport={(format) => downloadEvents(data?.data ?? [], format)}
        canExport={Boolean(data?.data.length)}
      />

      <EventsTable
        events={data?.data}
        isLoading={isLoading}
        sortBy={sortBy}
        sortOrder={sortOrder}
        onSort={handleSort}
        onSelect={setSelectedEvent}
        titleMap={titleMap}
      />

      {(data?.total ?? 0) > PER_PAGE && (
        <Pager page={page} perPage={PER_PAGE} total={data?.total ?? 0} onPage={setPage} />
      )}

      {selectedEvent && (
        <EventDrawer
          event={selectedEvent}
          onClose={() => setSelectedEvent(null)}
          onNavigate={onNavigate}
        />
      )}
    </div>
  );
}
