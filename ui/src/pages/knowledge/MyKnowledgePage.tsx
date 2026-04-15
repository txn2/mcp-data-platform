import { useState } from "react";
import {
  useMyInsights,
  useMyInsightStats,
  useMyMemories,
  useMyMemoryStats,
} from "@/api/portal/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { Lightbulb, ChevronLeft, ChevronRight } from "lucide-react";
import type { Insight, MemoryRecord } from "@/api/portal/types";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { formatEntityUrn } from "@/lib/formatEntityUrn";

const STATUS_BADGES: Record<string, { label: string; cls: string }> = {
  pending: {
    label: "Pending",
    cls: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
  },
  approved: {
    label: "Approved",
    cls: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  },
  applied: {
    label: "Applied",
    cls: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
  },
  rejected: {
    label: "Rejected",
    cls: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  },
  superseded: {
    label: "Superseded",
    cls: "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400",
  },
  active: {
    label: "Active",
    cls: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
  },
  stale: {
    label: "Stale",
    cls: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
  },
  archived: {
    label: "Archived",
    cls: "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400",
  },
};

const CATEGORY_LABELS: Record<string, string> = {
  correction: "Correction",
  business_context: "Business Context",
  data_quality: "Data Quality",
  usage_guidance: "Usage Guidance",
  relationship: "Relationship",
  enhancement: "Enhancement",
  general: "General",
};

const DIMENSION_LABELS: Record<string, string> = {
  knowledge: "Knowledge",
  event: "Event",
  entity: "Entity",
  relationship: "Relationship",
  preference: "Preference",
};

const PAGE_SIZE = 20;

type Tab = "knowledge" | "memory";

function BadgeLabel({ status }: { status: string }) {
  const badge = STATUS_BADGES[status] ?? {
    label: status,
    cls: "bg-gray-100 text-gray-600",
  };
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium ${badge.cls}`}
    >
      {badge.label}
    </span>
  );
}

function InsightCard({ insight }: { insight: Insight }) {
  return (
    <div className="rounded-lg border bg-card p-4 space-y-2">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          <BadgeLabel status={insight.status} />
          <span className="text-xs text-muted-foreground">
            {CATEGORY_LABELS[insight.category] ?? insight.category}
          </span>
        </div>
        <span
          className="shrink-0 text-[11px] text-muted-foreground"
          title={new Date(insight.created_at).toLocaleString()}
        >
          {new Date(insight.created_at).toLocaleDateString()}
        </span>
      </div>
      <MarkdownRenderer content={insight.insight_text} bare />
      {insight.entity_urns.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {insight.entity_urns.map((urn) => (
            <span
              key={urn}
              title={urn}
              className="inline-block rounded bg-muted px-1.5 py-0.5 text-[10px] font-mono text-muted-foreground"
            >
              {formatEntityUrn(urn)}
            </span>
          ))}
        </div>
      )}
      {insight.review_notes && (
        <p className="text-xs text-muted-foreground italic">
          Review: {insight.review_notes}
        </p>
      )}
    </div>
  );
}

function MemoryCard({ record }: { record: MemoryRecord }) {
  return (
    <div className="rounded-lg border bg-card p-4 space-y-2">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          <BadgeLabel status={record.status} />
          <span className="text-xs text-muted-foreground">
            {DIMENSION_LABELS[record.dimension] ?? record.dimension}
          </span>
          <span className="text-xs text-muted-foreground">
            {CATEGORY_LABELS[record.category] ?? record.category}
          </span>
        </div>
        <span
          className="shrink-0 text-[11px] text-muted-foreground"
          title={new Date(record.created_at).toLocaleString()}
        >
          {new Date(record.created_at).toLocaleDateString()}
        </span>
      </div>
      <MarkdownRenderer content={record.content} bare />
      {record.entity_urns && record.entity_urns.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {record.entity_urns.map((urn) => (
            <span
              key={urn}
              title={urn}
              className="inline-block rounded bg-muted px-1.5 py-0.5 text-[10px] font-mono text-muted-foreground"
            >
              {formatEntityUrn(urn)}
            </span>
          ))}
        </div>
      )}
      {record.status === "stale" && record.stale_reason && (
        <p className="text-xs text-muted-foreground italic">
          Stale: {record.stale_reason}
        </p>
      )}
    </div>
  );
}

export function MyKnowledgePage() {
  const [tab, setTab] = useState<Tab>("knowledge");

  return (
    <div className="space-y-6">
      <p className="text-sm text-muted-foreground mb-6">
        What the platform learned from your sessions. Knowledge items go through
        admin review before they reach the catalog. Memory records persist your
        corrections and preferences so you do not have to repeat yourself.
      </p>

      {/* Tab bar */}
      <div className="flex gap-1 border-b">
        <button
          onClick={() => setTab("knowledge")}
          className={`px-4 py-2 text-sm font-medium transition-colors ${
            tab === "knowledge"
              ? "border-b-2 border-primary text-primary"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          Knowledge
        </button>
        <button
          onClick={() => setTab("memory")}
          className={`px-4 py-2 text-sm font-medium transition-colors ${
            tab === "memory"
              ? "border-b-2 border-primary text-primary"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          Memory
        </button>
      </div>

      {tab === "knowledge" && <MyKnowledgeSection />}
      {tab === "memory" && <MyMemorySection />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Knowledge Section
// ---------------------------------------------------------------------------

function MyKnowledgeSection() {
  const [statusFilter, setStatusFilter] = useState("");
  const [offset, setOffset] = useState(0);

  const stats = useMyInsightStats();
  const insights = useMyInsights({
    status: statusFilter || undefined,
    limit: PAGE_SIZE,
    offset,
  });

  const s = stats.data;
  const totalItems = insights.data?.total ?? 0;
  const items = insights.data?.data ?? [];
  const hasNext = offset + PAGE_SIZE < totalItems;
  const hasPrev = offset > 0;

  const total = s?.by_status
    ? Object.values(s.by_status).reduce((a, b) => a + b, 0)
    : 0;

  const statusFilters = [
    { value: "", label: "All" },
    { value: "pending", label: "Pending" },
    { value: "approved", label: "Approved" },
    { value: "applied", label: "Applied" },
    { value: "rejected", label: "Rejected" },
  ];

  return (
    <>
      {/* Summary Cards */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard label="Total Insights" value={total} />
        <StatCard label="Pending" value={s?.by_status?.pending ?? 0} />
        <StatCard label="Approved" value={s?.by_status?.approved ?? 0} />
        <StatCard label="Applied" value={s?.by_status?.applied ?? 0} />
      </div>

      {/* Filters */}
      <div className="flex items-center gap-1">
        {statusFilters.map((f) => (
          <button
            key={f.value}
            onClick={() => {
              setStatusFilter(f.value);
              setOffset(0);
            }}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              statusFilter === f.value
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-muted"
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>

      {/* Insights List */}
      {insights.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : items.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <Lightbulb className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm font-medium">No insights yet</p>
          <div className="mt-3 max-w-md text-center space-y-2">
            <p className="text-xs">
              When you share knowledge about your data (corrections,
              business context, quality observations) it gets captured
              here for review.
            </p>
            <p className="text-xs">
              Try telling your assistant something like{" "}
              <em>&quot;the revenue column excludes returns&quot;</em> or{" "}
              <em>&quot;this table is refreshed weekly&quot;</em>.
            </p>
            <p className="text-xs">
              Approved insights improve the data catalog for everyone.
            </p>
          </div>
        </div>
      ) : (
        <div className="space-y-3">
          {items.map((insight) => (
            <InsightCard key={insight.id} insight={insight} />
          ))}
        </div>
      )}

      {/* Pagination */}
      {totalItems > PAGE_SIZE && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            {offset + 1}-{Math.min(offset + PAGE_SIZE, totalItems)} of{" "}
            {totalItems}
          </span>
          <div className="flex gap-1">
            <button
              disabled={!hasPrev}
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              className="rounded p-1 hover:bg-muted disabled:opacity-30"
            >
              <ChevronLeft className="h-4 w-4" />
            </button>
            <button
              disabled={!hasNext}
              onClick={() => setOffset(offset + PAGE_SIZE)}
              className="rounded p-1 hover:bg-muted disabled:opacity-30"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Memory Section
// ---------------------------------------------------------------------------

function MyMemorySection() {
  const [statusFilter, setStatusFilter] = useState("");
  const [dimensionFilter, setDimensionFilter] = useState("");
  const [offset, setOffset] = useState(0);

  const stats = useMyMemoryStats();
  const memories = useMyMemories({
    status: statusFilter || undefined,
    dimension: dimensionFilter || undefined,
    limit: PAGE_SIZE,
    offset,
  });

  const s = stats.data;
  const totalItems = memories.data?.total ?? 0;
  const items = memories.data?.data ?? [];
  const hasNext = offset + PAGE_SIZE < totalItems;
  const hasPrev = offset > 0;

  const statusFilters = [
    { value: "", label: "All" },
    { value: "active", label: "Active" },
    { value: "stale", label: "Stale" },
    { value: "archived", label: "Archived" },
  ];

  const dimensionFilters = [
    { value: "", label: "All Dimensions" },
    { value: "knowledge", label: "Knowledge" },
    { value: "event", label: "Event" },
    { value: "entity", label: "Entity" },
    { value: "relationship", label: "Relationship" },
    { value: "preference", label: "Preference" },
  ];

  return (
    <>
      {/* Summary Cards */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard label="Total Memories" value={s?.total ?? 0} />
        <StatCard label="Active" value={s?.by_status?.active ?? 0} />
        <StatCard label="Stale" value={s?.by_status?.stale ?? 0} />
        <StatCard
          label="Dimensions"
          value={s?.by_dimension ? Object.keys(s.by_dimension).length : 0}
        />
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-1">
          {statusFilters.map((f) => (
            <button
              key={f.value}
              onClick={() => {
                setStatusFilter(f.value);
                setOffset(0);
              }}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                statusFilter === f.value
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-muted"
              }`}
            >
              {f.label}
            </button>
          ))}
        </div>
        <select
          value={dimensionFilter}
          onChange={(e) => {
            setDimensionFilter(e.target.value);
            setOffset(0);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          {dimensionFilters.map((f) => (
            <option key={f.value} value={f.value}>
              {f.label}
            </option>
          ))}
        </select>
      </div>

      {/* Memory List */}
      {memories.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : items.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <p className="text-sm font-medium">No memories yet</p>
          <p className="mt-2 max-w-md text-center text-xs">
            Memories are created automatically as you work with the platform.
            Corrections, preferences, and observations about your data are
            stored here so agents remember your context across sessions.
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {items.map((record) => (
            <MemoryCard key={record.id} record={record} />
          ))}
        </div>
      )}

      {/* Pagination */}
      {totalItems > PAGE_SIZE && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            {offset + 1}-{Math.min(offset + PAGE_SIZE, totalItems)} of{" "}
            {totalItems}
          </span>
          <div className="flex gap-1">
            <button
              disabled={!hasPrev}
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              className="rounded p-1 hover:bg-muted disabled:opacity-30"
            >
              <ChevronLeft className="h-4 w-4" />
            </button>
            <button
              disabled={!hasNext}
              onClick={() => setOffset(offset + PAGE_SIZE)}
              className="rounded p-1 hover:bg-muted disabled:opacity-30"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>
        </div>
      )}
    </>
  );
}
