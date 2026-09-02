import { useState } from "react";
import { useDirectoryUsers } from "@/api/portal/hooks";
import { useScriptProduced, type ProducedItem } from "@/api/portal/hooks/producers";
import { useTransferScriptOwner } from "@/api/portal/hooks/scriptOwner";
import type { ScriptOwnerOutcome } from "@/api/portal/hooks/scriptOwner";
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
// The files the script's runs have written do not move on their own (#1588):
// an output records the owner's address when it is first written, and a
// transfer rewrites nothing about it. The confirmation states how many such
// files there are and offers to move them with the script, on by default,
// because an administrator moving a scheduled report to somebody almost always
// means the report too. Left off, the confirmation says what that leaves: files
// the new owner cannot open, share or delete, which every run goes on
// refreshing.
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
  const { data: produced } = useScriptProduced(scriptId);
  const outputs = createdOutputs(produced?.data ?? []);
  const [target, setTarget] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [moveOutputs, setMoveOutputs] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [done, setDone] = useState<ScriptOwnerOutcome | null>(null);

  const submit = () => {
    setFailure(null);
    setDone(null);
    transfer.mutate(
      {
        ownerEmail: target,
        outputs: outputs.length > 0 ? (moveOutputs ? "move" : "keep") : undefined,
      },
      {
        onSuccess: (outcome) => {
          setDone(outcome);
          setConfirming(false);
          setTarget("");
        },
        onError: (e: unknown) => {
          setFailure(e instanceof Error ? e.message : "The script could not be transferred");
          setConfirming(false);
        },
      },
    );
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
            outputs={outputs}
            moveOutputs={moveOutputs}
            onMoveOutputs={setMoveOutputs}
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
              setMoveOutputs(true);
              setConfirming(true);
            }}
          >
            Transfer ownership
          </Button>
        )}

        {done && (
          <Alert>
            <AlertDescription>
              <Outcome outcome={done} />
            </AlertDescription>
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

// createdOutputs is what a transfer is about: the live assets and collections
// the script's runs created. A file the script only modified is somebody
// else's; a resource is filed by library and has no owner address to move; a
// deleted file is gone either way. It is the same narrowing the route makes.
export function createdOutputs(items: ProducedItem[]): ProducedItem[] {
  return items.filter(
    (item) =>
      (item.target_kind === "asset" || item.target_kind === "collection") &&
      item.created &&
      !item.deleted,
  );
}

// countFiles renders "2 assets and 1 collection", naming only the kinds present.
export function countFiles(assets: number, collections: number): string {
  const parts: string[] = [];
  if (assets > 0) parts.push(assets === 1 ? "1 asset" : `${assets} assets`);
  if (collections > 0) {
    parts.push(collections === 1 ? "1 collection" : `${collections} collections`);
  }
  return parts.join(" and ");
}

// countOutputs is countFiles over a set of produced items.
function countOutputs(items: ProducedItem[]): string {
  const assets = items.filter((item) => item.target_kind === "asset").length;
  return countFiles(assets, items.length - assets);
}

// Outcome renders what the transfer did: the route's own sentence, and, when
// files were left behind that the new owner cannot reach, which ones.
function Outcome({ outcome }: { outcome: ScriptOwnerOutcome }) {
  const kept = outcome.outputs?.kept ?? [];
  return (
    <div className="space-y-2">
      <p>{outcome.message}</p>
      {kept.length > 0 && (
        <ul className="list-disc space-y-0.5 pl-5 text-xs" data-testid="script-owner-kept">
          {kept.map((file) => (
            <li key={`${file.target_kind}:${file.target_id}`}>
              {file.name || file.target_id}
              <span className="text-muted-foreground">
                {" "}
                ({file.target_kind}, {file.owner_email || "nobody"})
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
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
// losing it is the part an administrator can overlook -- and it names the
// files the script has written, because whether they go with it is the part
// nobody used to be asked (#1588).
function ConfirmRow({
  from,
  to,
  outputs,
  moveOutputs,
  onMoveOutputs,
  busy,
  onConfirm,
  onCancel,
}: {
  from?: string;
  to: string;
  outputs: ProducedItem[];
  moveOutputs: boolean;
  onMoveOutputs: (move: boolean) => void;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-sm">
        Move this script from{" "}
        <span className="font-medium">{from || "nobody"}</span> to{" "}
        <span className="font-medium">{to}</span>?{" "}
        {from ? `${from} will no longer see it.` : "It has belonged to nobody until now."}
      </p>
      {outputs.length > 0 && (
        <OutputsChoice outputs={outputs} to={to} move={moveOutputs} onChange={onMoveOutputs} />
      )}
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

// OutputsChoice is the decision about the files: how many there are, the box
// that moves them, and what each setting leaves the new owner able to do.
function OutputsChoice({
  outputs,
  to,
  move,
  onChange,
}: {
  outputs: ProducedItem[];
  to: string;
  move: boolean;
  onChange: (move: boolean) => void;
}) {
  return (
    <div className="space-y-1.5 border-t pt-3" data-testid="script-owner-outputs">
      <p className="text-sm">
        Its runs have written <span className="font-medium">{countOutputs(outputs)}</span>.
      </p>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={move}
          onChange={(e) => onChange(e.target.checked)}
          aria-label="Move the files it wrote as well"
        />
        Move them to {to} as well
      </label>
      <p className="text-xs text-muted-foreground">
        {move
          ? `${to} will own them, and each run goes on writing a new version ${to} can open.`
          : `They stay with their current owners. ${to} cannot open, share or delete them, and each run goes on writing a new version into files ${to} cannot reach.`}
      </p>
    </div>
  );
}
