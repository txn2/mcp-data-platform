import { useMemo } from "react";
import {
  isBackendUnconfigured,
  useObservabilityQuery,
  useObservabilityQueryRange,
} from "@/api/observability/hooks";
import { IndexThroughputTimeline } from "@/components/charts/IndexThroughputTimeline";
import { IndexLatencyTrack } from "@/components/charts/IndexLatencyTrack";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  STEP_SECONDS,
  WINDOW,
  WINDOW_SECONDS,
  completionsPerStep,
  latencyQuantile,
  matrixToThroughput,
  passesByKind,
  vectorsToLatency,
} from "../metrics";
import { Section } from "./panels";

// QueueMetricPanels is the dashboard's Throughput and Embed latency, read from
// Prometheus (#1837). They are counted as each job settles, so a backlog of any
// size leaves them intact; the job table can answer them only one page at a
// time, and during a backlog that page is all pending.
//
// One unconfigured answer covers both: the proxy is wired to a Prometheus or it
// is not. Everything else on the page reads the platform's own tables and is
// unaffected.
export function QueueMetricPanels() {
  // The window is fixed and ends now; rounding to the step keeps the range
  // query's key stable across re-renders within one step.
  const end = Math.floor(Date.now() / 1000 / STEP_SECONDS) * STEP_SECONDS;
  const start = end - WINDOW_SECONDS;

  const throughput = useObservabilityQueryRange(
    completionsPerStep(),
    start,
    end,
    STEP_SECONDS,
  );
  const p50 = useObservabilityQuery(latencyQuantile(0.5));
  const p95 = useObservabilityQuery(latencyQuantile(0.95));
  const p99 = useObservabilityQuery(latencyQuantile(0.99));
  const passes = useObservabilityQuery(passesByKind());

  const points = useMemo(
    () => matrixToThroughput(throughput.data),
    [throughput.data],
  );
  const latency = useMemo(
    () => vectorsToLatency(p50.data, p95.data, p99.data, passes.data),
    [p50.data, p95.data, p99.data, passes.data],
  );

  if (isBackendUnconfigured(throughput.error)) {
    return (
      <Alert>
        <AlertDescription>
          This deployment has no metrics backend configured, so Throughput and
          Embed latency have nothing to draw. The kind cards, In flight, Retry
          backoff and the job list come from the platform&apos;s own tables and
          are unaffected.
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Section
        title="Throughput"
        hint={`jobs completed per 15 min · last ${WINDOW}`}
      >
        <MetricBody loading={throughput.isLoading} failed={throughput.isError}>
          <IndexThroughputTimeline points={points} />
        </MetricBody>
      </Section>
      <Section
        title="Embed latency"
        hint={`claim → settled per kind · last ${WINDOW}`}
      >
        <MetricBody
          loading={p50.isLoading || passes.isLoading}
          failed={p50.isError || passes.isError}
        >
          <IndexLatencyTrack rows={latency} />
        </MetricBody>
      </Section>
    </div>
  );
}

// MetricBody holds a panel's chart back while its query is loading, and says
// so when the metrics backend answered with an error, rather than drawing the
// chart's "nothing in this window" over a question that was never answered.
function MetricBody({
  loading,
  failed,
  children,
}: {
  loading: boolean;
  failed: boolean;
  children: React.ReactNode;
}) {
  if (loading)
    return (
      <p className="py-6 text-center text-sm text-muted-foreground">Loading…</p>
    );
  if (failed) {
    return (
      <p className="py-6 text-center text-sm text-destructive">
        Could not load from the metrics backend.
      </p>
    );
  }
  return <>{children}</>;
}
