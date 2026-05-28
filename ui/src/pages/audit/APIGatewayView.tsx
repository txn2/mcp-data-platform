import { useMemo, useState } from "react";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import {
  useObservabilityQuery,
  useObservabilityQueryRange,
  isBackendUnconfigured,
} from "@/api/observability/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { TimeseriesChart, type TimeseriesSeries } from "@/components/charts/TimeseriesChart";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import {
  topConnectionsByVolume,
  connectionRequestTotal,
  connectionErrorRate,
  latencyQuantile,
  topEndpoints,
  endpointByLabel,
  requestRateRange,
  promVectorToBreakdown,
  promMatrixToTimeseries,
  firstScalar,
} from "./promql";

const presets: { value: TimeRangePreset; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
];

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

// RATE_SERIES plots the single aggregated request-rate line. Its
// dataKey MUST match the field promMatrixToTimeseries populates
// ("count"); APIGatewayView.test.tsx asserts that linkage so the line
// can't silently go flat again.
export const RATE_SERIES: TimeseriesSeries[] = [
  { dataKey: "count", name: "Requests/sec", stroke: "hsl(var(--primary))" },
];

function unixSeconds(iso: string): number {
  return Math.floor(Date.parse(iso) / 1000);
}

function formatPercent(v: number | undefined): string {
  return v === undefined ? "-" : `${(v * 100).toFixed(1)}%`;
}

function formatMillis(seconds: number | undefined): string {
  return seconds === undefined ? "-" : `${Math.round(seconds * 1000)} ms`;
}

// ClickableBreakdown renders a ranked list of {label, count} rows as
// buttons for drilldown. Used for the connection and endpoint levels
// where selecting a row navigates deeper.
function ClickableBreakdown({
  title,
  rows,
  isLoading,
  onSelect,
}: {
  title: string;
  rows: { label: string; count: number }[];
  isLoading: boolean;
  onSelect: (label: string) => void;
}) {
  return (
    <div className="rounded-lg border bg-card p-4">
      <h3 className="mb-3 text-sm font-medium">{title}</h3>
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">No traffic in this window.</p>
      ) : (
        <ul className="divide-y">
          {rows.map((r) => (
            <li key={r.label}>
              <button
                type="button"
                onClick={() => onSelect(r.label)}
                className="flex w-full items-center justify-between px-1 py-2 text-left text-sm hover:bg-muted"
              >
                <span className="truncate font-mono">{r.label}</span>
                <span className="ml-4 shrink-0 tabular-nums text-muted-foreground">
                  {r.count.toLocaleString()}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// APIGatewayView is the PromQL-backed admin view of inbound API gateway
// traffic. It drills connection -> endpoint, with a request-rate
// timeseries on whatever dimension is selected. Renders the
// "backend not configured" empty state when the proxy returns 503.
export function APIGatewayView() {
  const { preset, setPreset, getStartTime, getEndTime } = useTimeRangeStore();
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

  if (isBackendUnconfigured(topConns.error)) {
    return <ObservabilityEmptyState />;
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <Breadcrumb
          connection={connection}
          endpoint={endpoint}
          onRoot={() => {
            setConnection(null);
            setEndpoint(null);
          }}
          onConnection={() => setEndpoint(null)}
        />
        <div className="flex items-center gap-1">
          {presets.map((p) => (
            <button
              key={p.value}
              type="button"
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
      </div>

      {connection === null && (
        <TopConnections
          query={topConns}
          onSelect={setConnection}
          start={start}
          end={end}
          step={step}
          rate={rate}
        />
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

function Breadcrumb({
  connection,
  endpoint,
  onRoot,
  onConnection,
}: {
  connection: string | null;
  endpoint: string | null;
  onRoot: () => void;
  onConnection: () => void;
}) {
  return (
    <nav className="flex items-center gap-1 text-sm">
      <button type="button" onClick={onRoot} className="font-medium hover:underline">
        Connections
      </button>
      {connection !== null && (
        <>
          <span className="text-muted-foreground">/</span>
          <button
            type="button"
            onClick={onConnection}
            className="font-mono hover:underline"
            disabled={endpoint === null}
          >
            {connection}
          </button>
        </>
      )}
      {endpoint !== null && (
        <>
          <span className="text-muted-foreground">/</span>
          <span className="font-mono text-muted-foreground">{endpoint}</span>
        </>
      )}
    </nav>
  );
}

function RateTimeseries({
  query,
  start,
  end,
  step,
  preset,
}: {
  query: string;
  start: number;
  end: number;
  step: number;
  preset: TimeRangePreset;
}) {
  const r = useObservabilityQueryRange(query, start, end, step);
  return (
    <div className="rounded-lg border bg-card p-4">
      <h3 className="mb-3 text-sm font-medium">Request rate</h3>
      <TimeseriesChart
        data={promMatrixToTimeseries(r.data)}
        isLoading={r.isLoading}
        preset={preset}
        series={RATE_SERIES}
      />
    </div>
  );
}

function TopConnections({
  query,
  onSelect,
  start,
  end,
  step,
  rate,
}: {
  query: ReturnType<typeof useObservabilityQuery>;
  onSelect: (c: string) => void;
  start: number;
  end: number;
  step: number;
  rate: string;
}) {
  const { preset } = useTimeRangeStore();
  const rows = promVectorToBreakdown(query.data, "connection").map((e) => ({
    label: e.dimension,
    count: e.count,
  }));
  return (
    <>
      <RateTimeseries query={requestRateRange({}, rate)} start={start} end={end} step={step} preset={preset} />
      <ClickableBreakdown
        title="Top connections by request volume"
        rows={rows}
        isLoading={query.isLoading}
        onSelect={onSelect}
      />
    </>
  );
}

function ConnectionDetail({
  connection,
  window,
  start,
  end,
  step,
  rate,
  onSelectEndpoint,
}: {
  connection: string;
  window: string;
  start: number;
  end: number;
  step: number;
  rate: string;
  onSelectEndpoint: (op: string) => void;
}) {
  const { preset } = useTimeRangeStore();
  const total = useObservabilityQuery(connectionRequestTotal(connection, window));
  const errRate = useObservabilityQuery(connectionErrorRate(connection, window));
  const p50 = useObservabilityQuery(latencyQuantile(0.5, connection, window));
  const p95 = useObservabilityQuery(latencyQuantile(0.95, connection, window));
  const p99 = useObservabilityQuery(latencyQuantile(0.99, connection, window));
  const endpoints = useObservabilityQuery(topEndpoints(connection, window));

  const endpointRows = promVectorToBreakdown(endpoints.data, "operation_id").map((e) => ({
    label: e.dimension,
    count: e.count,
  }));

  return (
    <>
      <div className="grid grid-cols-3 gap-4 lg:grid-cols-5">
        <StatCard label="Total requests" value={firstScalar(total.data)?.toLocaleString() ?? "-"} />
        <StatCard label="Error rate" value={formatPercent(firstScalar(errRate.data))} />
        <StatCard label="p50" value={formatMillis(firstScalar(p50.data))} />
        <StatCard label="p95" value={formatMillis(firstScalar(p95.data))} />
        <StatCard label="p99" value={formatMillis(firstScalar(p99.data))} />
      </div>
      <RateTimeseries
        query={requestRateRange({ connection }, rate)}
        start={start}
        end={end}
        step={step}
        preset={preset}
      />
      <ClickableBreakdown
        title="Top endpoints by request volume"
        rows={endpointRows}
        isLoading={endpoints.isLoading}
        onSelect={onSelectEndpoint}
      />
    </>
  );
}

function EndpointDetail({
  connection,
  endpoint,
  window,
  start,
  end,
  step,
  rate,
}: {
  connection: string;
  endpoint: string;
  window: string;
  start: number;
  end: number;
  step: number;
  rate: string;
}) {
  const { preset } = useTimeRangeStore();
  const statusClasses = useObservabilityQuery(endpointByLabel("status_class", connection, endpoint, window));
  const methods = useObservabilityQuery(endpointByLabel("method", connection, endpoint, window));
  const identities = useObservabilityQuery(endpointByLabel("identity", connection, endpoint, window));

  return (
    <>
      <RateTimeseries
        query={requestRateRange({ connection, operationID: endpoint }, rate)}
        start={start}
        end={end}
        step={step}
        preset={preset}
      />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Breakdown title="Status class" query={statusClasses} labelKey="status_class" />
        <Breakdown title="Method" query={methods} labelKey="method" />
        <Breakdown title="Identity" query={identities} labelKey="identity" />
      </div>
    </>
  );
}

function Breakdown({
  title,
  query,
  labelKey,
}: {
  title: string;
  query: ReturnType<typeof useObservabilityQuery>;
  labelKey: string;
}) {
  return (
    <div className="rounded-lg border bg-card p-4">
      <h3 className="mb-3 text-sm font-medium">{title}</h3>
      <BreakdownBarChart data={promVectorToBreakdown(query.data, labelKey)} isLoading={query.isLoading} />
    </div>
  );
}

// ObservabilityEmptyState is shown when the PromQL proxy returns 503
// (Prometheus not configured for this deployment).
export function ObservabilityEmptyState() {
  return (
    <div className="rounded-lg border border-dashed bg-card p-10 text-center">
      <h3 className="text-sm font-medium">Observability backend not configured</h3>
      <p className="mt-2 text-sm text-muted-foreground">
        Configure a Prometheus instance under <code>observability.prometheus</code> to view API
        gateway metrics. See the observability documentation.
      </p>
    </div>
  );
}
