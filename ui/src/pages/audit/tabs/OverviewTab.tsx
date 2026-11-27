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
import { SectionCard } from "@/components/patterns/SectionCard";
import { UsageHeatmap } from "@/components/charts/UsageHeatmap";
import { TimeseriesChart } from "@/components/charts/TimeseriesChart";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import { LatencyPanel } from "@/components/charts/LatencyPanel";
import { RecentErrorsList } from "@/components/RecentErrorsList";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import type { Resolution } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { TimeRangePicker } from "../TimeRangePicker";
import { KPIRow } from "./overview/KPIRow";
import { ToolBreakdownTable } from "./overview/ToolBreakdownTable";
import { OtherEventsPanel } from "./overview/OtherEventsPanel";
import { FailuresPanel } from "./overview/FailuresPanel";
import { InsightStatsPanel, PendingReviewPanel } from "./overview/KnowledgePanels";

// MCP_EVENT_KIND scopes the MCP dashboard and the events table to
// MCP tool calls, excluding apigateway invocations whose 24/7 ETL
// volume otherwise drowns the human MCP signal (#464). The API Gateway
// tab covers that traffic via the PromQL-backed view instead.
const MCP_EVENT_KIND = "mcp_tool_call";

function getResolution(preset: TimeRangePreset): Resolution {
  switch (preset) {
    case "1h": return "minute";
    case "6h": return "minute";
    case "24h": return "hour";
    case "7d": return "day";
  }
}

export function OverviewTab({
  onNavigate,
  onOpenEvents,
}: {
  onNavigate?: (path: string) => void;
  onOpenEvents?: () => void;
}) {
  const titleMap = useToolTitleMap();
  const { preset, getStartTime, getEndTime } = useTimeRangeStore();
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
  // Every event kind, so what is recorded beside tool calls is counted apart
  // from them (#2819).
  const kindBreakdown = useAuditBreakdown({ groupBy: "event_kind", limit: 20, startTime, endTime });
  // The failed calls counted by category, source and persona, exactly (#2795).
  const failuresByCategory = useAuditBreakdown({ groupBy: "error_category", success: false, limit: 100, eventKind: MCP_EVENT_KIND, startTime, endTime });
  const failuresBySource = useAuditBreakdown({ groupBy: "source", success: false, limit: 20, eventKind: MCP_EVENT_KIND, startTime, endTime });
  const failuresByPersona = useAuditBreakdown({ groupBy: "persona", success: false, limit: 10, eventKind: MCP_EVENT_KIND, startTime, endTime });
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
      <TimeRangePicker />

      <KPIRow overview={o} buckets={timeseries.data ?? []} />

      <SectionCard title="Activity">
        <TimeseriesChart data={timeseries.data} isLoading={timeseries.isLoading} preset={preset} />
      </SectionCard>

      {/* Usage heatmap: weekday x hour over the last 7 days */}
      <SectionCard
        title="Usage Rhythm"
        action={
          <span className="text-xs text-muted-foreground">
            last 7 days · calls by weekday &amp; hour
          </span>
        }
      >
        <UsageHeatmap data={heatmapTs.data} isLoading={heatmapTs.isLoading} />
      </SectionCard>

      {/* Charts Grid */}
      <div className="grid gap-6 lg:grid-cols-2">
        <SectionCard
          title="Top Tools"
          action={<span className="text-xs text-muted-foreground">tool calls only</span>}
        >
          <ToolBreakdownTable
            data={toolBreakdown.data}
            isLoading={toolBreakdown.isLoading}
            labelMap={titleMap}
          />
        </SectionCard>
        <SectionCard title="Other events">
          <OtherEventsPanel
            data={kindBreakdown.data}
            isLoading={kindBreakdown.isLoading}
            onOpenEvents={onOpenEvents}
          />
        </SectionCard>
        <SectionCard title="Top Users">
          <BreakdownBarChart
            data={userBreakdown.data}
            isLoading={userBreakdown.isLoading}
            color="hsl(221, 83%, 53%)"
          />
        </SectionCard>
        <SectionCard title="By Persona">
          <BreakdownBarChart
            data={personaBreakdown.data}
            isLoading={personaBreakdown.isLoading}
            height={180}
            color="hsl(262, 83%, 58%)"
          />
        </SectionCard>
        <SectionCard title="By Toolkit">
          <BreakdownBarChart
            data={toolkitBreakdown.data}
            isLoading={toolkitBreakdown.isLoading}
            height={180}
            color="hsl(172, 66%, 50%)"
          />
        </SectionCard>
      </div>

      <SectionCard
        title="Failures"
        action={<span className="text-xs text-muted-foreground">tool calls only · exact counts</span>}
      >
        <FailuresPanel
          byCategory={failuresByCategory.data}
          bySource={failuresBySource.data}
          byPersona={failuresByPersona.data}
          isLoading={failuresByCategory.isLoading || failuresBySource.isLoading || failuresByPersona.isLoading}
        />
      </SectionCard>

      {/* Bottom Row */}
      <div className="grid gap-6 lg:grid-cols-2">
        <SectionCard
          title="Latency"
          action={
            performance.data && (
              <span className="text-xs text-muted-foreground">
                avg {formatDuration(performance.data.avg_ms)} ·{" "}
                {performance.data.avg_response_chars.toFixed(0)} chars/resp
              </span>
            )
          }
        >
          <LatencyPanel data={performance.data} isLoading={performance.isLoading} />
        </SectionCard>

        <SectionCard title="Recent Errors">
          <RecentErrorsList
            events={recentErrors.data?.data}
            onNavigate={onNavigate}
            titleMap={titleMap}
          />
        </SectionCard>
      </div>

      {/* Knowledge */}
      <div className="grid gap-6 lg:grid-cols-2">
        <InsightStatsPanel stats={k} total={knowledgeTotal} topCategory={topCategory} />
        <PendingReviewPanel insights={pendingInsights.data?.data} />
      </div>
    </div>
  );
}
