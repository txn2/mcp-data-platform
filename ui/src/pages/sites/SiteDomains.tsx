import { useState } from "react";
import { ExternalLink } from "lucide-react";
import { DEFAULT_ENVIRONMENT, useAddSiteDomain, useRemoveSiteDomain, type Site, type SiteDomain } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { bareAddress, errorText, hostUrl, when } from "./siteFormat";

export const PUBLIC_DOMAIN_NOTE =
  "A system administrator adds the ingress for each domain, and whoever owns it points its DNS here. A domain is public unless the site asks for a sign-in.";

const NEW_ENV = "_:new";

// SiteDomainsTab is the domains added to the site (#2559): each serves an
// environment to anyone, and whoever manages the site adds and removes them.
export function SiteDomainsTab({ site }: { site: Site }) {
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<SiteDomain | null>(null);
  const canEdit = site.permissions.manage;
  return (
    <>
      <SectionCard
        title="Domains"
        action={
          canEdit ? (
            <Button size="sm" onClick={() => setAdding(true)}>
              Add domain
            </Button>
          ) : undefined
        }
      >
        <div className="space-y-3">
          {site.address && (
            <dl className="grid gap-3 sm:grid-cols-2">
              <div>
                <dt className="text-xs text-muted-foreground">Site address</dt>
                <dd className="font-mono text-sm">{bareAddress(site.address)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Serves</dt>
                <dd className="text-sm">default, for people with access</dd>
              </div>
            </dl>
          )}
          {site.domains.length === 0 ? (
            <div className="rounded border border-dashed p-6 text-center">
              <p className="font-medium">No domains</p>
              <p className="text-xs text-muted-foreground">Only people with access can open this site at its own address. Add a domain to serve an environment to the public.</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Domain</TableHead>
                    <TableHead>Serves</TableHead>
                    <TableHead>Added</TableHead>
                    {canEdit && <TableHead />}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {site.domains.map((d) => (
                    <TableRow key={d.hostname}>
                      <TableCell className="break-all">
                        <a className="inline-flex items-center gap-1 font-medium hover:underline" href={hostUrl(site, d.hostname)} target="_blank" rel="noreferrer" title="Open in a new tab">
                          {d.hostname}
                          <ExternalLink className="h-3 w-3 shrink-0" aria-hidden />
                        </a>
                      </TableCell>
                      <TableCell>{d.environment}</TableCell>
                      <TableCell>
                        <div>{when(d.created_at)}</div>
                        <div className="text-xs text-muted-foreground">{d.created_by}</div>
                      </TableCell>
                      {canEdit && (
                        <TableCell>
                          {mayRemove(site, d) && (
                            <div className="flex justify-end">
                              <Button size="sm" variant="outline" onClick={() => setRemoving(d)}>
                                Remove
                              </Button>
                            </div>
                          )}
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          <p className="text-xs text-muted-foreground">
            {PUBLIC_DOMAIN_NOTE}
            {!canEdit && " Only the owner and the people who can manage this site add domains."}
          </p>
        </div>
      </SectionCard>
      {adding && <AddDomainDialog site={site} onClose={() => setAdding(false)} />}
      {removing && <RemoveDomainDialog site={site} domain={removing} onClose={() => setRemoving(null)} />}
    </>
  );
}

// mayRemove hides what the server refuses: a domain on an environment that
// requires approval is an administrator's to take off.
function mayRemove(site: Site, d: SiteDomain): boolean {
  const gated = site.environments.some((e) => e.name === d.environment && e.requires_approval);
  return !gated || site.permissions.delete;
}

export function AddDomainDialog({ site, onClose }: { site: Site; onClose: () => void }) {
  const add = useAddSiteDomain(site.name);
  const existing = site.environments.filter((e) => e.name !== DEFAULT_ENVIRONMENT);
  const [hostname, setHostname] = useState("");
  const [choice, setChoice] = useState(NEW_ENV);
  const [name, setName] = useState("");
  const [approval, setApproval] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const creating = choice === NEW_ENV;
  const environment = creating ? name.trim() : choice;
  const submit = () => {
    setFailure(null);
    add.mutate(
      { hostname: hostname.trim(), environment, requires_approval: creating && approval },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "This domain could not be added")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add domain</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          {failure && (
            <Alert variant="destructive" data-testid="domain-refused">
              <AlertTitle>Not added</AlertTitle>
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-1">
            <Label htmlFor="domain-host">Domain</Label>
            <Input id="domain-host" placeholder="dev.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} autoFocus />
            <p className="text-xs text-muted-foreground">Not the portal's own hostname, anything under the pages domain, or anything under the portal session's domain.</p>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label>Environment</Label>
              <Select value={choice} onValueChange={setChoice}>
                <SelectTrigger aria-label="Environment">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NEW_ENV}>New environment</SelectItem>
                  {existing.map((e) => (
                    <SelectItem key={e.name} value={e.name}>
                      {e.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {creating && (
              <div className="space-y-1">
                <Label htmlFor="domain-env">Name</Label>
                <Input id="domain-env" placeholder="dev" value={name} onChange={(e) => setName(e.target.value)} />
              </div>
            )}
          </div>
          {creating && (
            <label className="flex items-start gap-2">
              <input type="checkbox" className="mt-1" checked={approval} onChange={(e) => setApproval(e.target.checked)} />
              <span>
                <span className="block font-medium">Requires approval</span>
                <span className="block text-xs text-muted-foreground">A release reaches this environment only by Promote, and someone who is not an administrator has to ask first.</span>
              </span>
            </label>
          )}
          <p className="text-xs text-muted-foreground">{PUBLIC_DOMAIN_NOTE}</p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!hostname.trim() || !environment || add.isPending}>
            Add domain
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RemoveDomainDialog({ site, domain, onClose }: { site: Site; domain: SiteDomain; onClose: () => void }) {
  const remove = useRemoveSiteDomain(site.name);
  const [failure, setFailure] = useState<string | null>(null);
  const submit = () => {
    setFailure(null);
    remove.mutate(domain.hostname, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "This domain could not be removed")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove {domain.hostname}</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          It stops serving {site.name} / {domain.environment} as soon as it is removed. The environment and its release do not change.
        </p>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={remove.isPending}>
            Remove domain
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
