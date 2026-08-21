import { useState } from "react";
import { useDirectoryUsers } from "@/api/portal/hooks";
import { useTransferScriptOwner } from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import type { DirectoryUser } from "@/api/portal/types";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useDebounced } from "@/lib/useDebounced";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

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
//
// The new owner is CHOSEN rather than typed (#1407), from the people who have
// actually signed in. An address nobody has authenticated with cannot open the
// portal, so a script handed to one is a script only administrators can see —
// the transfer would read as done and the report would stop having an owner
// who could fix it.

interface Props {
  scriptId: string;
  contract: ScriptContract;
}

// DIRECTORY_PAGE is how many people the picker reads at once. It is the store's
// own ceiling, so asking for more would report a list as complete while
// returning part of it; a directory larger than this is narrowed by search.
const DIRECTORY_PAGE = 100;

// SEARCH_DEBOUNCE_MS is how long typing settles before the directory is asked
// again, matching the other server-side searches in the portal.
const SEARCH_DEBOUNCE_MS = 250;

export function ScriptOwnerTransfer({ scriptId, contract }: Props) {
  const transfer = useTransferScriptOwner(scriptId);
  const [target, setTarget] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const submit = () => {
    setFailure(null);
    setDone(null);
    transfer.mutate(target, {
      onSuccess: (outcome) => {
        setDone(outcome.message);
        setConfirming(false);
        setTarget("");
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
          <Label htmlFor="script-owner" className="text-xs text-muted-foreground">
            New owner
          </Label>
          <ChooseOwner owner={contract.owner_email} value={target} onChange={setTarget} />
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
            disabled={target === "" || transfer.isPending}
            onClick={() => {
              setDone(null);
              setFailure(null);
              setConfirming(true);
            }}
          >
            Transfer ownership
          </Button>
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

// ChooseOwner is the set the new owner is picked from: the people who have
// signed in, read a page at a time, with the box that narrows the directory
// when it holds more people than one page. Without that box the people past
// the cap would be unreachable while the control looked complete, which is
// what a silently truncated list always is.
function ChooseOwner({
  owner,
  value,
  onChange,
}: {
  owner?: string;
  value: string;
  onChange: (email: string) => void;
}) {
  const [typed, setTyped] = useState("");
  const search = useDebounced(typed.trim(), SEARCH_DEBOUNCE_MS);
  const { data: directory } = useDirectoryUsers(search, true, {
    confirmedOnly: true,
    limit: DIRECTORY_PAGE,
  });
  const candidates = transferrableTo(directory?.users, owner);
  const overflowing = (directory?.total ?? 0) > (directory?.users?.length ?? 0);

  return (
    <>
      {(overflowing || typed !== "") && (
        <SearchInput
          value={typed}
          onChange={(e) => {
            // The choice is cleared with the list it came from: a selection the
            // narrowed list no longer holds would leave the control reading
            // empty over an address still about to be submitted.
            setTyped(e.target.value);
            onChange("");
          }}
          placeholder="Search people by name or email..."
          aria-label="Search people"
        />
      )}
      <OwnerSelect
        candidates={candidates}
        value={value}
        narrowed={search !== ""}
        onChange={onChange}
      />
    </>
  );
}

// OwnerSelect is the choice itself: one of the people who have signed in, or
// the statement that there is nobody to choose. A directory this deployment
// cannot read answers the same way, because from here the two are the same
// situation — there is no one to hand the script to on this page.
function OwnerSelect({
  candidates,
  value,
  narrowed,
  onChange,
}: {
  candidates: DirectoryUser[];
  value: string;
  /** narrowed separates "nobody matched that" from "there is nobody". */
  narrowed: boolean;
  onChange: (email: string) => void;
}) {
  if (candidates.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {narrowed
          ? "Nobody who has signed in matches that."
          : "Nobody else has signed in yet, so there is no one to move this script to. A person appears here once they have signed in to the portal at least once."}
      </p>
    );
  }
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger id="script-owner" aria-label="New owner" className="w-full">
        <SelectValue placeholder="Choose a person" />
      </SelectTrigger>
      <SelectContent>
        {candidates.map((person) => (
          <SelectItem key={person.email} value={person.email}>
            {personLabel(person)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

// transferrableTo is who this script can be moved to: everybody who has signed
// in, except the person who already has it. Offering the current owner would
// offer a transfer that changes nothing but still writes a version.
function transferrableTo(users: DirectoryUser[] | undefined, owner?: string): DirectoryUser[] {
  const current = (owner ?? "").toLowerCase();
  return (users ?? []).filter((person) => person.email.toLowerCase() !== current);
}

// personLabel is how somebody is recognized in the list: their name where the
// directory knows one, with the address that is actually being written.
function personLabel(person: DirectoryUser): string {
  const name = [person.first_name, person.last_name].filter(Boolean).join(" ");
  return name ? `${name} — ${person.email}` : person.email;
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
