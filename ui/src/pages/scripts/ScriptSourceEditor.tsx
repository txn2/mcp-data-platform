import { useState } from "react";
import {
  useDryRunScript,
  useRunScript,
  useSaveScriptSource,
  useScriptConnections,
  useValidateScriptSource,
} from "@/api/portal/hooks/scripts";
import type {
  ScriptConnectionChoice,
  ScriptContract,
  ScriptDryRun,
  ScriptParam,
  ScriptValidation,
} from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { SourceEditor } from "@/components/SourceEditor";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { CT } from "@/lib/contentType";
import { DryRunReport, ValidationReport } from "./ScriptDraftChecks";
import {
  boundParams,
  declaresConnection,
  missingRequired,
  ScriptParameterForm,
  type Values,
} from "./ScriptParameterForm";
import { ScriptVersionHistory } from "./ScriptVersionHistory";

// ScriptSourceEditor is the code, editable by the person who owns it (#1307),
// checkable by them before saving the version that runs (#1364), and runnable
// from the same place (#1406).
//
// Until this existed, changing a script meant asking an agent to do it — an odd
// thing to require of the owner of a script who is looking straight at the
// code. It is the same editor the portal uses for every other source document
// (components/SourceEditor over lib/codemirror), told the content is Python:
// Starlark is a Python dialect, so the highlighting is the language's own.
//
// Run and Dry run sit side by side because they are the same question asked of
// two texts: Run executes the saved version, a dry run executes what is on
// screen. Running used to be its own section at the top of the page, which put
// the button somewhere other than the code it executes; somebody debugging a
// script reads the code, runs it, and reads the output, and all three are here.
//
// Neither check introduces authority: validate executes nothing at all, and a
// dry run is the author's own session, reaching exactly what they reach and
// persisting nothing. What they change is that a saved version has been parsed,
// what it reaches is known to its author, and somebody has run it.

interface Props {
  scriptId: string;
  contract: ScriptContract;
  /** The live script's code, served to its owner with the contract. */
  source: string;
  /** The parameter contract a run and a dry run bind against, read beside the
   * source. */
  draftParams: ScriptParam[];
}

export function ScriptSourceEditor({ scriptId, contract, source, draftParams }: Props) {
  const save = useSaveScriptSource(scriptId);
  const validate = useValidateScriptSource(scriptId);
  const dryRun = useDryRunScript(scriptId);
  const run = useRunScript(scriptId);
  // draft is the unsaved edit. Null means "no edit in progress", which is what
  // keeps a background refetch of the contract from discarding typing.
  const [draft, setDraft] = useState<string | null>(null);
  // submitted is the text the last save sent, so the editor can tell a real
  // change from a re-save of identical text before the contract refetches.
  const [submitted, setSubmitted] = useState<string | null>(null);
  // results holds what the last action said, as one value. Each action replaces
  // the whole of it, because a validate report next to a dry run of different
  // source — or a save outcome from two edits ago — is worse than no report:
  // the author cannot tell which text any of it describes.
  const [results, setResults] = useState<Results>(NOTHING_YET);
  const [values, setValues] = useState<Values>({});

  const params = draftParams;
  // A dry run executes as the author, so the connections it may name are the
  // ones the author's persona reaches (#1361).
  const { data: connections } = useScriptConnections(scriptId, declaresConnection(params));

  const current = draft ?? source;
  const changed = current !== (submitted ?? source);
  const busy = save.isPending || validate.isPending || dryRun.isPending || run.isPending;
  const unbound = missingRequired(params, values);
  const fail = (fallback: string) => (e: unknown) =>
    setResults({ ...NOTHING_YET, failure: e instanceof Error ? e.message : fallback });

  const submit = () => {
    setResults(NOTHING_YET);
    const sent = current;
    save.mutate(sent, {
      onSuccess: (res) => {
        setResults({ ...NOTHING_YET, outcome: res.message });
        setSubmitted(sent);
        // The applied edit IS the live source now, so the draft is dropped and
        // the editor follows the record.
        if (res.applied) setDraft(null);
      },
      onError: fail("The source could not be saved"),
    });
  };

  const check = () => {
    setResults(NOTHING_YET);
    validate.mutate(current, {
      onSuccess: (report) => setResults({ ...NOTHING_YET, report }),
      onError: fail("The source could not be checked"),
    });
  };

  const execute = () => {
    setResults(NOTHING_YET);
    dryRun.mutate(
      { source: current, params: boundParams(params, values) },
      {
        onSuccess: (ran) => setResults({ ...NOTHING_YET, ran }),
        onError: fail("The dry run could not be started"),
      },
    );
  };

  const queue = () => {
    setResults(NOTHING_YET);
    run.mutate(boundParams(params, values), {
      onSuccess: (res) => setResults({ ...NOTHING_YET, queued: res.message }),
      onError: fail("The run could not be queued"),
    });
  };

  const revert = () => {
    setDraft(null);
    setSubmitted(null);
    setResults(NOTHING_YET);
  };

  return (
    <SectionCard
      title="Source"
      action={
        <EditorActions
          busy={busy}
          reverting={current === source}
          changed={changed}
          unbound={unbound}
          validating={validate.isPending}
          running={dryRun.isPending}
          queueing={run.isPending}
          // A script the run gate would refuse gets no Run control at all: the
          // refusal is the gate's own, stated once at the top of the page, and
          // a button that cannot work is worse than its absence.
          runnable={!contract.refusal}
          onRevert={revert}
          onValidate={check}
          onDryRun={execute}
          onRun={queue}
          onSave={submit}
        />
      }
    >
      <div className="space-y-3">
        <SaveNotice runnable={!contract.refusal} version={contract.version} changed={changed} />

        <SourceEditor
          content={current}
          contentType={CT.python}
          fileName={`${contract.name}.star`}
          onChange={(value) => setDraft(value)}
        />

        <RunParams
          params={params}
          values={values}
          disabled={busy}
          connections={connections?.data}
          onChange={(name, value) => setValues({ ...values, [name]: value })}
        />

        <EditorResults results={results} changed={changed} contract={contract} />

        <ScriptVersionHistory scriptId={scriptId} contract={contract} />
      </div>
    </SectionCard>
  );
}

// Results is what the last action said. All five are cleared together, so the
// panel below the editor always describes one action on one text.
interface Results {
  outcome: string | null;
  failure: string | null;
  queued: string | null;
  report: ScriptValidation | null;
  ran: ScriptDryRun | null;
}

const NOTHING_YET: Results = {
  outcome: null,
  failure: null,
  queued: null,
  report: null,
  ran: null,
};

// EditorActions is what an author does with an edit, in the order they do it:
// undo it, check it, rehearse it, run the version in force, and send it.
function EditorActions({
  busy,
  reverting,
  changed,
  unbound,
  validating,
  running,
  queueing,
  runnable,
  onRevert,
  onValidate,
  onDryRun,
  onRun,
  onSave,
}: {
  busy: boolean;
  reverting: boolean;
  changed: boolean;
  /** The required parameters still unbound, which is what makes a run
   * unavailable and what the line under the buttons names. */
  unbound: string[];
  validating: boolean;
  running: boolean;
  queueing: boolean;
  runnable: boolean;
  onRevert: () => void;
  onValidate: () => void;
  onDryRun: () => void;
  onRun: () => void;
  onSave: () => void;
}) {
  return (
    // Wrapping, because five buttons do not fit a narrow card header. Without
    // it the header's min-content is the sum of all five and the page scrolls
    // sideways to reach Save — the same defect this section's run history
    // below was fixed for.
    <div className="flex flex-col items-end gap-1">
      <div className="flex flex-wrap items-center justify-end gap-2">
        <Button size="sm" variant="ghost" disabled={reverting || busy} onClick={onRevert}>
          Revert
        </Button>
        <Button size="sm" variant="outline" disabled={busy} onClick={onValidate}>
          {validating ? "Checking..." : "Validate"}
        </Button>
        <Button size="sm" variant="outline" disabled={busy || unbound.length > 0} onClick={onDryRun}>
          {running ? "Running..." : "Dry run"}
        </Button>
        {runnable && (
          <Button size="sm" variant="outline" disabled={busy || unbound.length > 0} onClick={onRun}>
            {queueing ? "Queueing..." : "Run"}
          </Button>
        )}
        <Button size="sm" disabled={!changed || busy} onClick={onSave}>
          Save
        </Button>
      </div>
      <UnboundNotice unbound={unbound} />
    </div>
  );
}

// UnboundNotice is why a control is unavailable, rendered beside the control: a
// disabled button with its reason a screen-height away, below the editor, is a
// dead end.
function UnboundNotice({ unbound }: { unbound: string[] }) {
  if (unbound.length === 0) return null;
  return (
    <p className="text-xs text-muted-foreground">
      {unbound.join(", ")} {unbound.length === 1 ? "is" : "are"} required before a run.
    </p>
  );
}

// RunParams is what an execution binds, for both of the ones this section
// offers. It is absent for a script that declares no parameters, which is most
// of them.
function RunParams({
  params,
  values,
  disabled,
  connections,
  onChange,
}: {
  params: ScriptParam[];
  values: Values;
  disabled: boolean;
  connections?: ScriptConnectionChoice[];
  onChange: (name: string, value: string) => void;
}) {
  if (params.length === 0) return null;
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        Run and Dry run both bind these values. A dry run writes nothing wherever it is
        addressed, so they affect what it computes and not what it leaves behind.
      </p>
      <ScriptParameterForm
        form="run"
        params={params}
        values={values}
        disabled={disabled}
        connections={connections}
        onChange={onChange}
      />
    </div>
  );
}

// EditorResults is what the last action said. The save outcome is withheld
// while the text has changed since: a message about code the author has already
// edited past describes something that is no longer on screen.
function EditorResults({
  results,
  changed,
  contract,
}: {
  results: Results;
  changed: boolean;
  contract: ScriptContract;
}) {
  return (
    <>
      {results.failure && (
        <Alert variant="destructive">
          <AlertDescription>{results.failure}</AlertDescription>
        </Alert>
      )}
      {results.outcome && !changed && (
        <Alert>
          <AlertDescription>{results.outcome}</AlertDescription>
        </Alert>
      )}
      {results.queued && (
        <Alert>
          <AlertDescription>{results.queued}</AlertDescription>
        </Alert>
      )}
      {results.report && <ValidationReport report={results.report} contract={contract} />}
      {results.ran && <DryRunReport result={results.ran} />}
    </>
  );
}

// SaveNotice says what each control will do before it is pressed.
//
// It names the version Run executes, and says so again when the editor holds
// an unsaved edit: the two controls sit side by side over one parameter form,
// and the difference between them is which text they execute. Somebody who
// edits, presses Run, and reads output from the old code has been told nothing
// by a page that only said "the saved version".
function SaveNotice({
  runnable,
  version,
  changed,
}: {
  runnable: boolean;
  version: number;
  changed: boolean;
}) {
  return (
    <p className="text-xs text-muted-foreground">
      Saving makes this the version that runs: run_script executes it and any schedule
      fires it, presenting the roles you hold when you save. Validate and dry run check the
      edit first — a dry run executes what is on screen, as you, and persists nothing.{" "}
      {runnable
        ? `Run executes version ${version} — the latest saved one — under the script's own identity, which is the run a schedule produces.`
        : "Nothing will execute this script, for the reason stated above, so there is no Run here until it is back in service."}
      {runnable && changed && (
        <span className="text-foreground">
          {" "}
          The edit below is not saved, so Run still executes version {version}; Dry run is
          what executes what you see.
        </span>
      )}
    </p>
  );
}
