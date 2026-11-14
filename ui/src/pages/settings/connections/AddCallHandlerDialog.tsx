import { useState } from "react";
import { CALL_HANDLER_KINDS, type CallHandlerKind } from "@/api/portal/hooks/scriptHandlers";
import { useScriptListing } from "@/api/portal/hooks/scriptListFilter";
import { useScriptContract } from "@/api/portal/hooks/scripts";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { CallHandlerForm, KIND_WORDS } from "@/pages/scripts/callhandlers/CallHandlerForm";
import { cn } from "@/lib/utils";

// AddCallHandlerDialog adds a policy, transform or watch to a connection
// (#2436): the kind, the script (any on the platform, found by name), then the
// handler form with the connection fixed. The script's version is approved as
// the administrator adding it unless they say otherwise.

export function AddCallHandlerDialog({ connection, onClose }: { connection: string; onClose: () => void }) {
  const [kind, setKind] = useState<CallHandlerKind>("policy");
  const [search, setSearch] = useState("");
  const [scriptId, setScriptId] = useState("");
  const listing = useScriptListing({ search: search.trim() || undefined, kind: "script" });
  const contract = useScriptContract(scriptId || null).data?.contract;
  const scripts = listing.data?.data ?? [];
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-2xl">
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
                  <input type="radio" name="add-kind" checked={kind === k} onChange={() => setKind(k)} />
                  {KIND_WORDS[k].label}
                </span>
                <span className="text-xs text-muted-foreground">{KIND_WORDS[k].hint}</span>
              </label>
            ))}
          </div>
        </fieldset>
        <div className="space-y-1.5">
          <Label htmlFor="add-script-search">Script</Label>
          <Input
            id="add-script-search"
            placeholder="Find a script by name"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <select
            aria-label="Script"
            className="h-9 w-full rounded-md border bg-background px-2 text-sm"
            value={scriptId}
            onChange={(e) => setScriptId(e.target.value)}
          >
            <option value="">Choose a script</option>
            {scripts.map(({ script: sc }) => (
              <option key={sc.id} value={sc.id}>
                {sc.name}
                {sc.owner_email ? ` · ${sc.owner_email}` : ""}
              </option>
            ))}
          </select>
          <p className="text-xs text-muted-foreground">A {KIND_WORDS[kind].label.toLowerCase()}&apos;s script defines {KIND_WORDS[kind].entry}.</p>
        </div>
        {contract && (
          <CallHandlerForm
            key={`${scriptId}-${kind}`}
            scriptId={scriptId}
            kind={kind}
            connection={connection}
            approval={{ latest: contract.version, running: 0 }}
            onDone={onClose}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
