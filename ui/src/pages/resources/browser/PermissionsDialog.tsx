import { useState } from "react";
import { AlertTriangle, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAddFolderGrant, useFolderGrants, useRemoveFolderGrant, type FolderGrant } from "@/api/resources/folderGrants";

// PermissionsDialog is Permissions on a Global folder (#2684), for an
// administrator: who else can write the folder and everything beneath it,
// the grants on folders above it marked inherited, the sites serving it as
// media, and adding or removing a person or a persona.
export function PermissionsDialog({ folder, onClose }: { folder: string; onClose: () => void }) {
  const { data, isLoading, error } = useFolderGrants(folder, true);
  const [removing, setRemoving] = useState<FolderGrant | null>(null);
  const name = folder.split("/").pop() ?? folder;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl" data-testid="folder-permissions">
        <DialogHeader>
          <DialogTitle>Permissions on {name}</DialogTitle>
          <DialogDescription>
            Everyone signed in reads Global. These people can also add, change, move and delete files and folders inside {name}. Only administrators
            rename, move or delete {name} itself.
          </DialogDescription>
        </DialogHeader>
        <SitesWarning sites={data?.sites ?? []} />
        {isLoading && (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> Loading...
          </p>
        )}
        {error && <p className="text-sm text-destructive">The grants could not be read.</p>}
        {data && <GrantTable grants={data.grants} onRemove={setRemoving} />}
        <AddGrant folder={folder} name={name} />
        {removing && <RemoveConfirm grant={removing} name={name} onDone={() => setRemoving(null)} />}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// SitesWarning names the sites a write to the folder changes what they serve.
function SitesWarning({ sites }: { sites: string[] }) {
  if (sites.length === 0) return null;
  const one = sites.length === 1;
  return (
    <div className="flex gap-2 rounded-md border border-amber-600/60 bg-amber-500/10 p-3 text-sm" role="note" data-testid="permissions-sites">
      <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <span>
        {one ? "The site " : "The sites "}
        <b>{sites.join(", ")}</b> {one ? "serves" : "serve"} this folder as media. A write here changes what {one ? "it serves" : "they serve"}.
      </span>
    </div>
  );
}

// AddGrant adds a person or a persona, showing the server's refusal.
function AddGrant({ folder, name }: { folder: string; name: string }) {
  const add = useAddFolderGrant();
  const [kind, setKind] = useState<"person" | "persona">("person");
  const [who, setWho] = useState("");
  const [refusal, setRefusal] = useState("");
  const submit = () => {
    setRefusal("");
    const value = who.trim();
    if (!value) return;
    add.mutate(
      { folder, ...(kind === "person" ? { email: value } : { persona: value }) },
      {
        onSuccess: () => setWho(""),
        onError: (e) => setRefusal(e instanceof Error ? e.message : "The grant could not be added."),
      },
    );
  };
  return (
    <div className="space-y-2">
      <Label htmlFor="grant-who">Add a person or persona</Label>
      <div className="flex flex-wrap gap-2">
        <select
          aria-label="Kind"
          value={kind}
          onChange={(e) => setKind(e.target.value as "person" | "persona")}
          className="h-10 rounded-md border bg-background px-2 text-sm"
        >
          <option value="person">Person</option>
          <option value="persona">Persona</option>
        </select>
        <Input
          id="grant-who"
          className="min-w-0 flex-1"
          value={who}
          placeholder={kind === "person" ? "name@example.com" : "persona name"}
          aria-invalid={refusal !== ""}
          onChange={(e) => setWho(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <Button onClick={submit} disabled={add.isPending || who.trim() === ""}>
          Add
        </Button>
      </div>
      {refusal && (
        <p role="alert" className="text-sm text-destructive" data-testid="grant-refusal">
          {refusal}
        </p>
      )}
      <p className="text-xs text-muted-foreground">
        Covers {name} and every folder inside it, now and later. A persona's Resources level still applies: a persona that cannot upload cannot write here.
      </p>
    </div>
  );
}

// RemoveConfirm asks before a grant is removed.
function RemoveConfirm({ grant, name, onDone }: { grant: FolderGrant; name: string; onDone: () => void }) {
  const remove = useRemoveFolderGrant();
  return (
    <div className="space-y-2 rounded-md border p-3 text-sm" data-testid="grant-remove-confirm">
      <b>
        Remove {grant.email || grant.persona} from {name}?
      </b>
      <p className="text-muted-foreground">Their next upload or change inside {name} is refused. Files they added stay.</p>
      <div className="flex justify-end gap-2">
        <Button variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button variant="destructive" onClick={() => remove.mutate(grant.id, { onSuccess: onDone })}>
          Remove
        </Button>
      </div>
    </div>
  );
}

function GrantTable({ grants, onRemove }: { grants: FolderGrant[]; onRemove: (g: FolderGrant) => void }) {
  if (grants.length === 0) {
    return (
      <div className="rounded-md border border-dashed p-4 text-sm" data-testid="grants-empty">
        <b>Only administrators write here</b>
        <p className="text-muted-foreground">Add a person or persona to let them upload and organize files inside this folder.</p>
      </div>
    );
  }
  return (
    <table className="w-full text-sm" data-testid="grants">
      <thead className="text-left text-xs text-muted-foreground">
        <tr>
          <th className="py-2 font-medium">Who</th>
          <th className="py-2 font-medium">Granted on</th>
          <th className="py-2 font-medium">By</th>
          <th className="py-2" />
        </tr>
      </thead>
      <tbody>
        {grants.map((g) => (
          <tr key={g.id} className="border-t" data-testid={`grant-${g.id}`}>
            <td className="py-2">{g.email ? g.email : <>Persona <b>{g.persona}</b></>}</td>
            <td className="py-2">{g.inherited ? <span className="text-muted-foreground">Inherited from {g.folder}</span> : g.folder}</td>
            <td className="py-2 text-muted-foreground">{g.created_by}</td>
            <td className="py-2 text-right">
              {g.inherited ? (
                <span className="text-xs text-muted-foreground">Change it there</span>
              ) : (
                <Button size="sm" variant="outline" onClick={() => onRemove(g)}>
                  Remove
                </Button>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
