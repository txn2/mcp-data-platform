import { useTimeRangeStore } from "@/stores/timerange";
import {
  useSystemInfo,
  useAuditOverview,
  useAuditTimeseries,
  useAuditBreakdown,
  useAuditEvents,
  useConnections,
  useAuditPerformance,
} from "@/api/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { TimeseriesChart } from "@/components/charts/TimeseriesChart";
import { BreakdownBarChart } from "@/components/charts/BarChart";

export function DashboardPage() {
  const { getStartTime, getEndTime } = useTimeRangeStore();
  const startTime = getStartTime();
  const endTime = getEndTime();

  const systemInfo = useSystemInfo();
  const overview = useAuditOverview({ startTime, endTime });
  const timeseries = useAuditTimeseries({ resolution: "hour", startTime, endTime });
  const toolBreakdown = useAuditBreakdown({ groupBy: "tool_name", limit: 8, startTime, endTime });
  const userBreakdown = useAuditBreakdown({ groupBy: "user_id", limit: 5, startTime, endTime });
  const recentErrors = useAuditEvents({ perPage: 5, success: false });
  const connections = useConnections();
  const performance = useAuditPerformance({ startTime, endTime });

  const o = overview.data;

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
          value={o ? `${o.avg_duration_ms.toFixed(0)}ms` : "-"}
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
                <p className="text-lg font-semibold">{performance.data.p50_ms.toFixed(0)}ms</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">P95</p>
                <p className="text-lg font-semibold">{performance.data.p95_ms.toFixed(0)}ms</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">P99</p>
                <p className="text-lg font-semibold">{performance.data.p99_ms.toFixed(0)}ms</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Avg</p>
                <p className="text-lg font-semibold">{performance.data.avg_ms.toFixed(0)}ms</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Max</p>
                <p className="text-lg font-semibold">{performance.data.max_ms.toFixed(0)}ms</p>
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
          {recentErrors.data?.data.length === 0 ? (
            <p className="text-sm text-muted-foreground">No recent errors</p>
          ) : (
            <div className="space-y-2">
              {recentErrors.data?.data.map((e) => (
                <div key={e.id} className="flex items-start gap-2 text-xs">
                  <StatusBadge variant="error">Error</StatusBadge>
                  <div className="min-w-0 flex-1">
                    <p className="font-medium">{e.tool_name}</p>
                    <p className="truncate text-muted-foreground">
                      {e.error_message || "Unknown error"}
                    </p>
                  </div>
                  <span className="shrink-0 text-muted-foreground">
                    {new Date(e.timestamp).toLocaleTimeString()}
                  </span>
                </div>
              ))}
            </div>
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
