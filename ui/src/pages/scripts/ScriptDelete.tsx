import { useState } from "react";
import { useDeleteScript } from "@/api/portal/hooks/scriptDelete";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { scheduleLine } from "./cadence";

// ScriptDelete removes a script from the page a person manages it on (#1575).
// It was the one verb in a script's life the portal could not do: an owner
// created, edited, documented, scheduled, ran, dry-ran, reset and handed over
// their automations here, and then had to open an agent session to get rid of
// one.
//
// It is the owner's control and an administrator's, which is the rule the
// tool's delete already applies and the rule the route applies. It sits beside
// the owner transfer for the same reason those two are one file on the server:
// they are the two acts about a script's existence rather than its contents.
//
// The confirmation names what goes rather than asking whether the reader is
// sure. A script's run history is its refresh history, and somebody removing a
// report that has been running for months is removing that record too — the
// list is the difference between deciding that and discovering it. It names
// what stays for the opposite reason: "delete the script" reads to a lot of
// people as "delete the reports it wrote", and that is not what happens.

interface Props {
  scriptId: string;
  contract: ScriptContract;
  /** Where the reader goes once the script is gone. This page is addressed by
   * the script it is showing, so there is nothing here to return to. */
  onDeleted: () => void;
}

export function ScriptDelete({ scriptId, contract, onDeleted }: Props) {
  const remove = useDeleteScript(scriptId);
  const [confirming, setConfirming] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const name = contract.display_name || contract.name;

  const submit = async () => {
    setFailure(null);
    try {
      await remove.mutateAsync();
      setConfirming(false);
      onDeleted();
    } catch (e: unknown) {
      setFailure(e instanceof Error ? e.message : "The script could not be deleted");
    }
  };

  return (
    <SectionCard title="Delete">
      <div className="space-y-4">
        <p className="text-sm text-muted-foreground">
          Removing this script takes everything that belongs to it: its saved versions, its
          schedule, its run history, and the state it carries between runs. The files it
          wrote are not part of that and stay where they are.
        </p>
        <Button
          size="sm"
          variant="outline"
          disabled={remove.isPending}
          onClick={() => {
            setFailure(null);
            setConfirming(true);
          }}
        >
          Delete script
        </Button>
        <ConfirmDialog
          open={confirming}
          onOpenChange={(open) => {
            setConfirming(open);
            if (!open) setFailure(null);
          }}
          title={`Delete ${name}?`}
          description={<WhatGoes contract={contract} />}
          confirmLabel="Delete script"
          destructive
          loading={remove.isPending}
          error={failure}
          onConfirm={submit}
        />
      </div>
    </SectionCard>
  );
}

// WhatGoes is the list the confirmation is for. It states the cadence in words
// where there is one, as every other surface states a cadence, so the person
// reading it recognizes the schedule they are about to stop.
//
// The schedule and the carried state are named only where the script has them:
// a list that told somebody they were removing a schedule that never existed
// would be teaching them not to read the next one. The account the delete
// answers with follows the same rule (script.DeleteMessage, #1593), over what
// the removal's own transaction found rather than over the contract.
//
// The run history is the exception, and it is the contract's limit rather than
// a choice: the contract carries the last SUCCESSFUL run, so a script whose
// every run failed would have this line withheld from it -- which is the case
// where the history is worth the most.
//
// Every line is phrasing content. The dialog renders this inside its
// description, which is a paragraph, so a list element here would be invalid
// markup nested in one.
function WhatGoes({ contract }: { contract: ScriptContract }) {
  const schedule = stoppedSchedule(contract.schedule);
  return (
    <span className="block space-y-2">
      <span className="block">This cannot be undone. Deleting the script also removes:</span>
      <span className="block">
        Every saved version of its code, including v{contract.version}, the version a run
        executes.
      </span>
      {schedule && (
        <span className="block">Its schedule, {schedule}, so nothing fires it again.</span>
      )}
      <span className="block">
        Its whole run history, which is the record of how this automation has been going.
      </span>
      {keepsState(contract.state) && (
        <span className="block">The state it carries from one run to the next.</span>
      )}
      <span className="block">
        The assets and resources it wrote stay where they are, and they go on recording that
        this script wrote them.
      </span>
    </span>
  );
}

// stoppedSchedule is the cadence this delete stops, in words, or null for a
// script nothing fires. A paused one is named as paused: it is a schedule that
// resumes on the fire it was parked on, so removing it is removing something.
function stoppedSchedule(schedule: ScriptContract["schedule"]): string | null {
  if (!schedule) return null;
  const words = scheduleLine(schedule.cron_spec, schedule.timezone);
  return schedule.enabled ? words : `${words} (paused)`;
}

// keepsState reports whether the platform is holding an object for this
// script, which is the only state a delete removes. What the source DOES with
// state is not it: a script whose code calls save_state but has never run has
// nothing stored, and listing it here would warn somebody about a loss that
// does not happen -- the defect this list is written to avoid.
function keepsState(state: ScriptContract["state"]): boolean {
  if (!state) return false;
  return state.revision > 0;
}
