import { useAdminScriptRuns } from "@/api/admin/hooks/scripts";
import type { Script } from "@/api/admin/types";
import {
  isBackendUnconfigured,
  useObservabilityQuery,
  useObservabilityQueryRange,
} from "@/api/observability/hooks";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import { TimeseriesChart, type TimeseriesSeries } from "@/components/charts/TimeseriesChart";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatDuration } from "@/lib/formatDuration";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import { Activity } from "lucide-react";
import {
  failuresOverWindow,
  missedFiresByScript,
  resolutionFor,
  runDurationP95,
  runRateByStatus,
  runTotalOverWindow,
  runsByScript,
  runsInFlight,
  scalar,
  statusMatrixToTimeseries,
  stepFor,
  vectorToBreakdown,
} from "./runMetrics";
import { runStatusLabel, runStatusVariant, runWhen } from "./runFormat";

// ScriptRunsTab is what the platform has been running unattended (#1307).
//
// It answers a different question from the per-script history an owner reads:
// not "how is my report going" but "how are the automations going", which is
// the question an operator has and previously could only answer by querying the
// run table.
//
// The charts come from Prometheus and the table comes from the run rows, and
// that split is deliberate. The metrics survive run retention, aggregate across
// replicas, and answer rates and percentiles; the rows are the exact recent
// history with the reason each failure failed. Neither one can do the other's
// job.

const RUN_SERIES: TimeseriesSeries[] = [
  { dataKey: "success_count", name: "Succeeded", stroke: "hsl(142, 76%, 36%)" },
  { dataKey: "error_count", name: "Failed or skipped", stroke: "hsl(0, 84%, 60%)" },
];

const PRESETS: { value: TimeRangePreset; label: string; text: string }[] = [
  { value: "1h", label: "The last hour", text: "1h" },
  { value: "6h", label: "The last six hours", text: "6h" },
  { value: "24h", label: "The last day", text: "24h" },
  { value: "7d", label: "The last week", text: "7d" },
];

export function ScriptRunsTab({ scripts }: { scripts: Script[] }) {
  const { preset, setPreset, getStartTime, getEndTime } = useTimeRangeStore();
  const start = Math.floor(new Date(getStartTime()).getTime() / 1000);
  const end = Math.floor(new Date(getEndTime()).getTime() / 1000);
  const step = resolutionFor(preset);
  const rateWindow = stepFor(preset);

  const rate = useObservabilityQueryRange(runRateByStatus(rateWindow), start, end, step);
  const total = useObservabilityQuery(runTotalOverWindow(preset));
  const failed = useObservabilityQuery(failuresOverWindow(preset));
  const p95 = useObservabilityQuery(runDurationP95(preset));
  const inFlight = useObservabilityQuery(runsInFlight());
  const byScript = useObservabilityQuery(runsByScript(preset));
  const missed = useObservabilityQuery(missedFiresByScript(preset));

  // One unconfigured answer is the same as all of them: the proxy is either
  // wired to a Prometheus or it is not, and saying so once is the honest
  // report. The run table below does not depend on it.
  const metricsOff = isBackendUnconfigured(rate.error);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          Every run the platform executed, across every script.
        </p>
        <SegmentedControl
          label="Time range"
          value={preset}
          onChange={setPreset}
          options={PRESETS}
        />
      </div>

      {metricsOff ? (
        <Alert>
          <AlertDescription>
            This deployment has no metrics backend configured, so the charts below have
            nothing to draw. The run history underneath comes from the platform's own
            records and is unaffected.
          </AlertDescription>
        </Alert>
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-4">
            <StatTile
              label="Runs"
              value={countText(scalar(total.data))}
              hint={`in the last ${preset}`}
            />
            <StatTile
              label="Failed"
              value={countText(scalar(failed.data))}
              hint={failureHint(scalar(total.data), scalar(failed.data))}
            />
            <StatTile
              label="Slowest 5%"
              value={durationText(scalar(p95.data))}
              hint="95th percentile run"
            />
            <StatTile
              label="Running now"
              value={countText(scalar(inFlight.data))}
              hint="across every replica"
            />
          </div>

          <SectionCard title="Runs over time">
            <TimeseriesChart
              data={statusMatrixToTimeseries(rate.data)}
              isLoading={rate.isLoading}
              preset={preset}
              series={RUN_SERIES}
            />
          </SectionCard>

          <div className="grid gap-4 lg:grid-cols-2">
            <SectionCard title="Busiest scripts">
              <BreakdownBarChart
                data={vectorToBreakdown(byScript.data, "script")}
                isLoading={byScript.isLoading}
              />
            </SectionCard>
            <SectionCard
              title="Missed fires"
              action={
                <span className="text-xs text-muted-foreground">
                  a schedule that is not keeping its cadence
                </span>
              }
            >
              <BreakdownBarChart
                data={vectorToBreakdown(missed.data, "script")}
                isLoading={missed.isLoading}
              />
            </SectionCard>
          </div>
        </>
      )}

      <RecentRuns scripts={scripts} />
    </div>
  );
}

// StatTile is one number with what it counts and over what window. The window
// is on the tile rather than only on the control above it, because a number
// with no window is not an answer.
function StatTile({ label, value, hint }: { label: string; value: string; hint: string }) {
  return (
    <SectionCard title={label}>
      <div className="space-y-0.5">
        <div className="text-2xl font-semibold tabular-nums">{value}</div>
        <div className="text-xs text-muted-foreground">{hint}</div>
      </div>
    </SectionCard>
  );
}

// countText renders a counter. An absent answer is "—" rather than 0: nothing
// recorded and nothing happened are different statements.
function countText(n: number | undefined): string {
  if (n === undefined) return "—";
  return Math.round(n).toLocaleString();
}

function durationText(seconds: number | undefined): string {
  if (seconds === undefined || !Number.isFinite(seconds)) return "—";
  return formatDuration(seconds * 1000);
}

// failureHint states the share of runs that failed, which is the number an
// operator actually reads. It says nothing when there is nothing to divide.
function failureHint(total: number | undefined, failed: number | undefined): string {
  if (!total || failed === undefined) return "in the window";
  return `${Math.round((failed / total) * 100)}% of runs`;
}

// RecentRuns is the exact history: which script, what triggered it, how it
// ended, and why when it failed. It reads the run rows rather than the metrics,
// because a rate cannot tell an operator which run to open.
function RecentRuns({ scripts }: { scripts: Script[] }) {
  const { data, isLoading, error } = useAdminScriptRuns();
  const runs = data?.data ?? [];
  const nameOf = (id: string) => {
    const script = scripts.find((s) => s.id === id);
    return script ? script.display_name || script.name : id;
  };

  return (
    <SectionCard title="Recent runs">
      {isLoading && <p className="text-sm text-muted-foreground">Loading runs...</p>}
      {error && (
        <p className="text-sm text-muted-foreground">
          The run history could not be loaded.
        </p>
      )}
      {!isLoading && !error && runs.length === 0 && (
        <EmptyState icon={Activity}>
          Nothing has run yet. A run happens when a schedule fires or somebody asks for one,
          and only an approved version ever executes.
        </EmptyState>
      )}
      {runs.length > 0 && data && runs.length >= data.limit && (
        <p className="pb-2 text-xs text-muted-foreground">
          Showing the {data.limit} most recent runs. Older ones are kept until this
          deployment's retention window ends — a year by default
          (<span className="font-mono">scripts.run_retention_days</span>) — and the charts
          above are computed from the metrics rather than from this list, so they cover the
          whole window whatever it holds.
        </p>
      )}
      {runs.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Script</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Trigger</TableHead>
              <TableHead>When</TableHead>
              <TableHead>Duration</TableHead>
              <TableHead>Outputs</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {runs.map((run) => (
              <TableRow key={run.id}>
                <TableCell>
                  <div className="font-medium">{nameOf(run.script_id)}</div>
                  {run.error && (
                    <div className="text-xs text-red-700 dark:text-red-300">{run.error}</div>
                  )}
                </TableCell>
                <TableCell>
                  <Badge variant={runStatusVariant(run.status)}>
                    {runStatusLabel(run.status)}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs">{run.trigger}</TableCell>
                <TableCell className="text-xs">{runWhen(run)}</TableCell>
                <TableCell className="text-xs">
                  {run.duration_ms > 0 ? formatDuration(run.duration_ms) : "—"}
                </TableCell>
                <TableCell className="text-xs">{run.output_count}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </SectionCard>
  );
}
