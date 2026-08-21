import { useState } from "react";
import { useScriptListing } from "@/api/portal/hooks/scripts";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import {
  isBackendUnconfigured,
  useObservabilityQuery,
  useObservabilityQueryRange,
} from "@/api/observability/hooks";
import { TimeseriesChart, type TimeseriesSeries } from "@/components/charts/TimeseriesChart";
import { SectionCard } from "@/components/patterns/SectionCard";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { formatDuration } from "@/lib/formatDuration";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
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
import { ScriptMetricRows } from "./ScriptMetricRows";
import { ScriptRunsList } from "./ScriptRunsList";

// ScriptRunsTab is what the platform has been running unattended (#1307).
//
// It answers a different question from the per-script history an owner reads:
// not "how is my report going" but "how are the scripts going", which is
// the question an operator has and previously could only answer by querying the
// run table.
//
// The charts come from Prometheus and the table comes from the run rows, and
// that split is deliberate. The metrics survive run retention, aggregate across
// replicas, and answer rates and percentiles; the rows are the exact recent
// history with the reason each failure failed. Neither one can do the other's
// job.
//
// Every panel that names a script leads somewhere (#1407): to the script, and
// to the runs behind the number, which is the listing at the bottom of this
// page narrowed to that script. A metric a reader cannot follow is a metric
// they have to go looking for by hand.

/** SECTION is the section a link from this page opens under. */
const SECTION = "/admin/scripts";

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

/** Narrowed is the script the run listing has been narrowed to, null for none. */
interface Narrowed {
  id: string;
  name: string;
}

export function ScriptRunsTab({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { preset, setPreset } = useTimeRangeStore();
  // The listing is read for one reason: the metric series label a script by
  // NAME, and a link needs the id. It is the same query the Scripts tab reads,
  // so on this page it costs nothing.
  const { data: listing } = useScriptListing();
  const [narrowed, setNarrowed] = useState<Narrowed | null>(null);

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

      <RunMetrics
        scripts={listing?.data ?? []}
        onOpenScript={(id) => onNavigate(`${SECTION}/${id}`)}
        onShowRuns={(id, name) => setNarrowed({ id, name })}
      />

      <ScriptRunsList
        audience="admin"
        basePath={SECTION}
        onNavigate={onNavigate}
        scriptId={narrowed?.id}
        scriptName={narrowed?.name}
        onClearScript={narrowed ? () => setNarrowed(null) : undefined}
      />
    </div>
  );
}

// RunMetrics is everything read from Prometheus: the four counters, the rate
// over time, and the two per-script rankings. It is one component so the tab
// above stays the page's shape — the metrics, then the exact history — and so
// the unconfigured answer is given once for all of them.
function RunMetrics({
  scripts,
  onOpenScript,
  onShowRuns,
}: {
  scripts: PortalScriptRow[];
  onOpenScript: (scriptId: string) => void;
  onShowRuns: (scriptId: string, name: string) => void;
}) {
  const { preset, getStartTime, getEndTime } = useTimeRangeStore();
  const start = Math.floor(new Date(getStartTime()).getTime() / 1000);
  const end = Math.floor(new Date(getEndTime()).getTime() / 1000);

  const rate = useObservabilityQueryRange(
    runRateByStatus(stepFor(preset)),
    start,
    end,
    resolutionFor(preset),
  );
  const total = useObservabilityQuery(runTotalOverWindow(preset));
  const failed = useObservabilityQuery(failuresOverWindow(preset));
  const p95 = useObservabilityQuery(runDurationP95(preset));
  const inFlight = useObservabilityQuery(runsInFlight());
  const byScript = useObservabilityQuery(runsByScript(preset));
  const missed = useObservabilityQuery(missedFiresByScript(preset));

  // One unconfigured answer is the same as all of them: the proxy is either
  // wired to a Prometheus or it is not, and saying so once is the honest
  // report. The run table below does not depend on it.
  if (isBackendUnconfigured(rate.error)) {
    return (
      <Alert>
        <AlertDescription>
          This deployment has no metrics backend configured, so the charts below have
          nothing to draw. The run history underneath comes from the platform's own
          records and is unaffected.
        </AlertDescription>
      </Alert>
    );
  }

  return (
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
          <ScriptMetricRows
            data={vectorToBreakdown(byScript.data, "script")}
            isLoading={byScript.isLoading}
            scripts={scripts}
            unit="runs"
            onOpenScript={onOpenScript}
            onShowRuns={onShowRuns}
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
          <ScriptMetricRows
            data={vectorToBreakdown(missed.data, "script")}
            isLoading={missed.isLoading}
            scripts={scripts}
            unit="missed"
            onOpenScript={onOpenScript}
            onShowRuns={onShowRuns}
          />
        </SectionCard>
      </div>
    </>
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
