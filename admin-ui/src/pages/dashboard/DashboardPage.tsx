import { useMemo } from "react";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import {
  useSystemInfo,
  useAuditOverview,
  useAuditTimeseries,
  useAuditBreakdown,
  useAuditEvents,
  useConnections,
  useAuditPerformance,
  useInsightStats,
  useInsights,
} from "@/api/hooks";
import type { Resolution } from "@/api/types";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { TimeseriesChart } from "@/components/charts/TimeseriesChart";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import { RecentErrorsList } from "@/components/RecentErrorsList";
import { formatDuration } from "@/lib/formatDuration";

const presets: { value: TimeRangePreset; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
];

function getResolution(preset: TimeRangePreset): Resolution {
  switch (preset) {
    case "1h": return "minute";
    case "6h": return "minute";
    case "24h": return "hour";
    case "7d": return "day";
  }
}

export function DashboardPage() {
  const { preset, setPreset, getStartTime, getEndTime } = useTimeRangeStore();
  const { startTime, endTime } = useMemo(
    () => ({ startTime: getStartTime(), endTime: getEndTime() }),
    // Recompute only when the preset changes — not on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [preset],
  );

  const systemInfo = useSystemInfo();
  const overview = useAuditOverview({ startTime, endTime });
  const timeseries = useAuditTimeseries({ resolution: getResolution(preset), startTime, endTime });
  const toolBreakdown = useAuditBreakdown({ groupBy: "tool_name", limit: 8, startTime, endTime });
  const userBreakdown = useAuditBreakdown({ groupBy: "user_id", limit: 5, startTime, endTime });
  const recentErrors = useAuditEvents({ perPage: 5, success: false });
  const connections = useConnections();
  const performance = useAuditPerformance({ startTime, endTime });
  const insightStats = useInsightStats();
  const pendingInsights = useInsights({ perPage: 5, status: "pending" });

  const o = overview.data;
  const k = insightStats.data;

  const knowledgeTotal = useMemo(() => {
    if (!k?.by_status) return 0;
    return Object.values(k.by_status).reduce((s, n) => s + n, 0);
  }, [k]);

  const topCategory = useMemo(() => {
    if (!k?.by_category) return "-";
    const entries = Object.entries(k.by_category);
    if (entries.length === 0) return "-";
    entries.sort((a, b) => b[1] - a[1]);
    const name = entries[0]![0];
    return name.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
  }, [k]);

  return (
    <div className="space-y-6">
      {/* System Info */}
      {systemInfo.data && (
        <div className="rounded-lg border bg-card p-4">
          <div className="flex items-center gap-4 text-sm">
            <span className="font-medium">{systemInfo.data.name}</span>
            <StatusBadge variant="neutral">{systemInfo.data.version}</StatusBadge>
            <StatusBadge variant="neutral">{systemInfo.data.transport}</StatusBadge>
            <StatusBadge variant="neutral">{systemInfo.data.config_mode}</StatusBadge>
            {systemInfo.data.features.audit && <StatusBadge variant="success">Audit</StatusBadge>}
            {systemInfo.data.features.knowledge && <StatusBadge variant="success">Knowledge</StatusBadge>}
            {systemInfo.data.features.oauth && <StatusBadge variant="success">OAuth</StatusBadge>}
          </div>
        </div>
      )}

      {/* Time Range */}
      <div className="flex items-center gap-1">
        {presets.map((p) => (
          <button
            key={p.value}
            onClick={() => setPreset(p.value)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              preset === p.value
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-muted"
            }`}
          >
            {p.label}
          </button>
        ))}
      </div>

      {/* Summary Cards */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4 lg:grid-cols-7">
        <StatCard
          label="Total Calls"
          value={o?.total_calls?.toLocaleString() ?? "-"}
        />
        <StatCard
          label="Success Rate"
          value={o ? `${(o.success_rate * 100).toFixed(1)}%` : "-"}
        />
        <StatCard
          label="Avg Duration"
          value={o ? formatDuration(o.avg_duration_ms) : "-"}
        />
        <StatCard
          label="Unique Users"
          value={o?.unique_users ?? "-"}
        />
        <StatCard
          label="Unique Tools"
          value={o?.unique_tools ?? "-"}
        />
        <StatCard
          label="Enrichment"
          value={o ? `${(o.enrichment_rate * 100).toFixed(0)}%` : "-"}
        />
        <StatCard
          label="Errors"
          value={o?.error_count ?? "-"}
          className={o && o.error_count > 0 ? "border-red-200" : undefined}
        />
      </div>

      {/* Activity Chart */}
      <div className="rounded-lg border bg-card p-4">
        <h2 className="mb-3 text-sm font-medium">Activity</h2>
        <TimeseriesChart data={timeseries.data} isLoading={timeseries.isLoading} />
      </div>

      {/* Charts Grid */}
      <div className="grid gap-6 lg:grid-cols-2">
        {/* Top Tools */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Top Tools</h2>
          <BreakdownBarChart data={toolBreakdown.data} isLoading={toolBreakdown.isLoading} />
        </div>

        {/* Top Users */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Top Users</h2>
          <BreakdownBarChart
            data={userBreakdown.data}
            isLoading={userBreakdown.isLoading}
            color="hsl(221, 83%, 53%)"
          />
        </div>
      </div>

      {/* Bottom Row */}
      <div className="grid gap-6 lg:grid-cols-2">
        {/* Performance */}
        {performance.data && (
          <div className="rounded-lg border bg-card p-4">
            <h2 className="mb-3 text-sm font-medium">Performance</h2>
            <div className="grid grid-cols-3 gap-3">
              <div>
                <p className="text-xs text-muted-foreground">P50</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.p50_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">P95</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.p95_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">P99</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.p99_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Avg</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.avg_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Max</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.max_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Avg Resp</p>
                <p className="text-lg font-semibold">{performance.data.avg_response_chars.toFixed(0)} chars</p>
              </div>
            </div>
          </div>
        )}

        {/* Recent Errors */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Recent Errors</h2>
          <RecentErrorsList events={recentErrors.data?.data} />
        </div>
      </div>

      {/* Knowledge */}
      <div className="grid gap-6 lg:grid-cols-2">
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Knowledge Insights</h2>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div>
              <p className="text-xs text-muted-foreground">Total</p>
              <p className="text-lg font-semibold">{knowledgeTotal || "-"}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Pending</p>
              <p className="text-lg font-semibold">
                {k?.total_pending ?? "-"}
              </p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Applied</p>
              <p className="text-lg font-semibold">
                {k?.by_status?.["applied"] ?? "-"}
              </p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Top Category</p>
              <p className="text-lg font-semibold">{topCategory}</p>
            </div>
          </div>
          {k?.by_category && Object.keys(k.by_category).length > 0 && (
            <div className="mt-4 space-y-2">
              {Object.entries(k.by_category)
                .sort((a, b) => b[1] - a[1])
                .map(([cat, count]) => {
                  const pct = knowledgeTotal > 0 ? (count / knowledgeTotal) * 100 : 0;
                  return (
                    <div key={cat}>
                      <div className="mb-0.5 flex items-center justify-between text-xs">
                        <span className="text-muted-foreground">
                          {cat.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())}
                        </span>
                        <span className="font-medium">{count}</span>
                      </div>
                      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                        <div
                          className="h-full rounded-full bg-primary/70 transition-all"
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                    </div>
                  );
                })}
            </div>
          )}
        </div>

        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Pending Review</h2>
          {pendingInsights.data?.data && pendingInsights.data.data.length > 0 ? (
            <div className="space-y-2">
              {pendingInsights.data.data.map((ins) => (
                <div key={ins.id} className="flex items-start gap-2 text-xs">
                  <StatusBadge variant="warning">
                    {ins.confidence}
                  </StatusBadge>
                  <div className="min-w-0 flex-1">
                    <p className="font-medium">
                      {ins.category.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())}
                    </p>
                    <p className="truncate text-muted-foreground">
                      {ins.insight_text}
                    </p>
                  </div>
                  <span className="shrink-0 text-muted-foreground">
                    {new Date(ins.created_at).toLocaleDateString()}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No pending insights</p>
          )}
        </div>
      </div>

      {/* Connections Health */}
      {connections.data && connections.data.connections.length > 0 && (
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Connections</h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {connections.data.connections.map((c) => (
              <div key={c.name} className="rounded-md border p-3">
                <div className="flex items-center gap-2">
                  <StatusBadge variant="success">{c.kind}</StatusBadge>
                  <span className="text-sm font-medium">{c.name}</span>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {c.tools.length} tools / {c.connection}
                </p>
              </div>
            ))}
          </div>
        </div>
      )}

    </div>
  );
}
