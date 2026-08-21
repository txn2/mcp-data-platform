import { useState } from "react";
import { useTransferScriptOwner } from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { UserPicker } from "@/components/UserPicker";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

// ScriptOwnerTransfer moves a script to another person (#1404). It is an
// administrator's control and appears for nobody else: ownership is the whole
// of what a script is, so handing it over hands over what its owner sees,
// edits, runs, and schedules, all at once.
//
// The consequence worth stating on the page is the one an administrator most
// often comes here for: a run presents the roles captured on the version it
// executes, and the transfer captures the roles of the administrator making
// it, so moving a script to an administrator is how it comes to run with an
// administrator's reach.
//
// The move is confirmed before it is made, because it is not undoable from the
// new owner's side: only an administrator can move it back.

interface Props {
  scriptId: string;
  contract: ScriptContract;
}

export function ScriptOwnerTransfer({ scriptId, contract }: Props) {
  const transfer = useTransferScriptOwner(scriptId);
  const [email, setEmail] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const target = email.trim();
  const unchanged = target.toLowerCase() === (contract.owner_email ?? "").toLowerCase();

  const submit = () => {
    setFailure(null);
    setDone(null);
    transfer.mutate(target, {
      onSuccess: (outcome) => {
        setDone(outcome.message);
        setConfirming(false);
        setEmail("");
      },
      onError: (e: unknown) => {
        setFailure(e instanceof Error ? e.message : "The script could not be transferred");
        setConfirming(false);
      },
    });
  };

  return (
    <SectionCard title="Owner">
      <div className="space-y-4">
        <p className="text-sm text-muted-foreground">
          This script belongs to{" "}
          <span className="font-medium text-foreground">
            {contract.owner_email || "nobody"}
          </span>
          . Its owner is the only person who sees it, edits it, runs it, and schedules it.
        </p>

        <div className="space-y-2">
          <UserPicker value={email} onChange={setEmail} placeholder="New owner's email" />
          <p className="text-xs text-muted-foreground">
            From the transfer on, a run presents the access you hold now, not the access
            the previous owner held. Moving a script to yourself is how it comes to run
            with an administrator's reach.
          </p>
        </div>

        {confirming ? (
          <ConfirmRow
            from={contract.owner_email}
            to={target}
            busy={transfer.isPending}
            onConfirm={submit}
            onCancel={() => setConfirming(false)}
          />
        ) : (
          <Button
            size="sm"
            variant="outline"
            disabled={target === "" || unchanged || transfer.isPending}
            onClick={() => {
              setDone(null);
              setFailure(null);
              setConfirming(true);
            }}
          >
            Transfer ownership
          </Button>
        )}

        {unchanged && target !== "" && (
          <p className="text-xs text-muted-foreground">
            The script already belongs to that person.
          </p>
        )}

        {done && (
          <Alert>
            <AlertDescription>{done}</AlertDescription>
          </Alert>
        )}

        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
      </div>
    </SectionCard>
  );
}

// ConfirmRow states both ends of the move before it is made. It names the
// person losing the script as well as the one gaining it, because the person
// losing it is the part an administrator can overlook.
function ConfirmRow({
  from,
  to,
  busy,
  onConfirm,
  onCancel,
}: {
  from?: string;
  to: string;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="space-y-2 rounded-md border p-3">
      <p className="text-sm">
        Move this script from{" "}
        <span className="font-medium">{from || "nobody"}</span> to{" "}
        <span className="font-medium">{to}</span>?{" "}
        {from ? `${from} will no longer see it.` : "It has belonged to nobody until now."}
      </p>
      <div className="flex items-center gap-2">
        <Button size="sm" disabled={busy} onClick={onConfirm}>
          {busy ? "Transferring..." : "Transfer"}
        </Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
