import { useState } from "react";
import {
  useDryRunScript,
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

// ScriptSourceEditor is the code, editable by the person who owns it (#1307),
// and checkable by them before saving the version that runs (#1364).
//
// Until this existed, changing a script meant asking an agent to do it — an odd
// thing to require of the owner of a script who is looking straight at the
// code. It is the same editor the portal uses for every other source document
// (components/SourceEditor over lib/codemirror), told the content is Python:
// Starlark is a Python dialect, so the highlighting is the language's own.
//
// Validate and dry-run introduce no authority: validate executes nothing at
// all, and a dry run is the author's own session, reaching exactly what they
// reach and persisting nothing. What they change is that a saved version has
// been parsed, what it reaches is known to its author, and somebody has run
// it.

interface Props {
  scriptId: string;
  contract: ScriptContract;
  /** The live script's code, served to its owner with the contract. */
  source: string;
  /** The parameter contract a dry run binds against, read beside the source. */
  draftParams: ScriptParam[];
}

export function ScriptSourceEditor({ scriptId, contract, source, draftParams }: Props) {
  const save = useSaveScriptSource(scriptId);
  const validate = useValidateScriptSource(scriptId);
  const dryRun = useDryRunScript(scriptId);
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
  const busy = save.isPending || validate.isPending || dryRun.isPending;
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
          blocked={unbound.length > 0}
          validating={validate.isPending}
          running={dryRun.isPending}
          onRevert={revert}
          onValidate={check}
          onDryRun={execute}
          onSave={submit}
        />
      }
    >
      <div className="space-y-3">
        <SaveNotice />

        <SourceEditor
          content={current}
          contentType={CT.python}
          fileName={`${contract.name}.star`}
          onChange={(value) => setDraft(value)}
        />

        <DraftParams
          params={params}
          values={values}
          disabled={busy}
          connections={connections?.data}
          unbound={unbound}
          onChange={(name, value) => setValues({ ...values, [name]: value })}
        />

        <EditorResults results={results} changed={changed} contract={contract} />
      </div>
    </SectionCard>
  );
}

// Results is what the last action said. All four are cleared together, so the
// panel below the editor always describes one action on one text.
interface Results {
  outcome: string | null;
  failure: string | null;
  report: ScriptValidation | null;
  ran: ScriptDryRun | null;
}

const NOTHING_YET: Results = { outcome: null, failure: null, report: null, ran: null };

// EditorActions is the four things an author does with an edit, in the order
// they do them: undo it, check it, run it, and send it.
function EditorActions({
  busy,
  reverting,
  changed,
  blocked,
  validating,
  running,
  onRevert,
  onValidate,
  onDryRun,
  onSave,
}: {
  busy: boolean;
  reverting: boolean;
  changed: boolean;
  blocked: boolean;
  validating: boolean;
  running: boolean;
  onRevert: () => void;
  onValidate: () => void;
  onDryRun: () => void;
  onSave: () => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <Button size="sm" variant="ghost" disabled={reverting || busy} onClick={onRevert}>
        Revert
      </Button>
      <Button size="sm" variant="outline" disabled={busy} onClick={onValidate}>
        {validating ? "Checking..." : "Validate"}
      </Button>
      <Button size="sm" variant="outline" disabled={busy || blocked} onClick={onDryRun}>
        {running ? "Running..." : "Dry run"}
      </Button>
      <Button size="sm" disabled={!changed || busy} onClick={onSave}>
        Save
      </Button>
    </div>
  );
}

// DraftParams is what a dry run binds. It is absent for a script that declares
// no parameters, which is most of them.
function DraftParams({
  params,
  values,
  disabled,
  connections,
  unbound,
  onChange,
}: {
  params: ScriptParam[];
  values: Values;
  disabled: boolean;
  connections?: ScriptConnectionChoice[];
  unbound: string[];
  onChange: (name: string, value: string) => void;
}) {
  if (params.length === 0) return null;
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        A dry run binds these values. It writes nothing wherever it is addressed, so they affect
        what it computes and not what it leaves behind.
      </p>
      <ScriptParameterForm
        form="draft"
        params={params}
        values={values}
        disabled={disabled}
        connections={connections}
        onChange={onChange}
      />
      {unbound.length > 0 && (
        <p className="text-xs text-muted-foreground">
          {unbound.join(", ")} {unbound.length === 1 ? "is" : "are"} required before a dry run.
        </p>
      )}
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
      {results.report && <ValidationReport report={results.report} contract={contract} />}
      {results.ran && <DryRunReport result={results.ran} />}
    </>
  );
}

// SaveNotice says what saving will do before it is done.
function SaveNotice() {
  return (
    <p className="text-xs text-muted-foreground">
      Saving makes this the version that runs: run_script executes it and any schedule fires
      it, presenting the roles you hold when you save. Validate and dry run check the edit
      first — a dry run executes it as you, and persists nothing.
    </p>
  );
}
