import { useState, useMemo } from "react";
import { useInsights, useInsightStats, useAuditFilters } from "@/api/admin/hooks";
import type { Insight } from "@/api/admin/types";
import { Pager } from "@/components/patterns/Pager";
import { InsightDrawer } from "./InsightDrawer";
import { InsightFilters, type InsightOrder } from "./InsightFilters";
import { InsightStatsRow } from "./InsightStatsRow";
import { InsightsTable } from "./InsightsTable";
import { PER_PAGE } from "./helpers";

export function KnowledgeCaptureTab() {
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("");
  const [confidenceFilter, setConfidenceFilter] = useState("");
  const [order, setOrder] = useState<InsightOrder>("newest");
  const [selectedInsight, setSelectedInsight] = useState<Insight | null>(null);
  const { data: filters } = useAuditFilters();
  const ul = filters?.user_labels ?? {};

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      status: statusFilter || undefined,
      category: categoryFilter || undefined,
      confidence: confidenceFilter || undefined,
      order,
    }),
    [page, statusFilter, categoryFilter, confidenceFilter, order],
  );

  const { data, isLoading } = useInsights(params);
  const { data: stats } = useInsightStats();
  const total = data?.total ?? 0;

  // Every facet change restarts the queue at its first page: page 4 of the old
  // filter is not page 4 of the new one.
  const onFilter =
    <T,>(set: (v: T) => void) =>
    (v: T) => {
      set(v);
      setPage(1);
    };

  return (
    <>
      <InsightStatsRow stats={stats} />

      <InsightFilters
        statusFilter={statusFilter}
        categoryFilter={categoryFilter}
        confidenceFilter={confidenceFilter}
        order={order}
        onStatusChange={onFilter(setStatusFilter)}
        onCategoryChange={onFilter(setCategoryFilter)}
        onConfidenceChange={onFilter(setConfidenceFilter)}
        onOrderChange={onFilter(setOrder)}
      />

      <InsightsTable
        insights={data?.data}
        loading={isLoading}
        userLabels={ul}
        onSelect={setSelectedInsight}
      />

      {total > PER_PAGE && (
        <Pager page={page} perPage={PER_PAGE} total={total} onPage={setPage} />
      )}

      {selectedInsight && (
        <InsightDrawer
          insight={selectedInsight}
          onClose={() => setSelectedInsight(null)}
          userLabels={ul}
        />
      )}
    </>
  );
}
