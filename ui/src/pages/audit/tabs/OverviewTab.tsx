import { useMemo } from "react";
import {
  useAuditEvents,
  useAuditOverview,
  useAuditTimeseries,
  useAuditBreakdown,
  useAuditPerformance,
  useToolTitleMap,
  useInsightStats,
  useInsights,
} from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { MetricTile } from "@/components/charts/MetricTile";
import { UsageHeatmap } from "@/components/charts/UsageHeatmap";
import { TimeseriesChart } from "@/components/charts/TimeseriesChart";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import { LatencyPanel } from "@/components/charts/LatencyPanel";
import { RecentErrorsList } from "@/components/RecentErrorsList";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import type { Resolution } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";

// MCP_EVENT_KIND scopes the MCP dashboard and the events table to
// MCP tool calls, excluding apigateway invocations whose 24/7 ETL
// volume otherwise drowns the human MCP signal (#464). The API Gateway
// tab covers that traffic via the PromQL-backed view instead.
const MCP_EVENT_KIND = "mcp_tool_call";

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

// halfDelta estimates a period-over-period change from a single timeseries:
// it compares the newer half of the buckets to the older half. Returns
// undefined when there are too few buckets or the older half is empty (so
// the tile shows no misleading trend). agg is "sum" for counts, "avg" for
// rates/durations.
function halfDelta(vals: number[], agg: "sum" | "avg"): number | undefined {
  if (vals.length < 4) return undefined;
  const mid = Math.floor(vals.length / 2);
  const reduce = (a: number[]) => {
    const sum = a.reduce((s, v) => s + v, 0);
    return agg === "sum" ? sum : a.length ? sum / a.length : 0;
  };
  const older = reduce(vals.slice(0, mid));
  const newer = reduce(vals.slice(mid));
  if (older === 0) return undefined;
  return (newer - older) / older;
}

export function OverviewTab({ onNavigate }: { onNavigate?: (path: string) => void }) {
  const titleMap = useToolTitleMap();
  const { preset, setPreset, getStartTime, getEndTime } = useTimeRangeStore();
  const { startTime, endTime } = useMemo(
    () => ({ startTime: getStartTime(), endTime: getEndTime() }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [preset],
  );

  const overview = useAuditOverview({ eventKind: MCP_EVENT_KIND, startTime, endTime });
  const timeseries = useAuditTimeseries({ resolution: getResolution(preset), eventKind: MCP_EVENT_KIND, startTime, endTime });
  const toolBreakdown = useAuditBreakdown({ groupBy: "tool_name", limit: 8, eventKind: MCP_EVENT_KIND, startTime, endTime });
  const userBreakdown = useAuditBreakdown({ groupBy: "user_id", limit: 5, eventKind: MCP_EVENT_KIND, startTime, endTime });
  const personaBreakdown = useAuditBreakdown({ groupBy: "persona", limit: 6, eventKind: MCP_EVENT_KIND, startTime, endTime });
  const toolkitBreakdown = useAuditBreakdown({ groupBy: "toolkit_kind", limit: 6, eventKind: MCP_EVENT_KIND, startTime, endTime });
  const recentErrors = useAuditEvents({ perPage: 5, success: false, eventKind: MCP_EVENT_KIND });
  const performance = useAuditPerformance({ eventKind: MCP_EVENT_KIND, startTime, endTime });

  // The usage heatmap always shows the last 7 days at hourly resolution
  // (independent of the page preset) so the weekday/hour rhythm is visible.
  // Snap to the hour to keep the React Query key stable across renders.
  const heat = useMemo(() => {
    const hourMs = 3_600_000;
    const end = Math.floor(Date.now() / hourMs) * hourMs;
    return {
      start: new Date(end - 7 * 24 * hourMs).toISOString(),
      end: new Date(end).toISOString(),
    };
  }, []);
  const heatmapTs = useAuditTimeseries({
    resolution: "hour",
    eventKind: MCP_EVENT_KIND,
    startTime: heat.start,
    endTime: heat.end,
  });

  // Knowledge insights are platform-wide (not call-scoped), so they are
  // not filtered by event kind.
  const insightStats = useInsightStats();
  const pendingInsights = useInsights({ perPage: 5, status: "pending" });

  const o = overview.data;
  const k = insightStats.data;

  // Derive per-KPI sparklines and period-over-period deltas from the
  // activity timeseries so each tile shows its own trend, not just a number.
  const ts = timeseries.data ?? [];
  const callsSpark = ts.map((b) => b.count);
  const errorSpark = ts.map((b) => b.error_count);
  const durationSpark = ts.map((b) => b.avg_duration_ms);
  const successSpark = ts.map((b) => (b.count > 0 ? (b.success_count / b.count) * 100 : 0));
  const callsDelta = halfDelta(callsSpark, "sum");
  const errorDelta = halfDelta(errorSpark, "sum");
  const durationDelta = halfDelta(durationSpark, "avg");
  const successDelta = halfDelta(successSpark, "avg");

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
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4 lg:grid-cols-7">
        <MetricTile
          label="Total Calls"
          value={o?.total_calls?.toLocaleString() ?? "-"}
          spark={callsSpark}
          delta={callsDelta}
          goodDirection="neutral"
          emphasize
        />
        <MetricTile
          label="Success Rate"
          value={o ? `${(o.success_rate * 100).toFixed(1)}%` : "-"}
          spark={successSpark}
          delta={successDelta}
          goodDirection="up"
          accent="hsl(142, 71%, 45%)"
        />
        <MetricTile
          label="Avg Duration"
          value={o ? formatDuration(o.avg_duration_ms) : "-"}
          spark={durationSpark}
          delta={durationDelta}
          goodDirection="down"
        />
        <MetricTile label="Unique Users" value={o?.unique_users?.toString() ?? "-"} />
        <MetricTile label="Unique Tools" value={o?.unique_tools?.toString() ?? "-"} />
        <MetricTile
          label="Enrichment"
          value={o ? `${(o.enrichment_rate * 100).toFixed(0)}%` : "-"}
          goodDirection="up"
        />
        <MetricTile
          label="Errors"
          value={o?.error_count?.toLocaleString() ?? "-"}
          spark={errorSpark}
          delta={errorDelta}
          goodDirection="down"
          accent="hsl(0, 72%, 51%)"
        />
      </div>

      {/* Activity Chart */}
      <div className="rounded-lg border bg-card p-4">
        <h2 className="mb-3 text-sm font-medium">Activity</h2>
        <TimeseriesChart data={timeseries.data} isLoading={timeseries.isLoading} preset={preset} />
      </div>

      {/* Usage heatmap: weekday x hour over the last 7 days */}
      <div className="rounded-lg border bg-card p-4">
        <div className="mb-3 flex items-baseline justify-between">
          <h2 className="text-sm font-medium">Usage Rhythm</h2>
          <span className="text-xs text-muted-foreground">last 7 days · calls by weekday &amp; hour</span>
        </div>
        <UsageHeatmap data={heatmapTs.data} isLoading={heatmapTs.isLoading} />
      </div>

      {/* Charts Grid */}
      <div className="grid gap-6 lg:grid-cols-2">
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Top Tools</h2>
          <BreakdownBarChart data={toolBreakdown.data} isLoading={toolBreakdown.isLoading} labelMap={titleMap} />
        </div>
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Top Users</h2>
          <BreakdownBarChart
            data={userBreakdown.data}
            isLoading={userBreakdown.isLoading}
            color="hsl(221, 83%, 53%)"
          />
        </div>
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">By Persona</h2>
          <BreakdownBarChart
            data={personaBreakdown.data}
            isLoading={personaBreakdown.isLoading}
            height={180}
            color="hsl(262, 83%, 58%)"
          />
        </div>
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">By Toolkit</h2>
          <BreakdownBarChart
            data={toolkitBreakdown.data}
            isLoading={toolkitBreakdown.isLoading}
            height={180}
            color="hsl(172, 66%, 50%)"
          />
        </div>
      </div>

      {/* Bottom Row */}
      <div className="grid gap-6 lg:grid-cols-2">
        {/* Latency distribution */}
        <div className="flex flex-col rounded-lg border bg-card p-4">
          <div className="mb-3 flex items-baseline justify-between">
            <h2 className="text-sm font-medium">Latency</h2>
            {performance.data && (
              <span className="text-xs text-muted-foreground">
                avg {formatDuration(performance.data.avg_ms)} · {performance.data.avg_response_chars.toFixed(0)} chars/resp
              </span>
            )}
          </div>
          <div className="flex-1">
            <LatencyPanel data={performance.data} isLoading={performance.isLoading} />
          </div>
        </div>

        {/* Recent Errors */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Recent Errors</h2>
          <RecentErrorsList events={recentErrors.data?.data} onNavigate={onNavigate} titleMap={titleMap} />
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
              <p className="text-lg font-semibold">{k?.total_pending ?? "-"}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Applied</p>
              <p className="text-lg font-semibold">{k?.by_status?.["applied"] ?? "-"}</p>
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
                  <StatusBadge variant="warning">{ins.confidence}</StatusBadge>
                  <div className="min-w-0 flex-1">
                    <p className="font-medium">
                      {ins.category.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())}
                    </p>
                    <p className="truncate text-muted-foreground">{ins.insight_text}</p>
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

    </div>
  );
}
