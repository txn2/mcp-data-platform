import { useMemo, useState } from "react";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import {
  useObservabilityQuery,
  useObservabilityQueryRange,
  isBackendUnconfigured,
} from "@/api/observability/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { FlowSankey } from "@/components/charts/FlowSankey";
import { StatusStackChart } from "@/components/charts/StatusStackChart";
import { UsageHeatmap } from "@/components/charts/UsageHeatmap";
import {
  topConnectionsByVolume,
  connectionOperationFlow,
  outboundByPersona,
  statusClassRateRange,
  promVectorToFlow,
  promMatrixToStatusStack,
  promMatrixToTimeseries,
  firstScalar,
} from "./promql";
import { TimeRangePicker } from "./TimeRangePicker";
import { Breadcrumb, Breakdown, ChartPanel } from "./apigateway/panels";
import {
  ConnectionDetail,
  EndpointDetail,
  GatewayOverview,
  TopConnections,
} from "./apigateway/levels";

// presetParams maps a time-range preset to the PromQL window (for
// increase/quantile over the whole range), the range-query step in
// seconds (chart resolution), and the rate window for the timeseries
// line (>= step so the line is smooth).
function presetParams(preset: TimeRangePreset): { window: string; step: number; rate: string } {
  switch (preset) {
    case "1h":
      return { window: "1h", step: 60, rate: "5m" };
    case "6h":
      return { window: "6h", step: 300, rate: "10m" };
    case "24h":
      return { window: "24h", step: 3600, rate: "30m" };
    case "7d":
      return { window: "7d", step: 86400, rate: "6h" };
  }
}

function unixSeconds(iso: string): number {
  return Math.floor(Date.parse(iso) / 1000);
}

// APIGatewayView is the PromQL-backed admin view of inbound API gateway
// traffic. It drills connection -> endpoint, with a request-rate
// timeseries on whatever dimension is selected. Renders the
// "backend not configured" empty state when the proxy returns 503.
export function APIGatewayView() {
  const { preset, getStartTime, getEndTime } = useTimeRangeStore();
  const { window, step, rate } = presetParams(preset);
  const { start, end } = useMemo(
    () => ({ start: unixSeconds(getStartTime()), end: unixSeconds(getEndTime()) }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [preset],
  );

  const [connection, setConnection] = useState<string | null>(null);
  const [endpoint, setEndpoint] = useState<string | null>(null);

  const topConns = useObservabilityQuery(topConnectionsByVolume(window), {
    enabled: connection === null,
  });
  const flow = useObservabilityQuery(connectionOperationFlow(window), {
    enabled: connection === null,
  });
  const inboundTotal = useObservabilityQuery(
    `sum(increase(apigateway_inbound_requests_total[${window}]))`,
    { enabled: connection === null },
  );
  const outboundTotal = useObservabilityQuery(
    `sum(increase(apigateway_outbound_total[${window}]))`,
    { enabled: connection === null },
  );
  const outboundByCat = useObservabilityQuery(
    `sum by (status_category) (increase(apigateway_outbound_total[${window}]))`,
    { enabled: connection === null },
  );
  const outboundByPrincipal = useObservabilityQuery(outboundByPersona(window), {
    enabled: connection === null,
  });
  const statusStack = useObservabilityQueryRange(statusClassRateRange(rate), start, end, step, {
    enabled: connection === null,
  });
  // Usage heatmap always shows the last 7 days at hourly resolution
  // (independent of the page preset). Snap to the hour for a stable key.
  const heat = useMemo(() => {
    const hr = 3600;
    const e = Math.floor(Date.now() / 1000 / hr) * hr;
    return { start: e - 7 * 24 * hr, end: e };
  }, []);
  const heatRange = useObservabilityQueryRange(
    "sum(increase(apigateway_inbound_requests_total[1h]))",
    heat.start,
    heat.end,
    3600,
    { enabled: connection === null },
  );

  if (isBackendUnconfigured(topConns.error)) {
    return <ObservabilityEmptyState />;
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        {/* Time chooser on the left, consistent with the MCP tab. */}
        <TimeRangePicker />
        <Breadcrumb
          connection={connection}
          endpoint={endpoint}
          onRoot={() => {
            setConnection(null);
            setEndpoint(null);
          }}
          onConnection={() => setEndpoint(null)}
        />
      </div>

      {connection === null && (
        <>
          <GatewayOverview
            inbound={firstScalar(inboundTotal.data)}
            outbound={firstScalar(outboundTotal.data)}
            outboundByCat={outboundByCat.data}
          />
          <Breakdown
            title="Outbound calls by principal"
            hint="upstream calls by the persona that caused them"
            query={outboundByPrincipal}
            labelKey="persona"
          />
          <ChartPanel title="Status Mix" hint="requests/sec by class">
            <StatusStackChart
              data={promMatrixToStatusStack(statusStack.data)}
              isLoading={statusStack.isLoading}
              height={200}
            />
          </ChartPanel>
          <ChartPanel title="Usage Rhythm" hint="last 7 days · requests by weekday & hour">
            <UsageHeatmap
              data={promMatrixToTimeseries(heatRange.data)}
              isLoading={heatRange.isLoading}
            />
          </ChartPanel>
          <ChartPanel title="Traffic Flow" hint="connection → operation, by volume">
            <FlowSankey graph={promVectorToFlow(flow.data)} isLoading={flow.isLoading} height={400} />
          </ChartPanel>
          <TopConnections
            query={topConns}
            onSelect={setConnection}
            start={start}
            end={end}
            step={step}
            rate={rate}
          />
        </>
      )}

      {connection !== null && endpoint === null && (
        <ConnectionDetail
          connection={connection}
          window={window}
          start={start}
          end={end}
          step={step}
          rate={rate}
          onSelectEndpoint={setEndpoint}
        />
      )}

      {connection !== null && endpoint !== null && (
        <EndpointDetail
          connection={connection}
          endpoint={endpoint}
          window={window}
          start={start}
          end={end}
          step={step}
          rate={rate}
        />
      )}
    </div>
  );
}

// ObservabilityEmptyState is shown when the PromQL proxy returns 503
// (metrics backend unavailable for this deployment). The copy is generic
// on purpose: the people viewing this do not control the deployment, so
// it states the situation without ops-facing configuration detail.
function ObservabilityEmptyState() {
  return (
    <EmptyState className="p-10">
      <p className="text-sm font-medium text-foreground">API gateway metrics unavailable</p>
      <p className="mt-2 text-sm">
        API gateway metrics are not available for this platform right now.
      </p>
    </EmptyState>
  );
}
