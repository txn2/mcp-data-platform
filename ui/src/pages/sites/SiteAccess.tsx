import { useState } from "react";
import { useGrantSite, useRevokeSite, useSiteAccess, useUpdateSite, type Site, type SiteGrant } from "@/api/portal/hooks/sites";
import { useDirectoryUsers } from "@/api/portal/hooks";
import { ExternalBadge } from "@/components/ExternalBadge";
import { SectionCard } from "@/components/patterns/SectionCard";
import { UserPicker } from "@/components/UserPicker";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { bareAddress, day, errorText } from "./siteFormat";

const LEVEL_LABEL: Record<SiteGrant["level"], string> = { manage: "Can manage", view: "Can view" };

// SiteAccessTab is who reaches the site (#2559). The owner and administrators
// add and remove people who manage it; anyone who manages it shares view access.
export function SiteAccessTab({ site }: { site: Site }) {
  const { data, isLoading, error } = useSiteAccess(site.name, true);
  const update = useUpdateSite(site.name);
  const revoke = useRevokeSite(site.name);
  const [adding, setAdding] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const own = site.permissions.own;
  const address = site.address ? bareAddress(site.address) : "this site's own address";
  const remove = (email: string) => {
    setFailure(null);
    revoke.mutate(email, { onError: (e) => setFailure(errorText(e, "This person could not be removed")) });
  };
  const toggleInternal = (internal_view: boolean) => {
    setFailure(null);
    update.mutate({ internal_view }, { onError: (e) => setFailure(errorText(e, "The site could not be saved")) });
  };
  return (
    <>
      <SectionCard
        title="Access"
        action={
          <Button size="sm" onClick={() => setAdding(true)}>
            Add person
          </Button>
        }
      >
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            Can manage: publish, promote, roll back, add domains and share view access. Can view: open {address} and read the files it serves now, and change nothing. Administrators can always manage this site.
          </p>
          <label className="flex items-start gap-2 rounded border p-3">
            <input type="checkbox" className="mt-1" checked={site.internal_view} disabled={update.isPending} onChange={(e) => toggleInternal(e.target.checked)} />
            <span>
              <span className="block font-medium">Everyone internal can view</span>
              <span className="block text-xs text-muted-foreground">Anyone signed in to this platform whose persona is not external can open {address}. Use this for an intranet.</span>
            </span>
          </label>
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
          {error ? (
            <Alert variant="destructive">
              <AlertDescription>{error.message}</AlertDescription>
            </Alert>
          ) : isLoading || !data ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Person</TableHead>
                  <TableHead>Access</TableHead>
                  <TableHead>Added</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell className="break-all font-medium">{data.owner_email}</TableCell>
                  <TableCell>
                    <Badge variant="info">Owner</Badge>
                  </TableCell>
                  <TableCell>
                    <div>{day(site.created_at)}</div>
                    <div className="text-xs text-muted-foreground">Created the site</div>
                  </TableCell>
                  <TableCell />
                </TableRow>
                {data.grants.map((g) => (
                  <GrantRow key={g.email} grant={g} removable={own || g.level === "view"} onRemove={remove} />
                ))}
              </TableBody>
            </Table>
          )}
          {!own && <p className="text-xs text-muted-foreground">Only the owner and administrators add or remove people who can manage.</p>}
        </div>
      </SectionCard>
      {adding && <AddPersonDialog site={site} viewOnly={!own} onClose={() => setAdding(false)} />}
    </>
  );
}

function GrantRow({ grant, removable, onRemove }: { grant: SiteGrant; removable: boolean; onRemove: (email: string) => void }) {
  const { data } = useDirectoryUsers(grant.email, true);
  const external = data?.users.some((u) => u.email.toLowerCase() === grant.email && u.external);
  return (
    <TableRow>
      <TableCell className="break-all font-medium">
        <span className="inline-flex flex-wrap items-center gap-2">
          {grant.email}
          {external && <ExternalBadge />}
        </span>
      </TableCell>
      <TableCell>
        <Badge variant="muted">{LEVEL_LABEL[grant.level]}</Badge>
      </TableCell>
      <TableCell>
        <div>{day(grant.created_at)}</div>
        <div className="text-xs text-muted-foreground">Added by {grant.granted_by}</div>
      </TableCell>
      <TableCell>
        {removable && (
          <div className="flex justify-end">
            <Button size="sm" variant="outline" onClick={() => onRemove(grant.email)}>
              Remove
            </Button>
          </div>
        )}
      </TableCell>
    </TableRow>
  );
}

// AddPersonDialog gives someone access. viewOnly is someone who manages the
// site sharing it, or the Share action beside its address.
export function AddPersonDialog({ site, viewOnly, onClose }: { site: Site; viewOnly?: boolean; onClose: () => void }) {
  const grant = useGrantSite(site.name);
  const [email, setEmail] = useState("");
  const [level, setLevel] = useState<SiteGrant["level"]>(viewOnly ? "view" : "manage");
  const [notify, setNotify] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const address = site.address ? bareAddress(site.address) : "its own address";
  const submit = () => {
    setFailure(null);
    grant.mutate(
      { email: email.trim(), level, notify: level === "view" && notify },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "This person could not be added")) },
    );
  };
  const pick = (value: SiteGrant["level"], title: string, help: string) => (
    <label className={`flex cursor-pointer items-start gap-2 rounded border p-3 ${level === value ? "border-primary" : ""}`}>
      <input type="radio" name="level" aria-label={title} checked={level === value} onChange={() => setLevel(value)} className="mt-1" />
      <span>
        <span className="block font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{help}</span>
      </span>
    </label>
  );
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Give someone access to {site.name}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label>Person</Label>
            <UserPicker value={email} onChange={setEmail} placeholder="Name or email" />
          </div>
          <div className="space-y-2">
            <Label>Access</Label>
            {pick("view", "Can view", `Opens ${address} after signing in and reads the files it serves now. Changes nothing.`)}
            {!viewOnly &&
              pick("manage", "Can manage", "Publishes, promotes, rolls back, adds domains and shares view access. Internal people only. Cannot add people who can manage, change the owner or delete the site.")}
          </div>
          {level === "view" && (
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={notify} onChange={(e) => setNotify(e.target.checked)} />
              Email them the link
            </label>
          )}
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!email.includes("@") || grant.isPending}>
            Add person
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
