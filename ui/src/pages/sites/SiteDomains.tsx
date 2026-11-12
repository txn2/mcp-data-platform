import { useState } from "react";
import { DEV_ENVIRONMENT, placeChoices, useAddSiteDomain, useRemoveSiteDomain, type Site, type SiteDomain } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAuthStore } from "@/stores/auth";
import { envNameProblem } from "./envName";
import { ConfirmPublic, MakeDomainPrivateDialog, MakeDomainPublicDialog } from "./SiteDomainAccess";
import { AddressAccess, AddressLink } from "./SiteParts";
import { bareAddress, envAddress, errorText, hostUrl, orderedEnvironments, when } from "./siteFormat";

export const DOMAIN_NOTE = "A system administrator adds the ingress for each domain, and whoever owns it points its DNS here.";

const NEW_ENV = "_:new";

// SiteDomainsTab is every address the site answers at (#2559, #2604, #2619):
// one on the pages domain for each environment, which needs nothing from DNS
// and always asks for a sign-in, and the domains added to an environment, each
// private unless it is made public.
export function SiteDomainsTab({ site }: { site: Site }) {
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<SiteDomain | null>(null);
  const [opening, setOpening] = useState<SiteDomain | null>(null);
  const [closing, setClosing] = useState<SiteDomain | null>(null);
  const canEdit = site.permissions.manage;
  const envs = orderedEnvironments(site.environments);
  const withAddress = envs.filter((e) => envAddress(site, e.name));
  return (
    <>
      {withAddress.length > 0 && (
        <SectionCard title="Addresses on the pages domain">
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">Every environment has one. They need nothing from DNS or the ingress, and each asks for a sign-in.</p>
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
                        <AddressAccess isPublic={false} />
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
                    <DomainRow
                      key={d.hostname}
                      site={site}
                      domain={d}
                      canEdit={canEdit}
                      onRemove={() => setRemoving(d)}
                      onPublic={() => setOpening(d)}
                      onPrivate={() => setClosing(d)}
                    />
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          <p className="text-xs text-muted-foreground">
            A domain is private unless it is made public: a private one asks for a sign-in on that domain and sends people back to it, and a public one opens for anyone. Only the owner or an administrator makes a domain public. {DOMAIN_NOTE} dev can't have a domain.
            {!canEdit && " Only the owner and the people who can manage this site add domains."}
          </p>
        </div>
      </SectionCard>
      <DomainDialogs
        site={site}
        adding={adding}
        removing={removing}
        opening={opening}
        closing={closing}
        onClose={() => {
          setAdding(false);
          setRemoving(null);
          setOpening(null);
          setClosing(null);
        }}
      />
    </>
  );
}

function DomainDialogs({
  site,
  adding,
  removing,
  opening,
  closing,
  onClose,
}: {
  site: Site;
  adding: boolean;
  removing: SiteDomain | null;
  opening: SiteDomain | null;
  closing: SiteDomain | null;
  onClose: () => void;
}) {
  if (adding) return <AddDomainDialog site={site} onClose={onClose} />;
  if (removing) return <RemoveDomainDialog site={site} domain={removing} onClose={onClose} />;
  if (opening) return <MakeDomainPublicDialog site={site} hostname={opening.hostname} environment={opening.environment ?? ""} onClose={onClose} />;
  if (closing) return <MakeDomainPrivateDialog site={site} domain={closing} onClose={onClose} />;
  return null;
}

function DomainRow({
  site,
  domain: d,
  canEdit,
  onRemove,
  onPublic,
  onPrivate,
}: {
  site: Site;
  domain: SiteDomain;
  canEdit: boolean;
  onRemove: () => void;
  onPublic: () => void;
  onPrivate: () => void;
}) {
  return (
    <TableRow>
      <TableCell className="break-all">
        <AddressLink href={hostUrl(site, d.hostname)} text={d.hostname} />
      </TableCell>
      <TableCell>{d.environment}</TableCell>
      <TableCell>
        <AddressAccess isPublic={d.public} />
      </TableCell>
      <TableCell>
        <div>{when(d.created_at)}</div>
        <div className="text-xs text-muted-foreground">{d.created_by}</div>
      </TableCell>
      {canEdit && (
        <TableCell>
          <div className="flex flex-wrap justify-end gap-2">
            {d.public ? (
              <Button size="sm" variant="outline" onClick={onPrivate}>
                Make private
              </Button>
            ) : (
              site.permissions.own && (
                <Button size="sm" variant="outline" onClick={onPublic}>
                  Make public
                </Button>
              )
            )}
            {mayRemove(site, d) && (
              <Button size="sm" variant="outline" onClick={onRemove}>
                Remove
              </Button>
            )}
          </div>
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

/** The environment Add domain starts on: prod, else the first one people use, else a new one. */
function firstDomainEnvironment(names: string[]): string {
  if (names.includes("prod")) return "prod";
  return names[0] ?? NEW_ENV;
}

// useAddDomainForm is the Add domain form's state and the request it sends.
function useAddDomainForm(site: Site, onClose: () => void) {
  const add = useAddSiteDomain(site.name);
  const isAdmin = useAuthStore((st) => st.isAdmin());
  const existing = orderedEnvironments(site.environments).filter((e) => e.name !== DEV_ENVIRONMENT);
  const [hostname, setHostname] = useState("");
  const [choice, setChoice] = useState(() => firstDomainEnvironment(existing.map((e) => e.name)));
  const [name, setName] = useState("");
  const [approval, setApproval] = useState(false);
  const [isPublic, setPublic] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const creating = choice === NEW_ENV;
  const environment = creating ? name.trim() : choice;
  const choices = placeChoices(site.environments.map((e) => e.name), environment);
  const [after, setAfter] = useState(choices[0] ?? "");
  const gated = creating ? approval : !!site.environments.find((e) => e.name === choice)?.requires_approval;
  const host = hostname.trim();
  const submit = (reason?: string) => {
    setFailure(null);
    const place = creating ? { requires_approval: approval, ...(choices.length > 1 ? { after } : {}) } : {};
    const access = isPublic ? { public: true, ...(reason ? { reason } : {}) } : {};
    add.mutate({ hostname: host, environment, ...access, ...place }, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "This domain could not be added")) });
  };
  return {
    add, existing, hostname, setHostname, choice, setChoice, name, setName, approval, setApproval, isPublic, setPublic, confirming, setConfirming,
    failure, creating, environment, choices, after, setAfter, asks: gated && !isAdmin, host, submit,
    problem: creating ? envNameProblem(site.name, environment) : "",
  };
}

export function AddDomainDialog({ site, onClose }: { site: Site; onClose: () => void }) {
  const f = useAddDomainForm(site, onClose);
  const { add, existing, hostname, setHostname, choice, setChoice, name, setName, approval, setApproval, isPublic, setPublic, confirming, setConfirming } = f;
  const { failure, creating, environment, choices, after, setAfter, problem, host, submit } = f;
  if (confirming) {
    return <ConfirmPublic site={site} hostname={host} environment={environment} asks={f.asks} busy={add.isPending} failure={failure} onConfirm={submit} onClose={onClose} adding />;
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Add domain</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <Refused failure={failure} />
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
                    {e.name}
                  </SelectItem>
                ))}
                <SelectItem value={NEW_ENV}>New environment</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">dev is not listed: it is the working copy and can't have a domain.</p>
          </div>
          {creating && (
            <NewEnvironmentFields site={site} name={name} onName={setName} problem={problem} choices={choices} after={after} onAfter={setAfter} approval={approval} onApproval={setApproval} />
          )}
          <DomainAccessChoice site={site} hostname={host || "this domain"} environment={environment} isPublic={isPublic} onPublic={setPublic} gated={f.asks} />
          <p className="text-xs text-muted-foreground">{DOMAIN_NOTE}</p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => (isPublic ? setConfirming(true) : submit())} disabled={!f.host || !environment || !!problem || add.isPending}>
            Add domain
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Refused({ failure }: { failure: string | null }) {
  if (!failure) return null;
  return (
    <Alert variant="destructive" data-testid="domain-refused">
      <AlertTitle>Not added</AlertTitle>
      <AlertDescription>{failure}</AlertDescription>
    </Alert>
  );
}

// DomainAccessChoice is whether a domain being added answers anyone (#2619):
// private unless Public is chosen, which only the owner or an administrator can.
function DomainAccessChoice({
  site,
  hostname,
  environment,
  isPublic,
  onPublic,
  gated,
}: {
  site: Site;
  hostname: string;
  environment: string;
  isPublic: boolean;
  onPublic: (v: boolean) => void;
  gated: boolean;
}) {
  const env = environment || "the environment";
  const own = site.permissions.own;
  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 text-sm font-medium">Access at this domain</legend>
      <label className={`flex items-start gap-2 rounded border p-3 ${!isPublic ? "border-primary bg-primary/5" : ""}`}>
        <input type="radio" name="domain-access" aria-label="Private" className="mt-1" checked={!isPublic} onChange={() => onPublic(false)} />
        <span>
          <span className="block font-medium">Private</span>
          <span className="block text-xs text-muted-foreground">
            Asks for a sign-in at {hostname} and opens for whoever {env} is shared with, the people who manage {site.name} and administrators.
          </span>
        </span>
      </label>
      <label className={`flex items-start gap-2 rounded border p-3 ${isPublic ? "border-primary bg-primary/5" : ""} ${own ? "cursor-pointer" : "opacity-60"}`}>
        <input type="radio" name="domain-access" aria-label="Public" className="mt-1" checked={isPublic} disabled={!own} onChange={() => onPublic(true)} />
        <span>
          <span className="block font-medium">Public</span>
          <span className="block text-xs text-muted-foreground">
            {own
              ? `Opens for anyone at ${hostname}, with no sign-in. ${env}'s pages addresses still ask for one. You confirm before it is added.`
              : `Only the owner (${site.owner_email}) or an administrator can make a domain public.`}
          </span>
        </span>
      </label>
      {isPublic && gated && (
        <p className="text-xs text-muted-foreground" data-testid="domain-asks">
          {env} requires approval: the domain is added private, and a request to make it public goes to Approvals. It opens to anyone when it is approved and you finish.
        </p>
      )}
    </fieldset>
  );
}

function NewEnvironmentFields({
  site,
  name,
  onName,
  problem,
  choices,
  after,
  onAfter,
  approval,
  onApproval,
}: {
  site: Site;
  name: string;
  onName: (v: string) => void;
  problem: string;
  choices: string[];
  after: string;
  onAfter: (v: string) => void;
  approval: boolean;
  onApproval: (v: boolean) => void;
}) {
  return (
    <>
      <div className="space-y-1">
        <Label htmlFor="domain-env">Name</Label>
        <Input id="domain-env" placeholder="stage" value={name} onChange={(e) => onName(e.target.value)} />
        {problem && <p className="text-xs text-destructive">{problem}</p>}
      </div>
      {choices.length > 1 ? (
        <div className="space-y-1">
          <Label>Goes after</Label>
          <Select value={after} onValueChange={onAfter}>
            <SelectTrigger aria-label="Goes after">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {choices.map((c) => (
                <SelectItem key={c} value={c}>
                  After {c}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">dev is always first and prod always last, so nothing goes after prod.</p>
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">It goes between dev and prod in {site.name}'s order.</p>
      )}
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
