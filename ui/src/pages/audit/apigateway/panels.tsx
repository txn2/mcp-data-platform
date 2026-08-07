import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import { TimeseriesChart, type TimeseriesSeries } from "@/components/charts/TimeseriesChart";
import {
  useObservabilityQuery,
  useObservabilityQueryRange,
} from "@/api/observability/hooks";
import { useTimeRangeStore } from "@/stores/timerange";
import { promMatrixToTimeseries, promVectorToBreakdown } from "../promql";

// The panels the API Gateway view is assembled from: the section box every
// chart sits in, the two breakdown lists, the request-rate chart, and the
// drilldown breadcrumb. Extracted from APIGatewayView.tsx (#1207).

// RATE_SERIES plots the single aggregated request-rate line. Its
// dataKey MUST match the field promMatrixToTimeseries populates
// ("count"); APIGatewayView.test.tsx asserts that linkage so the line
// can't silently go flat again.
export const RATE_SERIES: TimeseriesSeries[] = [
  { dataKey: "count", name: "Requests/sec", stroke: "hsl(var(--primary))" },
];

// ChartPanel is the one box a gateway chart sits in: the panel's title with
// the unit it is measured in on the header's own row.
export function ChartPanel({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <SectionCard
      title={title}
      action={hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    >
      {children}
    </SectionCard>
  );
}

// ClickableBreakdown renders a ranked list of {label, count} rows as
// buttons for drilldown. Used for the connection and endpoint levels
// where selecting a row navigates deeper.
export function ClickableBreakdown({
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
    <ChartPanel title={title}>
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : rows.length === 0 ? (
        <EmptyState>No traffic in this window.</EmptyState>
      ) : (
        <ul className="divide-y">
          {rows.map((r) => (
            <li key={r.label}>
              <Button
                type="button"
                variant="ghost"
                onClick={() => onSelect(r.label)}
                className="h-auto w-full justify-between px-1 py-2 text-left font-normal"
              >
                <span className="truncate font-mono">{r.label}</span>
                <span className="ml-4 shrink-0 tabular-nums text-muted-foreground">
                  {r.count.toLocaleString()}
                </span>
              </Button>
            </li>
          ))}
        </ul>
      )}
    </ChartPanel>
  );
}

// Breakdown is the read-only counterpart: a bar chart of one label's
// distribution at the endpoint level, where there is nothing deeper to
// drill into.
export function Breakdown({
  title,
  query,
  labelKey,
}: {
  title: string;
  query: ReturnType<typeof useObservabilityQuery>;
  labelKey: string;
}) {
  return (
    <ChartPanel title={title}>
      <BreakdownBarChart
        data={promVectorToBreakdown(query.data, labelKey)}
        isLoading={query.isLoading}
      />
    </ChartPanel>
  );
}

export function RateTimeseries({
  query,
  start,
  end,
  step,
}: {
  query: string;
  start: number;
  end: number;
  step: number;
}) {
  const { preset } = useTimeRangeStore();
  const r = useObservabilityQueryRange(query, start, end, step);
  return (
    <ChartPanel title="Request rate">
      <TimeseriesChart
        data={promMatrixToTimeseries(r.data)}
        isLoading={r.isLoading}
        preset={preset}
        series={RATE_SERIES}
      />
    </ChartPanel>
  );
}

export function Breadcrumb({
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
      <Button type="button" variant="link" size="xs" onClick={onRoot} className="px-0">
        Connections
      </Button>
      {connection !== null && (
        <>
          <span className="text-muted-foreground">/</span>
          <Button
            type="button"
            variant="link"
            size="xs"
            onClick={onConnection}
            disabled={endpoint === null}
            className="px-0 font-mono disabled:opacity-100"
          >
            {connection}
          </Button>
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
