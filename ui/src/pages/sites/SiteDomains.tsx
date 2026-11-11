import { useState } from "react";
import { DEV_ENVIRONMENT, useAddSiteDomain, useRemoveSiteDomain, type Site, type SiteDomain } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { envNameProblem } from "./envName";
import { AccessBadge, AddressLink } from "./SiteParts";
import { bareAddress, envAddress, errorText, hostUrl, orderedEnvironments, when } from "./siteFormat";

export const DOMAIN_NOTE = "A system administrator adds the ingress for each domain, and whoever owns it points its DNS here.";

const NEW_ENV = "_:new";

// SiteDomainsTab is every address the site answers at (#2559, #2604): one on
// the pages domain for each environment, which needs nothing from DNS, and the
// domains added to an environment, which answer with that environment's access.
export function SiteDomainsTab({ site }: { site: Site }) {
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<SiteDomain | null>(null);
  const canEdit = site.permissions.manage;
  const envs = orderedEnvironments(site.environments);
  const withAddress = envs.filter((e) => envAddress(site, e.name));
  return (
    <>
      {withAddress.length > 0 && (
        <SectionCard title="Addresses on the pages domain">
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">Every environment has one. They need nothing from DNS or the ingress.</p>
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Address</TableHead>
                    <TableHead>Serves</TableHead>
                    <TableHead>Access</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {withAddress.map((e) => (
                    <TableRow key={e.name}>
                      <TableCell className="break-all">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <AddressLink href={envAddress(site, e.name)} text={bareAddress(envAddress(site, e.name))} />
                          {site.main_environment === e.name && <Badge variant="muted">Main address</Badge>}
                        </div>
                      </TableCell>
                      <TableCell>{e.name}</TableCell>
                      <TableCell>
                        <AccessBadge env={e} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        </SectionCard>
      )}
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
          {site.domains.length === 0 ? (
            <div className="rounded border border-dashed p-6 text-center">
              <p className="font-medium">No domains</p>
              <p className="text-xs text-muted-foreground">Add a domain to serve an environment somewhere other than the pages domain. It answers with that environment's access.</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Domain</TableHead>
                    <TableHead>Serves</TableHead>
                    <TableHead>Access</TableHead>
                    <TableHead>Added</TableHead>
                    {canEdit && <TableHead />}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {site.domains.map((d) => (
                    <DomainRow key={d.hostname} site={site} domain={d} canEdit={canEdit} onRemove={() => setRemoving(d)} />
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          <p className="text-xs text-muted-foreground">
            A domain answers with its environment's access: a private one asks for a sign-in on that domain and sends people back to it. {DOMAIN_NOTE} dev can't have a domain.
            {!canEdit && " Only the owner and the people who can manage this site add domains."}
          </p>
        </div>
      </SectionCard>
      {adding && <AddDomainDialog site={site} onClose={() => setAdding(false)} />}
      {removing && <RemoveDomainDialog site={site} domain={removing} onClose={() => setRemoving(null)} />}
    </>
  );
}

function DomainRow({ site, domain: d, canEdit, onRemove }: { site: Site; domain: SiteDomain; canEdit: boolean; onRemove: () => void }) {
  const env = site.environments.find((e) => e.name === d.environment);
  return (
    <TableRow>
      <TableCell className="break-all">
        <AddressLink href={hostUrl(site, d.hostname)} text={d.hostname} />
      </TableCell>
      <TableCell>{d.environment}</TableCell>
      <TableCell>{env && <AccessBadge env={env} />}</TableCell>
      <TableCell>
        <div>{when(d.created_at)}</div>
        <div className="text-xs text-muted-foreground">{d.created_by}</div>
      </TableCell>
      {canEdit && (
        <TableCell>
          {mayRemove(site, d) && (
            <div className="flex justify-end">
              <Button size="sm" variant="outline" onClick={onRemove}>
                Remove
              </Button>
            </div>
          )}
        </TableCell>
      )}
    </TableRow>
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
  const existing = orderedEnvironments(site.environments).filter((e) => e.name !== DEV_ENVIRONMENT);
  const [hostname, setHostname] = useState("");
  const [choice, setChoice] = useState(existing[0]?.name ?? NEW_ENV);
  const [name, setName] = useState("");
  const [approval, setApproval] = useState(false);
  const [isPublic, setPublic] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const creating = choice === NEW_ENV;
  const environment = creating ? name.trim() : choice;
  const problem = creating ? envNameProblem(site.name, environment) : "";
  const submit = () => {
    setFailure(null);
    add.mutate(
      { hostname: hostname.trim(), environment, ...(creating ? { requires_approval: approval, public: isPublic } : {}) },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "This domain could not be added")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add domain</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
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
            <Input id="domain-host" placeholder="www.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} autoFocus />
            <p className="text-xs text-muted-foreground">Not the portal's own hostname or anything under the pages domain. A domain beside the portal (portal.example.com, site.example.com) is fine.</p>
          </div>
          <div className="space-y-1">
            <Label>Environment</Label>
            <Select value={choice} onValueChange={setChoice}>
              <SelectTrigger aria-label="Environment">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {existing.map((e) => (
                  <SelectItem key={e.name} value={e.name}>
                    {e.name} ({e.public ? "public" : "private"})
                  </SelectItem>
                ))}
                <SelectItem value={NEW_ENV}>New environment</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">dev is not listed: it is the working copy and can't have a domain.</p>
          </div>
          {creating ? (
            <NewEnvironmentFields site={site} name={name} onName={setName} problem={problem} isPublic={isPublic} onPublic={setPublic} approval={approval} onApproval={setApproval} />
          ) : (
            <ExistingAccess site={site} env={choice} />
          )}
          <p className="text-xs text-muted-foreground">{DOMAIN_NOTE}</p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!hostname.trim() || !environment || !!problem || add.isPending}>
            Add domain
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ExistingAccess({ site, env }: { site: Site; env: string }) {
  const e = site.environments.find((x) => x.name === env);
  if (!e) return null;
  return (
    <p className="text-xs text-muted-foreground">
      The domain takes the environment's access.{" "}
      {e.public ? `${env} is public, so this domain opens for anyone.` : `${env} is private, so this domain asks for a sign-in and opens only for whoever ${env} is shared with and the people who manage ${site.name}.`}
    </p>
  );
}

function NewEnvironmentFields({
  site,
  name,
  onName,
  problem,
  isPublic,
  onPublic,
  approval,
  onApproval,
}: {
  site: Site;
  name: string;
  onName: (v: string) => void;
  problem: string;
  isPublic: boolean;
  onPublic: (v: boolean) => void;
  approval: boolean;
  onApproval: (v: boolean) => void;
}) {
  return (
    <>
      <div className="space-y-1">
        <Label htmlFor="domain-env">Name</Label>
        <Input id="domain-env" placeholder="prod" value={name} onChange={(e) => onName(e.target.value)} />
        {problem && <p className="text-xs text-destructive">{problem}</p>}
      </div>
      <fieldset className="space-y-2">
        <legend className="mb-2 text-sm font-medium">Access</legend>
        <label className={`flex items-start gap-2 rounded border p-3 ${!isPublic ? "border-primary bg-primary/5" : ""}`}>
          <input type="radio" name="domain-access" aria-label="Private" className="mt-1" checked={!isPublic} onChange={() => onPublic(false)} />
          <span>
            <span className="block font-medium">Private</span>
            <span className="block text-xs text-muted-foreground">Asks for a sign-in on this domain and opens only for the people and personas you share it with.</span>
          </span>
        </label>
        <label className={`flex items-start gap-2 rounded border p-3 ${isPublic ? "border-primary bg-primary/5" : ""} ${site.permissions.own ? "" : "opacity-60"}`}>
          <input type="radio" name="domain-access" aria-label="Public" className="mt-1" checked={isPublic} disabled={!site.permissions.own} onChange={() => onPublic(true)} />
          <span>
            <span className="block font-medium">Public</span>
            <span className="block text-xs text-muted-foreground">Opens for anyone. Only the owner or an administrator can choose this.</span>
          </span>
        </label>
      </fieldset>
      <label className="flex items-start gap-2">
        <input type="checkbox" className="mt-1" checked={approval} onChange={(e) => onApproval(e.target.checked)} />
        <span>
          <span className="block font-medium">Requires approval</span>
          <span className="block text-xs text-muted-foreground">A release reaches this environment only by Promote, and someone who is not an administrator has to ask first.</span>
        </span>
      </label>
    </>
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
