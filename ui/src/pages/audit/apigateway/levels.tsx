import { StatCard } from "@/components/cards/StatCard";
import { useObservabilityQuery } from "@/api/observability/hooks";
import type { PromVectorResponse } from "@/api/observability/types";
import {
  connectionErrorRate,
  connectionRequestTotal,
  endpointByLabel,
  latencyQuantile,
  outboundByPersona,
  promVectorToBreakdown,
  requestRateRange,
  topEndpoints,
  firstScalar,
} from "../promql";
import { Breakdown, ClickableBreakdown, RateTimeseries } from "./panels";

// The three drilldown levels of the API Gateway view: the root summary, one
// connection, one endpoint of that connection. Extracted from
// APIGatewayView.tsx (#1207).

function formatPercent(v: number | undefined): string {
  return v === undefined ? "-" : `${(v * 100).toFixed(1)}%`;
}

function formatMillis(seconds: number | undefined): string {
  return seconds === undefined ? "-" : `${Math.round(seconds * 1000)} ms`;
}

// outboundTally reduces the by-status-category vector to the three numbers
// the overview reports: how many upstream calls there were, what share of them
// failed, and how many of those were the upstream's own fault (5xx).
function outboundTally(byCategory?: PromVectorResponse): {
  total: number;
  errRate: number;
  serverErr: number;
} {
  const cats: Record<string, number> = {};
  for (const r of byCategory?.data?.result ?? []) {
    cats[r.metric["status_category"] ?? "unknown"] = Math.round(Number(r.value[1]));
  }
  const clientErr = cats["client_error"] ?? 0;
  const serverErr = cats["server_error"] ?? 0;
  const total = (cats["success"] ?? 0) + clientErr + serverErr;
  return { total, errRate: total > 0 ? (clientErr + serverErr) / total : 0, serverErr };
}

// GatewayOverview summarizes inbound vs outbound traffic at the root level.
// Inbound = requests the platform received at the REST shim; outbound =
// upstream calls the gateway made on their behalf. The outbound error rate
// (client + server categories) is the headline health signal for upstreams.
export function GatewayOverview({
  inbound,
  outbound,
  outboundByCat,
}: {
  inbound?: number;
  outbound?: number;
  outboundByCat?: PromVectorResponse;
}) {
  const { total: outTotal, errRate, serverErr } = outboundTally(outboundByCat);

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <StatCard
        label="Inbound requests"
        value={inbound !== undefined ? Math.round(inbound).toLocaleString() : "-"}
      />
      <StatCard
        label="Outbound calls"
        value={outbound !== undefined ? Math.round(outbound).toLocaleString() : "-"}
      />
      <StatCard
        label="Outbound error rate"
        value={outTotal > 0 ? `${(errRate * 100).toFixed(1)}%` : "-"}
        className={errRate > 0.05 ? "border-destructive/40" : undefined}
      />
      <StatCard label="Upstream 5xx" value={serverErr.toLocaleString()} />
    </div>
  );
}

export function TopConnections({
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
  const rows = promVectorToBreakdown(query.data, "connection").map((e) => ({
    label: e.dimension,
    count: e.count,
  }));
  return (
    <>
      <RateTimeseries query={requestRateRange({}, rate)} start={start} end={end} step={step} />
      <ClickableBreakdown
        title="Top connections by request volume"
        rows={rows}
        isLoading={query.isLoading}
        onSelect={onSelect}
      />
    </>
  );
}

export function ConnectionDetail({
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
  const total = useObservabilityQuery(connectionRequestTotal(connection, window));
  const errRate = useObservabilityQuery(connectionErrorRate(connection, window));
  const p50 = useObservabilityQuery(latencyQuantile(0.5, connection, window));
  const p95 = useObservabilityQuery(latencyQuantile(0.95, connection, window));
  const p99 = useObservabilityQuery(latencyQuantile(0.99, connection, window));
  const endpoints = useObservabilityQuery(topEndpoints(connection, window));
  const principals = useObservabilityQuery(outboundByPersona(window, connection));

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
      />
      <ClickableBreakdown
        title="Top endpoints by request volume"
        rows={endpointRows}
        isLoading={endpoints.isLoading}
        onSelect={onSelectEndpoint}
      />
      <Breakdown
        title="Outbound calls by principal"
        hint="this connection's upstream calls, by the persona that caused them"
        query={principals}
        labelKey="persona"
      />
    </>
  );
}

export function EndpointDetail({
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
  const statusClasses = useObservabilityQuery(
    endpointByLabel("status_class", connection, endpoint, window),
  );
  const methods = useObservabilityQuery(endpointByLabel("method", connection, endpoint, window));
  const identities = useObservabilityQuery(
    endpointByLabel("identity", connection, endpoint, window),
  );

  return (
    <>
      <RateTimeseries
        query={requestRateRange({ connection, operationID: endpoint }, rate)}
        start={start}
        end={end}
        step={step}
      />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Breakdown title="Status class" query={statusClasses} labelKey="status_class" />
        <Breakdown title="Method" query={methods} labelKey="method" />
        <Breakdown title="Identity" query={identities} labelKey="identity" />
      </div>
    </>
  );
}
