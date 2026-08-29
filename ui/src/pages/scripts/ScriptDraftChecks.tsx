import type {
  ScriptContract,
  ScriptDryRun,
  ScriptValidation,
} from "@/api/portal/hooks/scripts";
import type { ScriptFinding } from "@/api/admin/types";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { dryRunOutputPhrase } from "./runFormat";

// The two answers an author gets before saving an edit
// (#1364): what the code would reach, and what happened when they ran it.
//
// Both are rendered here rather than in the editor so the editor stays the
// thing it is — a text box with a save button — and so the two results, which
// are read together, are laid out together.

// ValidationReport is what the edited source would reach, and what is wrong
// with it. The reach lists say what the edit touches — which host calls, which
// tools it invokes by name, which connections, where it writes — so a change in
// any of them is something the author notices here rather than in a run
// somebody else is watching.
export function ValidationReport({
  report,
  contract,
}: {
  report: ScriptValidation;
  contract: ScriptContract;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <div className="flex items-center gap-2">
        <Badge variant={report.ok ? "secondary" : "destructive"}>
          {report.ok ? "Parses" : "Does not parse"}
        </Badge>
        <span className="text-xs text-muted-foreground">
          {report.ok
            ? "Nothing was executed and nothing was saved."
            : "This cannot be saved until it parses."}
        </span>
      </div>

      <Findings findings={report.findings} />

      <ReachLists report={report} />

      {report.note && <p className="text-xs text-muted-foreground">{report.note}</p>}

      <p className="text-xs text-muted-foreground">
        This is what the EDIT reaches. Version {contract.version} keeps running until the
        edit is saved.
      </p>
    </div>
  );
}

// ReachLists is everything the source touches. The three that are always a fact
// about a script are always shown, including when they are empty; the two that
// only some scripts have are shown when there is something to say.
function ReachLists({ report }: { report: ScriptValidation }) {
  const tools = report.tools ?? [];
  const refreshes = report.refresh_targets ?? [];
  return (
    <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
      <Reached label="Capabilities" values={report.capabilities} />
      <Reached label="Connections" values={report.connections} />
      <Reached label="Output destinations" values={report.destinations} />
      {tools.length > 0 && <Reached label="Tools" values={tools} />}
      {refreshes.length > 0 && <Reached label="Refreshes" values={refreshes} />}
      {(report.reads_state || report.saves_state) && (
        <Reached label="State" values={stateUse(report)} />
      )}
    </dl>
  );
}

// stateUse names what the source does with the state it carries between runs,
// shown only when it does something: most scripts keep none.
function stateUse(report: ScriptValidation): string[] {
  const out: string[] = [];
  if (report.reads_state) out.push("reads run.state");
  if (report.saves_state) out.push("saves with platform.save_state");
  return out;
}

// Reached is one list of what the source touches, naming the empty case rather
// than rendering a blank: "no connections" is a fact about the script.
function Reached({ label, values }: { label: string; values: string[] }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="break-words font-mono text-xs">
        {values.length === 0 ? "none" : values.join(", ")}
      </dd>
    </div>
  );
}

// Findings is the validator's complaints, each with the correction. The hint
// carries most of the value: an author writing Python at a Starlark interpreter
// needs to be told what to write instead, not that the parser disagreed.
function Findings({ findings }: { findings: ScriptFinding[] }) {
  if (findings.length === 0) return null;
  return (
    <ul className="space-y-2">
      {findings.map((f, i) => (
        <li key={`${f.line ?? 0}-${i}`} className="text-xs">
          <span className="font-medium">
            {f.severity}
            {f.line ? ` (line ${f.line})` : ""}:
          </span>{" "}
          {f.message}
          {f.hint && <span className="block text-muted-foreground">{f.hint}</span>}
        </li>
      ))}
    </ul>
  );
}

// DryRunReport is what happened when the author ran the edit. A failed run is
// reported with the same fields a successful one is: the log is the whole
// reason to have run it.
export function DryRunReport({ result }: { result: ScriptDryRun }) {
  const failed = result.status === "failed";
  return (
    <div className="space-y-3 rounded-md border p-3">
      <div className="flex items-center gap-2">
        <Badge variant={failed ? "destructive" : "secondary"}>{result.status}</Badge>
        <span className="text-xs text-muted-foreground">{result.message}</span>
      </div>

      {failed && result.error && (
        <Alert variant="destructive">
          <AlertDescription>
            <pre className="overflow-x-auto whitespace-pre-wrap text-xs">{result.error}</pre>
          </AlertDescription>
        </Alert>
      )}

      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-4">
        <Metric label="Steps" value={result.metrics.steps} />
        <Metric label="Duration" value={`${result.metrics.duration_ms} ms`} />
        <Metric label="Queries" value={result.metrics.queries} />
        <Metric label="Outputs" value={result.outputs.length} />
      </dl>

      <DryRunOutputs result={result} />

      {result.state && (
        <div className="space-y-1 text-xs">
          <p className="text-muted-foreground">
            Would save this state for the next run to read (a dry run saves nothing):
          </p>
          <pre className="max-h-40 overflow-auto rounded-md bg-muted p-2 font-mono">
            {JSON.stringify(result.state, null, 2)}
          </pre>
        </div>
      )}

      {result.log && (
        <div className="space-y-1">
          <p className="text-xs text-muted-foreground">
            Log{result.log_truncated ? " (truncated at the cap)" : ""}
          </p>
          <pre className="max-h-64 overflow-auto rounded-md bg-muted p-2 text-xs">
            {result.log}
          </pre>
        </div>
      )}
    </div>
  );
}

// DryRunOutputs is the shape of what the run would have written. Every entry is
// a preview, so it names a size and a row count and no location: nothing was
// written anywhere to link to.
function DryRunOutputs({ result }: { result: ScriptDryRun }) {
  if (result.outputs.length === 0) return null;
  return (
    <ul className="space-y-1 text-xs">
      {result.outputs.map((o) => (
        <li key={`${o.name}-${o.destination ?? ""}`}>
          <span className="font-mono">{o.name}</span> would write{" "}
          {dryRunOutputPhrase(o)}.
        </li>
      ))}
    </ul>
  );
}

// Metric is one number the run cost, in the units every other run surface
// reports.
function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate">{value}</dd>
    </div>
  );
}
