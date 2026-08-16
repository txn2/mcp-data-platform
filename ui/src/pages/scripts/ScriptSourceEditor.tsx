import { useState } from "react";
import { useSaveScriptSource } from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { SourceEditor } from "@/components/SourceEditor";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { CT } from "@/lib/contentType";

// ScriptSourceEditor is the code, editable by the person who owns it (#1307).
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

interface Props {
  scriptId: string;
  contract: ScriptContract;
  /** The live script's code, served to its owner with the contract. */
  source: string;
}

export function ScriptSourceEditor({ scriptId, contract, source }: Props) {
  const save = useSaveScriptSource(scriptId);
  // draft is the unsaved edit. Null means "no edit in progress", which is what
  // keeps a background refetch of the contract from discarding typing.
  const [draft, setDraft] = useState<string | null>(null);
  // submitted is the text the last save sent. It is what makes an edit queued
  // for review stay on screen: the LIVE source is still the approved version's,
  // so resetting to it would wipe the change the owner just made and leave them
  // looking at the code they were replacing.
  const [submitted, setSubmitted] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const current = draft ?? source;
  const changed = current !== (submitted ?? source);

  const submit = () => {
    setOutcome(null);
    setFailure(null);
    const sent = current;
    save.mutate(sent, {
      onSuccess: (res) => {
        setOutcome(res.message);
        setSubmitted(sent);
        // An applied edit IS the live source now, so the draft is dropped and
        // the editor follows the record. A queued draft is not, so it stays.
        if (res.applied) setDraft(null);
      },
      onError: (e: unknown) =>
        setFailure(e instanceof Error ? e.message : "The source could not be saved"),
    });
  };

  return (
    <SectionCard
      title="Source"
      action={
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="ghost"
            disabled={current === source || save.isPending}
            onClick={() => {
              setDraft(null);
              setSubmitted(null);
              setFailure(null);
              setOutcome(null);
            }}
          >
            Revert
          </Button>
          <Button size="sm" disabled={!changed || save.isPending} onClick={submit}>
            Save
          </Button>
        </div>
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

        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        {outcome && !changed && (
          <Alert>
            <AlertDescription>{outcome}</AlertDescription>
          </Alert>
        )}
      </div>
    </SectionCard>
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
        it becomes what executes.
      </p>
    );
  }
  return (
    <p className="text-xs text-muted-foreground">
      Nothing is approved for this script, so saving changes it directly. It still executes
      nothing unattended until an administrator approves a version.
    </p>
  );
}
