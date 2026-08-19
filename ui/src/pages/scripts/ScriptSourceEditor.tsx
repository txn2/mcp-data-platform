import { useState } from "react";
import {
  SCRIPT_RUN_AUDIENCE,
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
// and checkable by them before anyone is asked to approve it (#1364).
//
// Until this existed, changing a script meant asking an agent to do it — an odd
// thing to require of the owner of an automation who is looking straight at the
// code. It is the same editor the portal uses for every other source document
// (components/SourceEditor over lib/codemirror), told the content is Python:
// Starlark is a Python dialect, so the highlighting is the language's own.
//
// Editing is not an approval and cannot become one. A script with an approved
// version keeps running that version, and the edit is saved as a draft for a
// reviewer; the page says which of the two happened, in those words, because
// "Saved" alone would leave an owner believing their change is live.
//
// Validate and dry-run are not approvals either, and neither introduces
// authority: validate executes nothing at all, and a dry run is the author's
// own session, reaching exactly what they reach and persisting nothing. What
// they change is that the version a reviewer receives has been parsed, its
// capability change is known to its author, and somebody has run it.

interface Props {
  scriptId: string;
  contract: ScriptContract;
  /** The live script's code, served to its owner with the contract. */
  source: string;
  /**
   * The LIVE record's parameter contract, which is what a dry run binds
   * against. It is not always the contract's: that one is the approved
   * version's, because that is what a RUN binds, and a draft is precisely the
   * code that does not match the approved version yet.
   */
  draftParams: ScriptParam[];
}

export function ScriptSourceEditor({ scriptId, contract, source, draftParams }: Props) {
  const save = useSaveScriptSource(scriptId);
  const validate = useValidateScriptSource(scriptId);
  const dryRun = useDryRunScript(scriptId);
  // draft is the unsaved edit. Null means "no edit in progress", which is what
  // keeps a background refetch of the contract from discarding typing.
  const [draft, setDraft] = useState<string | null>(null);
  // submitted is the text the last save sent. It is what makes an edit queued
  // for review stay on screen: the LIVE source is still the approved version's,
  // so resetting to it would wipe the change the owner just made and leave them
  // looking at the code they were replacing.
  const [submitted, setSubmitted] = useState<string | null>(null);
  // results holds what the last action said, as one value. Each action replaces
  // the whole of it, because a validate report next to a dry run of different
  // source — or a save outcome from two edits ago — is worse than no report:
  // the author cannot tell which text any of it describes.
  const [results, setResults] = useState<Results>(NOTHING_YET);
  const [values, setValues] = useState<Values>({});

  const params = draftParams;
  // A dry run executes as the author with no grant layer, so the connections it
  // may name are the ones the author's persona reaches — not the set an
  // approved run is confined to (#1361).
  const { data: connections } = useScriptConnections(
    scriptId,
    declaresConnection(params),
    SCRIPT_RUN_AUDIENCE.draft,
  );

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
        // An applied edit IS the live source now, so the draft is dropped and
        // the editor follows the record. A queued draft is not, so it stays.
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
        <ReviewNotice contract={contract} />

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

// ReviewNotice says what saving will do before it is done. The two outcomes are
// genuinely different — one changes what runs tonight, the other queues a
// decision for somebody else — and which one applies is a property of the
// script, not of the edit.
function ReviewNotice({ contract }: { contract: ScriptContract }) {
  if (contract.approval.approved) {
    return (
      <p className="text-xs text-muted-foreground">
        Version {contract.approval.version} is approved and keeps running. Saving a change here
        does not touch it: the edit is queued as a draft, and an administrator decides whether
        it becomes what executes. Validate and dry run check the edit first — a dry run executes
        it as you, and persists nothing.
      </p>
    );
  }
  return (
    <p className="text-xs text-muted-foreground">
      Nothing is approved for this script, so saving changes it directly. It still executes
      nothing unattended until an administrator approves a version. Validate and dry run check
      the edit first — a dry run executes it as you, and persists nothing.
    </p>
  );
}
