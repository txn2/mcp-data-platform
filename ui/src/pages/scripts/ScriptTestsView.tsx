import { CheckCircle2, XCircle } from "lucide-react";
import type { ScriptVersionTests } from "@/api/admin/types";
import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
import { useValidateScriptSource } from "@/api/portal/hooks/scriptDrafts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

// ScriptTestsView is the Tests tab (#1972): the version's test_* functions,
// how each one did when the version was saved, and the share of its
// statements they reach. A save runs the tests and keeps what they found on
// the version, so this reads the version and runs nothing.

interface Props {
  scriptId: string;
  version: number;
  /** source is the saved version's source, which an owner can have tested
   * when the version kept no report. */
  source: string;
  /** owned offers to run the tests of a version that kept no report: the
   * tests replay the script's recorded runs, which are its owner's. */
  owned: boolean;
  onShowLines?: (lines: number[]) => void;
}

export function ScriptTestsView({ scriptId, version, source, owned, onShowLines }: Props) {
  const versions = usePortalScriptVersions(scriptId, true);
  const run = useValidateScriptSource(scriptId);
  if (versions.isLoading) return <p className="text-sm text-muted-foreground">Reading the tests…</p>;
  if (versions.error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>The tests of this script could not be read.</AlertDescription>
      </Alert>
    );
  }
  const stored = versions.data?.data.find((v) => v.version === version)?.tests;
  const report = stored ?? run.data?.tests;
  if (!report) return <NoReport version={version} owned={owned} run={run} onRun={() => run.mutate(source)} />;
  return <TestReport report={report} version={version} ranNow={!stored} onShowLines={onShowLines} />;
}

// NoReport is a version that kept no report: saved before reports were kept.
// Its owner can have the tests run now.
function NoReport({
  version,
  owned,
  run,
  onRun,
}: {
  version: number;
  owned: boolean;
  run: ReturnType<typeof useValidateScriptSource>;
  onRun: () => void;
}) {
  return (
    <div className="space-y-3" data-testid="script-tests-none">
      <p className="text-sm text-muted-foreground">
        Version {version} was saved before test results were kept with each version.
      </p>
      {owned && (
        <Button type="button" variant="outline" size="sm" disabled={run.isPending} onClick={onRun}>
          {run.isPending ? "Running the tests…" : "Run the tests"}
        </Button>
      )}
      {run.data && !run.data.tests && <p className="text-sm">{run.data.save_refusal ?? "The tests could not be run."}</p>}
      {run.error && <p className="text-sm text-destructive">{run.error.message}</p>}
    </div>
  );
}

function TestReport({
  report,
  version,
  ranNow,
  onShowLines,
}: {
  report: ScriptVersionTests;
  version: number;
  ranNow: boolean;
  onShowLines?: (lines: number[]) => void;
}) {
  const { coverage } = report;
  return (
    <div className="space-y-4" data-testid="script-tests">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
        <span className="text-2xl font-semibold tabular-nums" data-testid="script-tests-coverage">
          {Math.round(coverage.percent)}%
        </span>
        <span className="text-sm text-muted-foreground">
          of statements reached by the tests ({coverage.covered} of {coverage.statements})
        </span>
        <span className="text-sm">
          {report.tests.length} {report.tests.length === 1 ? "test" : "tests"}: {report.passed} passed
          {report.failed > 0 ? `, ${report.failed} failed` : ""}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">
        {ranNow ? `Run just now against version ${version}.` : `As they ran when version ${version} was saved.`}
      </p>
      {report.tests.length === 0 ? (
        <p className="text-sm text-muted-foreground">This version has no tests.</p>
      ) : (
        <ul className="divide-y rounded-md border">
          {report.tests.map((t) => (
            <li key={t.name} className="flex items-start gap-3 px-3 py-2 text-sm">
              {t.passed ? (
                <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" aria-label="passed" />
              ) : (
                <XCircle className="mt-0.5 size-4 shrink-0 text-destructive" aria-label="failed" />
              )}
              <div className="min-w-0 flex-1">
                <div className="font-mono break-words">{t.name}</div>
                {t.failure && <div className="mt-0.5 text-xs break-words text-destructive">{t.failure}</div>}
              </div>
              {t.line ? (
                onShowLines ? (
                  <Button type="button" variant="ghost" size="xs" onClick={() => onShowLines([t.line!])}>
                    line {t.line}
                  </Button>
                ) : (
                  <span className="text-xs text-muted-foreground">line {t.line}</span>
                )
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {coverage.missed_lines.length > 0 && (
        <div className="space-y-1.5 text-sm">
          <p>
            {coverage.missed_lines.length} {coverage.missed_lines.length === 1 ? "line" : "lines"} no test reaches
          </p>
          <div className="flex flex-wrap gap-1">
            {coverage.missed_lines.map((l) =>
              onShowLines ? (
                <Button key={l} type="button" variant="outline" size="xs" className="font-mono" onClick={() => onShowLines([l])}>
                  {l}
                </Button>
              ) : (
                <span key={l} className="rounded border px-1.5 font-mono text-xs">
                  {l}
                </span>
              ),
            )}
          </div>
        </div>
      )}
    </div>
  );
}
