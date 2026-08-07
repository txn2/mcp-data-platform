import { useState } from "react";
import { Lightbulb, Brain } from "lucide-react";

import {
  useMyInsights,
  useMyInsightStats,
  useMyMemories,
  useMyMemoryStats,
  useSearchMyInsights,
  useSearchMyMemories,
} from "@/api/portal/hooks";
import type { Insight, InsightStats, MemoryRecord, MemoryStats } from "@/api/portal/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { Pager } from "@/components/patterns/Pager";
import { SearchInput } from "@/components/patterns/SearchInput";
import { useDebounced } from "@/lib/useDebounced";
import { SINK_CLASSES } from "@/lib/sinkClass";
import { InsightCard, MemoryCard } from "./mine/cards";
import { activeList, Results, StatGrid, StatusChips, type StatusOption } from "./mine/parts";

const PAGE_SIZE = 20;

const INSIGHT_STATUSES: StatusOption[] = [
  { value: "", label: "All" },
  { value: "pending", label: "Pending" },
  { value: "approved", label: "Approved" },
  { value: "applied", label: "Applied" },
  { value: "rejected", label: "Rejected" },
];

const MEMORY_STATUSES: StatusOption[] = [
  { value: "", label: "All" },
  { value: "active", label: "Active" },
  { value: "stale", label: "Stale" },
  { value: "archived", label: "Archived" },
];

const SINK_CLASS_OPTIONS = [{ value: "", label: "All classes" }, ...SINK_CLASSES];

// The insight summary counts the statuses the server reports; the total is their
// sum, since an insight is always in exactly one of them.
function insightStats(s: InsightStats | undefined) {
  const byStatus = s?.by_status ?? {};
  const total = Object.values(byStatus).reduce((a, b) => a + b, 0);
  return [
    { label: "Total Insights", value: total },
    { label: "Pending", value: byStatus.pending ?? 0 },
    { label: "Approved", value: byStatus.approved ?? 0 },
    { label: "Applied", value: byStatus.applied ?? 0 },
  ];
}

function memoryStats(s: MemoryStats | undefined) {
  const byStatus = s?.by_status ?? {};
  return [
    { label: "Total Memories", value: s?.total ?? 0 },
    { label: "Active", value: byStatus.active ?? 0 },
    { label: "Stale", value: byStatus.stale ?? 0 },
    { label: "Archived", value: byStatus.archived ?? 0 },
  ];
}

// The former MyKnowledgePage tab wrapper was folded into KnowledgeHub (#661);
// the Insights and Memory tabs there compose MyKnowledgeSection and
// MyMemorySection directly, so the standalone two-tab page is no longer routed.

// ---------------------------------------------------------------------------
// Knowledge Section
// ---------------------------------------------------------------------------

export function MyKnowledgeSection() {
  const [statusFilter, setStatusFilter] = useState("");
  const [offset, setOffset] = useState(0);
  const [searchInput, setSearchInput] = useState("");
  const search = useDebounced(searchInput, 300);
  const searching = search.trim().length > 0;

  const stats = useMyInsightStats();
  const insights = useMyInsights({
    status: statusFilter || undefined,
    limit: PAGE_SIZE,
    offset,
  });
  // Search refines by the active status filter and ranks by relevance.
  const searchResults = useSearchMyInsights(search, {
    status: statusFilter || undefined,
    limit: PAGE_SIZE,
  });

  const { items, loading, total } = activeList<Insight>(searching, searchResults, insights);

  return (
    <>
      <StatGrid stats={insightStats(stats.data)} />

      <div className="flex flex-wrap items-center gap-3">
        <SearchInput
          className="min-w-[220px] flex-1"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          placeholder="Search insights..."
          aria-label="Search insights"
        />
        <StatusChips
          options={INSIGHT_STATUSES}
          value={statusFilter}
          onChange={(v) => {
            setStatusFilter(v);
            setOffset(0);
          }}
        />
      </div>

      <Results
        loading={loading}
        searching={searching}
        query={search.trim()}
        count={items.length}
        empty={
          <EmptyState icon={Lightbulb} className="gap-3">
            <p className="text-sm font-medium text-foreground">No insights yet</p>
            <p className="mx-auto mt-2 max-w-md text-xs">
              When you share knowledge about your data (corrections, business
              context, quality observations) it gets captured here for review.
            </p>
            <p className="mx-auto mt-2 max-w-md text-xs">
              Try telling your agent something like{" "}
              <em>&quot;the revenue column excludes returns&quot;</em> or{" "}
              <em>&quot;this table is refreshed weekly&quot;</em>.
            </p>
            <p className="mx-auto mt-2 max-w-md text-xs">
              Reviewed insights can be promoted into your team&apos;s shared
              knowledge.
            </p>
          </EmptyState>
        }
      >
        {items.map((insight) => (
          <InsightCard key={insight.id} insight={insight} />
        ))}
      </Results>

      {/* Pagination (browse mode only; search returns a ranked top-K) */}
      {!searching && total > PAGE_SIZE && (
        <Pager
          page={Math.floor(offset / PAGE_SIZE) + 1}
          perPage={PAGE_SIZE}
          total={total}
          onPage={(p) => setOffset((p - 1) * PAGE_SIZE)}
        />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Memory Section
// ---------------------------------------------------------------------------

export function MyMemorySection() {
  const [statusFilter, setStatusFilter] = useState("");
  const [sinkClassFilter, setSinkClassFilter] = useState("");
  const [offset, setOffset] = useState(0);
  const [searchInput, setSearchInput] = useState("");
  const search = useDebounced(searchInput, 300);
  const searching = search.trim().length > 0;

  const stats = useMyMemoryStats();
  const memories = useMyMemories({
    status: statusFilter || undefined,
    sinkClass: sinkClassFilter || undefined,
    limit: PAGE_SIZE,
    offset,
  });
  // Relevance search refines by status only; the sink_class browse filter
  // applies to the list view (the search endpoint ranks by text + status).
  const searchResults = useSearchMyMemories(search, {
    status: statusFilter || undefined,
    limit: PAGE_SIZE,
  });

  const { items, loading, total } = activeList<MemoryRecord>(
    searching,
    searchResults,
    memories,
  );

  return (
    <>
      <StatGrid stats={memoryStats(stats.data)} />

      <div className="flex flex-wrap items-center gap-3">
        <SearchInput
          className="min-w-[220px] flex-1"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          placeholder="Search memories..."
          aria-label="Search memories"
        />
        <StatusChips
          options={MEMORY_STATUSES}
          value={statusFilter}
          onChange={(v) => {
            setStatusFilter(v);
            setOffset(0);
          }}
        />
        <FilterSelect
          label="Filter by class"
          title="The lifecycle class a memory was captured as"
          value={sinkClassFilter}
          onChange={(v) => {
            setSinkClassFilter(v);
            setOffset(0);
          }}
          options={SINK_CLASS_OPTIONS}
          // The class filter applies to the browse list; relevance search ranks
          // by text and status only, so disable it while searching to avoid
          // implying it narrows the results.
          disabled={searching}
        />
      </div>

      <Results
        loading={loading}
        searching={searching}
        query={search.trim()}
        count={items.length}
        empty={
          <EmptyState icon={Brain} className="gap-3">
            <p className="text-sm font-medium text-foreground">No memories yet</p>
            <p className="mx-auto mt-2 max-w-md text-xs">
              Memories are created automatically as you work with the platform.
              Corrections, preferences, and observations about your data are
              stored here so agents remember your context across sessions.
            </p>
          </EmptyState>
        }
      >
        {items.map((record) => (
          <MemoryCard key={record.id} record={record} />
        ))}
      </Results>

      {/* Pagination (browse mode only; search returns a ranked top-K) */}
      {!searching && total > PAGE_SIZE && (
        <Pager
          page={Math.floor(offset / PAGE_SIZE) + 1}
          perPage={PAGE_SIZE}
          total={total}
          onPage={(p) => setOffset((p - 1) * PAGE_SIZE)}
        />
      )}
    </>
  );
}
