import { RefreshCw } from "lucide-react";
import { type IndexKindSummary, type IndexCoverage } from "@/api/admin/indexjobs";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { STATUS_COLORS, relTime } from "./helpers";
import { VerdictBadge } from "./badges";

// coverageEmpty reports whether a kind genuinely has nothing to index: a
// known-ratio kind whose expected and indexed are both zero, or an indexed-only
// kind with nothing indexed. Such a kind must render one coherent "nothing to
// index" state rather than three independent defaults that contradict each other
// ("not yet indexed" vs "fully indexed" vs "Up to date").
function coverageEmpty(cov?: IndexCoverage): boolean {
  if (!cov) return false;
  if (cov.expected_known) return cov.expected === 0 && cov.indexed === 0;
  return cov.indexed === 0;
}

// coverageLine renders the vector-coverage family (how much is indexed),
// labelled "Vectors" so it never reads as a job count. expected_known
// distinguishes a real ratio from a continuously-syncing kind.
function CoverageLine({ summary }: { summary: IndexKindSummary }) {
  const cov = summary.coverage;
  if (!cov) {
    return <span className="text-xs text-muted-foreground">Vectors: coverage n/a</span>;
  }
  if (coverageEmpty(cov)) {
    // Nothing to index. One coherent line; syncedText suppresses its footer so
    // the card never pairs "nothing to index" with "fully indexed".
    return <span className="text-xs text-muted-foreground">Vectors: nothing to index</span>;
  }
  if (!cov.expected_known) {
    // No fixed denominator (e.g. tools, sized by the live registry). Once
    // anything is indexed it is in sync, so render a full bar to match the
    // ratio-known kinds visually (the empty case is handled above).
    return (
      <div className="space-y-1">
        <div className="flex items-center justify-between text-xs">
          <span className="tabular-nums">
            <span className="text-muted-foreground">Vectors: </span>
            {cov.indexed.toLocaleString()} indexed
          </span>
          <span className="text-emerald-500">in sync</span>
        </div>
        <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <div
            className="h-full rounded-full"
            style={{ width: "100%", backgroundColor: STATUS_COLORS.succeeded }}
          />
        </div>
      </div>
    );
  }
  // expected_known with expected > 0 (the empty case is handled above).
  const pct = cov.expected > 0 ? Math.round((cov.indexed / cov.expected) * 100) : 100;
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-xs">
        <span className="tabular-nums">
          <span className="text-muted-foreground">Vectors: </span>
          {cov.indexed.toLocaleString()} / {cov.expected.toLocaleString()} indexed
        </span>
        <span className={pct >= 100 ? "text-emerald-500" : "text-muted-foreground"}>{pct}%</span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full"
          style={{
            width: `${Math.min(100, pct)}%`,
            backgroundColor: pct >= 100 ? STATUS_COLORS.succeeded : STATUS_COLORS.running,
          }}
        />
      </div>
    </div>
  );
}

// nowText describes what is running for the kind, derived from the
// job-state counts. Distinct from the verdict so the card answers both
// "is it healthy?" and "what is it doing right now?".
function nowText(summary: IndexKindSummary): string {
  if (summary.running > 0) {
    return `embedding ${summary.running} unit${summary.running === 1 ? "" : "s"}`;
  }
  if (summary.pending > 0) {
    return `${summary.pending} queued`;
  }
  return "idle";
}

// syncedText is the recency line under the coverage bar. When the kind
// has job history it reads "last indexed <relative>"; a kind whose
// vectors were seeded outside the queue (no history) simply reads
// "fully indexed" rather than "never", since there is no job timestamp
// to report and the verdict already says it is up to date.
function syncedText(summary: IndexKindSummary): string {
  if (!summary.last_activity) {
    // A kind with nothing to index has no "fully indexed" story to tell; saying
    // so would contradict the "nothing to index" coverage line.
    return coverageEmpty(summary.coverage) ? "" : "fully indexed";
  }
  return `last indexed ${relTime(summary.last_activity)}`;
}

export function KindCard({
  summary,
  onReindex,
  reindexing,
}: {
  summary: IndexKindSummary;
  onReindex: (kind: string) => void;
  reindexing: boolean;
}) {
  // The per-state breakdown is only meaningful when something is in
  // flight or needs attention. For a kind that is simply up to date it
  // is all zeros (or, confusingly, a stale "N succeeded"), so it is
  // hidden: an up-to-date card is just the verdict, the coverage bar,
  // and recency. It reappears when there is real work or a failure.
  const showStates =
    summary.running > 0 || summary.pending > 0 || summary.unresolved_failures > 0;
  return (
    <Card className="gap-3 p-4">
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 flex-col items-start gap-1">
          <span className="truncate font-mono text-sm font-medium">{summary.kind}</span>
          <VerdictBadge verdict={summary.verdict} />
        </div>
        <Button
          type="button"
          variant="outline"
          size="xs"
          onClick={() => onReindex(summary.kind)}
          disabled={reindexing}
          title="Re-index every out-of-sync unit of this kind"
          className="text-muted-foreground"
        >
          <RefreshCw className={reindexing ? "animate-spin" : undefined} /> Re-index
        </Button>
      </div>

      <CoverageLine summary={summary} />

      <div className="flex items-center justify-between text-[11px] text-muted-foreground">
        <span>{syncedText(summary)}</span>
        <span>
          now: <span className="text-foreground">{nowText(summary)}</span>
        </span>
      </div>

      {/* Job-state family, shown only when there is active work or an
          open failure; labelled so "succeeded" reads as "units whose
          last run succeeded", not a job count. */}
      {showStates && (
        <div className="border-t pt-2">
          <div className="mb-1 text-[10px] uppercase tracking-wide text-muted-foreground">
            Units by last run
          </div>
          <div className="grid grid-cols-4 gap-1 text-center text-xs">
            {(["pending", "running", "succeeded", "failed"] as const).map((s) => (
              <div key={s}>
                <div className="font-semibold tabular-nums" style={{ color: STATUS_COLORS[s] }}>
                  {summary[s].toLocaleString()}
                </div>
                <div className="text-[10px] text-muted-foreground">{s}</div>
              </div>
            ))}
          </div>
          {summary.unresolved_failures > 0 && (
            <div className="mt-1 text-[10px] text-destructive">
              {summary.unresolved_failures} unit{summary.unresolved_failures === 1 ? "" : "s"} need
              attention
            </div>
          )}
        </div>
      )}
    </Card>
  );
}
