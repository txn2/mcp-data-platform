import { AlertTriangle, Loader2, Wrench } from "lucide-react";
import { ModalShell } from "@/components/ModalShell";
import { Button } from "@/components/ui/button";

// RefusalDialog is what a refused registration says, and the way out of it.
//
// It is a dialog rather than a notice under the form because of where the form
// lives: the viewer's details column is 320px wide (ViewerLayout, `lg:w-80`),
// and a CSV refusal is several sentences that name rows and columns. Wrapped
// into that column it was small red type below the fold -- the most important
// thing on the page at that moment rendered as the least readable thing on it
// (#1780). The reason gets a measure to be read at, and the action that
// resolves it is a full-size button in a footer rather than a control
// pretending to be a paragraph.
//
// Every refusal reads the same way here. One with a next step carries it; one
// without -- a name already taken, a connection that cannot hold a table, a
// platform failure -- carries only a dismiss, so a reader meets one shape
// rather than two.
export function RefusalDialog({
  reason,
  repairable,
  pending,
  follow,
  onRepair,
  onClose,
}: {
  reason: string;
  repairable: boolean;
  pending: boolean;
  /** follow decides whether the correction keeps happening; see below. */
  follow: boolean;
  onRepair: () => void;
  onClose: () => void;
}) {
  return (
    <ModalShell
      onClose={onClose}
      label="Registration refused"
      busy={pending}
      width="max-w-xl"
      bodyClass="space-y-3 p-4"
      header={
        <div className="flex items-start gap-2 border-b p-4">
          <AlertTriangle className="mt-0.5 size-5 shrink-0 text-destructive" />
          <h2 className="text-lg font-semibold">
            This file was not registered
          </h2>
        </div>
      }
      footer={
        <div className="flex flex-wrap justify-end gap-2 border-t p-4">
          <Button type="button" variant="outline" onClick={onClose}>
            {repairable ? "Cancel" : "Close"}
          </Button>
          {repairable && (
            <Button
              type="button"
              onClick={onRepair}
              disabled={pending}
              data-testid="table-repair-button"
            >
              {pending ? <Loader2 className="animate-spin" /> : <Wrench />}
              Save a corrected copy and register that
            </Button>
          )}
        </div>
      }
    >
      <p data-testid="table-register-error" className="text-sm leading-relaxed">
        {reason}
      </p>
      {repairable && (
        <p className="text-sm text-muted-foreground">
          The file you uploaded is kept as the version before it, so the
          correction can be undone from the version history.
          {/*
            The correction keeps happening only for a table that follows its
            file: a pinned one is never moved onto a new version, so it never
            meets one to correct (#1577). The offer says which of the two this
            registration will be, because the box behind it is the person's to
            untick and the promise would otherwise be one the platform does not
            keep.
          */}
          {follow
            ? " The table keeps correcting: a later version of the file with the same problem is" +
              " saved corrected too, and the table moves onto it."
            : " This corrects the file once. The table is pinned to the corrected version, so a" +
              " later version of the file is left alone; tick Follow the file to keep correcting it."}
        </p>
      )}
    </ModalShell>
  );
}
