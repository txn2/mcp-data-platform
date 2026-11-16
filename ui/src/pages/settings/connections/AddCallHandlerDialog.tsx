import { useState } from "react";
import { useHandlerScripts, type HandlerScriptChoice } from "@/api/portal/hooks/callHandlers";
import { CALL_HANDLER_KINDS, type CallHandlerKind } from "@/api/portal/hooks/scriptHandlers";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { CallHandlerForm, KIND_WORDS } from "@/pages/scripts/callhandlers/CallHandlerForm";
import { cn } from "@/lib/utils";

// AddCallHandlerDialog adds a policy, transform or watch to a connection
// (#2436, #2650): the kind, then who and which calls it applies to, then the
// script, chosen from the scripts whose source defines the kind's entry. What
// the picked version defines is read from its source. The script's version is
// approved as the administrator adding it unless they say otherwise.

export function AddCallHandlerDialog({ connection, onClose }: { connection: string; onClose: () => void }) {
  const [kind, setKind] = useState<CallHandlerKind>("policy");
  const [picked, setPicked] = useState<HandlerScriptChoice | null>(null);
  const choose = (k: CallHandlerKind) => {
    setKind(k);
    setPicked(null);
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Add a handler to {connection}</DialogTitle>
          <DialogDescription>
            It runs with the roles of the administrator who approves its script&apos;s version. Only administrators add,
            change or approve one.
          </DialogDescription>
        </DialogHeader>
        <fieldset className="space-y-1.5">
          <legend className="text-sm font-medium">Kind</legend>
          <div className="grid grid-cols-1 overflow-hidden rounded-md border sm:grid-cols-3">
            {CALL_HANDLER_KINDS.map((k) => (
              <label
                key={k}
                className={cn(
                  "flex cursor-pointer flex-col gap-0.5 border-b p-2.5 text-sm last:border-b-0 sm:border-r sm:border-b-0 sm:last:border-r-0",
                  kind === k && "bg-muted ring-2 ring-inset ring-ring",
                )}
              >
                <span className="flex items-center gap-1.5">
                  <input type="radio" name="add-kind" checked={kind === k} onChange={() => choose(k)} />
                  {KIND_WORDS[k].label}
                </span>
                <span className="text-xs text-muted-foreground">{KIND_WORDS[k].hint}</span>
              </label>
            ))}
          </div>
        </fieldset>
        <CallHandlerForm
          key={kind}
          scriptId={picked?.id ?? ""}
          kind={kind}
          connection={connection}
          approval={
            picked
              ? { latest: picked.version, running: picked.running_version, latestDefines: true, runningDefines: picked.running_defines }
              : undefined
          }
          scriptSlot={<ScriptPicker kind={kind} picked={picked} onPick={setPicked} onKind={choose} />}
          onDone={onClose}
        />
      </DialogContent>
    </Dialog>
  );
}

// ScriptPicker finds the scripts that define the kind's entry by name, or
// shows the one picked.
function ScriptPicker({
  kind,
  picked,
  onPick,
  onKind,
}: {
  kind: CallHandlerKind;
  picked: HandlerScriptChoice | null;
  onPick: (s: HandlerScriptChoice | null) => void;
  onKind: (k: CallHandlerKind) => void;
}) {
  const [search, setSearch] = useState("");
  const q = search.trim();
  const listing = useHandlerScripts(kind, q);
  const entry = KIND_WORDS[kind].entry;
  if (picked) {
    return (
      <div className="space-y-1.5">
        <p className="text-sm font-medium">Script</p>
        <div className="flex items-center justify-between rounded-md border border-primary px-3 py-2 text-sm" data-testid="picked-script">
          <span className="min-w-0 truncate">
            <span className="font-medium">{picked.name}</span>
            {picked.owner_email && <span className="text-muted-foreground"> · {picked.owner_email}</span>}
          </span>
          <Button variant="link" size="sm" onClick={() => onPick(null)}>
            Change
          </Button>
        </div>
      </div>
    );
  }
  const scripts = listing.data?.scripts ?? [];
  return (
    <div className="space-y-1.5">
      <Label htmlFor="add-script-search">Script</Label>
      <Input id="add-script-search" placeholder="Find a script by name" value={search} onChange={(e) => setSearch(e.target.value)} />
      {scripts.length > 0 && (
        <ul aria-label="Scripts" className="divide-y overflow-hidden rounded-md border">
          {scripts.map((sc) => (
            <li key={sc.id}>
              <button
                type="button"
                className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                onClick={() => onPick(sc)}
              >
                <span className="min-w-0 truncate">
                  <span className="font-medium">{sc.name}</span>
                  {sc.owner_email && <span className="text-muted-foreground"> · {sc.owner_email}</span>}
                </span>
                <span className="shrink-0 text-xs text-muted-foreground">v{sc.version}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {listing.data && scripts.length === 0 && <NoScript kind={kind} q={q} onKind={onKind} />}
      {listing.data && scripts.length > 0 && (
        <p className="text-xs text-muted-foreground">
          {scripts.length} of {listing.data.looked} scripts define {entry}, so only those are listed.
        </p>
      )}
    </div>
  );
}

// NoScript says no script found defines the entry, and offers the watch when
// a script by that name runs main(), which a watch runs.
function NoScript({ kind, q, onKind }: { kind: CallHandlerKind; q: string; onKind: (k: CallHandlerKind) => void }) {
  const watches = useHandlerScripts("watch", q);
  const asWatch = kind !== "watch" && q !== "" ? watches.data?.scripts[0] : undefined;
  return (
    <div className="rounded-md border border-dashed px-3 py-2 text-sm text-muted-foreground" data-testid="no-script">
      {q ? `No script named like ${q} defines ${KIND_WORDS[kind].entry}.` : `No script defines ${KIND_WORDS[kind].entry}.`}
      {asWatch && (
        <>
          {" "}
          {asWatch.name} defines main(), which a watch runs.{" "}
          <Button variant="link" size="sm" className="h-auto p-0" onClick={() => onKind("watch")}>
            Add it as a watch
          </Button>
        </>
      )}
    </div>
  );
}
